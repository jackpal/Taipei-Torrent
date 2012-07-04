package dht

import (
	"math/rand"
	"net"
	"testing"
	"time"

	"github.com/jackpal/Taipei-Torrent/bencode"
)

func startDhtNode(t *testing.T) *DhtEngine {
	port := rand.Intn(10000) + 40000
	node, err := NewDhtNode(port, 100, false)
	node.nodeId = "abcdefghij0123456789"
	if err != nil {
		t.Errorf("NewDhtNode(): %v", err)
	}
	go node.DoDht()
	return node
}

// Requires Internet access and can be flaky if the server or the internet is
// slow.
func TestDhtLarge(t *testing.T) {
	node := startDhtNode(t)
	realDHTNodes := []string{
		"1.a.magnets.im",
	}
	for _, addr := range realDHTNodes {
		ip, err := net.LookupHost(addr)
		if err != nil {
			t.Error(err)
			continue
		}
		node.ping(ip[0] + ":6881")
	}

	// Test that we can reach at least one node.
	success := false
	for i := 0; i < 10; i++ {
		// XXX: Not synchronized.
		tbl := node.routingTable.reachableNodes()
		if len(tbl) > 0 {
			t.Logf("Contacted %d DHT nodes.", len(tbl))
			success = true
			break
		}
		time.Sleep(time.Second)
	}
	if !success {
		t.Fatal("No external DHT node could be contacted.")
	}

	// Test that we can find peers for a known torrent in a timely fashion.
	//
	// Torrent from: http://www.clearbits.net/torrents/244-time-management-for-anarchists-1
	infoHash := "\xb4\x62\xc0\xa8\xbc\xef\x1c\xe5\xbb\x56\xb9\xfd\xb8\xcf\x37\xff\xd0\x2f\x5f\x59"
	go node.PeersRequest(infoHash, true)
	timeout := make(chan bool, 1)
	go func() {
		time.Sleep(10 * time.Second)
		timeout <- true
	}()
	var infoHashPeers map[string][]string
	select {
	case infoHashPeers = <-node.PeersRequestResults:
		t.Logf("Found %d peers.", len(infoHashPeers[infoHash]))
	case <-timeout:
		t.Fatal("Could not find new peers: timed out")
	}
	for ih, peers := range infoHashPeers {
		if infoHash != ih {
			t.Fatal("Unexpected infohash returned")
		}
		if len(peers) == 0 {
			t.Fatal("Could not find new torrent peers.")
		}
		for _, peer := range peers {
			t.Logf("peer found: %+v\n", bencode.BinaryToDottedPort(peer))
		}
	}
	t.Logf("=== Stats ===")
	t.Logf("totalReachableNodes: %v", totalReachableNodes)
	t.Logf("totalDupes: %v", totalDupes)
	t.Logf("totalPeers: %v", totalPeers)
	t.Logf("totalSentGetPeers: %v", totalSentGetPeers)
}

func init() {
	rand.Seed((time.Now().Unix() % (1e9 - 1)))
}
