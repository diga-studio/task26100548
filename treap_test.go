package vibralert

import (
	"math/rand"
	"sort"
	"testing"
)

func TestTreapBasicOps(t *testing.T) {
	tr := newTreap[int](1)
	keys := []int64{5, 3, 8, 1, 4, 7, 9, 0, 2, 6}
	for i, k := range keys {
		tr.Insert(k, i)
	}
	if tr.Len() != len(keys) {
		t.Fatalf("Len = %d, want %d", tr.Len(), len(keys))
	}
	// 替换已有键不增加数量
	tr.Insert(5, 999)
	if tr.Len() != len(keys) {
		t.Fatalf("Len after replace = %d", tr.Len())
	}
	if v, ok := tr.Get(5); !ok || v != 999 {
		t.Fatalf("Get(5) = %v,%v", v, ok)
	}
	// 排名与选择
	for rank := 1; rank <= 10; rank++ {
		k, v := tr.SelectAt(rank)
		if k != int64(rank-1) {
			t.Fatalf("SelectAt(%d) key = %d", rank, k)
		}
		if int(v) != int(k) && k != 5 {
			_ = v
		}
		if got := tr.RankLE(k); got != rank {
			t.Fatalf("RankLE(%d) = %d, want %d", k, got, rank)
		}
	}
	// 前驱后继
	if k, _, ok := tr.Predecessor(5); !ok || k != 4 {
		t.Fatalf("Predecessor(5) = %d,%v", k, ok)
	}
	if k, _, ok := tr.Successor(5); !ok || k != 6 {
		t.Fatalf("Successor(5) = %d,%v", k, ok)
	}
	if _, _, ok := tr.Predecessor(0); ok {
		t.Fatal("Predecessor(0) should be false")
	}
	if _, _, ok := tr.Successor(9); ok {
		t.Fatal("Successor(9) should be false")
	}
	// 遍历有序
	var got []int64
	tr.Ascend(func(k int64, _ int) bool { got = append(got, k); return true })
	if !sort.SliceIsSorted(got, func(i, j int) bool { return got[i] < got[j] }) || len(got) != 10 {
		t.Fatalf("Ascend = %v", got)
	}
	// 删除
	tr.Delete(5)
	if _, ok := tr.Get(5); ok {
		t.Fatal("Get(5) after delete")
	}
	if tr.Len() != 9 {
		t.Fatalf("Len after delete = %d", tr.Len())
	}
	if got := tr.RankLE(6); got != 6 {
		t.Fatalf("RankLE(6) after delete = %d, want 6", got)
	}
	tr.Delete(5) // 不存在，无影响
	if tr.Len() != 9 {
		t.Fatalf("Len after redundant delete = %d", tr.Len())
	}
}

func TestTreapRandomCrossCheck(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	tr := newTreap[int](7)
	ref := map[int64]int{}
	for i := 0; i < 20000; i++ {
		k := int64(rng.Intn(500))
		switch rng.Intn(3) {
		case 0:
			delete(ref, k)
			tr.Delete(k)
		default:
			v := rng.Int()
			ref[k] = v
			tr.Insert(k, v)
		}
	}
	if tr.Len() != len(ref) {
		t.Fatalf("Len = %d, want %d", tr.Len(), len(ref))
	}
	keys := make([]int64, 0, len(ref))
	for k := range ref {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	for i, k := range keys {
		if got := tr.RankLE(k); got != i+1 {
			t.Fatalf("RankLE(%d) = %d, want %d", k, got, i+1)
		}
		sk, sv := tr.SelectAt(i + 1)
		if sk != k || sv != ref[k] {
			t.Fatalf("SelectAt(%d) = %d,%d", i+1, sk, sv)
		}
	}
}
