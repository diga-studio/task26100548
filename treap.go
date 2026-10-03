package vibralert

// treap 是一棵按 int64 键有序、带子树大小的随机平衡树，
// 支持 O(log n) 的插入、删除、查找、排名与前驱/后继查询。
type treapNode[V any] struct {
	key         int64
	val         V
	prio        uint64
	size        int
	left, right *treapNode[V]
}

type treap[V any] struct {
	root *treapNode[V]
	seed uint64
}

func newTreap[V any](seed uint64) *treap[V] {
	return &treap[V]{seed: seed}
}

// nextPrio 用 splitmix64 生成下一个随机优先级。
func (t *treap[V]) nextPrio() uint64 {
	t.seed += 0x9e3779b97f4a7c15
	z := t.seed
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
}

func nodeSize[V any](n *treapNode[V]) int {
	if n == nil {
		return 0
	}
	return n.size
}

func (n *treapNode[V]) update() {
	n.size = 1 + nodeSize(n.left) + nodeSize(n.right)
}

// Len 返回键值对数量。
func (t *treap[V]) Len() int { return nodeSize(t.root) }

// Get 返回键对应的值。
func (t *treap[V]) Get(key int64) (V, bool) {
	n := t.root
	for n != nil {
		switch {
		case key < n.key:
			n = n.left
		case key > n.key:
			n = n.right
		default:
			return n.val, true
		}
	}
	var zero V
	return zero, false
}

// Insert 插入或替换键对应的值。
func (t *treap[V]) Insert(key int64, val V) {
	prio := t.nextPrio()
	t.root = insertNode(t.root, key, val, prio)
}

func insertNode[V any](n *treapNode[V], key int64, val V, prio uint64) *treapNode[V] {
	if n == nil {
		return &treapNode[V]{key: key, val: val, prio: prio, size: 1}
	}
	switch {
	case key < n.key:
		n.left = insertNode(n.left, key, val, prio)
		if n.left.prio > n.prio {
			n = rotateRight(n)
		}
	case key > n.key:
		n.right = insertNode(n.right, key, val, prio)
		if n.right.prio > n.prio {
			n = rotateLeft(n)
		}
	default:
		n.val = val
	}
	n.update()
	return n
}

func rotateRight[V any](n *treapNode[V]) *treapNode[V] {
	l := n.left
	n.left = l.right
	l.right = n
	n.update()
	l.update()
	return l
}

func rotateLeft[V any](n *treapNode[V]) *treapNode[V] {
	r := n.right
	n.right = r.left
	r.left = n
	n.update()
	r.update()
	return r
}

// Delete 删除键，不存在时不做任何事。
func (t *treap[V]) Delete(key int64) {
	t.root = deleteNode(t.root, key)
}

func deleteNode[V any](n *treapNode[V], key int64) *treapNode[V] {
	if n == nil {
		return nil
	}
	switch {
	case key < n.key:
		n.left = deleteNode(n.left, key)
	case key > n.key:
		n.right = deleteNode(n.right, key)
	default:
		return mergeNodes(n.left, n.right)
	}
	n.update()
	return n
}

// mergeNodes 合并两棵键互不重叠（a 的键都小于 b）的子树。
func mergeNodes[V any](a, b *treapNode[V]) *treapNode[V] {
	switch {
	case a == nil:
		return b
	case b == nil:
		return a
	case a.prio > b.prio:
		a.right = mergeNodes(a.right, b)
		a.update()
		return a
	default:
		b.left = mergeNodes(a, b.left)
		b.update()
		return b
	}
}

// RankLE 返回键 <= key 的元素个数（即 key 的 1 基排名，若 key 存在）。
func (t *treap[V]) RankLE(key int64) int {
	rank := 0
	n := t.root
	for n != nil {
		switch {
		case key < n.key:
			n = n.left
		case key > n.key:
			rank += nodeSize(n.left) + 1
			n = n.right
		default:
			return rank + nodeSize(n.left) + 1
		}
	}
	return rank
}

// SelectAt 返回 1 基排名为 rank 的键值对；越界时 panic。
func (t *treap[V]) SelectAt(rank int) (int64, V) {
	n := t.root
	for n != nil {
		ls := nodeSize(n.left)
		switch {
		case rank <= ls:
			n = n.left
		case rank == ls+1:
			return n.key, n.val
		default:
			rank -= ls + 1
			n = n.right
		}
	}
	panic("vibralert: SelectAt rank out of range")
}

// Predecessor 返回严格小于 key 的最大键值对。
func (t *treap[V]) Predecessor(key int64) (int64, V, bool) {
	var best *treapNode[V]
	n := t.root
	for n != nil {
		if n.key < key {
			best = n
			n = n.right
		} else {
			n = n.left
		}
	}
	if best == nil {
		var zero V
		return 0, zero, false
	}
	return best.key, best.val, true
}

// Successor 返回严格大于 key 的最小键值对。
func (t *treap[V]) Successor(key int64) (int64, V, bool) {
	var best *treapNode[V]
	n := t.root
	for n != nil {
		if n.key > key {
			best = n
			n = n.left
		} else {
			n = n.right
		}
	}
	if best == nil {
		var zero V
		return 0, zero, false
	}
	return best.key, best.val, true
}

// Ascend 按键升序遍历，f 返回 false 时停止。
func (t *treap[V]) Ascend(f func(key int64, val V) bool) {
	ascendNode(t.root, f)
}

func ascendNode[V any](n *treapNode[V], f func(key int64, val V) bool) bool {
	if n == nil {
		return true
	}
	if !ascendNode(n.left, f) {
		return false
	}
	if !f(n.key, n.val) {
		return false
	}
	return ascendNode(n.right, f)
}
