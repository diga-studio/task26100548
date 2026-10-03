package vibalert

// Reading 是一条振动读数。Seq 为采样序号，Value 为振动值。
type Reading struct {
	Seq   int64
	Value float64
}

// Alert 是一条连续越界告警，记录越界段的起始序号、首次达到 K 条时的
// 触发序号、末尾序号和读数数量。告警身份由 Start 决定。
type Alert struct {
	Start   int64
	Trigger int64
	End     int64
	Count   int
}

// Diff 是一次为一次批量写入相对调用前状态的告警净差分。
// Added/Updated 携带完整新记录，Removed 携带旧告警记录（身份为 Start）。
type Diff struct {
	Added   []Alert
	Updated []Alert
	Removed []Alert
}

// Empty 报告差分是否为空。
func (d Diff) Empty() bool {
	return len(d.Added) == 0 && len(d.Updated) == 0 && len(d.Removed) == 0
}
