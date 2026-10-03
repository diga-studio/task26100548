package vibalert

import "sort"

// refAlerts 是独立的全量计算实现：从原始读数表出发，按采样序号排序后
// 扫描出所有越界段并生成告警，用于与 Monitor 的增量结果交叉核对。
func refAlerts(readings map[int64]float64, limit float64, k int) []Alert {
	seqs := make([]int64, 0, len(readings))
	for s := range readings {
		seqs = append(seqs, s)
	}
	sort.Slice(seqs, func(i, j int) bool { return seqs[i] < seqs[j] })

	var alerts []Alert
	runStart := -1
	runLen := 0
	flush := func(endIdx int) {
		if runLen >= k {
			alerts = append(alerts, Alert{
				Start:   seqs[runStart],
				Trigger: seqs[runStart+k-1],
				End:     seqs[endIdx],
				Count:   runLen,
			})
		}
		runStart = -1
		runLen = 0
	}
	for i, s := range seqs {
		if readings[s] > limit {
			if runLen == 0 {
				runStart = i
			}
			runLen++
		} else if runLen > 0 {
			flush(i - 1)
		}
	}
	if runLen > 0 {
		flush(len(seqs) - 1)
	}
	return alerts
}

// refDiff 计算两份告警列表（均按 Start 排序）之间的差分。
func refDiff(before, after []Alert) Diff {
	bm := make(map[int64]Alert, len(before))
	for _, a := range before {
		bm[a.Start] = a
	}
	am := make(map[int64]Alert, len(after))
	for _, a := range after {
		am[a.Start] = a
	}
	var d Diff
	for s, a := range am {
		if b, ok := bm[s]; !ok {
			d.Added = append(d.Added, a)
		} else if b != a {
			d.Updated = append(d.Updated, a)
		}
	}
	for s, b := range bm {
		if _, ok := am[s]; !ok {
			d.Removed = append(d.Removed, b)
		}
	}
	byStart := func(s []Alert) { sort.Slice(s, func(i, j int) bool { return s[i].Start < s[j].Start }) }
	byStart(d.Added)
	byStart(d.Updated)
	byStart(d.Removed)
	return d
}
