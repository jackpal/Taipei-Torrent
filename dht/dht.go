// DHT node for Taipei Torrent, for tracker-less peer information exchange.
//
// Status:
//  - able to get peers from the network
//  - uses a very simple and slow routing table
//  - almost never removes nodes from the routing table.
//  - does not proactively ping nodes to see if they are reachable.
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
//	       run when DHT node count drops, or every X minutes. Just to ensure
//	       our DHT routing table is still useful.
//      get_peers:
//	       the real deal. Iteratively queries DHT nodes and find new sources
//	       for a particular infohash.
//	announce_peer:
//         announce that the peer associated with this node is downloading a
//         torrent.
//
// Reference:
//     http://www.bittorrent.org/beps/bep_0005.html
//

// TODO: Create a proper routing table with buckets, per the protocol.
// TODO: Save routing table on disk to be preserved between instances.
// TODO: Cleanup bad nodes from time to time.

// All methods of DhtRemoteNode (supposedly) run in a single thread so there
// should be no races. We use auxiliary goroutines for IO and they communicate
// with the main goroutine via channels.

// Now for the true story: there are a few methods of DhtRemoteNode which can
// be called by the client (different thread) and are clearly unsafe. They will
// be fixed in time :-).

package dht

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

	"github.com/jackpal/Taipei-Torrent/bencode"
)

const (
	// How many nodes to contact initially each time we are asked to find new torrent peers.
	NUM_INCREMENTAL_NODE_QUERIES = 5

	// If we have less than so peers for a particular node, be
	// aggressive about collecting new ones. Otherwise, wait for the
	// torrent client to ask us. (currently does not consider reachability).
	// MIN_INFOHASH_PEERS = 15

	// Consider a node stale if it has more than this number of oustanding queries from us.
	MAX_NODE_PENDING_QUERIES = 5

	// Ask the same infoHash to a node after a long time.
	MIN_SECONDS_NODE_REPEAT_QUERY = 30 * time.Minute

	GET_PEERS_NUM_NODES_RESPONSE = 8
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
	peerID           string
	port             int
	remoteNodes      map[string]*DhtRemoteNode // key == address 
	infoHashPeers    map[string]map[string]int // key1 == infoHash, key2 == address in binary form. value=ignored.
	activeInfoHashes map[string]bool           // infoHashes for which we are peers.
	targetNumPeers   int

	// Public channels:
	remoteNodeAcquaintance chan *DhtNodeCandidate
	peersRequest           chan string
	PeersRequestResults    chan map[string][]string // key = infohash, v = slice of peers.
}

func NewDhtNode(nodeId string, port, targetNumPeers int) (node *DhtEngine, err error) {
	node = &DhtEngine{
		peerID:                 nodeId,
		port:                   port,
		remoteNodes:            make(map[string]*DhtRemoteNode),
		PeersRequestResults:    make(chan map[string][]string, 1),
		remoteNodeAcquaintance: make(chan *DhtNodeCandidate),
		peersRequest:           make(chan string, 1), // buffer to avoid deadlock.
		infoHashPeers:          make(map[string]map[string]int),
		activeInfoHashes:       make(map[string]bool),
		targetNumPeers:         targetNumPeers,
	}
	return
}

type DhtNodeCandidate struct {
	Id      string
	Address string
}

// PeersRequest tells the DHT to search for more peers for the infoHash
// provided. active should be true if the connected peer is actively download
// this infohash. False should be used for when the DHT node is just a probe
// and shouldn't send announce_peer.
// Must be called as a goroutine.
// XXX unsafe.
func (d *DhtEngine) PeersRequest(ih string, active bool) {
	if active {
		d.activeInfoHashes[ih] = true
	}
	// Signals the main DHT goroutine.
	d.peersRequest <- ih
}

func (d *DhtEngine) RemoteNodeAcquaintance(n *DhtNodeCandidate) {
	d.remoteNodeAcquaintance <- n
}

// XXX unsafe.
func (d *DhtEngine) Ping(address string) {
	// TODO: should translate to an IP first.
	r, err := d.getOrCreateRemoteNode(address)
	if err != nil {
		log.Println("ping:", err)
		return
	}
	log.Printf("DHT: ping => %+v\n", address)
	t := r.newQuery("ping")

	queryArguments := map[string]interface{}{"id": r.localNode.peerID}
	query := queryMessage{t, "q", "ping", queryArguments}
	go sendMsg(d.port, r.address, query)
}

// RoutingTable outputs the routing table. Needed for persisting the table
// between sessions.
// XXX unsafe.
func (d *DhtEngine) RoutingTable() (tbl map[string][]byte) {
	tbl = make(map[string][]byte)
	for addr, r := range d.remoteNodes {
		if r.reachable && len(r.id) == 20 {
			tbl[addr] = []byte(r.id)
		}
	}
	return
}

// Asks for more peers for a torrent. Runs on the main dht goroutine so it must
// finish quickly. Currently this does not implement the official DHT routing
// table from the spec, but my own stupid thing :-P.
//
// The basic principle is to store as many node addresses as possible, even if
// their hash is distant from other nodes we asked.
//
// It's very slow, but I dont need the performance right now.  .. although it
// runs in the same goroutine, so it must be fast, or I should
// move this to another routine. Or live with it.
//
// XXX called by client. Unsafe.
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
		log.Printf("send get_peers. target: %x, distance: %x", r.id, di)
		d.getPeers(r, ih)
	}
	log.Println("DHT: totalSentGetPeers", totalSentGetPeers.String())
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
			if _, ok := d.remoteNodes[helloNode.Id]; !ok {
				if _, err := d.newRemoteNode(helloNode.Id, helloNode.Address); err != nil {
					log.Println("newRemoteNode:", err)
				} else {
					d.Ping(helloNode.Address)
				}
			}

		case needPeers := <-d.peersRequest:
			// torrent server is asking for more peers for a particular infoHash.  Ask the closest nodes for
			// directions. The goroutine will write into the PeersNeededResults channel.
			log.Printf("DHT: torrent client asking more peers for %x. Calling GetPeers().", needPeers)
			d.GetPeers(needPeers)
		case p := <-socketChan:
			if p.b[0] != 'd' {
				// UDP packet of unknown protocol.
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
				switch r.Q {
				case "ping":
					d.replyPing(p.raddr, r)
				case "get_peers":
					d.replyGetPeers(p.raddr, r)
				case "find_node":
					d.replyFindNode(p.raddr, r)
				default:
					log.Println("DHT XXX non-implemented handler for type", r.Q)
				}
			default:
				log.Printf("DHT: Bogus DHT query from %v.", p.raddr)
			}
		}
	}
}

func (d *DhtEngine) newRemoteNode(id string, hostPort string) (r *DhtRemoteNode, err error) {
	address, err := net.ResolveUDPAddr("udp", hostPort)
	if err != nil {
		return nil, err
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

// getOrCreateRemoteNode returns the DhtRemoteNode with the provided address.
// Creates a new object if necessary.
func (d *DhtEngine) getOrCreateRemoteNode(address string) (r *DhtRemoteNode, err error) {
	var ok bool
	if r, ok = d.remoteNodes[address]; !ok {
		r, err = d.newRemoteNode("", address)
		if err != nil {
			return nil, err
		}
		d.remoteNodes[address] = r
	}
	return
}

func (d *DhtEngine) getPeers(r *DhtRemoteNode, ih string) {
	totalSentGetPeers.Add(1)
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

func (d *DhtEngine) announcePeer(address *net.UDPAddr, ih string, token string) {
	r, err := d.getOrCreateRemoteNode(address.String())
	if err != nil {
		log.Println("announcePeer:", err)
		return
	}
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

func (d *DhtEngine) replyGetPeers(addr *net.UDPAddr, r responseType) {
	totalRecvGetPeers.Add(1)
	x, _ := hashDistance(r.A.InfoHash, d.peerID)
	log.Printf("DHT XXXX get_peers. Host: %v , peerID: %x , InfoHash: %x , distance to me: %x", addr, r.A.Id, r.A.InfoHash, x)

	ih := r.A.InfoHash
	r0 := map[string]interface{}{"id": ih, "token": r.A.Token}
	reply := replyMessage{
		T: r.T,
		Y: "r",
		R: r0,
	}

	if peers, ok := d.infoHashPeers[ih]; ok {
		peerContacts := make([]string, 0, len(peers))
		for p, _ := range peers {
			peerContacts = append(peerContacts, p)
		}
		log.Printf("replyGetPeers: Giving peers! %v wanted %x, and we knew %d peers!",
			addr.String(), ih, len(peerContacts))
		reply.R["values"] = peerContacts
	} else {
		targets := &nodeDistances{ih, make([]*DhtRemoteNode, 0, len(d.remoteNodes)), map[string]string{}}
		for _, r := range d.remoteNodes {
			if r.reachable {
				targets.nodes = append(targets.nodes, r)
			}
		}
		sort.Sort(targets)
		n := make([]string, 0, GET_PEERS_NUM_NODES_RESPONSE)
		for i, r := range targets.nodes {
			if i == GET_PEERS_NUM_NODES_RESPONSE {
				break
			}
			n = append(n, r.id+bencode.DottedPortToBinary(r.address.String()))
			log.Printf("replyGetPeers: [%d] distance %x", i, targets.distances[r.id])
		}
		log.Printf("replyGetPeers: Nodes only. Giving %d", len(n))
		reply.R["nodes"] = n
	}
	go sendMsg(d.port, addr, reply)

}

func (d *DhtEngine) replyFindNode(addr *net.UDPAddr, r responseType) {
	totalRecvFindNode.Add(1)
	x, _ := hashDistance(r.A.Target, d.peerID)
	log.Printf("DHT XX find_node. Host: %v , peerID: %x , nodeID: %x , distance to me: %x", addr, r.A.Id, r.A.Target, x)

	node := r.A.Target
	r0 := map[string]interface{}{"id": node}
	reply := replyMessage{
		T: r.T,
		Y: "r",
		R: r0,
	}

	// XXX we currently can't give out the peer contact. Probably requires processing announce_peer.
	// XXX Also needs optimization.
	targets := &nodeDistances{node, make([]*DhtRemoteNode, 0, len(d.remoteNodes)), map[string]string{}}
	for _, r := range d.remoteNodes {
		if r.reachable {
			targets.nodes = append(targets.nodes, r)
		}
	}
	sort.Sort(targets)
	n := make([]string, 0, GET_PEERS_NUM_NODES_RESPONSE)
	for i, r := range targets.nodes {
		if i == GET_PEERS_NUM_NODES_RESPONSE {
			break
		}
		n = append(n, r.id+bencode.DottedPortToBinary(r.address.String()))
	}
	log.Printf("replyFindNode: Nodes only. Giving %d", len(n))
	reply.R["nodes"] = n
	go sendMsg(d.port, addr, reply)
}

func (d *DhtEngine) replyPing(addr *net.UDPAddr, response responseType) {
	log.Printf("DHT: reply ping => %v\n", addr)
	reply := replyMessage{
		T: response.T,
		Y: "r",
		R: map[string]interface{}{"id": d.peerID},
	}
	go sendMsg(d.port, addr, reply)
}

// Process another node's response to a get_peers query. If the response
// contains peers, send them to the Torrent engine, our client, using the
// DhtEngine.PeersRequestResults channel. If it contains closest nodes, query
// them if we still need it.
// Also announce ourselves as a peer for that node, unless we are in supernode mode.
func (d *DhtEngine) processGetPeerResults(node *DhtRemoteNode, resp responseType) {
	query, _ := node.pendingQueries[resp.T]
	if d.activeInfoHashes[query.ih] {
		d.announcePeer(node.address, query.ih, resp.R.Token)
	}
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
			// If it's in our routing table already, ignore it.
			if _, ok := d.remoteNodes[address]; ok {
				totalDupes.Add(1)
				// XXX Gotta improve things so we stop receiving so many dupes. Waste.
			} else {
				// And it is actually new. Interesting.
				dist, _ := hashDistance(query.ih, node.id)
				log.Printf("DHT: Got new node reference: %x@%v from %x@%v. Distance: %x.",
					id, address, node.id, node.address, dist)
				nr, err := d.newRemoteNode(id, address)
				if err != nil {
					log.Println("newRemoteNode", err)
					// XXX Send an error to the host that
					// gave us this address.
					continue
				}
				d.remoteNodes[address] = nr
				if len(d.infoHashPeers[query.ih]) < d.targetNumPeers {
					d.GetPeers(query.ih)
				} else {
					// just saving in the routing table")
				}
			}
		}
	}
}

// Calculates the distance between two hashes. In DHT/Kademlia, "distance" is
// the XOR of the torrent infohash and the peer node ID.
func hashDistance(id1 string, id2 string) (distance string, err error) {
	d := make([]byte, 20)
	if len(id1) != 20 || len(id2) != 20 {
		err = errors.New(
			fmt.Sprintf("idDistance unexpected id length(s): %d %d", len(id1), len(id2)))
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
	// We do more work than necessary. When comparing node distances, we don't
	// have to XOR both strings. Comparing the initial bytes would be
	// sufficient. That said, this is nice for debugging and we don't care
	// about efficiency right now.
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

// Debugging information:
// Which nodes we contacted.
var nodesVar = expvar.NewMap("Nodes")
var totalReachableNodes = expvar.NewInt("totalReachableNodes")
var totalDupes = expvar.NewInt("totalDupes")
var totalPeers = expvar.NewInt("totalPeers")
var totalSentGetPeers = expvar.NewInt("totalSentGetPeers")
var totalRecvGetPeers = expvar.NewInt("totalRecvGetPeers")
var totalRecvFindNode = expvar.NewInt("totalRecvFindNode")

func (d *DhtEngine) bootStrapNetwork() {
	d.Ping(dhtRouter)
}

func init() {
	//	DhtStats.engines = make([]*DhtEngine, 1, 10)
	//	expvar.Publish("dhtengine", expvar.StringFunc(dhtstats))
	//	expvar.NewMap("nodes")
}
