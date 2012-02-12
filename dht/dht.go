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
	// Alias to ease disabling of debug logging.
	// debug "log"
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
	peerID string
	port   int

	remoteNodes map[string]*DhtRemoteNode // key == address
	nodes       []*DhtRemoteNode

	infoHashPeers    map[string]map[string]int // key1 == infoHash, key2 == address in binary form. value=ignored.
	activeInfoHashes map[string]bool           // infoHashes for which we are peers.
	targetNumPeers   int
	conn             *net.UDPConn
	Logger           Logger

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
		nodes:                  make([]*DhtRemoteNode, 0, 100), // It can grow up to targetNumPeers.
		PeersRequestResults:    make(chan map[string][]string, 1),
		remoteNodeAcquaintance: make(chan *DhtNodeCandidate),
		peersRequest:           make(chan string, 1), // buffer to avoid deadlock.
		infoHashPeers:          make(map[string]map[string]int),
		activeInfoHashes:       make(map[string]bool),
		targetNumPeers:         targetNumPeers,
	}
	return
}

// Logger allows the DHT client to attach hooks for certain RPCs so it can log
// interesting events any way it wants.
type Logger interface {
	GetPeers(*net.UDPAddr, string, string)
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
		// debug.Println("ping:", err)
		return
	}
	// debug.Printf("DHT: ping => %+v\n", address)
	t := r.newQuery("ping")

	queryArguments := map[string]interface{}{"id": r.localNode.peerID}
	query := queryMessage{t, "q", "ping", queryArguments}
	go sendMsg(d.conn, r.address, query)
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
	closest := closestNodes(infoHash, d.nodes, NUM_INCREMENTAL_NODE_QUERIES)
	for _, r := range closest {
		d.getPeers(r, infoHash)
	}
}

func closestNodes(ih string, nodes []*DhtRemoteNode, max int) []*DhtRemoteNode {
	if len(ih) != 20 {
		log.Fatalf("Programming error, bogus infohash: len(%v)=%d", ih, len(ih))
	}

	closest := make([]*DhtRemoteNode, 0, max)

	distances := &nodeDistances{ih, nodes}

	// XXX Shouldn't do this every time.
	sort.Sort(distances)

	for i := 0; len(closest) < max && i < len(distances.nodes); i++ {
		// Skip nodes with pending queries. First, we don't want to flood them, but most importantly they are
		// probably unreachable. We just need to make sure we clean the pendingQueries map when appropriate.
		r := distances.nodes[i]
		if !r.reachable {
			continue
		}
		if len(r.id) != 20 {
			log.Fatalf("Programming error, bogus infohash: len(%v)=%d", r.id, len(r.id))
		}

		if len(r.pendingQueries) > MAX_NODE_PENDING_QUERIES {
			// debug.Println("DHT: Skipping because there are too many queries pending for this dude.")
			// debug.Println("DHT: This shouldn't happen because we should have stopped trying already. Might be a BUG.")
			continue
		}
		// Skip if we are already asking them for this infoHash.
		skip := false
		for _, q := range r.pendingQueries {
			if q.Type == "get_peers" && q.ih == ih {
				skip = true
			}
		}
		// Skip if we asked for this infoHash recently.
		for _, q := range r.pastQueries {
			if q.Type == "get_peers" && q.ih == ih {
				ago := time.Now().Sub(r.lastTime)
				if ago < MIN_SECONDS_NODE_REPEAT_QUERY {
					skip = true
				} else {
					// This is an act of desperation. Query
					// them again.  Most likely this will
					// only generate dupes, but it's worth
					// a try.
					// debug.Printf("Re-sending get_peers. Last time: %v (%v ago) %v", r.lastTime.String(), ago.Seconds(), ago > 10*time.Second)
				}
			}
		}
		if !skip {
			closest = append(closest, r)
		}
	}
	// debug.Printf("DHT: Candidate nodes for asking: %d", len(targets.nodes))
	// debug.Printf("DHT: Currently know %d nodes", len(d.remoteNodes))
	return closest
	// debug.Println("DHT: totalSentGetPeers", totalSentGetPeers.String())
}

// DoDht is the DHT node main loop and should be run as a goroutine by the torrent client.
func (d *DhtEngine) DoDht() {
	socketChan := make(chan packetType)
	socket, err := listen(d.port)
	if err != nil {
		return
	}
	d.conn = socket
	go readFromSocket(socket, socketChan)

	d.bootStrapNetwork()

	// debug.Println("DHT: Starting DHT node.")
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
					// debug.Println("newRemoteNode:", err)
				} else {
					d.Ping(helloNode.Address)
				}
			}

		case needPeers := <-d.peersRequest:
			// torrent server is asking for more peers for a particular infoHash.  Ask the closest nodes for
			// directions. The goroutine will write into the PeersNeededResults channel.
			// debug.Printf("DHT: torrent client asking more peers for %x. Calling GetPeers().", needPeers)
			d.GetPeers(needPeers)
		case p := <-socketChan:
			if p.b[0] != 'd' {
				// Malformed DHT packet. There are protocol extensions out
				// there that we don't support or understand.
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
					// debug.Println("DHT: Received reply from a host we don't know:", p.raddr)
					d.Ping(p.raddr.String())
					// XXX: Add this guy to a list of dubious hosts.
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
						// debug.Println("DHT: Unknown query type:", query.Type, p.raddr)
					}
					node.pastQueries[r.T] = query
					delete(node.pendingQueries, r.T)
				} else {
					// XXX debugging.
					// debug.Println("DHT: Unknown query id:", r.T)
				}
			case r.Y == "q":
				if _, ok := d.remoteNodes[p.raddr.String()]; !ok {
					// Another candidate for the routing table. See if it's reachable.
					d.Ping(p.raddr.String())
				}
				switch r.Q {
				case "ping":
					d.replyPing(p.raddr, r)
				case "get_peers":
					d.replyGetPeers(p.raddr, r)
				case "find_node":
					d.replyFindNode(p.raddr, r)
				default:
					// debug.Println("DHT XXX non-implemented handler for type", r.Q)
				}
			default:
				// debug.Printf("DHT: Bogus DHT query from %v.", p.raddr)
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
	d.remoteNodes[hostPort] = r
	d.nodes = append(d.nodes, r)

	nodesVar.Add(hostPort, 1)
	return

}

// getOrCreateRemoteNode returns the DhtRemoteNode with the provided address.
// Creates a new object if necessary.
func (d *DhtEngine) getOrCreateRemoteNode(address string) (r *DhtRemoteNode, err error) {
	var ok bool
	if r, ok = d.remoteNodes[address]; !ok {
		r, err = d.newRemoteNode("", address)
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
	go sendMsg(d.conn, r.address, query)
}

func (d *DhtEngine) announcePeer(address *net.UDPAddr, ih string, token string) {
	r, err := d.getOrCreateRemoteNode(address.String())
	if err != nil {
		// debug.Println("announcePeer:", err)
		return
	}
	ty := "announce_peer"
	// debug.Printf("DHT: announce_peer => %v %x %x\n", address, ih, token)
	transId := r.newQuery(ty)
	queryArguments := map[string]interface{}{
		"id":        r.localNode.peerID,
		"info_hash": ih,
		"port":      d.port,
		"token":     token,
	}
	query := queryMessage{transId, "q", ty, queryArguments}
	go sendMsg(d.conn, address, query)
}

func (d *DhtEngine) replyGetPeers(addr *net.UDPAddr, r responseType) {
	totalRecvGetPeers.Add(1)
	// x, _ := hashDistance(r.A.InfoHash, d.peerID)
	// debug.Printf("DHT XXXX get_peers. Host: %v , nodeID: %x , InfoHash: %x , distance to me: %x", addr, r.A.Id, r.A.InfoHash, x)
	if d.Logger != nil {
		d.Logger.GetPeers(addr, r.A.Id, r.A.InfoHash)
	}

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
		// debug.Printf("replyGetPeers: Giving peers! %v wanted %x, and we knew %d peers!", addr.String(), ih, len(peerContacts))
		reply.R["values"] = peerContacts
	} else {
		nodes := make([]*DhtRemoteNode, 0, len(d.remoteNodes))
		// XXX Better way to do this.
		for _, r := range d.remoteNodes {
			if len(r.id) == 20 {
				nodes = append(nodes, r)
			}
		}

		n := make([]string, 0, GET_PEERS_NUM_NODES_RESPONSE)
		for _, r := range closestNodes(ih, nodes, GET_PEERS_NUM_NODES_RESPONSE) {
			n = append(n, r.id+bencode.DottedPortToBinary(r.address.String()))
			// debug.Printf("replyGetPeers: [%d] distance %x", i, targets.distances[r.id])
		}
		// debug.Printf("replyGetPeers: Nodes only. Giving %d", len(n))
		reply.R["nodes"] = n
	}
	go sendMsg(d.conn, addr, reply)

}

func (d *DhtEngine) replyFindNode(addr *net.UDPAddr, r responseType) {
	totalRecvFindNode.Add(1)
	// x, _ := hashDistance(r.A.Target, d.peerID)
	// debug.Printf("DHT XX find_node. Host: %v , peerID: %x , nodeID: %x , distance to me: %x", addr, r.A.Id, r.A.Target, x)

	node := r.A.Target
	r0 := map[string]interface{}{"id": node}
	reply := replyMessage{
		T: r.T,
		Y: "r",
		R: r0,
	}

	// XXX we currently can't give out the peer contact. Probably requires processing announce_peer.
	targets := &nodeDistances{node, make([]*DhtRemoteNode, 0, len(d.remoteNodes))}
	for _, r := range d.remoteNodes {
		if r.reachable {
			targets.nodes = append(targets.nodes, r)
		}
	}
	// XXX Slow. Don't run this every time.
	sort.Sort(targets)
	n := make([]string, 0, GET_PEERS_NUM_NODES_RESPONSE)
	for i, r := range targets.nodes {
		if i == GET_PEERS_NUM_NODES_RESPONSE {
			break
		}
		n = append(n, r.id+bencode.DottedPortToBinary(r.address.String()))
	}
	// debug.Printf("replyFindNode: Nodes only. Giving %d", len(n))
	reply.R["nodes"] = n
	go sendMsg(d.conn, addr, reply)
}

func (d *DhtEngine) replyPing(addr *net.UDPAddr, response responseType) {
	// debug.Printf("DHT: reply ping => %v\n", addr)
	reply := replyMessage{
		T: response.T,
		Y: "r",
		R: map[string]interface{}{"id": d.peerID},
	}
	go sendMsg(d.conn, addr, reply)
}

// Process another node's response to a get_peers query. If the response
// contains peers, send them to the Torrent engine, our client, using the
// DhtEngine.PeersRequestResults channel. If it contains closest nodes, query
// them if we still need it.
// Also announce ourselves as a peer for that node, unless we are in supernode mode.
func (d *DhtEngine) processGetPeerResults(node *DhtRemoteNode, resp responseType) {
	totalRecvGetPeersReply.Add(1)
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
			// debug.Println("DHT: totalPeers:", totalPeers.String())
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
				// dist, _ := hashDistance(query.ih, node.id)
				// debug.Printf("DHT: Got new node reference: %x@%v from %x@%v. Distance: %x.", id, address, node.id, node.address, dist)
				if _, err := d.newRemoteNode(id, address); err == nil {
					if len(d.infoHashPeers[query.ih]) < d.targetNumPeers {
						d.GetPeers(query.ih)
					}
				}
			}
		}
	}
}

// Calculates the distance between two hashes. In DHT/Kademlia, "distance" is
// the XOR of the torrent infohash and the peer node ID.
// This is slower than necessary. Should only be used for displaying friendly messages.
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
	infoHash string
	nodes    []*DhtRemoteNode
}

func (n *nodeDistances) Len() int {
	return len(n.nodes)
}
func (n *nodeDistances) Less(i, j int) bool {
	xor1 := n.nodes[i].id
	xor2 := n.nodes[j].id
	return xorcmp(xor1, xor2, n.infoHash)
}

func (n *nodeDistances) Swap(i, j int) {
	n.nodes[i], n.nodes[j] = n.nodes[j], n.nodes[i]
}

func xorcmp(xor1, xor2, ref string) bool {
	// If xor1 or xor2 have bogus lengths, move them to the last position.
	if len(xor1) != 20 {
		return false // Causes a swap, moving it to last.
	}
	if len(xor2) != 20 {
		return true
	}
	// Inspired by dht.c from Juliusz Chroboczek.
	for i := 0; i < 20; i++ {
		if xor1[i] == xor2[i] {
			continue
		}
		return xor1[i]^ref[i] < xor2[i]^ref[i]
	}
	// Identical infohashes.
	return false
}

// Debugging information:
// Which nodes we contacted.
var nodesVar = expvar.NewMap("Nodes")
var totalReachableNodes = expvar.NewInt("totalReachableNodes")
var totalDupes = expvar.NewInt("totalDupes")
var totalPeers = expvar.NewInt("totalPeers")
var totalSentGetPeers = expvar.NewInt("totalSentGetPeers")
var totalRecvGetPeers = expvar.NewInt("totalRecvGetPeers")
var totalRecvGetPeersReply = expvar.NewInt("totalRecvGetPeersReply")
var totalRecvFindNode = expvar.NewInt("totalRecvFindNode")

func (d *DhtEngine) bootStrapNetwork() {
	d.Ping(dhtRouter)
}

func init() {
	//	DhtStats.engines = make([]*DhtEngine, 1, 10)
	//	expvar.Publish("dhtengine", expvar.StringFunc(dhtstats))
	//	expvar.NewMap("nodes")
}
