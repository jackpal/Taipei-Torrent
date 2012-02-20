package dht

import (
	"errors"
	"fmt"
)

type nTree struct {
	left, right, parent *nTree
	value               *DhtRemoteNode
}

const (
	idLen = 20
	// Each query returns up to this number of nodes.
	kNodes = 8
)

func (n *nTree) insert(newNode *DhtRemoteNode) {
	id := newNode.id
	if len(id) != idLen {
		return
	}
	var bit uint
	var chr byte
	next := n
	for i := 0; i < len(id); i++ {
		chr = id[i]
		for bit = 0; bit < 8; bit++ {
			if chr>>bit&1 == 1 {
				if next.right == nil {
					next.right = &nTree{parent: next}
				}
				next = next.right
			} else {
				if next.left == nil {
					next.left = &nTree{parent: next}
				}
				next = next.left
			}
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
	next := n
	var bit uint
	var chr byte
	for i := 0; i < len(id); i++ {
		chr = id[i]
		for bit = 0; bit < 8; bit++ {
			if chr>>bit&1 == 1 {
				if next.right == nil {
					// Reached bottom of the match tree. Start going backwards.
					return next.left.reverse()
				}
				next = next.right
			} else {
				if next.left == nil {
					return next.right.reverse()
				}
				next = next.left
			}
		}
	}
	// Found exact match. Lookup neighbors anyway.

	neighbors := next.reverse()
	return append(neighbors, next.value)
}

func (n *nTree) reverse() []*DhtRemoteNode {
	ret := make([]*DhtRemoteNode, 0, kNodes)
	var back *nTree
	node := n

	if node == nil {
		return ret
	}

	for {
		if len(ret) >= kNodes {
			return ret
		}
		// Don't go down the same branch we came from.
		if node.right != nil && node.right != back {
			ret = node.right.everything(ret)
		} else if node.left != nil && node.left != back {
			ret = node.left.everything(ret)
		}
		if node.parent == nil {
			// Reached top of the tree.
			break
		}
		back = node
		node = node.parent
	}
	return ret // Partial results :-(.
}

// evertyhing traverses the whole tree and collects up to
// kNodes values, without any ordering guarantees.
func (n *nTree) everything(ret []*DhtRemoteNode) []*DhtRemoteNode {
	if n.value != nil {
		if n.value.reachable {
			return append(ret, n.value)
		}
		return ret
	}
	if len(ret) >= kNodes {
		goto RET
	}
	if n.right != nil {
		ret = n.right.everything(ret)
	}
	if n.left != nil {
		ret = n.left.everything(ret)
	}
RET:
	if len(ret) > kNodes {
		ret = ret[0:kNodes]
	}
	return ret
}

// Calculates the distance between two hashes. In DHT/Kademlia, "distance" is
// the XOR of the torrent infohash and the peer node ID.
// This is slower than necessary. Should only be used for displaying friendly messages.
func hashDistance(id1 string, id2 string) (distance string, err error) {
	d := make([]byte, 20)
	if len(id1) != 20 || len(id2) != 20 {
		err = errors.New(
			fmt.Sprintf("idDistance unexpected id length(s): %d %d", len(id1), len(id2)))
	} else {
		for i := 0; i < 20; i++ {
			d[i] = id1[i] ^ id2[i]
		}
		distance = string(d)
	}
	return
}
