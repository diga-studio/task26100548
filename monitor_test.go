package vibalert

import (
	"math/rand"
	"reflect"
	"testing"
)

func readings(seqs ...int64) []Reading {
	out := make([]Reading, len(seqs))
	for i, s := range seqs {
		out[i] = Reading{Seq: s, Value: 11} // 上限 10，11 为越界
	}
	return out
}

func TestBoundaryAndSingleAlert(t *testing.T) {
	m := New(10, 3)
	// 等于上限算正常
	if d := m.Write([]Reading{{1, 10}, {2, 10.5}, {3, 10}}); !d.Empty() {
		t.Fatalf("expected empty diff, got %+v", d)
	}
	// 不足 K 条不告警
	if d := m.Write(readings(1, 2)); !d.Empty() {
		t.Fatalf("expected empty diff, got %+v", d)
	}
	// 达到 K 条，产生且仅产生一个告警
	d := m.Write(readings(3))
	want := Diff{Added: []Alert{{Start: 1, Trigger: 3, End: 3, Count: 3}}}
	if !reflect.DeepEqual(d, want) {
		t.Fatalf("got %+v, want %+v", d, want)
	}
	// 段继续增长：更新原告警，不产生第二个
	d = m.Write(readings(4, 5, 6))
	want = Diff{Updated: []Alert{{Start: 1, Trigger: 3, End: 6, Count: 6}}}
	if !reflect.DeepEqual(d, want) {
		t.Fatalf("got %+v, want %+v", d, want)
	}
	if got := m.Alerts(); !reflect.DeepEqual(got, want.Updated) {
		t.Fatalf("alerts = %+v", got)
	}
}

func TestBackfillMergesTwoAlerts(t *testing.T) {
	m := New(10, 2)
	// 先上传两段，中间以正常读数隔开，各自成警
	d1 := m.Write(readings(10, 11, 12))
	d2 := m.Write(append([]Reading{{Seq: 15, Value: 1}}, readings(20, 21, 22)...))
	if len(d1.Added) != 1 || len(d2.Added) != 1 {
		t.Fatalf("expected two alerts, got %+v %+v", d1, d2)
	}
	// 乱序补录中间缺口（含将 15 更正为越界），两段合并：
	// 旧身份 20 撤销，身份 10 更新
	d := m.Write([]Reading{
		{Seq: 17, Value: 11}, {Seq: 13, Value: 11}, {Seq: 19, Value: 11},
		{Seq: 15, Value: 11}, {Seq: 14, Value: 11}, {Seq: 18, Value: 11},
		{Seq: 16, Value: 11},
	})
	want := Diff{
		Updated: []Alert{{Start: 10, Trigger: 11, End: 22, Count: 13}},
		Removed: []Alert{{Start: 20, Trigger: 21, End: 22, Count: 3}},
	}
	if !reflect.DeepEqual(d, want) {
		t.Fatalf("got %+v, want %+v", d, want)
	}
	if got := m.Alerts(); !reflect.DeepEqual(got, want.Updated) {
		t.Fatalf("alerts = %+v", got)
	}
}

func TestSplitThenMergeByCorrection(t *testing.T) {
	m := New(10, 3)
	m.Write(readings(1, 2, 3, 4, 5, 6, 7, 8, 9, 10))
	// 长段中部插入正常读数：分裂为两段，各自达到 K
	d := m.Write([]Reading{{Seq: 5, Value: 5}})
	want := Diff{
		Added:   []Alert{{Start: 6, Trigger: 8, End: 10, Count: 5}},
		Updated: []Alert{{Start: 1, Trigger: 3, End: 4, Count: 4}},
	}
	if !reflect.DeepEqual(d, want) {
		t.Fatalf("split: got %+v, want %+v", d, want)
	}
	// 该读数又被更正为越界：两段合并，新身份告警撤销，原身份恢复
	d = m.Write([]Reading{{Seq: 5, Value: 99}})
	want = Diff{
		Updated: []Alert{{Start: 1, Trigger: 3, End: 10, Count: 10}},
		Removed: []Alert{{Start: 6, Trigger: 8, End: 10, Count: 5}},
	}
	if !reflect.DeepEqual(d, want) {
		t.Fatalf("merge: got %+v, want %+v", d, want)
	}
}

func TestBatchPermutationAndReplay(t *testing.T) {
	base := New(10, 3)
	base.Write(readings(1, 2, 3, 10, 11, 12))

	batch := []Reading{
		{Seq: 4, Value: 11}, {Seq: 5, Value: 1}, {Seq: 6, Value: 12},
		{Seq: 7, Value: 13}, {Seq: 8, Value: 1}, {Seq: 9, Value: 14},
		{Seq: 2, Value: 2}, // 更正：越界 -> 正常
	}

	// 基准：按原顺序写入
	m1 := New(10, 3)
	m1.Write(readings(1, 2, 3, 10, 11, 12))
	d1 := m1.Write(batch)

	// 同一批数据的不同排列，净差分必须一致
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 20; trial++ {
		perm := append([]Reading(nil), batch...)
		rng.Shuffle(len(perm), func(i, j int) { perm[i], perm[j] = perm[j], perm[i] })
		m2 := New(10, 3)
		m2.Write(readings(1, 2, 3, 10, 11, 12))
		d2 := m2.Write(perm)
		if !reflect.DeepEqual(d1, d2) {
			t.Fatalf("trial %d: diff mismatch\n got %+v\nwant %+v", trial, d2, d1)
		}
		if !reflect.DeepEqual(m1.Alerts(), m2.Alerts()) {
			t.Fatalf("trial %d: alerts mismatch", trial)
		}
	}

	// 原样重放同一批数据，差分必须为空
	if d := m1.Write(batch); !d.Empty() {
		t.Fatalf("replay: expected empty diff, got %+v", d)
	}
	_ = base
}

func TestCorrectionRemovesAlert(t *testing.T) {
	m := New(10, 3)
	m.Write(readings(1, 2, 3, 4))
	// 更正使段缩短到 K 以下：告警撤销
	d := m.Write([]Reading{{Seq: 2, Value: 0}, {Seq: 3, Value: 0}})
	want := Diff{Removed: []Alert{{Start: 1, Trigger: 3, End: 4, Count: 4}}}
	if !reflect.DeepEqual(d, want) {
		t.Fatalf("got %+v, want %+v", d, want)
	}
	if len(m.Alerts()) != 0 {
		t.Fatalf("expected no alerts, got %+v", m.Alerts())
	}
}

func TestTriggerMovesOnLateInsert(t *testing.T) {
	m := New(10, 3)
	m.Write(readings(10, 20, 30))
	// 段前部迟到补录：起始序号前移，旧身份撤销、新身份产生
	d := m.Write(readings(5))
	want := Diff{
		Added:   []Alert{{Start: 5, Trigger: 20, End: 30, Count: 4}},
		Removed: []Alert{{Start: 10, Trigger: 30, End: 30, Count: 3}},
	}
	if !reflect.DeepEqual(d, want) {
		t.Fatalf("got %+v, want %+v", d, want)
	}
	// 段中部迟到补录：起止不变，但触发序号与数量变化，属于更新
	d = m.Write(readings(15))
	want = Diff{Updated: []Alert{{Start: 5, Trigger: 15, End: 30, Count: 5}}}
	if !reflect.DeepEqual(d, want) {
		t.Fatalf("got %+v, want %+v", d, want)
	}
}

func TestSeqGapsDoNotBreakRuns(t *testing.T) {
	m := New(10, 3)
	// 序号有缺口但不打断连续性
	d := m.Write(readings(100, 500, 9000))
	if len(d.Added) != 1 || d.Added[0] != (Alert{Start: 100, Trigger: 9000, End: 9000, Count: 3}) {
		t.Fatalf("got %+v", d)
	}
}
