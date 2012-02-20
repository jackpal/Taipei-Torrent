package dht

import (
	// debug "log"
	"math/rand"
	"net"
	"os"
	"testing"
	"time"
)

type pingTest struct {
	transId string
	nodeId  string
	out     string
}

func startDhtNode(t *testing.T) *DhtEngine {
	port := rand.Intn(10000) + 40000
	node, err := NewDhtNode("abcdefghij0123456789", port, 100)
	if err != nil {
		t.Errorf("NewDhtNode(): %v", err)
	}
	go node.DoDht()
	return node
}

func dumpStats() {
	// debug.Println("=== Stats ===")
	// debug.Println("totalReachableNodes", totalReachableNodes)
	// debug.Println("totalDupes", totalDupes)
	// debug.Println("totalPeers", totalPeers)
	// debug.Println("totalGetPeers", totalGetPeers)
}

// Requires Internet access.
func TestDhtBigAndSlow(t *testing.T) {
	// debug.Println("start node.")
	node := startDhtNode(t)
	// debug.Println("done start node.", node.port)
	realDHTNodes := map[string]string{
		//"DHT_ROUTER": "router.bittorrent.com",
		//"DHT_ROUTER": "cetico.org",
		"DHTROUTER__DHTROUTER": "65.99.215.8",
		// DHT test router.
		//"DHT_ROUTER": "dht.cetico.org:9660",
		//"DHT_ROUTER": "localhost:33149",
	}
	// make this a ping response instead.
	//for id, address := range realDHTNodes {
	for id, addr := range realDHTNodes {
		ip, err := net.LookupHost(addr)
		if err != nil {
			t.Error(err)
			continue
		}
		addr = ip[0] + ":6881"
		realDHTNodes[id] = addr
		candidate := &DhtNodeCandidate{Id: id, Address: addr}
		go node.RemoteNodeAcquaintance(candidate)
	}
	time.Sleep(1.5 * UDP_TIMEOUT)
	for _, address := range realDHTNodes {
		if _, ok := node.remoteNodes[address]; !ok {
			t.Fatalf("External DHT node not reachable: %s", address)
		}
	}
	// Bah, need to find a more permanent test torrent.
	// http://www.osst.co.uk/Download/DamnSmallLinux/current/dsl-4.4.10.iso.torrent
	infoHash := "\xa4\x1d\x1f\x89\x28\x64\x54\xb1\x8d\x8d\x4c\xb2\xe0\x2f\xfe\x11\x58\x74\x76\xc4"
	time.Sleep(1.5e9)
	go node.PeersRequest(infoHash, true)
	timeout := make(chan bool, 1)
	go func() {
		time.Sleep(10e9) // seconds
		timeout <- true
	}()
	var infoHashPeers map[string][]string
	select {
	case infoHashPeers = <-node.PeersRequestResults:
	case <-timeout:
		t.Fatal("could not find new torrent peers: timeout")
	}
	t.Logf("%d new torrent peers obtained.", len(infoHashPeers))
	for ih, peers := range infoHashPeers {
		if infoHash != ih {
			t.Fatal("Unexpected infohash returned")
		}
		if len(peers) == 0 {
			t.Fatal("Could not find new torrent peers.")
		}
		// for _, peer := range peers {
		// 	debug.Printf("peer found: %+v\n", bencode.BinaryToDottedPort(peer))
		// }
	}

	dumpStats()
	os.Exit(0)
}

func init() {
	rand.Seed((time.Now().Unix() % (1e9 - 1)))
}
