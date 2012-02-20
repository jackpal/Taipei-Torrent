package dht

import (
	"crypto/rand"
	"fmt"
	"log"
	"testing"
)

func BenchmarkFindClosest(b *testing.B) {
	b.StopTimer()

	// 16 bytes prefix for very distant nodes.
	distant := "\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff"

	node, err := NewDhtNode("00bcdefghij01234567", 0, 1e7)
	if err != nil {
		log.Fatal(err)
	}

	// Add 100k nodes to the remote nodes slice.
	for i := 0; i < 100000; i++ {

		rId := make([]byte, 4)
		if _, err := rand.Read(rId); err != nil {
			log.Fatal("Couldnt produce random numbers for FindClosest:", err)
		}
		r, _ := node.newRemoteNode(distant+string(rId), ":0")
		if len(r.id) != 20 {
			log.Fatalf("DhtRemoteNode construction error, wrong len: want %d, got %d",
				20, len(r.id))
		}
		r.reachable = true
	}
	b.StartTimer()
	for i := 0; i < b.N; i++ {
		f := closestNodes(fmt.Sprintf("x%10v", i)+"xxxxxxxxx", node.tree, NUM_INCREMENTAL_NODE_QUERIES)
		if len(f) != NUM_INCREMENTAL_NODE_QUERIES {
			log.Fatalf("Missing results. Wanted %d, got %d", NUM_INCREMENTAL_NODE_QUERIES, len(f))
		}
	}
}

type testData struct {
	query string
	want  int // just the size.
}

func TestNodeDistance(t *testing.T) {
	zeros := "\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00"

	tree := &nTree{}

	nodes := []*DhtRemoteNode{
		{id: "FOOOOOOOOOOOOOOOOOOO", address: nil},
		{id: "FOOOOOOOOOOOOOOOOOO4", address: nil},
		{id: "FOOOOOOOOOOOOOOOO500", address: nil},
		{id: "FOOOOOOOOOOOOOOOOO70", address: nil},
		{id: "FOOOOOOOOOOOOOOOOO80", address: nil},
		{id: "FOOOOOOOOOOOOOOOOO90", address: nil},
		{id: "mnopqrstuvwxyz12345\x00", address: nil},
		{id: "mnopqrstuvwxyz12345\x01", address: nil},
		{id: "mnopqrstuvwxyz12345\x02", address: nil},
		{id: zeros, address: nil},
		{id: "bogus", address: nil},
		{id: "WEEEEEEEEEEEEEEEEEEE", address: nil},
	}
	for _, r := range nodes {
		r.reachable = true
		tree.insert(r)
	}

	tests := []testData{
		{"FOOOOOOOOOOOOOOOOOOO", 0}, // exact match.
		{"FOOOOOOOOOOOOOOOOOO1", 8},
		{"FOOOOOOOOOOOOOOOOO10", 8},
		{"FOOOOOOOOOOOOOOOOOO1", 8},
	}

	for _, r := range tests {
		_, neighbors := tree.lookupClosest(r.query)
		if len(neighbors) != r.want {
			t.Errorf("wanted len=%d, got len=%d", r.want, len(neighbors))
		}
	}

}

// Results for nictuku's machine:
//
// #1
// BenchmarkFindClosest	       1	7,020,661,000 ns/op
//
// #2 not-checked in attempt to use a trie. Not even correct.
// BenchmarkFindClosest	       1	1,072,682,000 ns/op
//
// #3 only compare bytes that we need. 
// BenchmarkFindClosest	       1	1,116,333,000 ns/op
//
// #4 using my new nTree (and a faster computer)
// BenchmarkFindClosest	  500000	      5064 ns/op
