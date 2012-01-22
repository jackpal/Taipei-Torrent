package dht

import (
	"crypto/rand"
	"log"
	"testing"
)

func BenchmarkFindClosest(b *testing.B) {
	b.StopTimer()

	// 16 bytes prefix for very distant nodes.
	distant := "\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff"

	node, err := NewDhtNode("00bcdefghij01234567", 0, 1e9)
	if err != nil {
		log.Fatal(err)
	}

	// Add 10k nodes to the remote nodes slice.
	for i := 0; i < 10000; i++ {

		rId := make([]byte, 4)
		if _, err := rand.Read(rId); err != nil {
			log.Fatal("Couldnt produce random numbers for FindClosest:", err)
		}
		r := &DhtRemoteNode{
			id: distant + string(rId), address: nil}
		if len(r.id) != 20 {
			log.Fatalf("DhtRemoteNode construction error, wrong len: want %d, got %d",
				20, len(r.id))
		}
		node.remoteNodes["fakeIP-"+string(i)] = r
	}
	b.StartTimer()
	for i := 0; i < b.N; i++ {
		f := node.closestNodes("xxxxxxxxxxxxxxxxxxxx")
		if len(f) != NUM_INCREMENTAL_NODE_QUERIES {
			log.Fatalf("Missing results")
		}
	}
	b.StopTimer()
}

// Results for nictuku's machine:
//
// #1
// ok  	github.com/jackpal/Taipei-Torrent/dht	4.764s 
