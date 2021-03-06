package art

type iteratorLevel struct {
	node     *artNode
	childIdx int
}

type iterator struct {
	version int // tree version

	tree       *tree
	nextNode   *artNode
	prevNode   *artNode
	depthLevel int
	depth      []*iteratorLevel
}

type bufferedIterator struct {
	options  int
	nextNode Node
	prevNode Node
	err      error
	it       *iterator
}

func traverseOptions(opts ...int) int {
	options := 0
	for _, opt := range opts {
		options |= opt
	}
	options &= TraverseAll
	if options == 0 {
		// By default filter only leafs
		options = TraverseLeaf
	}
	return options
}

func traverseFilter(options int, callback Callback) Callback {
	if options == TraverseAll {
		return callback
	}

	return func(node Node) bool {
		if options&TraverseLeaf == TraverseLeaf && node.Kind() == Leaf {
			return callback(node)
		} else if options&TraverseNode == TraverseNode && node.Kind() != Leaf {
			return callback(node)
		}
		return true
	}
}

func (t *tree) ForEach(callback Callback, opts ...int) {
	options := traverseOptions(opts...)
	t.forEach(t.root, traverseFilter(options, callback))
}

func (t *tree) _forEach(children []*artNode, callback Callback) {
	for i, limit := 0, len(children); i < limit; i++ {
		child := children[i]
		if child != nil {
			t.forEach(child, callback)
		}
	}
}

func (t *tree) forEach(current *artNode, callback Callback) {
	if current == nil {
		return
	}

	if !callback(current) {
		return
	}

	switch current.kind {
	case Node4:
		t._forEach(current.node4().children[:], callback)

	case Node16:
		t._forEach(current.node16().children[:], callback)

	case Node48:
		node := current.node48()
		for i, limit := 0, len(node.keys); i < limit; i++ {
			idx := node.keys[byte(i)]
			if idx <= 0 {
				continue
			}
			child := node.children[idx-1]
			if child != nil {
				t.forEach(child, callback)
			}
		}

	case Node256:
		t._forEach(current.node256().children[:], callback)
	}
}

func (t *tree) ForEachPrefix(key Key, callback Callback) {
	t.forEachPrefix(t.root, key, callback)
}

func (t *tree) forEachPrefix(current *artNode, key Key, callback Callback) {
	if current == nil {
		return
	}

	depth := 0
	for current != nil {
		if current.isLeaf() {
			leaf := current.leaf()

			if leaf.prefixMatch(key) {
				callback(current)
			}
			return
		}

		if depth == len(key) {
			leaf := current.minimum()
			if leaf.prefixMatch(key) {
				t.forEach(current, callback)
			}

			return
		}

		node := current.node()
		if node.prefixLen > 0 {
			prefixLen := current.matchDeep(key, depth)

			if prefixLen > node.prefixLen {
				prefixLen = node.prefixLen
			}

			if prefixLen == 0 {
				return
			} else if depth+prefixLen == len(key) {
				t.forEach(current, callback)
				return
			}
			depth += node.prefixLen
		}

		// Find a child to recursive to
		next := current.findChild(key.charAt(depth))
		if *next == nil {
			return
		}
		current = *next
		depth++
	}
}

// Iterator pattern
func (t *tree) Iterator(opts ...int) Iterator {
	options := traverseOptions(opts...)

	it := &iterator{
		version:    t.version,
		tree:       t,
		nextNode:   t.root,
		prevNode:   t.root,
		depthLevel: 0,
		depth:      []*iteratorLevel{{t.root, 0}}}

	if options&TraverseAll == TraverseAll {
		return it
	}

	bti := &bufferedIterator{
		options: options,
		it:      it,
	}
	return bti
}

func (ti *iterator) checkConcurrentModification() error {
	if ti.version == ti.tree.version {
		return nil
	}

	return ErrConcurrentModification
}

func (ti *iterator) HasNext() bool {
	return ti != nil && ti.nextNode != nil
}

func (ti *iterator) HasPrev() bool {
	return ti != nil && ti.prevNode != nil
}

func (ti *iterator) Value() Value {
	if ti.HasNext() {
		return ti.nextNode.Value()
	}
	return nil
}

func (ti *iterator) Seek(key Key) {
	//	var otherNode *artNode
	current := ti.tree.root
	depth := 0
	for current != nil {
		if current.isLeaf() {
			ti.prevNode = current
			ti.nextNode = current
			return
		}
		node := current.node()
		if node.prefixLen > 0 {
			prefixLen := node.match(key, depth)
			if prefixLen != min(node.prefixLen, MaxPrefixLen) {
				ti.prevNode = current
				ti.nextNode = current
				return
			}
			depth += node.prefixLen
		}
		childIdx := current.index(key.charAt(depth))
		if childIdx < 0 {
			ti.prevNode = current
			ti.nextNode = current
			return
		}
		next := current.findChildByIndex(childIdx)

		if *next != nil {
			current = *next
			if ti.depthLevel+1 >= cap(ti.depth) {
				newDepthLevel := make([]*iteratorLevel, ti.depthLevel+2)
				copy(newDepthLevel, ti.depth)
				ti.depth = newDepthLevel
			}
			ti.depth[ti.depthLevel].childIdx = childIdx // should be the index of next node
			ti.depthLevel++
			ti.depth[ti.depthLevel] = &iteratorLevel{
				current,
				0}
		} else {
			// return current.minimum()
			ti.prevNode = current
			ti.nextNode = current
			return
		}
		depth += node.prefixLen
	}
}

func (ti *iterator) Next() (Node, error) {
	if !ti.HasNext() {
		return nil, ErrNoMoreNodes
	}

	err := ti.checkConcurrentModification()
	if err != nil {
		return nil, err
	}

	cur := ti.nextNode
	ti.next()
	return cur, nil
}

func (ti *iterator) Prev() (Node, error) {
	if !ti.HasPrev() {
		return nil, ErrNoMoreNodes
	}

	err := ti.checkConcurrentModification()
	if err != nil {
		return nil, err
	}

	cur := ti.prevNode
	ti.prev()
	return cur, nil
}

func nextChild(childIdx int, children []*artNode) (otherChildIdx int, otherNode *artNode) {
	otherChildIdx, otherNode = -1, nil
	i, nodeLimit := childIdx, len(children)
	for ; i < nodeLimit; i++ {
		child := children[i]
		if child != nil {
			otherChildIdx, otherNode = i, child
			break
		}
	}
	return
}

func prevChild(childIdx int, children []*artNode) (otherChildIdx int, otherNode *artNode) {
	otherChildIdx, otherNode = -1, nil

	for i := childIdx; i > 0; i-- {
		child := children[i]
		if child != nil {
			otherChildIdx, otherNode = i, child
			break
		}
	}
	return
}

func (ti *iterator) prev() {
	for {
		var otherNode *artNode
		otherChildIdx := -1

		prevNode := ti.depth[ti.depthLevel].node
		childIdx := ti.depth[ti.depthLevel].childIdx

		switch prevNode.kind {
		case Node4:
			otherChildIdx, otherNode = prevChild(childIdx, prevNode.node4().children[:])

		case Node16:
			otherChildIdx, otherNode = prevChild(childIdx, prevNode.node16().children[:])

		case Node48:
			node := prevNode.node48()
			for i := childIdx; i > 0; i-- {
				idx := node.keys[byte(i)]
				if idx <= 0 {
					continue
				}
				child := node.children[idx-1]
				if child != nil {
					otherChildIdx = i
					otherNode = child
					break
				}
			}

		case Node256:
			otherChildIdx, otherNode = prevChild(childIdx, prevNode.node256().children[:])
		}

		if otherNode == nil {
			if ti.depthLevel > 0 {
				// return to previous level
				ti.depthLevel--
			} else {
				ti.nextNode = ti.prevNode
				ti.prevNode = nil // done!
				return
			}
		} else {
			// star from the next when we come back from the child node
			ti.depth[ti.depthLevel].childIdx = otherChildIdx - 1
			ti.nextNode = ti.prevNode
			ti.prevNode = otherNode

			// make sure that w we have enough space for levels
			if ti.depthLevel+1 >= cap(ti.depth) {
				newDepthLevel := make([]*iteratorLevel, ti.depthLevel+2)
				copy(newDepthLevel, ti.depth)
				ti.depth = newDepthLevel
			}

			ti.depthLevel++
			ti.depth[ti.depthLevel] = &iteratorLevel{otherNode, 0}
			if otherNode.Kind() == Leaf{
				return
			}
		}
	}
}

func (ti *iterator) next() {
	for {
		var otherNode *artNode
		otherChildIdx := -1

		nextNode := ti.depth[ti.depthLevel].node
		childIdx := ti.depth[ti.depthLevel].childIdx

		switch nextNode.kind {
		case Node4:
			otherChildIdx, otherNode = nextChild(childIdx, nextNode.node4().children[:])

		case Node16:
			otherChildIdx, otherNode = nextChild(childIdx, nextNode.node16().children[:])

		case Node48:
			node := nextNode.node48()
			i, nodeLimit := childIdx, len(node.keys)
			for ; i < nodeLimit; i++ {
				idx := node.keys[byte(i)]
				if idx <= 0 {
					continue
				}
				child := node.children[idx-1]
				if child != nil {
					otherChildIdx = i
					otherNode = child
					break
				}
			}

		case Node256:
			otherChildIdx, otherNode = nextChild(childIdx, nextNode.node256().children[:])
		}

		if otherNode == nil {
			if ti.depthLevel > 0 {
				// return to previous level
				ti.depthLevel--
			} else {
				ti.prevNode = ti.nextNode
				ti.nextNode = nil // done!
				return
			}
		} else {
			// star from the next when we come back from the child node
			ti.depth[ti.depthLevel].childIdx = otherChildIdx + 1
			ti.prevNode = ti.nextNode
			ti.nextNode = otherNode

			// make sure that w we have enough space for levels
			if ti.depthLevel+1 >= cap(ti.depth) {
				newDepthLevel := make([]*iteratorLevel, ti.depthLevel+2)
				copy(newDepthLevel, ti.depth)
				ti.depth = newDepthLevel
			}

			ti.depthLevel++
			ti.depth[ti.depthLevel] = &iteratorLevel{otherNode, 0}
			if otherNode.Kind() == Leaf{
				return
			}
		}
	}
}

func (bti *bufferedIterator) HasNext() bool {
	for bti.it.HasNext() {
		bti.prevNode = bti.nextNode
		bti.nextNode, bti.err = bti.it.Next()
		if bti.err != nil {
			return true
		}
		if bti.options&TraverseLeaf == TraverseLeaf && bti.nextNode.Kind() == Leaf {
			return true
		} else if bti.options&TraverseNode == TraverseNode && bti.nextNode.Kind() != Leaf {
			return true
		}
	}
	bti.prevNode = bti.nextNode
	bti.nextNode = nil
	bti.err = nil
	return false
}

func (bti *bufferedIterator) Next() (Node, error) {
	return bti.nextNode, bti.err
}

func (bti *bufferedIterator) HasPrev() bool {
	for bti.it.HasPrev() {
		bti.nextNode = bti.prevNode
		bti.prevNode, bti.err = bti.it.Prev()
		if bti.err != nil {
			return true
		}
		if bti.options&TraverseLeaf == TraverseLeaf && bti.prevNode.Kind() == Leaf {
			return true
		} else if bti.options&TraverseNode == TraverseNode && bti.prevNode.Kind() != Leaf {
			return true
		}
	}
	bti.nextNode = bti.prevNode
	bti.prevNode = nil
	bti.err = nil
	return false
}

func (bti *bufferedIterator) Prev() (Node, error) {
	return bti.prevNode, bti.err
}

func (bti *bufferedIterator) Seek(key Key) {
	bti.it.Seek(key)
	bti.nextNode = bti.it.nextNode
	bti.prevNode = bti.it.prevNode
}

func (bti *bufferedIterator) Value()Value {
	if bti.nextNode != nil {
		return bti.nextNode.Value()
	}
	return nil
}
