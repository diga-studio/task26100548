package vibalert

import (
	"math/rand"
	"runtime"
	"testing"
	"time"
)

const (
	perfHistory  = 1_000_000
	perfOps      = 20_000
	perfTimeMax  = 20 * time.Second
	perfMemMaxMi = 512
)

// memSampler 在操作过程中周期性采样堆内存，跟踪峰值。
type memSampler struct{ peak uint64 }

func (s *memSampler) sample() {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	if v := ms.HeapInuse + ms.StackInuse; v > s.peak {
		s.peak = v
	}
}

func (s *memSampler) peakMiB() float64 { return float64(s.peak) / (1 << 20) }

// TestPerfLongSegment 覆盖 K=100000、历史主体为一个长越界段的场景：
// 先装载 100 万条越界读数，再连续执行 2 万次单点操作——
// 偶数次为段前部补录（告警身份不断前移），奇数次为段中部反复更正
// （正常/越界来回翻转，引发大段分裂与合并）。
func TestPerfLongSegment(t *testing.T) {
	m := New(10, 100000)

	batch := make([]Reading, perfHistory)
	state := make(map[int64]float64, perfHistory+perfOps)
	for i := range batch {
		batch[i] = Reading{Seq: int64(i + 1), Value: 11}
		state[int64(i+1)] = 11
	}
	m.Write(batch)

	var smp memSampler
	const mid = int64(perfHistory / 2)
	start := time.Now()
	for i := 0; i < perfOps; i++ {
		var wr Reading
		if i%2 == 0 {
			wr = Reading{Seq: int64(-i), Value: 11} // 段前部补录
		} else if (i/2)%2 == 0 {
			wr = Reading{Seq: mid, Value: 1} // 段中部更正为正常：大段分裂
		} else {
			wr = Reading{Seq: mid, Value: 11} // 再更正回越界：大段合并
		}
		m.Write([]Reading{wr})
		state[wr.Seq] = wr.Value
		if i%256 == 0 {
			smp.sample()
		}
	}
	elapsed := time.Since(start)
	smp.sample()

	if got, want := m.Alerts(), refAlerts(state, 10, 100000); !equalAlerts(got, want) {
		t.Fatalf("final alerts mismatch:\ngot  %+v\nwant %+v", got, want)
	}
	t.Logf("long-segment K=100000: %d ops in %v (%.1f ops/ms), peak mem %.1f MiB",
		perfOps, elapsed, float64(perfOps)/float64(elapsed.Milliseconds()), smp.peakMiB())
	if elapsed > perfTimeMax {
		t.Fatalf("too slow: %v > %v", elapsed, perfTimeMax)
	}
	if smp.peakMiB() > perfMemMaxMi {
		t.Fatalf("peak memory %.1f MiB exceeds %d MiB", smp.peakMiB(), perfMemMaxMi)
	}
}

// TestPerfManyShortSegments 覆盖大量短越界段的场景：
// 100 万条历史读数构成约 25 万个告警段，随后 2 万次随机单点补录/更正。
func TestPerfManyShortSegments(t *testing.T) {
	m := New(10, 3)

	batch := make([]Reading, perfHistory)
	state := make(map[int64]float64, perfHistory+perfOps)
	for i := range batch {
		v := float64(11)
		if i%4 == 3 {
			v = 1 // 每 3 条越界夹 1 条正常
		}
		batch[i] = Reading{Seq: int64(i), Value: v}
		state[int64(i)] = v
	}
	m.Write(batch)

	rng := rand.New(rand.NewSource(7))
	var smp memSampler
	start := time.Now()
	for i := 0; i < perfOps; i++ {
		seq := int64(rng.Intn(perfHistory + perfOps))
		v := float64(11)
		if rng.Intn(2) == 0 {
			v = 1
		}
		m.Write([]Reading{{Seq: seq, Value: v}})
		state[seq] = v
		if i%256 == 0 {
			smp.sample()
		}
	}
	elapsed := time.Since(start)
	smp.sample()

	if got, want := m.Alerts(), refAlerts(state, 10, 3); !equalAlerts(got, want) {
		t.Fatalf("final alerts mismatch: got %d, want %d", len(got), len(want))
	}
	t.Logf("many-short-segments K=3: %d ops in %v (%.1f ops/ms), peak mem %.1f MiB",
		perfOps, elapsed, float64(perfOps)/float64(elapsed.Milliseconds()), smp.peakMiB())
	if elapsed > perfTimeMax {
		t.Fatalf("too slow: %v > %v", elapsed, perfTimeMax)
	}
	if smp.peakMiB() > perfMemMaxMi {
		t.Fatalf("peak memory %.1f MiB exceeds %d MiB", smp.peakMiB(), perfMemMaxMi)
	}
}

func equalAlerts(a, b []Alert) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
