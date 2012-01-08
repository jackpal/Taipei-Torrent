package main

import (
	"log"
	"time"

	"taipei"
)

const port = 63010

func main() {
	c, err := openConfig(port)
	if err != nil {
		log.Println("openConfig():", err)
		return
	}
	dht, err := taipei.NewDhtNode(c.Id, c.Port)
	if err != nil {
		log.Println("DHT node creation error", err)
		return
	}
	go dht.DoDht()
	// Add back the hosts we already knew.
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
		time.Sleep(5*time.Second)
	}
}
