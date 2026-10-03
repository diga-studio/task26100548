package vibalert

import "sort"

// segment 表示一段连续越界读数：采样序号相邻（允许序号缺口）且均越界、
// 不能向两端继续延伸。start/end 为段首末读数的采样序号。
type segment struct {
	start int64
	end   int64
	count int
}

// Monitor 处理一台资产的一路振动读数，维护连续越界告警。
// 所有数据保存在进程内存中；调用按顺序执行，不提供并发安全。
type Monitor struct {
	limit float64
	k     int

	readings *treap[bool]    // 全部读数，key 为采样序号，val 为是否越界
	segs     *treap[segment] // 当前全部越界段，key 为段起始序号
	alerts   map[int64]Alert // 当前全部告警，key 为起始序号
}

// New 创建一个实例。limit 为振动上限（严格大于才算越界），
// k 为连续越界次数，必须在 1 至 100000 之间，否则 panic。
func New(limit float64, k int) *Monitor {
	if k < 1 || k > 100000 {
		panic("vibalert: k out of range [1, 100000]")
	}
	return &Monitor{
		limit:    limit,
		k:        k,
		readings: newTreap[bool](0x5eed1),
		segs:     newTreap[segment](0x5eed2),
		alerts:   make(map[int64]Alert),
	}
}

// deltaEnt 记录某个告警身份（起始序号）在一批内的旧值与最新值。
type deltaEnt struct {
	old    Alert
	hasOld bool
	new    Alert
	hasNew bool
}

// Write 批量写入或更正读数，返回相对调用前状态的告警净差分。
// 同批输入保证序号唯一；只报告整批完成后的净变化。
func (m *Monitor) Write(batch []Reading) Diff {
	if len(batch) == 0 {
		return Diff{}
	}
	delta := make(map[int64]*deltaEnt)
	for _, r := range batch {
		m.apply(r.Seq, r.Value, delta)
	}
	return buildDiff(delta)
}

// Alerts 返回当前全部告警，按起始序号排序。
func (m *Monitor) Alerts() []Alert {
	var out []Alert
	for _, a := range m.alerts {
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Start < out[j].Start })
	return out
}

// apply 应用单条读数（新写入或更正）。
func (m *Monitor) apply(seq int64, value float64, delta map[int64]*deltaEnt) {
	out := value > m.limit
	if n := m.readings.get(seq); n != nil {
		if n.val == out {
			return // 越界分类未变，无结构性影响
		}
		n.val = out
		if out {
			m.connect(seq, delta)
		} else {
			m.disconnect(seq, delta)
		}
		return
	}
	m.readings.upsert(seq, out)
	if out {
		m.connect(seq, delta)
	} else {
		m.maybeSplit(seq, delta)
	}
}

// connect 处理 seq 处读数变为越界（新插入或更正）后的段合并/延伸。
// 调用前该读数已存在于 readings 中且标记为越界。
func (m *Monitor) connect(seq int64, delta map[int64]*deltaEnt) {
	p := m.readings.pred(seq)
	q := m.readings.succ(seq)
	pOut := p != nil && p.val
	qOut := q != nil && q.val
	switch {
	case pOut && qOut:
		l := m.segs.floor(p.key).val
		r := m.segs.floor(q.key).val
		if l.start == r.start {
			// 新点插入同一段内部（p、q 原本相邻）。
			l.count++
			m.putSegment(l, delta)
			return
		}
		// 左右两段经新点合并为一段。
		m.removeSegment(l, delta)
		m.removeSegment(r, delta)
		m.putSegment(segment{start: l.start, end: r.end, count: l.count + 1 + r.count}, delta)
	case pOut:
		l := m.segs.floor(p.key).val
		l.end = seq
		l.count++
		m.putSegment(l, delta)
	case qOut:
		r := m.segs.floor(q.key).val
		m.removeSegment(r, delta)
		r.start = seq
		r.count++
		m.putSegment(r, delta)
	default:
		m.putSegment(segment{start: seq, end: seq, count: 1}, delta)
	}
}

// disconnect 处理 seq 处读数由越界更正为正常后的段缩短/分裂/消失。
// 调用前该读数已标记为正常。
func (m *Monitor) disconnect(seq int64, delta map[int64]*deltaEnt) {
	s := m.segs.floor(seq).val
	m.removeSegment(s, delta)
	switch {
	case s.count == 1:
		// 段消失。
	case s.start == seq:
		s.start = m.readings.succ(seq).key
		s.count--
		m.putSegment(s, delta)
	case s.end == seq:
		s.end = m.readings.pred(seq).key
		s.count--
		m.putSegment(s, delta)
	default:
		si := m.readings.rank(seq)
		sti := m.readings.rank(s.start)
		leftCount := si - sti
		m.putSegment(segment{start: s.start, end: m.readings.at(si - 1).key, count: leftCount}, delta)
		m.putSegment(segment{start: m.readings.at(si + 1).key, end: s.end, count: s.count - leftCount - 1}, delta)
	}
}

// maybeSplit 处理新插入的正常读数：若它落在某段内部，则将该段分裂。
// 调用前该读数已存在于 readings 中且标记为正常。
func (m *Monitor) maybeSplit(seq int64, delta map[int64]*deltaEnt) {
	p := m.readings.pred(seq)
	q := m.readings.succ(seq)
	if p == nil || q == nil || !p.val || !q.val {
		return
	}
	s := m.segs.floor(p.key).val // p、q 相邻且均越界，必属同一段
	m.removeSegment(s, delta)
	pi := m.readings.rank(p.key)
	sti := m.readings.rank(s.start)
	leftCount := pi - sti + 1
	m.putSegment(segment{start: s.start, end: p.key, count: leftCount}, delta)
	m.putSegment(segment{start: q.key, end: s.end, count: s.count - leftCount}, delta)
}

// putSegment 写入（新增或替换）一个越界段，并同步其告警。
func (m *Monitor) putSegment(s segment, delta map[int64]*deltaEnt) {
	m.segs.upsert(s.start, s)
	old, hasOld := m.alerts[s.start]
	if s.count >= m.k {
		trigger := m.readings.at(m.readings.rank(s.start) + m.k - 1).key
		a := Alert{Start: s.start, Trigger: trigger, End: s.end, Count: s.count}
		if !hasOld || old != a {
			m.alerts[s.start] = a
			recordDelta(delta, s.start, old, hasOld, a, true)
		}
	} else if hasOld {
		delete(m.alerts, s.start)
		recordDelta(delta, s.start, old, true, Alert{}, false)
	}
}

// removeSegment 删除一个越界段，并撤销其告警（若有）。
func (m *Monitor) removeSegment(s segment, delta map[int64]*deltaEnt) {
	m.segs.remove(s.start)
	if old, ok := m.alerts[s.start]; ok {
		delete(m.alerts, s.start)
		recordDelta(delta, s.start, old, true, Alert{}, false)
	}
}

// recordDelta 记录某告警身份的一次变化；首次记录保存旧值，之后只更新新值。
func recordDelta(delta map[int64]*deltaEnt, start int64, old Alert, hasOld bool, newA Alert, hasNew bool) {
	e, ok := delta[start]
	if !ok {
		e = &deltaEnt{old: old, hasOld: hasOld}
		delta[start] = e
	}
	e.new = newA
	e.hasNew = hasNew
}

// buildDiff 将批内 delta 汇总为净差分，按起始序号排序。
func buildDiff(delta map[int64]*deltaEnt) Diff {
	var d Diff
	for start, e := range delta {
		_ = start
		switch {
		case !e.hasOld && e.hasNew:
			d.Added = append(d.Added, e.new)
		case e.hasOld && !e.hasNew:
			d.Removed = append(d.Removed, e.old)
		case e.hasOld && e.hasNew && e.old != e.new:
			d.Updated = append(d.Updated, e.new)
		}
	}
	byStart := func(s []Alert) { sort.Slice(s, func(i, j int) bool { return s[i].Start < s[j].Start }) }
	byStart(d.Added)
	byStart(d.Updated)
	byStart(d.Removed)
	return d
}
