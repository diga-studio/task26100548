package vibralert

import "sort"

// run 表示一段连续（按采样顺序相邻）的越界读数。
type run struct {
	start int64 // 段首读数的采样序号
	end   int64 // 段末读数的采样序号
	count int   // 段内读数条数
}

// Monitor 处理一台资产的一路振动读数，维护连续越界告警。
// 不是并发安全的；调用方需按顺序调用。
type Monitor struct {
	limit  float64
	k      int
	points *treap[float64] // 采样序号 -> 振动值
	runs   *treap[run]     // 段首序号 -> 越界段
	diff   *diffTracker
}

// NewMonitor 创建实例。limit 为振动上限（严格大于才算越界），k 为连续次数（1..100000）。
func NewMonitor(limit float64, k int) *Monitor {
	if k < 1 || k > 100000 {
		panic("vibralert: k out of range [1, 100000]")
	}
	return &Monitor{
		limit:  limit,
		k:      k,
		points: newTreap[float64](0x9e3779b97f4a7c15),
		runs:   newTreap[run](0xc2b2ae3d27d4eb4f),
	}
}

// ApplyBatch 批量写入或更正读数（同批序号唯一，允许乱序），
// 返回相对于调用前状态的告警净差分（按起始序号排序）。
func (m *Monitor) ApplyBatch(readings []Reading) []AlertChange {
	m.diff = &diffTracker{entries: make(map[int64]*diffEntry)}
	for _, r := range readings {
		m.applyOne(r)
	}
	changes := m.diff.changes()
	m.diff = nil
	return changes
}

// Alerts 返回当前全部告警，按起始序号排序。
func (m *Monitor) Alerts() []Alert {
	var out []Alert
	m.runs.Ascend(func(_ int64, r run) bool {
		if r.count >= m.k {
			out = append(out, m.alertFor(r))
		}
		return true
	})
	return out
}

// applyOne 应用单条读数的写入或更正。
func (m *Monitor) applyOne(r Reading) {
	out := r.Value > m.limit
	old, existed := m.points.Get(r.Seq)
	if existed {
		oldOut := old > m.limit
		if oldOut == out {
			m.points.Insert(r.Seq, r.Value) // 越界状态不变，仅更新数值
			return
		}
	}
	if !out {
		// 新正常点或越界更正为正常：分裂/收缩跨越该位置的越界段（若有）。
		m.removeOutPoint(r.Seq)
		m.points.Insert(r.Seq, r.Value)
		return
	}
	// 新越界点或正常更正为越界。
	m.insertOutPoint(r.Seq, r.Value)
}

// insertOutPoint 把 seq 处的越界读数并入段结构。
// 分两个阶段：先在旧点集上定位受影响的段并记录旧告警，
// 再把点写入 points、更新段结构并记录新告警。
func (m *Monitor) insertOutPoint(seq int64, val float64) {
	predSeq, predVal, hasPred := m.points.Predecessor(seq)
	succSeq, succVal, hasSucc := m.points.Successor(seq)
	predOut := hasPred && predVal > m.limit
	succOut := hasSucc && succVal > m.limit
	var left, right run
	sameRun := false
	switch {
	case predOut && succOut:
		left, _ = m.findRunContaining(predSeq)
		right, _ = m.findRunContaining(succSeq)
		sameRun = left.start == right.start
		m.recordOld(left)
		if !sameRun {
			m.recordOld(right)
		}
	case predOut:
		left, _ = m.findRunContaining(predSeq)
		m.recordOld(left)
	case succOut:
		right, _ = m.findRunContaining(succSeq)
		m.recordOld(right)
	}
	m.points.Insert(seq, val)
	switch {
	case predOut && succOut:
		if sameRun {
			// 插入点落在段内部缺口：段不变，仅数量 +1。
			m.runs.Delete(left.start)
			grown := run{start: left.start, end: left.end, count: left.count + 1}
			m.runs.Insert(grown.start, grown)
			m.recordNew(grown)
			return
		}
		// 前驱点所属段的 end 必为 predSeq，后继点所属段的 start 必为 succSeq。
		m.runs.Delete(left.start)
		m.runs.Delete(right.start)
		merged := run{start: left.start, end: right.end, count: left.count + 1 + right.count}
		m.runs.Insert(merged.start, merged)
		m.recordNew(merged)
	case predOut:
		m.runs.Delete(left.start)
		grown := run{start: left.start, end: seq, count: left.count + 1}
		m.runs.Insert(grown.start, grown)
		m.recordNew(grown)
	case succOut:
		m.runs.Delete(right.start)
		grown := run{start: seq, end: right.end, count: right.count + 1}
		m.runs.Insert(grown.start, grown)
		m.recordNew(grown)
	default:
		single := run{start: seq, end: seq, count: 1}
		m.runs.Insert(single.start, single)
		m.recordNew(single)
	}
}

// removeOutPoint 把 seq 处（仍在 points 中）的读数从段结构中移除，必要时分裂段。
func (m *Monitor) removeOutPoint(seq int64) {
	r, ok := m.findRunContaining(seq)
	if !ok {
		return
	}
	m.recordOld(r)
	m.runs.Delete(r.start)
	if r.start < seq {
		predSeq, _, _ := m.points.Predecessor(seq)
		left := run{
			start: r.start,
			end:   predSeq,
			count: m.points.RankLE(predSeq) - m.points.RankLE(r.start) + 1,
		}
		m.runs.Insert(left.start, left)
		m.recordNew(left)
	}
	if seq < r.end {
		succSeq, _, _ := m.points.Successor(seq)
		right := run{
			start: succSeq,
			end:   r.end,
			count: m.points.RankLE(r.end) - m.points.RankLE(succSeq) + 1,
		}
		m.runs.Insert(right.start, right)
		m.recordNew(right)
	}
}

// findRunContaining 查找覆盖采样序号 seq 的越界段。
func (m *Monitor) findRunContaining(seq int64) (run, bool) {
	if r, ok := m.runs.Get(seq); ok {
		return r, true
	}
	_, r, ok := m.runs.Predecessor(seq)
	if !ok || r.end < seq {
		return run{}, false
	}
	return r, true
}

// alertFor 由段生成完整告警记录；要求 r.count >= m.k。
func (m *Monitor) alertFor(r run) Alert {
	trigger, _ := m.points.SelectAt(m.points.RankLE(r.start) + m.k - 1)
	return Alert{StartSeq: r.start, TriggerSeq: trigger, EndSeq: r.end, Count: r.count}
}

// recordOld 在段被修改/删除前调用：首次记录其旧告警，并把新告警置空
// （段此后若仍存在，会由 recordNew 重新记录）。
func (m *Monitor) recordOld(r run) {
	if m.diff == nil || r.count < m.k {
		return
	}
	e, ok := m.diff.entries[r.start]
	if !ok {
		a := m.alertFor(r)
		e = &diffEntry{old: &a}
		m.diff.entries[r.start] = e
	}
	e.cur = nil
}

// recordNew 在段被创建/修改后记录其新告警（覆盖批次内较旧的值）。
func (m *Monitor) recordNew(r run) {
	if m.diff == nil || r.count < m.k {
		return
	}
	e, ok := m.diff.entries[r.start]
	if !ok {
		e = &diffEntry{}
		m.diff.entries[r.start] = e
	}
	a := m.alertFor(r)
	e.cur = &a
}

// diffTracker 累积一个批次内的告警净变化，键为告警起始序号。
type diffTracker struct {
	entries map[int64]*diffEntry
}

type diffEntry struct {
	old *Alert
	cur *Alert
}

func (d *diffTracker) changes() []AlertChange {
	out := make([]AlertChange, 0, len(d.entries))
	for _, e := range d.entries {
		switch {
		case e.old == nil && e.cur != nil:
			out = append(out, AlertChange{Kind: AlertAdded, Alert: *e.cur})
		case e.old != nil && e.cur == nil:
			out = append(out, AlertChange{Kind: AlertRemoved, Alert: *e.old})
		case e.old != nil && e.cur != nil && *e.old != *e.cur:
			out = append(out, AlertChange{Kind: AlertUpdated, Alert: *e.cur})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Alert.StartSeq < out[j].Alert.StartSeq })
	return out
}
