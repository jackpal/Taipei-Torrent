package dht

import (
	"log"
)

type nTree struct {
	left, right, parent *nTree
	value               *DhtRemoteNode
}

func (n *nTree) insert(newNode *DhtRemoteNode) {
	id := newNode.id
	if len(id) != 20 {
		// Ignore nodes of unknown id.
		// They have to be re-added later.
		return
	}

	next := n
	var bit uint
	var chr byte
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
		// not replacing dupe node.
		return
	}
	next.value = newNode
}

func (n *nTree) lookupClosest(id string) []*DhtRemoteNode {
	// Find value, or neighbors up to GET_PEERS_NUM_NODES_RESPONSE.
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
	log.Println("found exact match for", id)
	return []*DhtRemoteNode{next.value}
}

func (n *nTree) reverse() []*DhtRemoteNode {
	ret := make([]*DhtRemoteNode, 0, GET_PEERS_NUM_NODES_RESPONSE)
	var back *nTree
	node := n

	for {
		if node.value != nil {
			panic("value should never be set somewhere above the tree")
		}
		if len(ret) >= GET_PEERS_NUM_NODES_RESPONSE {
			return ret
		}
		// Don't go down the same path we came from.
		if node.right != nil && node.right != back {
			ret = node.right.everything(ret)
		}
		if node.left != nil && node.left != back {
			ret = node.left.everything(ret)
		}
		if node.parent == nil {
			// Reached top of the reverse.
			break
		}
		back = node
		node = node.parent
	}
	return ret // Partial results :-(.
}

// recursive.
func (n *nTree) everything(ret []*DhtRemoteNode) []*DhtRemoteNode {
	if n.value != nil {
		return append(ret, n.value)
	}
	if len(ret) >= GET_PEERS_NUM_NODES_RESPONSE {
		return ret
	}
	if n.right != nil {
		ret = n.right.everything(ret)
	}
	if len(ret) >= GET_PEERS_NUM_NODES_RESPONSE {
		return ret
	}
	if n.left != nil {
		ret = n.left.everything(ret)
	}
	if len(ret) > GET_PEERS_NUM_NODES_RESPONSE {
		return ret[0:GET_PEERS_NUM_NODES_RESPONSE]
	}
	return ret
}
