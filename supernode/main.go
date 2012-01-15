// Stand-alone DHT node.

package main

import (
	"flag"
	"fmt"
	"log"
	"time"

	"taipei"
)

// command line.
const port = 63010

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
			dht.PeersRequest(infoHash, false)
		}
		// Assumes one result per request.
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

func drainresults(dht *taipei.DhtEngine) {
	for {
		<-dht.PeersRequestResults // blocks.
	}
}
