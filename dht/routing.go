package dht

import (
	"errors"
	"fmt"
	"log"
	"sort"
	"time"
)

const (
	bucketLen = 8
	zero      = "\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00"
)

type bucket struct {
	id    string // XXX change to []byte, or an infohash type.
	nodes []*DhtRemoteNode
}

type routingTable struct {
	buckets []bucket
	// XXX Stop using a single bucket.
	FOREVERALONE bucket
}

func newRoutingTable() *routingTable {
	t := &routingTable{buckets: make([]bucket, 0, 20)}
	b := bucket{id: zero, nodes: make([]*DhtRemoteNode, 0, bucketLen)}
	t.FOREVERALONE = b
	// unused.
	t.buckets = append(t.buckets, b)
	return t
}

func (t *routingTable) insert(n *DhtRemoteNode) {
	t.FOREVERALONE.nodes = append(t.FOREVERALONE.nodes, n)
}

func (t *routingTable) closestNodes(ih string) []*DhtRemoteNode {
	if len(ih) != 20 {
		log.Fatalf("Programming error, bogus infohash: len(%v)=%d", ih, len(ih))
	}

	closest := make([]*DhtRemoteNode, 0, bucketLen)

	distances := &nodeDistances{ih, t.FOREVERALONE.nodes}

	// XXX Shouldn't do this every time.
	sort.Sort(distances)

	for i := 0; len(closest) < bucketLen && i < len(distances.nodes); i++ {
		// Skip nodes with pending queries. First, we don't want to flood them, but most importantly they are
		// probably unreachable. We just need to make sure we clean the pendingQueries map when appropriate.
		r := distances.nodes[i]
		if !r.reachable {
			continue
		}
		if len(r.id) != 20 {
			log.Fatalf("Programming error, bogus infohash: len(%v)=%d", r.id, len(r.id))
		}

		if len(r.pendingQueries) > MAX_NODE_PENDING_QUERIES {
			// debug.Println("DHT: Skipping because there are too many queries pending for this dude.")
			// debug.Println("DHT: This shouldn't happen because we should have stopped trying already. Might be a BUG.")
			continue
		}
		// Skip if we are already asking them for this infoHash.
		skip := false
		for _, q := range r.pendingQueries {
			if q.Type == "get_peers" && q.ih == ih {
				skip = true
			}
		}
		// Skip if we asked for this infoHash recently.
		for _, q := range r.pastQueries {
			if q.Type == "get_peers" && q.ih == ih {
				ago := time.Now().Sub(r.lastTime)
				if ago < MIN_SECONDS_NODE_REPEAT_QUERY {
					skip = true
				} else {
					// This is an act of desperation. Query
					// them again.  Most likely this will
					// only generate dupes, but it's worth
					// a try.
					// debug.Printf("Re-sending get_peers. Last time: %v (%v ago) %v", r.lastTime.String(), ago.Seconds(), ago > 10*time.Second)
				}
			}
		}
		if !skip {
			closest = append(closest, r)
		}
	}
	// debug.Printf("DHT: Candidate nodes for asking: %d", len(targets.nodes))
	// debug.Printf("DHT: Currently know %d nodes", len(d.remoteNodes))
	return closest
	// debug.Println("DHT: totalSentGetPeers", totalSentGetPeers.String())
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

// Implements sort.Interface to find the closest nodes for a particular
// infoHash.
type nodeDistances struct {
	infoHash string
	nodes    []*DhtRemoteNode
}

func (n *nodeDistances) Len() int {
	return len(n.nodes)
}
func (n *nodeDistances) Less(i, j int) bool {
	xor1 := n.nodes[i].id
	xor2 := n.nodes[j].id
	return xorcmp(xor1, xor2, n.infoHash)
}

func (n *nodeDistances) Swap(i, j int) {
	n.nodes[i], n.nodes[j] = n.nodes[j], n.nodes[i]
}

func xorcmp(xor1, xor2, ref string) bool {
	// If xor1 or xor2 have bogus lengths, move them to the last position.
	if len(xor1) != 20 {
		return false // Causes a swap, moving it to last.
	}
	if len(xor2) != 20 {
		return true
	}
	// Inspired by dht.c from Juliusz Chroboczek.
	for i := 0; i < 20; i++ {
		if xor1[i] == xor2[i] {
			continue
		}
		return xor1[i]^ref[i] < xor2[i]^ref[i]
	}
	// Identical infohashes.
	return false
}


