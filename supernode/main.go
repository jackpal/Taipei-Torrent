// Stand-alone DHT node.

package main

import (
	"log"
	"time"

	"taipei"
)

// command line.
const port = 63010

func main() {
	c := openConfig(port)
	if c.Id == "" {
		// TODO: Create a new node config.
		log.Fatal("Empty config file found.")
	}
	dht, err := taipei.NewDhtNode(c.Id, port)
	if err != nil {
		log.Println("DHT node creation error", err)
		return
	}
	go dht.DoDht()
	// Add back the hosts we already knew, if any.
	for addr, id := range c.Remotes {
		if len(id) != 20 {
			dht.Ping(addr)
			continue
		}
		dht.RemoteNodeAcquaintance(&taipei.DhtNodeCandidate{string(id), addr})
	}

	for {
		dht.PeersRequest("+bcdefghij0123456789")
		tbl := dht.RoutingTable()
		c.Remotes = tbl
		saveConfig(*c)
		time.Sleep(5 * time.Second)
	}
}
