package vibalert

// treapNode 是按键排序的笛卡尔树节点，同时维护子树大小以支持
// 按序号求排名与按排名取节点。读数树与越界段树共用此结构。
type treapNode[V any] struct {
	key  int64
	val  V
	prio uint32
	size int
	l    *treapNode[V]
	r    *treapNode[V]
}

// treap 是按键排序的随机化笛卡尔树，期望 O(log n) 完成各操作。
type treap[V any] struct {
	root *treapNode[V]
	seed uint64
}

func newTreap[V any](seed uint64) *treap[V] {
	return &treap[V]{seed: seed}
}

func (t *treap[V]) nextPrio() uint32 {
	// splitmix64，取高 32 位作为随机优先级。
	t.seed += 0x9e3779b97f4a7c15
	z := t.seed
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	z = z ^ (z >> 31)
	return uint32(z >> 32)
}

func sizeOf[V any](n *treapNode[V]) int {
	if n == nil {
		return 0
	}
	return n.size
}

func (n *treapNode[V]) pull() {
	n.size = 1 + sizeOf(n.l) + sizeOf(n.r)
}

func rotateR[V any](n *treapNode[V]) *treapNode[V] {
	c := n.l
	n.l = c.r
	c.r = n
	n.pull()
	c.pull()
	return c
}

func rotateL[V any](n *treapNode[V]) *treapNode[V] {
	c := n.r
	n.r = c.l
	c.l = n
	n.pull()
	c.pull()
	return c
}

// get 返回键为 key 的节点，不存在时返回 nil。
func (t *treap[V]) get(key int64) *treapNode[V] {
	n := t.root
	for n != nil {
		switch {
		case key < n.key:
			n = n.l
		case key > n.key:
			n = n.r
		default:
			return n
		}
	}
	return nil
}

// upsert 插入或更新键为 key 的节点，返回 (节点, 是否为新插入)。
func (t *treap[V]) upsert(key int64, val V) (*treapNode[V], bool) {
	if n := t.get(key); n != nil {
		n.val = val
		return n, false
	}
	n := &treapNode[V]{key: key, val: val, prio: t.nextPrio(), size: 1}
	t.root = insertNode(t.root, n)
	return n, true
}

func insertNode[V any](root, n *treapNode[V]) *treapNode[V] {
	if root == nil {
		return n
	}
	if n.prio > root.prio {
		l, r := splitByKey(root, n.key)
		n.l, n.r = l, r
		n.pull()
		return n
	}
	if n.key < root.key {
		root.l = insertNode(root.l, n)
	} else {
		root.r = insertNode(root.r, n)
	}
	root.pull()
	return root
}

// splitByKey 将树按 key 拆为 (< key) 与 (>= key) 两棵。
func splitByKey[V any](root *treapNode[V], key int64) (l, r *treapNode[V]) {
	if root == nil {
		return nil, nil
	}
	if root.key < key {
		l, rr := splitByKey(root.r, key)
		root.r = l
		root.pull()
		return root, rr
	}
	ll, r := splitByKey(root.l, key)
	root.l = r
	root.pull()
	return ll, root
}

// remove 删除键为 key 的节点，返回是否删除成功。
func (t *treap[V]) remove(key int64) bool {
	if t.get(key) == nil {
		return false
	}
	t.root = removeNode(t.root, key)
	return true
}

func removeNode[V any](root *treapNode[V], key int64) *treapNode[V] {
	if root == nil {
		return nil
	}
	switch {
	case key < root.key:
		root.l = removeNode(root.l, key)
		root.pull()
		return root
	case key > root.key:
		root.r = removeNode(root.r, key)
		root.pull()
		return root
	default:
		return mergeTrees(root.l, root.r)
	}
}

func mergeTrees[V any](l, r *treapNode[V]) *treapNode[V] {
	if l == nil {
		return r
	}
	if r == nil {
		return l
	}
	if l.prio > r.prio {
		l.r = mergeTrees(l.r, r)
		l.pull()
		return l
	}
	r.l = mergeTrees(l, r.l)
	r.pull()
	return r
}

// rank 返回键为 key 的节点的排名（小于 key 的节点数）；键必须存在。
func (t *treap[V]) rank(key int64) int {
	rk := 0
	n := t.root
	for n != nil {
		switch {
		case key < n.key:
			n = n.l
		case key > n.key:
			rk += sizeOf(n.l) + 1
			n = n.r
		default:
			return rk + sizeOf(n.l)
		}
	}
	return -1
}

// at 返回排名为 i（0 起）的节点；i 越界时返回 nil。
func (t *treap[V]) at(i int) *treapNode[V] {
	n := t.root
	for n != nil {
		ls := sizeOf(n.l)
		switch {
		case i < ls:
			n = n.l
		case i == ls:
			return n
		default:
			i -= ls + 1
			n = n.r
		}
	}
	return nil
}

// pred 返回键严格小于 key 的最大节点，不存在时返回 nil。
func (t *treap[V]) pred(key int64) *treapNode[V] {
	var best *treapNode[V]
	n := t.root
	for n != nil {
		if n.key < key {
			best = n
			n = n.r
		} else {
			n = n.l
		}
	}
	return best
}

// succ 返回键严格大于 key 的最小节点，不存在时返回 nil。
func (t *treap[V]) succ(key int64) *treapNode[V] {
	var best *treapNode[V]
	n := t.root
	for n != nil {
		if n.key > key {
			best = n
			n = n.l
		} else {
			n = n.r
		}
	}
	return best
}

// floor 返回键小于等于 key 的最大节点，不存在时返回 nil。
func (t *treap[V]) floor(key int64) *treapNode[V] {
	var best *treapNode[V]
	n := t.root
	for n != nil {
		if n.key <= key {
			best = n
			n = n.r
		} else {
			n = n.l
		}
	}
	return best
}

// forEach 按键升序遍历所有节点。
func (t *treap[V]) forEach(fn func(key int64, val V)) {
	var walk func(n *treapNode[V])
	walk = func(n *treapNode[V]) {
		if n == nil {
			return
		}
		walk(n.l)
		fn(n.key, n.val)
		walk(n.r)
	}
	walk(t.root)
}

// len 返回节点总数。
func (t *treap[V]) len() int {
	return sizeOf(t.root)
}
