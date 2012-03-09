// DHT routing using a binary tree and no buckets.
//
// Nodes have ids of 20-bytes. When looking up an infohash for itself or for a
// remote host, the nodes have to look in its routing table for the closest
// nodes and give out those.
//
// The distance between a node and an infohash is the XOR of the respective
// strings. This means that 'sorting' nodes only makes sense with an infohash
// as the pivot. You can't pre-sort nodes in any meaningful way.
//
// Most bittorrent/kademlia DHT implementations use a mix of bit-by-bit
// comparison with the usage of buckets. That works very well. But I wanted to
// try something different, that doesn't use buckets. So I use a simple binary tree.
//
// All nodes are inserted in the binary tree, with a fixed height of 160 (20
// bytes). To lookup an infohash, I do an inorder traversal using the infohash
// bit for each level.
//
// In most cases I'll reach the end of the tree without a precise hits, but I
// simply continue the in-order traversal (but then to the 'left') and return
// after I collect the 8 closest nodes.
//
// I don't know how slow this is compared to a implementation that uses
// buckets. It's not slow as you would expect for a recursion of 160 levels,
// and it's definitely more correct (the nodes in a bucket may or may not be
// the closest that one knows for a particular infohash).
//
// TODO: Compress the tree since I don't actually need to have all 160 levels.
package dht

import (
	"time"
)

type nTree struct {
	zero, one *nTree
	value     *DhtRemoteNode
}

const (
	// Each query returns up to this number of nodes.
	kNodes = 8
	// Ask the same infoHash to a node after a long time.
	getPeersRetryPeriod = 30 * time.Minute
	// Consider a node stale if it has more than this number of oustanding
	// queries from us.
	maxNodePendingQueries = 5
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

func (n *nTree) lookup(id string) []*DhtRemoteNode {
	ret := make([]*DhtRemoteNode, 0, kNodes)
	if n == nil || id == "" {
		return nil
	}
	return n.traverse(id, 0, ret, false)
}

func (n *nTree) lookupFiltered(id string) []*DhtRemoteNode {
	ret := make([]*DhtRemoteNode, 0, kNodes)
	if n == nil || id == "" {
		return nil
	}
	return n.traverse(id, 0, ret, true)
}

func (n *nTree) traverse(id string, i int, ret []*DhtRemoteNode, filter bool) []*DhtRemoteNode {
	if n == nil {
		return ret
	}
	if n.value != nil {
		if !filter || n.filter(id) {
			return append(ret, n.value)
		}
	}
	if i >= len(id)*8 {
		return ret
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

	ret = left.traverse(id, i+1, ret, filter)
	if len(ret) >= kNodes {
		return ret
	}
	return right.traverse(id, i+1, ret, filter)
}

// cut goes down the tree and deletes the children nodes if all their leaves
// became empty.
func (n *nTree) cut(id string, i int) (cutMe bool) {
	if n == nil {
		return true
	}
	if i >= len(id)*8 {
		return true
	}
	chr := byte(id[i/8])
	bit := byte(i % 8)

	if (chr<<bit)&128 != 0 {
		if n.one.cut(id, i+1) {
			n.one = nil
			if n.zero == nil {
				return true
			}
		}
	} else {
		if n.zero.cut(id, i+1) {
			n.zero = nil
			if n.one == nil {
				return true
			}
		}
	}

	return false
}

func (n *nTree) filter(ih string) bool {
	if n.value == nil || n.value.id == "" {
		return false
	}
	r := n.value

	if len(r.pendingQueries) > maxNodePendingQueries {
		// debug.Println("DHT: Skipping because there are too many queries pending for this dude.")
		// debug.Println("DHT: This shouldn't happen because we should have stopped trying already. Might be a BUG.")
		return false
	}
	for _, q := range r.pendingQueries {
		if q.Type == "get_peers" && q.ih == ih {
			return false
		}
	}
	// Skip if we asked for this infoHash recently.
	for _, q := range r.pastQueries {
		if q.Type == "get_peers" && q.ih == ih {
			ago := time.Now().Sub(r.lastTime)
			if ago < getPeersRetryPeriod {
				return false
			} else {
				// This is an act of desperation. Query
				// them again.  Most likely this will
				// only generate dupes, but it's worth
				// a try.
				// debug.Printf("Re-sending get_peers. Last time: %v (%v ago) %v", r.lastTime.String(), ago.Seconds(), ago > 10*time.Second)
			}
		}
	}
	return true
}

// Calculates the distance between two hashes. In DHT/Kademlia, "distance" is
// the XOR of the torrent infohash and the peer node ID.  This is slower than
// necessary. Should only be used for displaying friendly messages.
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
