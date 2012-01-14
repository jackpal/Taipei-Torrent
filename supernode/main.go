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
	// Add back the hosts we already knew, if any.
	for addr, id := range c.Remotes {
		if len(id) != 20 {
			dht.Ping(addr)
			continue
		}
		dht.RemoteNodeAcquaintance(&taipei.DhtNodeCandidate{string(id), addr})
	}

	for {
		// From test.torrent.
		dht.PeersRequest("\x66\xcb\x16\x1e\x27\xe5\xcd\x7c\x44\xab\x32\x38\x30\x67\x57\x68\xa2\x76\x01\x29")
		tbl := dht.RoutingTable()
		c.Remotes = tbl
		saveConfig(*c)
		time.Sleep(5 * time.Second)
	}
}
