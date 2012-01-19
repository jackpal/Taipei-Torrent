// Stand-alone DHT node.
//
// Creates a single DHT node on the specified port. If an infoHash is specified
// in the commandline, start spamming the network asking for it. The more hosts
// we contact, the more hosts will know about us which increases our
// popularity. Well, that's the theory at least.
//
// Example usage:
//
// - Passive mode:
// $ ./supernode
//
// - Aggressive mode:
// $ ./supernode -infoHash=31176c3be58352326464f161c407320412bb952d
//
// Remember to open up any NAT ports yourself since our UPnP support doesn't
// work very well.

package main

import (
	"flag"
	"fmt"
	"log"
	"time"

	"taipei"
)

const (
	// XXX maybe use taipei.port instead.
	port = 63010
	// I tried avoiding this, since it would save some work/bandwidth and
	// because it's somewhat hurtful to the network. But the network becames
	// very quiet if we don't do it.
	sendAnnouncements = true
)

var infoHashFlag string

func init() {
	flag.StringVar(&infoHashFlag, "infoHash", "", "Infohash to query frequently.")
}

func main() {
	var infoHash string
	flag.Parse()

	if infoHashFlag != "" {
		_, err := fmt.Sscanf(infoHashFlag, "%x", &infoHash)
		if err != nil {
			log.Fatal("infoHash:", err.Error())
		}
		if len(infoHash) != 20 {
			log.Fatal("len(infoHash): got %d, want %d", len(infoHash), 20)
		}
		log.Printf("infoHash: %x", infoHash)
	}

	c := openConfig(port)
	if len(c.Id) != 20 {
		// TODO: Create a new node config.
		log.Fatal("Bogus config file found. c.Id:", c.Id, len(c.Id))
	}
	dht, err := taipei.NewDhtNode(c.Id, port)
	if err != nil {
		log.Println("DHT node creation error", err)
		return
	}
	go dht.DoDht()
	go drainresults(dht)
	reconnect(dht, c)

	for {
		if infoHash != "" {
			dht.PeersRequest(infoHash, sendAnnouncements)
		}
		tbl := dht.RoutingTable()
		c.Remotes = tbl
		saveConfig(*c)
		time.Sleep(5 * time.Second)
	}
}

// Add back the hosts we already knew, if any.
func reconnect(dht *taipei.DhtEngine, c *DhtConfig) {
	for addr, id := range c.Remotes {
		if len(id) != 20 {
			dht.Ping(addr)
		} else {
			dht.RemoteNodeAcquaintance(&taipei.DhtNodeCandidate{string(id), addr})
		}
	}
}

// drainresults loops, constantly reading any new peer information sent by the
// DHT node and just ignoring them. We don't care about those :-P.
func drainresults(dht *taipei.DhtEngine) {
	for {
		<-dht.PeersRequestResults
	}
}
