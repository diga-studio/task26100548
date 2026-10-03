package vibalert

import (
	"math/rand"
	"reflect"
	"testing"
)

// TestRandomAgainstReference 对随机小规模写入序列，逐批将 Monitor 的
// 增量差分与独立全量计算的结果核对，并校验 Alerts() 与重放空差分。
func TestRandomAgainstReference(t *testing.T) {
	rng := rand.New(rand.NewSource(20261005))
	for trial := 0; trial < 300; trial++ {
		k := 1 + rng.Intn(6)
		m := New(10, k)
		state := make(map[int64]float64)
		prev := refAlerts(state, 10, k)

		for batch := 0; batch < 40; batch++ {
			n := 1 + rng.Intn(8)
			seen := make(map[int64]bool)
			var wr []Reading
			for len(wr) < n {
				var seq int64
				// 一半概率更正已有序号，一半概率写入新序号（含缺口）
				if len(state) > 0 && rng.Intn(2) == 0 {
					idx := rng.Intn(len(state))
					for s := range state {
						if idx == 0 {
							seq = s
							break
						}
						idx--
					}
				} else {
					seq = int64(rng.Intn(60))
				}
				if seen[seq] {
					continue
				}
				seen[seq] = true
				wr = append(wr, Reading{Seq: seq, Value: float64(rng.Intn(25))})
			}

			got := m.Write(wr)
			for _, r := range wr {
				state[r.Seq] = r.Value
			}
			now := refAlerts(state, 10, k)
			want := refDiff(prev, now)
			prev = now

			if !reflect.DeepEqual(got, want) {
				t.Fatalf("trial %d batch %d: k=%d\nbatch=%+v\ngot  %+v\nwant %+v",
					trial, batch, k, wr, got, want)
			}
			if !reflect.DeepEqual(m.Alerts(), now) {
				t.Fatalf("trial %d batch %d: alerts mismatch\ngot  %+v\nwant %+v",
					trial, batch, m.Alerts(), now)
			}
			// 原样重放该批，差分必须为空
			if d := m.Write(wr); !d.Empty() {
				t.Fatalf("trial %d batch %d: replay not empty: %+v", trial, batch, d)
			}
		}
	}
}
