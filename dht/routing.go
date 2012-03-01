package dht

type nTree struct {
	zero, one *nTree
	value     *DhtRemoteNode
}

const (
	// Each query returns up to this number of nodes.
	kNodes = 8
)

func (n *nTree) insert(newNode *DhtRemoteNode) {
	id := newNode.id
	var bit uint8
	var chr uint8
	next := n
	for i := 0; i < len(id)*8; i++ {
		chr = byte(id[i/8])
		bit = byte(i % 8)
		if (chr<<bit)&128 != 0 {
			if next.one == nil {
				next.one = &nTree{}
			}
			next = next.one
		} else {
			if next.zero == nil {
				next.zero = &nTree{}
			}
			next = next.zero
		}
	}
	if next.value != nil && next.value.id == id {
		// There's already a node with this id. Keep.
		return
	}
	next.value = newNode
}

func (n *nTree) lookupNeighbors(id string) []*DhtRemoteNode {
	// Find value, or neighbors up to kNodes.
	ret := make([]*DhtRemoteNode, 0, kNodes)
	if n == nil {
		return nil
	}
	return n.traverse(id, 0, ret)
}

func (n *nTree) traverse(id string, i int, ret []*DhtRemoteNode) []*DhtRemoteNode {
	if n == nil {
		return ret
	}
	if n.value != nil {
		return append(ret, n.value)
	}
	if len(ret) >= kNodes {
		return ret
	}

	chr := byte(id[i/8])
	bit := byte(i % 8)

	// This is not needed, but it's clearer.
	var left, right *nTree

	if (chr<<bit)&128 != 0 {
		left = n.one
		right = n.zero
	} else {
		left = n.zero
		right = n.one
	}
	ret = left.traverse(id, i+1, ret)
	if len(ret) >= kNodes {
		return ret
	}
	return right.traverse(id, i+1, ret)
}

// Calculates the distance between two hashes. In DHT/Kademlia, "distance" is
// the XOR of the torrent infohash and the peer node ID.
// This is slower than necessary. Should only be used for displaying friendly messages.
func hashDistance(id1 string, id2 string) (distance string) {
	d := make([]byte, len(id1))
	if len(id1) != len(id2) {
		return ""
	} else {
		for i := 0; i < len(id1); i++ {
			d[i] = id1[i] ^ id2[i]
		}
		return string(d)
	}
	return ""
}
