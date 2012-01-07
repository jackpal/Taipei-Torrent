// DHT node for Taipei Torrent, for tracker-less peer information exchange.
//
// Status:
//  - able to get peers from the network
//  - uses a very simple routing table
//  - not able to _answer_ queries from remote nodes
//  - does not 'bucketize' the remote nodes
//  - does not announce torrents to the network.
//  - has only soft limits for memory growth.
//
// Usage: 
//
//  dhtNode := NewDhtNode("abcdefghij0123456789", port)  // Torrent node ID, UDP port.
//  go dhtNode.PeersRequest(infoHash)
//  -- wait --
//  infoHashPeers = <-node.PeersRequestResults
//
//  infoHashPeers will contain:
//  => map[string][]string
//  -> key = infoHash
//  -> value = slice of peer contacts in binary form. 
//
// Message types:
// - query
// - response
// - error
//
// RPCs:
//      ping:
//         see if node is reachable and save it on routing table.
//      find_node:
//	   run when DHT node count drops, or every X minutes. Just to
//   	   ensure our DHT routing table is still useful.
//      get_peers:
//	   the real deal. Iteratively queries DHT nodes and find new
//         sources for a particular infohash.
//	announce_peer:
//         announce that this node is downloading a torrent.
//
// Reference:
//     http://www.bittorrent.org/beps/bep_0005.html
//

package taipei

import (
	"errors"
	"expvar"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"net"
	"sort"
	"time"
)

const (
	// How many nodes to contact initially each time we are asked to find new torrent peers.
	NUM_INCREMENTAL_NODE_QUERIES = 5
	// If we have less than so peers for a particular node, be
	// aggressive about collecting new ones. Otherwise, wait for the
	// torrent client to ask us. (currently does not consider reachability).
	//MIN_INFOHASH_PEERS = 15
	// Consider a node stale if it has more than this number of oustanding queries from us.
	MAX_NODE_PENDING_QUERIES = 5
	// Ask the same infoHash to a node after a long time.
	MIN_SECONDS_NODE_REPEAT_QUERY = 30 * time.Minute
)

var dhtRouter string

func init() {
	flag.StringVar(&dhtRouter, "dhtRouter", "67.215.242.138:6881",
		"IP:Port address of the DHT router used to bootstrap the DHT network.")
}

// DhtEngine should be created by NewDhtNode(). It provides DHT features to a torrent client, such as finding new peers
// for torrent downloads without requiring a tracker. The client can only use the public (first letter uppercase)
// channels for communicating with the DHT goroutines.
type DhtEngine struct {
	peerID        string
	port          int
	remoteNodes   map[string]*DhtRemoteNode // key == address 
	infoHashPeers map[string]map[string]int // key1 == infoHash, key2 == address in binary form. value=ignored.

	// Public channels:
	remoteNodeAcquaintance chan *DhtNodeCandidate
	peersRequest           chan string
	PeersRequestResults    chan map[string][]string // key = infohash, v = slice of peers.
}

func NewDhtNode(nodeId string, port int) (node *DhtEngine, err error) {
	node = &DhtEngine{
		peerID:                 nodeId,
		port:                   port,
		remoteNodes:            make(map[string]*DhtRemoteNode),
		PeersRequestResults:    make(chan map[string][]string, 1),
		remoteNodeAcquaintance: make(chan *DhtNodeCandidate),
		peersRequest:           make(chan string, 1), // buffer to avoid deadlock.
		infoHashPeers:          make(map[string]map[string]int),
	}
	return
}

type DhtNodeCandidate struct {
	id      string
	address string
}

func (d *DhtEngine) newRemoteNode(id string, hostPort string) (r *DhtRemoteNode) {
	address, err := net.ResolveUDPAddr("udp", hostPort)
	if err != nil {
		log.Println(err)
		return nil
	}
	r = &DhtRemoteNode{
		address:        address,
		lastQueryID:    rand.Intn(255) + 1, // Doesn't have to be crypto safe.
		id:             id,
		localNode:      d,
		reachable:      false,
		pendingQueries: map[string]*queryType{},
		pastQueries:    map[string]*queryType{},
	}
	nodesVar.Add(hostPort, 1)
	return

}

// getRemodeNode returns the DhtRemoteNode with the provided address. Creates a new object if necessary.
func (d *DhtEngine) getOrCreateRemoteNode(address string) (r *DhtRemoteNode) {
	var ok bool
	if r, ok = d.remoteNodes[address]; !ok {
		r = d.newRemoteNode("", address)
		d.remoteNodes[address] = r
	}
	return r
}

func (d *DhtEngine) ping(address string) {
	// TODO: should translate to an IP first.
	r := d.getOrCreateRemoteNode(address)
	log.Printf("DHT: ping => %+v\n", r)
	t := r.newQuery("ping")

	//p, _ := r.encodedPing(t)
	//err = r.sendMsg(p)
	//if err != nil {
	//	log.Println("DHT: Handshake error with node", r.address, err.Error())
	//}

	queryArguments := map[string]interface{}{"id": r.localNode.peerID}
	query := queryMessage{t, "q", "ping", queryArguments}
	go sendMsg(d.port, r.address, query)
}

func (d *DhtEngine) getPeers(r *DhtRemoteNode, ih string) {
	totalGetPeers.Add(1)
	ty := "get_peers"
	transId := r.newQuery(ty)
	r.pendingQueries[transId].ih = ih
	queryArguments := map[string]interface{}{
		"id":        r.localNode.peerID,
		"info_hash": ih,
	}
	query := queryMessage{transId, "q", ty, queryArguments}
	go sendMsg(d.port, r.address, query)
}

// blocks.
func (d *DhtEngine) announcePeer(address *net.UDPAddr, ih string, token string) {
	r := d.getOrCreateRemoteNode(address.String())
	ty := "announce_peer"
	log.Printf("DHT: announce_peer => %v %x %x\n", address, ih, token)
	transId := r.newQuery(ty)
	queryArguments := map[string]interface{}{
		"id":        r.localNode.peerID,
		"info_hash": ih,
		"port":      d.port,
		"token":     token,
	}
	query := queryMessage{transId, "q", ty, queryArguments}
	go sendMsg(d.port, address, query)
}

// blocks.
func (d *DhtEngine) replyPing(addr *net.UDPAddr, response responseType) {
	//r := d.getOrCreateRemoteNode(address)
	log.Printf("DHT: reply ping => %v\n", addr)
	reply := pingReplyMessage{
		T: response.T,
		Y: "r",
		R: map[string]string{"id": d.peerID},
	}
	go sendMsg(d.port, addr, reply)
}

// PeersRequest tells the DHT to search for more peers for the infoHash
// provided. Must be called as a goroutine.
func (d *DhtEngine) PeersRequest(ih string) {
	// Signals the main DHT goroutine.
	d.peersRequest <- ih
}

func (d *DhtEngine) RemoteNodeAcquaintance(n *DhtNodeCandidate) {
	d.remoteNodeAcquaintance <- n
}

// DoDht is the DHT node main loop and should be run as a goroutine by the torrent client.
func (d *DhtEngine) DoDht() {
	socketChan := make(chan packetType)
	socket, err := listen(d.port)
	if err != nil {
		return
	}
	go readFromSocket(socket, socketChan)

	d.bootStrapNetwork()

	log.Println("DHT: Starting DHT node.")
	for {
		select {
		case helloNode := <-d.remoteNodeAcquaintance:
			// We've got a new node id. We need to:
			// - see if we know it already, skip accordingly.
			// - ping it and see if it's reachable. Ignore otherwise.
			// - save it on our list of good nodes.
			// - later, we'll implement bucketing, etc.
			if _, ok := d.remoteNodes[helloNode.id]; !ok {
				_ = d.newRemoteNode(helloNode.id, helloNode.address)
				d.ping(helloNode.address)
			}

		case needPeers := <-d.peersRequest:
			// torrent server is asking for more peers for a particular infoHash.  Ask the closest nodes for
			// directions. The goroutine will write into the PeersNeededResults channel.
			log.Println("DHT: torrent client asking for more peers. Calling GetPeers().")
			d.GetPeers(needPeers)
		case p := <-socketChan:
			if p.b[0] != 'd' {
				log.Println("DHT: UDP packet of unknown protocol")
				continue
			}
			r, err := readResponse(p)
			if err != nil {
				log.Printf("DHT: readResponse Error: %v, %q", err, string(p.b))
				continue
			}
			switch {
			// Response.
			case r.Y == "r":
				node, ok := d.remoteNodes[p.raddr.String()]
				if !ok {
					log.Println("DHT: Received reply from a host we don't know:", p.raddr)
					log.Println("DHT: -> ignoring. Details:", r, err)
					continue
				}
				// Fix the node ID.
				if node.id == "" {
					node.id = r.R.Id
				}
				if query, ok := node.pendingQueries[r.T]; ok {
					if !node.reachable {
						node.reachable = true
						totalReachableNodes.Add(1)
					}
					node.lastTime = time.Now()
					if _, ok := d.infoHashPeers[query.ih]; !ok {
						d.infoHashPeers[query.ih] = map[string]int{}
					}
					switch query.Type {
					case "ping", "announce_peer":
						// served its purpose, nothing else to be done.
					case "get_peers":
						d.processGetPeerResults(node, r)
					default:
						log.Println("DHT: Unknown query type:", query.Type, p.raddr)
					}
					node.pastQueries[r.T] = query
					delete(node.pendingQueries, r.T)
				} else {
					// XXX debugging.
					log.Println("DHT: Unknown query id:", r.T)
				}
			case r.Y == "q":
				log.Printf("DHT XXX query %q ==> %#v", p.b, r)
				switch r.Q {
				case "ping":
					d.replyPing(p.raddr, r)
				case "get_peers":
					x, _ := hashDistance(r.A.InfoHash, d.peerID)
					log.Printf("DHT XXXX get_peers not implemented. Host: %v, peerID: %x, InfoHash: %x, distance to me: %x", p.raddr, r.A.Id, r.A.InfoHash, x)
				default:
					log.Println("DHT XXX non-implemented handler for type", r.Q)
				}
			default:
				log.Printf("DHT: Bogus DHT query from %v.", p.raddr)
			}
		}
	}
}

// Process another node's response to a get_peers query. If the response contains peers, send them to the Torrent
// engine, our client, using the DhtEngine.PeersRequestResults channel. If it contains closest nodes, query them if we
// still need it.
// Also announce ourselves as a peer for that node.
func (d *DhtEngine) processGetPeerResults(node *DhtRemoteNode, resp responseType) {
	query, _ := node.pendingQueries[resp.T]
	d.announcePeer(node.address, query.ih, resp.R.Token)
	if resp.R.Values != nil {
		peers := make([]string, 0)
		for _, peerContact := range resp.R.Values {
			if _, ok := d.infoHashPeers[query.ih][peerContact]; !ok {
				// Finally, a new peer.
				d.infoHashPeers[query.ih][peerContact] = 0
				peers = append(peers, peerContact)
			}
		}
		if len(peers) > 0 {
			result := map[string][]string{query.ih: peers}
			totalPeers.Add(int64(len(peers)))
			log.Println("DHT: totalPeers:", totalPeers.String())
			d.PeersRequestResults <- result
		}
	}
	if resp.R.Nodes != "" {
		for id, address := range parseNodesString(resp.R.Nodes) {
			// XXX
			log.Printf("DHT: Got node reference: %x@%v from %x@%v.", id, address, node.id, node.address)
			// If it's in our routing table already, ignore it.
			if _, ok := d.remoteNodes[address]; ok {
				totalDupes.Add(1)
				d, _ := hashDistance(query.ih, node.id)
				log.Printf("distance to receiver node: %x", d)
				// XXX Gotta improve things so we stop receiving so many dupes. Waste.
				log.Println("DHT: total dupes:", totalDupes.String())
			} else {
				log.Println("DHT: and it is actually new. Interesting. LEN:", len(d.infoHashPeers[query.ih]))
				nr := d.newRemoteNode(id, address)
				d.remoteNodes[address] = nr
				if len(d.infoHashPeers[query.ih]) < TARGET_NUM_PEERS {
					d.GetPeers(query.ih)
				} else {
					log.Println("DHT: .. just saving in the routing table")
				}
			}
		}
	}
}

// Calculates the distance between two hashes. In DHT/Kademlia, "distance" is the XOR of the torrent infohash and the
// peer node ID.
func hashDistance(id1 string, id2 string) (distance string, err error) {
	d := make([]byte, 20)
	if len(id1) != 20 || len(id2) != 20 {
		err = errors.New(fmt.Sprintf("idDistance unexpected id length(s): %d %d", len(id1), len(id2)))
	} else {
		for i := 0; i < 20; i++ {
			d[i] = id1[i] ^ id2[i]
		}
		distance = string(d)
	}
	return
}

// Implements sort.Interface to find the closest nodes for a particular
// infoHash.
type nodeDistances struct {
	infoHash  string
	nodes     []*DhtRemoteNode
	distances map[string]string
}

func (n *nodeDistances) distance(i int) string {
	nid := n.nodes[i].id
	ret, ok := n.distances[nid]
	if !ok {
		var err error
		ret, err = hashDistance(n.infoHash, nid)
		if err != nil {
			log.Println("hashDistance err:", err)
			ret = "\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff\xff"
		}
		n.distances[nid] = ret
	}
	return ret
}

func (n *nodeDistances) Len() int {
	return len(n.nodes)
}
func (n *nodeDistances) Less(i, j int) bool {
	return n.distance(i) < n.distance(j)
}
func (n *nodeDistances) Swap(i, j int) {
	ni := n.nodes[i]
	nj := n.nodes[j]
	n.nodes[i] = nj
	n.nodes[j] = ni
}

// Asks for more peers for a torrent. Runs on the main dht goroutine so it must
// finish quickly. Currently this does not implement the official DHT routing
// table from the spec, but my own thing :-P.
//
// The basic principle is to store as many node addresses as possible, even if
// their hash is distant from other nodes we asked.
//
// It's probably very slow, but I dont need the performance right now.
func (d *DhtEngine) GetPeers(infoHash string) {
	ih := infoHash
	if d.remoteNodes == nil {
		log.Println("DHT: Error: no remote nodes are known yet.")
		return
	}
	// XXX: I shouldn't recalculate the distances after every GetPeers.
	targets := &nodeDistances{infoHash, make([]*DhtRemoteNode, 0, len(d.remoteNodes)), map[string]string{}}
	for _, r := range d.remoteNodes {
		// Skip nodes with pending queries. First, we don't want to flood them, but most importantly they are
		// probably unreachable. We just need to make sure we clean the pendingQueries map when appropriate.
		if len(r.pendingQueries) > MAX_NODE_PENDING_QUERIES {
			log.Println("DHT: Skipping because there are too many queries pending for this dude.")
			log.Println("DHT: This shouldn't happen because we should have stopped trying already. Might be a BUG.")
			for _, q := range r.pendingQueries {
				log.Printf("DHT: %v=>%x\n", q.Type, q.ih)
			}
			continue
		}
		// Skip if we are already asking them for this infoHash.
		skip := false
		for _, q := range r.pendingQueries {
			if q.Type == "get_peers" && q.ih == infoHash {
				skip = true
			}
		}
		// Skip if we asked for this infoHash recently.
		for _, q := range r.pastQueries {
			if q.Type == "get_peers" && q.ih == infoHash {
				ago := time.Now().Sub(r.lastTime)
				if ago < MIN_SECONDS_NODE_REPEAT_QUERY {
					skip = true
				} else {
					// This is an act of desperation. Query
					// them again.  Most likely this will
					// only generate dupes, but it's worth
					// a try.
					log.Printf("Re-sending get_peers. Last time: %v (%v ago) %v",
						r.lastTime.String(), ago.Seconds(), ago > 10*time.Second)
				}
			}
		}
		if !skip {
			targets.nodes = append(targets.nodes, r)
		}
	}
	log.Printf("DHT: Candidate nodes for asking: %d", len(targets.nodes))
	log.Printf("DHT: Currently know %d nodes", len(d.remoteNodes))

	sort.Sort(targets)
	for i := 0; i < NUM_INCREMENTAL_NODE_QUERIES && i < len(targets.nodes); i++ {
		r := targets.nodes[i]
		di, ok := targets.distances[r.id]
		if !ok {
			di, _ = hashDistance(r.id, ih)
		}
		log.Printf("target: %x, distance: %x", r.id, di)
		d.getPeers(r, ih)
	}
	log.Println("DHT: totalGetPeers", totalGetPeers.String())
}

// Debugging information:
// Which nodes we contacted.
var nodesVar = expvar.NewMap("Nodes")
var totalReachableNodes = expvar.NewInt("totalReachableNodes")
var totalDupes = expvar.NewInt("totalDupes")
var totalPeers = expvar.NewInt("totalPeers")
var totalGetPeers = expvar.NewInt("totalGetPeers")

func (d *DhtEngine) bootStrapNetwork() {
	d.ping(dhtRouter)
}

// TODO: Create a proper routing table with buckets, per the protocol.
// TODO: Save routing table on disk to be preserved between instances.
// TODO: Cleanup bad nodes from time to time.

// === Notes ==
//
// All methods of DhtRemoteNode run in a single goroutine so synchronization is not an issue. There are exceptions of methods
// that may run in their own goroutine. 
// - sendMsg()
// - readFromSocket()
//

func init() {
	//	DhtStats.engines = make([]*DhtEngine, 1, 10)
	//	expvar.Publish("dhtengine", expvar.StringFunc(dhtstats))
	//	expvar.NewMap("nodes")
}
