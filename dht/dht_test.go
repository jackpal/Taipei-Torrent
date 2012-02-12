package dht

import (
	"crypto/rand"
	"log"
	"reflect"
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
		f := closestNodes(string(i)+"xxxxxxxxxxxxxxxxxxx", node.nodes, NUM_INCREMENTAL_NODE_QUERIES)
		if len(f) != NUM_INCREMENTAL_NODE_QUERIES {
			log.Fatalf("Missing results. Wanted %d, got %d", NUM_INCREMENTAL_NODE_QUERIES, len(f))
		}
	}
}

func TestNodeDistance(t *testing.T) {
	zeros := "\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00"

	nd := &nodeDistances{"mnopqrstuvwxyz12345\x01", []*DhtRemoteNode{
		{id: "FOOOOOOOOOOOOOOOOOOO", address: nil},
		{id: "mnopqrstuvwxyz12345\x00", address: nil},
		{id: "mnopqrstuvwxyz12345\x01", address: nil}, // zeroDistance.
		{id: "mnopqrstuvwxyz12345\x02", address: nil},
		{id: zeros, address: nil},
		{id: "WEEEEEEEEEEEEEEEEEEE", address: nil}}}

	want := []string{
		"mnopqrstuvwxyz12345\x01",
		"mnopqrstuvwxyz12345\x00",
		"mnopqrstuvwxyz12345\x02",
		"FOOOOOOOOOOOOOOOOOOO",
		"WEEEEEEEEEEEEEEEEEEE",
		zeros}

	sort.Sort(nd)

	ids := make([]string, 0, len(nd.nodes))
	for i := 0; i < len(nd.nodes); i++ {
		ids = append(ids, nd.nodes[i].id)
	}

	if !reflect.DeepEqual(ids, want) {
		t.Errorf("wanted %#v, got %#v", want, ids)
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
