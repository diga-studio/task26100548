package vibralert

import (
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

// ---- 参考实现：独立的全量计算，用于交叉核对 ----

type reference struct {
	limit float64
	k     int
	data  map[int64]float64
}

func newReference(limit float64, k int) *reference {
	return &reference{limit: limit, k: k, data: make(map[int64]float64)}
}

func (r *reference) apply(batch []Reading) []AlertChange {
	before := r.alerts()
	for _, rd := range batch {
		r.data[rd.Seq] = rd.Value
	}
	return diffAlerts(before, r.alerts())
}

func (r *reference) alerts() []Alert {
	seqs := make([]int64, 0, len(r.data))
	for s := range r.data {
		seqs = append(seqs, s)
	}
	sort.Slice(seqs, func(i, j int) bool { return seqs[i] < seqs[j] })
	var out []Alert
	for i := 0; i < len(seqs); {
		if r.data[seqs[i]] <= r.limit {
			i++
			continue
		}
		j := i
		for j+1 < len(seqs) && r.data[seqs[j+1]] > r.limit {
			j++
		}
		if n := j - i + 1; n >= r.k {
			out = append(out, Alert{
				StartSeq:   seqs[i],
				TriggerSeq: seqs[i+r.k-1],
				EndSeq:     seqs[j],
				Count:      n,
			})
		}
		i = j + 1
	}
	return out
}

func diffAlerts(before, after []Alert) []AlertChange {
	oldByStart := make(map[int64]Alert, len(before))
	for _, a := range before {
		oldByStart[a.StartSeq] = a
	}
	newByStart := make(map[int64]Alert, len(after))
	for _, a := range after {
		newByStart[a.StartSeq] = a
	}
	out := []AlertChange{}
	for s, na := range newByStart {
		if oa, ok := oldByStart[s]; !ok {
			out = append(out, AlertChange{Kind: AlertAdded, Alert: na})
		} else if oa != na {
			out = append(out, AlertChange{Kind: AlertUpdated, Alert: na})
		}
	}
	for s, oa := range oldByStart {
		if _, ok := newByStart[s]; !ok {
			out = append(out, AlertChange{Kind: AlertRemoved, Alert: oa})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Alert.StartSeq < out[j].Alert.StartSeq })
	return out
}

// ---- 用例 ----

func TestThresholdBoundary(t *testing.T) {
	m := NewMonitor(10, 1)
	changes := m.ApplyBatch([]Reading{{Seq: 1, Value: 10}}) // 等于上限，正常
	if len(changes) != 0 || len(m.Alerts()) != 0 {
		t.Fatalf("equal-to-limit should be normal: changes=%v alerts=%v", changes, m.Alerts())
	}
	changes = m.ApplyBatch([]Reading{{Seq: 2, Value: 10.0000001}})
	want := []AlertChange{{Kind: AlertAdded, Alert: Alert{StartSeq: 2, TriggerSeq: 2, EndSeq: 2, Count: 1}}}
	if !reflect.DeepEqual(changes, want) {
		t.Fatalf("changes = %+v, want %+v", changes, want)
	}
}

func TestMergeTwoAlertsByCorrection(t *testing.T) {
	m := NewMonitor(10, 2)
	m.ApplyBatch([]Reading{
		{Seq: 1, Value: 20}, {Seq: 2, Value: 20},
		{Seq: 3, Value: 5}, // 正常读数把两侧分开
		{Seq: 4, Value: 20}, {Seq: 5, Value: 20},
	})
	before := m.Alerts()
	wantBefore := []Alert{
		{StartSeq: 1, TriggerSeq: 2, EndSeq: 2, Count: 2},
		{StartSeq: 4, TriggerSeq: 5, EndSeq: 5, Count: 2},
	}
	if !reflect.DeepEqual(before, wantBefore) {
		t.Fatalf("before = %+v, want %+v", before, wantBefore)
	}
	// 更正 seq3 为越界，两段合并；起始序号不变 -> 更新原告警，撤销 start=4 的告警
	changes := m.ApplyBatch([]Reading{{Seq: 3, Value: 20}})
	want := []AlertChange{
		{Kind: AlertUpdated, Alert: Alert{StartSeq: 1, TriggerSeq: 2, EndSeq: 5, Count: 5}},
		{Kind: AlertRemoved, Alert: Alert{StartSeq: 4, TriggerSeq: 5, EndSeq: 5, Count: 2}},
	}
	if !reflect.DeepEqual(changes, want) {
		t.Fatalf("changes = %+v, want %+v", changes, want)
	}
	if got := m.Alerts(); !reflect.DeepEqual(got, []Alert{{StartSeq: 1, TriggerSeq: 2, EndSeq: 5, Count: 5}}) {
		t.Fatalf("alerts = %+v", got)
	}
}

func TestLateBackfillMergesAcrossGap(t *testing.T) {
	// 序号有缺口不补点也不打断连续性：1..3 与 10..12 之间没有正常读数，本就是一个段。
	// 这里验证乱序补录中间点后段仍然完整、告警只更新一次。
	m := NewMonitor(10, 3)
	m.ApplyBatch([]Reading{
		{Seq: 1, Value: 20}, {Seq: 2, Value: 20}, {Seq: 3, Value: 20},
		{Seq: 10, Value: 20}, {Seq: 11, Value: 20}, {Seq: 12, Value: 20},
	})
	if got := m.Alerts(); !reflect.DeepEqual(got, []Alert{{StartSeq: 1, TriggerSeq: 3, EndSeq: 12, Count: 6}}) {
		t.Fatalf("alerts = %+v", got)
	}
	// 乱序补录缺口中的点（先 5 后 4），段变长，告警更新，触发点不变
	changes := m.ApplyBatch([]Reading{{Seq: 5, Value: 20}, {Seq: 4, Value: 20}})
	want := []AlertChange{
		{Kind: AlertUpdated, Alert: Alert{StartSeq: 1, TriggerSeq: 3, EndSeq: 12, Count: 8}},
	}
	if !reflect.DeepEqual(changes, want) {
		t.Fatalf("changes = %+v, want %+v", changes, want)
	}
}

func TestSplitThenMergeLongRun(t *testing.T) {
	m := NewMonitor(10, 2)
	m.ApplyBatch([]Reading{
		{Seq: 10, Value: 20}, {Seq: 20, Value: 20}, {Seq: 30, Value: 20},
		{Seq: 40, Value: 20}, {Seq: 50, Value: 20}, {Seq: 60, Value: 20},
	})
	if got := m.Alerts(); !reflect.DeepEqual(got, []Alert{{StartSeq: 10, TriggerSeq: 20, EndSeq: 60, Count: 6}}) {
		t.Fatalf("alerts = %+v", got)
	}
	// 中间插入正常读数 -> 段分裂：左段告警更新（末尾缩短），右段产生新告警
	changes := m.ApplyBatch([]Reading{{Seq: 25, Value: 5}})
	want := []AlertChange{
		{Kind: AlertUpdated, Alert: Alert{StartSeq: 10, TriggerSeq: 20, EndSeq: 20, Count: 2}},
		{Kind: AlertAdded, Alert: Alert{StartSeq: 30, TriggerSeq: 40, EndSeq: 60, Count: 4}},
	}
	if !reflect.DeepEqual(changes, want) {
		t.Fatalf("split changes = %+v, want %+v", changes, want)
	}
	// 再把该点更正为越界 -> 重新合并：start=30 的告警撤销，start=10 的告警更新
	changes = m.ApplyBatch([]Reading{{Seq: 25, Value: 20}})
	want = []AlertChange{
		{Kind: AlertUpdated, Alert: Alert{StartSeq: 10, TriggerSeq: 20, EndSeq: 60, Count: 7}},
		{Kind: AlertRemoved, Alert: Alert{StartSeq: 30, TriggerSeq: 40, EndSeq: 60, Count: 4}},
	}
	if !reflect.DeepEqual(changes, want) {
		t.Fatalf("merge changes = %+v, want %+v", changes, want)
	}
	if got := m.Alerts(); !reflect.DeepEqual(got, []Alert{{StartSeq: 10, TriggerSeq: 20, EndSeq: 60, Count: 7}}) {
		t.Fatalf("alerts = %+v", got)
	}
}

func TestShrinkBelowKRemovesAlert(t *testing.T) {
	m := NewMonitor(10, 3)
	m.ApplyBatch([]Reading{
		{Seq: 1, Value: 20}, {Seq: 2, Value: 20}, {Seq: 3, Value: 20}, {Seq: 4, Value: 20},
	})
	// 更正 seq2 为正常：段分裂为 {1} 和 {3,4}，都不足 K -> 告警撤销
	changes := m.ApplyBatch([]Reading{{Seq: 2, Value: 1}})
	want := []AlertChange{
		{Kind: AlertRemoved, Alert: Alert{StartSeq: 1, TriggerSeq: 3, EndSeq: 4, Count: 4}},
	}
	if !reflect.DeepEqual(changes, want) {
		t.Fatalf("changes = %+v, want %+v", changes, want)
	}
	if len(m.Alerts()) != 0 {
		t.Fatalf("alerts = %+v", m.Alerts())
	}
}

func TestStartChangeIsRemovePlusAdd(t *testing.T) {
	m := NewMonitor(10, 2)
	m.ApplyBatch([]Reading{{Seq: 5, Value: 20}, {Seq: 6, Value: 20}})
	// 在段首之前补录越界点：起始序号改变 -> 旧告警撤销 + 新告警产生
	changes := m.ApplyBatch([]Reading{{Seq: 4, Value: 20}})
	want := []AlertChange{
		{Kind: AlertAdded, Alert: Alert{StartSeq: 4, TriggerSeq: 5, EndSeq: 6, Count: 3}},
		{Kind: AlertRemoved, Alert: Alert{StartSeq: 5, TriggerSeq: 6, EndSeq: 6, Count: 2}},
	}
	if !reflect.DeepEqual(changes, want) {
		t.Fatalf("changes = %+v, want %+v", changes, want)
	}
}

func TestTriggerSeqChangesWithSameBounds(t *testing.T) {
	// 起止序号不变，但触发序号/数量变化也算更新。
	// K=3，段 [3,5,7,9]，触发点为 7；在 3 与 5 之间补录 4，触发点前移为 5。
	m := NewMonitor(10, 3)
	m.ApplyBatch([]Reading{
		{Seq: 3, Value: 20}, {Seq: 5, Value: 20}, {Seq: 7, Value: 20}, {Seq: 9, Value: 20},
	})
	if got := m.Alerts(); !reflect.DeepEqual(got, []Alert{{StartSeq: 3, TriggerSeq: 7, EndSeq: 9, Count: 4}}) {
		t.Fatalf("alerts = %+v", got)
	}
	changes := m.ApplyBatch([]Reading{{Seq: 4, Value: 20}})
	want := []AlertChange{
		{Kind: AlertUpdated, Alert: Alert{StartSeq: 3, TriggerSeq: 5, EndSeq: 9, Count: 5}},
	}
	if !reflect.DeepEqual(changes, want) {
		t.Fatalf("changes = %+v, want %+v", changes, want)
	}
}

func TestPermutationAndReplay(t *testing.T) {
	const limit = 10.0
	const k = 2
	initial := []Reading{
		{Seq: 1, Value: 20}, {Seq: 2, Value: 20},
		{Seq: 3, Value: 5},
		{Seq: 4, Value: 20}, {Seq: 5, Value: 20},
		{Seq: 10, Value: 5},
	}
	batch := []Reading{
		{Seq: 3, Value: 20},  // 更正：合并两段
		{Seq: 6, Value: 5},   // 新正常点
		{Seq: 7, Value: 20},  // 孤立越界点（不足 K）
		{Seq: 8, Value: 20},  // 与 7 连成新告警
		{Seq: 4, Value: 25},  // 同状态更正数值，无结构变化
		{Seq: 12, Value: 20}, // 孤立点
	}
	var wantAlerts []Alert
	var wantChanges []AlertChange
	rng := rand.New(rand.NewSource(7))
	for trial := 0; trial < 20; trial++ {
		m := NewMonitor(limit, k)
		m.ApplyBatch(initial)
		perm := rand.Perm(len(batch))
		shuffled := make([]Reading, len(batch))
		for i, j := range perm {
			shuffled[i] = batch[j]
		}
		changes := m.ApplyBatch(shuffled)
		if trial == 0 {
			wantAlerts = m.Alerts()
			wantChanges = changes
		} else {
			if !reflect.DeepEqual(m.Alerts(), wantAlerts) {
				t.Fatalf("trial %d: alerts = %+v, want %+v", trial, m.Alerts(), wantAlerts)
			}
			if !reflect.DeepEqual(changes, wantChanges) {
				t.Fatalf("trial %d: changes = %+v, want %+v", trial, changes, wantChanges)
			}
		}
		// 原样重放同一批：差分必须为空
		if got := m.ApplyBatch(batch); len(got) != 0 {
			t.Fatalf("trial %d: replay changes = %+v, want empty", trial, got)
		}
		_ = rng
	}
}

func TestRandomizedCrossCheck(t *testing.T) {
	rng := rand.New(rand.NewSource(20261005))
	for trial := 0; trial < 100; trial++ {
		k := 1 + rng.Intn(4)
		m := NewMonitor(10, k)
		ref := newReference(10, k)
		for batch := 0; batch < 200; batch++ {
			n := 1 + rng.Intn(8)
			seen := make(map[int64]bool)
			rs := make([]Reading, 0, n)
			for len(rs) < n {
				seq := int64(rng.Intn(60))
				if seen[seq] {
					continue
				}
				seen[seq] = true
				var v float64
				switch rng.Intn(5) {
				case 0:
					v = 10 // 边界：等于上限
				case 1, 2:
					v = 5
				default:
					v = 20
				}
				rs = append(rs, Reading{Seq: seq, Value: v})
			}
			gotChanges := m.ApplyBatch(rs)
			wantChanges := ref.apply(rs)
			if !reflect.DeepEqual(gotChanges, wantChanges) {
				t.Fatalf("trial %d batch %d: changes = %+v, want %+v (readings=%+v)",
					trial, batch, gotChanges, wantChanges, rs)
			}
			if got, want := m.Alerts(), ref.alerts(); !reflect.DeepEqual(got, want) {
				t.Fatalf("trial %d batch %d: alerts = %+v, want %+v", trial, batch, got, want)
			}
		}
	}
}
