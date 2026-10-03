package vibralert

// Reading 是一条振动读数，Seq 为采样序号，Value 为振动值。
type Reading struct {
	Seq   int64
	Value float64
}

// Alert 表示一个连续越界段产生的告警。
// 告警身份由 StartSeq 决定。
type Alert struct {
	StartSeq   int64 // 段起始序号
	TriggerSeq int64 // 段内第 K 条读数的采样序号（首次达到 K 条时触发）
	EndSeq     int64 // 段末尾序号
	Count      int   // 段内读数数量
}

// ChangeKind 区分告警差分类型。
type ChangeKind int

const (
	AlertAdded ChangeKind = iota
	AlertUpdated
	AlertRemoved
)

func (k ChangeKind) String() string {
	switch k {
	case AlertAdded:
		return "added"
	case AlertUpdated:
		return "updated"
	case AlertRemoved:
		return "removed"
	}
	return "unknown"
}

// AlertChange 是一条告警差分。
// Added/Updated 的 Alert 为完整新记录；Removed 的 Alert 为被撤销的旧记录（身份即 StartSeq）。
type AlertChange struct {
	Kind  ChangeKind
	Alert Alert
}
