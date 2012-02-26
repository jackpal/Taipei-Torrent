package dht

import (
	"crypto/rand"
	"fmt"
	"log"
	"sort"
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
		f := closestNodes(fmt.Sprintf("x%10v", i)+"xxxxxxxxx", node.tree, GET_PEERS_NUM_NODES_RESPONSE)
		if len(f) != GET_PEERS_NUM_NODES_RESPONSE {
			log.Fatalf("Missing results. Wanted %d, got %d", GET_PEERS_NUM_NODES_RESPONSE, len(f))
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
		{"FOOOOOOOOOOOOOOOOOOO", 1}, // exact match.
		{"FOOOOOOOOOOOOOOOOOO1", 8},
		{"FOOOOOOOOOOOOOOOOO10", 8},
		{"FOOOOOOOOOOOOOOOOOO1", 8},
	}

	for _, r := range tests {
		log.Printf("query: %v", r.query)
		distances := make([]string, 0, len(tests))
		neighbors := tree.lookupNeighbors(r.query)
		if len(neighbors) != r.want {
			t.Errorf("wanted len=%d, got len=%d", r.want, len(neighbors))
		}
		for _, x := range neighbors {
			d := hashDistance(r.query, x.id)
			distances = append(distances, d)
		}
		if !sort.StringsAreSorted(distances) {
			t.Errorf("Resulting distances for %v are not sorted", r.query)
		}
	}

}

// $ go test -v -bench='BenchmarkFindClosest' -run=NONE
//
// Results for nictuku's machine.
//
// #1
// BenchmarkFindClosest	       1	7020661000 ns/op
//
// #2 not-checked in attempt to use a trie. Not even correct.
// BenchmarkFindClosest	       1	1072682000 ns/op
//
// #3 only compare bytes that we need. 
// BenchmarkFindClosest	       1	1116333000 ns/op
//
// #4 moved to buckets, but using only one.
// BenchmarkFindClosest	       1	1170809000 ns/op
//
// #5 using my new nTree
// BenchmarkFindClosest	  100000	     27194 ns/op
