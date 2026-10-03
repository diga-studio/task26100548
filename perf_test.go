package vibralert

import (
	"runtime"
	"testing"
	"time"
)

const perfLimit = 100.0

// peakSampler 在后台轻量采样堆内存峰值。
type peakSampler struct{ peak uint64 }

func (p *peakSampler) sample() {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	if cur := ms.HeapInuse + ms.StackInuse; cur > p.peak {
		p.peak = cur
	}
}

// TestPerfLongSegment 对应验收场景：K=100000，历史主体为一个长越界段，
// 装载 100 万条后连续 2 万单点批（长段前部补录 + 段中部反复更正）。
func TestPerfLongSegment(t *testing.T) {
	const total = 1_000_000
	const batches = 20_000
	m := NewMonitor(perfLimit, 100000)

	load := make([]Reading, total)
	for i := range load {
		load[i] = Reading{Seq: int64(i + 1), Value: 200}
	}
	m.ApplyBatch(load)
	if got := len(m.Alerts()); got != 1 {
		t.Fatalf("alerts after load = %d, want 1", got)
	}

	var sampler peakSampler
	sampler.sample()
	start := time.Now()
	for i := 0; i < batches; i++ {
		var changes []AlertChange
		if i%2 == 0 {
			// 长段前部补录：序号 0, -1, -2, ...
			changes = m.ApplyBatch([]Reading{{Seq: int64(-(i / 2)), Value: 200}})
		} else {
			// 段中部反复更正：在正常与越界之间切换，引发分裂再合并
			v := 50.0
			if (i/2)%2 == 1 {
				v = 200
			}
			changes = m.ApplyBatch([]Reading{{Seq: total / 2, Value: v}})
		}
		if len(changes) == 0 {
			t.Fatalf("batch %d: expected non-empty diff", i)
		}
		if i%997 == 0 {
			sampler.sample()
		}
	}
	elapsed := time.Since(start)
	sampler.sample()

	// 最终状态校验：前部补录 1 万个越界点，段中部恢复越界 -> 仍是一个告警
	alerts := m.Alerts()
	if len(alerts) != 1 {
		t.Fatalf("final alerts = %+v", alerts)
	}
	// 触发点为段内第 100000 条：补录的 1 万条（-9999..0）之后第 90000 条，即序号 90000。
	want := Alert{StartSeq: -9999, TriggerSeq: 90000, EndSeq: total, Count: total + 10000}
	if alerts[0] != want {
		t.Fatalf("final alert = %+v, want %+v", alerts[0], want)
	}

	t.Logf("long-segment: %d single-point batches in %v (%.1f µs/batch), peak heap %d MiB",
		batches, elapsed, float64(elapsed.Microseconds())/batches, sampler.peak>>20)
	if elapsed > 20*time.Second {
		t.Fatalf("too slow: %v > 20s", elapsed)
	}
	if limit := uint64(512) << 20; sampler.peak > limit {
		t.Fatalf("peak memory %d MiB exceeds 512 MiB", sampler.peak>>20)
	}
}

// TestPerfManyShortRuns 对应验收场景：大量短越界段（25 万个告警），
// 装载 100 万条后连续 2 万单点批（随机位置正常/越界翻转）。
func TestPerfManyShortRuns(t *testing.T) {
	const total = 1_000_000
	const batches = 20_000
	m := NewMonitor(perfLimit, 3)

	// out,out,out,normal 循环：25 万个长度为 3 的短段，每段一个告警
	load := make([]Reading, total)
	for i := range load {
		v := 200.0
		if i%4 == 3 {
			v = 50
		}
		load[i] = Reading{Seq: int64(i + 1), Value: v}
	}
	m.ApplyBatch(load)
	if got := len(m.Alerts()); got != 250000 {
		t.Fatalf("alerts after load = %d, want 250000", got)
	}

	var sampler peakSampler
	sampler.sample()
	start := time.Now()
	// 确定性伪随机位置翻转，覆盖段合并/分裂/产生/撤销
	state := uint64(12345)
	for i := 0; i < batches; i++ {
		state = state*6364136223846793005 + 1442695040888963407
		seq := int64(state%total) + 1
		old, _ := m.points.Get(seq)
		v := 200.0
		if old > perfLimit {
			v = 50
		}
		m.ApplyBatch([]Reading{{Seq: seq, Value: v}})
		if i%997 == 0 {
			sampler.sample()
		}
	}
	elapsed := time.Since(start)
	sampler.sample()

	t.Logf("many-short-runs: %d single-point batches in %v (%.1f µs/batch), peak heap %d MiB, final alerts %d",
		batches, elapsed, float64(elapsed.Microseconds())/batches, sampler.peak>>20, len(m.Alerts()))
	if elapsed > 20*time.Second {
		t.Fatalf("too slow: %v > 20s", elapsed)
	}
	if limit := uint64(512) << 20; sampler.peak > limit {
		t.Fatalf("peak memory %d MiB exceeds 512 MiB", sampler.peak>>20)
	}
}
