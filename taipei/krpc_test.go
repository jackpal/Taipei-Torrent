package taipei

import (
	"expvar"
	"log"
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
	node, err := NewDhtNode("abcdefghij0123456789", port)
	if err != nil {
		t.Errorf("NewDhtNode(): %v", err)
	}
	go node.DoDht()
	return node
}

var pingTests = []pingTest{
	pingTest{"XX", "abcdefghij0123456789", "d1:ad2:id20:abcdefghij0123456789e1:q4:ping1:t2:XX1:y1:qe"},
}

func TestPing(t *testing.T) {
	for _, p := range pingTests {
		node := startDhtNode(t)
		r := node.newRemoteNode("", "") // id, Address
		v, _ := r.encodedPing(p.transId)
		if v != p.out {
			t.Errorf("Ping(%s) = %s, want %s.", p.nodeId, v, p.out)
		}
	}
}

type getPeersTest struct {
	transId  string
	nodeId   string
	infoHash string
	out      string
}

var getPeersTests = []getPeersTest{
	getPeersTest{"aa", "abcdefghij0123456789", "mnopqrstuvwxyz123456",
		"d1:ad2:id20:abcdefghij01234567899:info_hash20:mnopqrstuvwxyz123456e1:q9:get_peers1:t2:aa1:y1:qe"},
}

func TestGetPeers(t *testing.T) {
	for _, p := range getPeersTests {
		n := startDhtNode(t)
		r := n.newRemoteNode("", "") // id, address
		v, _ := r.encodedGetPeers(p.transId, p.infoHash)
		if v != p.out {
			t.Errorf("GetPeers(%s, %s) = %s, want %s.", p.nodeId, p.infoHash, v, p.out)
		}
	}
}

func dumpStats() {
	log.Println("=== Stats ===")
	log.Printf("total nodes contacted =>  %v\n", expvar.Get("nodes"))
}

// Requires Internet access.
func TestDhtBigAndSlow(t *testing.T) {
	log.Println("start node.")
	node := startDhtNode(t)
	log.Println("done start node.")
	realDHTNodes := map[string]string{
		// We currently don't support hostnames.
		//"DHT_ROUTER": "router.bittorrent.com:6881",
		"DHT_ROUTER": "67.215.242.138:6881",
		// DHT test router.
		//"DHT_ROUTER": "dht.cetico.org:9660",
		//"DHT_ROUTER": "localhost:33149",
	}
	// make this a ping response instead.
	//for id, address := range realDHTNodes {
	for id, _ := range realDHTNodes {
		_, addrs := net.LookupHost("router.bittorrent.com")
		candidate := &DhtNodeCandidate{id: id, address: addrs[0] + ":6881"}
		node.RemoteNodeAcquaintance <- candidate
	}
	time.Sleep(1.5 * UDP_TIMEOUT)
	for _, address := range realDHTNodes {
		if _, ok := node.remoteNodes[address]; !ok {
			t.Fatalf("External DHT node not reachable: %s", address)
		}
	}
	// Test the needPeers feature using an Ubuntu image.
	// http://releases.ubuntu.com/9.10/ubuntu-9.10-desktop-i386.iso.torrent
	infoHash := string([]byte{0x98, 0xc5, 0xc3, 0x61, 0xd0, 0xbe, 0x5f,
		0x2a, 0x07, 0xea, 0x8f, 0xa5, 0x05, 0x2e,
		0x5a, 0xa4, 0x80, 0x97, 0xe7, 0xf6})
	time.Sleep(3e9)
	node.PeersRequest <- infoHash
	timeout := make(chan bool, 1)
	go func() {
		time.Sleep(5e9) // seconds
		timeout <- true
	}()
	var infoHashPeers map[string][]string
	select {
	case infoHashPeers = <-node.PeersRequestResults:
	case <-timeout:
		t.Fatal("could not find new torrent peers: timeout")
	}
	//time.Sleep(1e9)
	t.Logf("%d new torrent peers obtained.", len(infoHashPeers))
	for ih, peers := range infoHashPeers {
		if infoHash != ih {
			t.Fatal("Unexpected infohash returned")
		}
		if len(peers) == 0 {
			t.Fatal("Could not find new torrent peers.")
		}
		for _, peer := range peers {
			log.Printf("peer found: %+v\n", binaryToDottedPort(peer))
		}
	}

	dumpStats()
	os.Exit(0)
}

func init() {
	rand.Seed(int64(time.Now() % (1e9 - 1)))
}
