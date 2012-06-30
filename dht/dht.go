// DHT node for Taipei Torrent, for tracker-less peer information exchange.
//
// Status:
//  Supports all DHT operations from the specification, except:
//  - doesn't handle announce_peer.
//  - doesn't try to maintain a minimum number of healthy node in the routing
//  table (find_node).
//
// Summary from the bittorrent DHT protocol specification: 
//
// Message types:
// - query
// - response
// - error
//
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

// There is very few computation involved here, so almost everything runs in a
// single thread.

package dht

import (
	"crypto/rand"
	"expvar"
	"flag"
	"fmt"
	"net"
	"strings"
	"time"

	l4g "code.google.com/p/log4go"
	"github.com/jackpal/Taipei-Torrent/bencode"
	"github.com/jackpal/Taipei-Torrent/nettools"
)

var (
	dhtRouter     string
	maxNodes      int
	cleanupPeriod time.Duration
	savePeriod    time.Duration
	rateLimit     int64
)

func init() {
	// TODO: Run our own router.
	flag.StringVar(&dhtRouter, "dhtRouter", "router.utorrent.com:6881",
		"IP:Port address of the DHT router used to bootstrap the DHT network.")
	flag.IntVar(&maxNodes, "maxNodes", 1000,
		"Maximum number of nodes to store in the routing table, in memory.")
	flag.DurationVar(&cleanupPeriod, "cleanupPeriod", 10*time.Minute,
		"How often to ping nodes in the network to see if they are reachable.")
	flag.DurationVar(&savePeriod, "savePeriod", 5*time.Minute,
		"How often to save the routing table to disk.")
	flag.Int64Var(&rateLimit, "rateLimit", 1000,
		"Maximum packets per second to be processed. Beyond this limit they are silently dropped.")
}

// DhtEngine should be created by NewDhtNode(). It provides DHT features to a
// torrent client, such as finding new peers for torrent downloads without
// requiring a tracker.
type DhtEngine struct {
	nodeId string
	port   int

	remoteNodes map[string]*DhtRemoteNode // key == address
	tree        *nTree

	infoHashPeers    map[string]map[string]int // key1 == infoHash, key2 == address in binary form. value=ignored.
	activeInfoHashes map[string]bool           // infoHashes for which we are peers.
	numTargetPeers   int
	conn             *net.UDPConn
	Logger           Logger

	// Public channels:
	remoteNodeAcquaintance chan string
	peersRequest           chan peerReq
	PeersRequestResults    chan map[string][]string // key = infohash, v = slice of peers.
	clientThrottle         *nettools.ClientThrottle

	store *DhtStore
}

func NewDhtNode(port, numTargetPeers int, writeStore bool) (node *DhtEngine, err error) {
	node = &DhtEngine{
		port:                port,
		remoteNodes:         make(map[string]*DhtRemoteNode),
		tree:                &nTree{},
		PeersRequestResults: make(chan map[string][]string, 1),
		// Buffer to avoid blocking on sends.
		remoteNodeAcquaintance: make(chan string, 10),
		// Buffer to avoid deadlocks and blocking on sends.
		peersRequest:     make(chan peerReq, 10),
		infoHashPeers:    make(map[string]map[string]int),
		activeInfoHashes: make(map[string]bool),
		numTargetPeers:   numTargetPeers,
		clientThrottle:   nettools.NewThrottler(),
	}
	c := openStore(port)
	if writeStore {
		node.store = c
	}
	if len(c.Id) != 20 {
		c.Id = newNodeId()
		l4g.Info("newId: %x %d", c.Id, len(c.Id))
		if writeStore {
			saveStore(*c)
		}
	}
	// The types don't match because JSON marshalling needs []byte.
	node.nodeId = string(c.Id)

	for addr, _ := range c.Remotes {
		go node.RemoteNodeAcquaintance(addr)
	}
	return
}

// Logger allows the DHT client to attach hooks for certain RPCs so it can log
// interesting events any way it wants.
type Logger interface {
	GetPeers(*net.UDPAddr, string, string)
}

type peerReq struct {
	ih       string
	announce bool
}

// PeersRequest tells the DHT to search for more peers for the infoHash
// provided. announce should be true if the connected peer is actively
// downloading this infohash. False should be used for when the DHT node is
// just a probe and shouldn't send announce_peer.
func (d *DhtEngine) PeersRequest(ih string, announce bool) {
	d.peersRequest <- peerReq{ih, announce}
}

func (d *DhtEngine) RemoteNodeAcquaintance(addr string) {
	d.remoteNodeAcquaintance <- addr
}

// routingTable outputs the routing table. Needed for persisting the table
// between sessions.
func (d *DhtEngine) routingTable() (tbl map[string][]byte) {
	tbl = make(map[string][]byte)
	for addr, r := range d.remoteNodes {
		if r.reachable && len(r.id) == 20 {
			tbl[addr] = []byte(r.id)
		}
	}
	return
}

// Asks for more peers for a torrent.
func (d *DhtEngine) getPeers(infoHash string) {
	closest := d.tree.lookupFiltered(infoHash)
	for _, r := range closest {
		go d.getPeersFrom(r, infoHash)
	}
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

	// Bootstrap the network.
	d.ping(dhtRouter)
	cleanupTicker := time.Tick(cleanupPeriod)

	saveTicker := make(<-chan time.Time)
	if d.store != nil {
		saveTicker = time.Tick(savePeriod)
	}

	// Token bucket for limiting the number of packets per second.
	fillTokenBucket := time.Tick(time.Second / 10)
	tokenBucket := rateLimit

	if rateLimit < 10 {
		// Less than 10 leads to rounding problems.
		rateLimit = 10
	}

	l4g.Info("DHT: Starting DHT node %x.", d.nodeId)

	for {
		select {
		case addr := <-d.remoteNodeAcquaintance:
			d.helloFromPeer(addr)
		case peersRequest := <-d.peersRequest:
			// torrent server is asking for more peers for a particular infoHash.  Ask the closest nodes for
			// directions. The goroutine will write into the PeersNeededResults channel.
			if peersRequest.announce {
				d.activeInfoHashes[peersRequest.ih] = true
			}
			l4g.Trace("DHT: torrent client asking more peers for %x. Calling getPeers().", peersRequest)
			d.getPeers(peersRequest.ih)
		case p := <-socketChan:
			if tokenBucket > 0 {
				d.process(p)
				tokenBucket -= 1
			} else {
				// In the future it might be better to avoid dropping things like ping replies.
				totalDroppedPackets.Add(1)
			}
		case <-fillTokenBucket:
			if tokenBucket < rateLimit {
				tokenBucket += rateLimit / 10
			}
		case <-cleanupTicker:
			d.routingTableCleanup()
		case <-saveTicker:
			tbl := d.routingTable()
			if len(tbl) > 5 {
				d.store.Remotes = tbl
				saveStore(*d.store)
			}
		}
	}
}

func (d *DhtEngine) helloFromPeer(addr string) {
	// We've got a new node id. We need to:
	// - see if we know it already, skip accordingly.
	// - ping it and see if it's reachable.
	// - if it responds, save it in the routing table.
	if _, ok := d.remoteNodes[addr]; ok {
		// Node host+port already known.
		return
	}
	if len(d.remoteNodes) < maxNodes {
		d.ping(addr)
		return
	}
}

func (d *DhtEngine) process(p packetType) {
	totalRecv.Add(1)
	if !d.clientThrottle.CheckBlock(p.raddr.IP.String()) {
		totalPacketsFromBlockedHosts.Add(1)
		return
	}
	if p.b[0] != 'd' {
		// Malformed DHT packet. There are protocol extensions out
		// there that we don't support or understand.
		return
	}
	r, err := readResponse(p)
	if err != nil {
		l4g.Warn("DHT: readResponse Error: %v, %q", err, string(p.b))
		return
	}
	switch {
	// Response.
	case r.Y == "r":
		node, ok := d.remoteNodes[p.raddr.String()]
		if !ok {
			l4g.Info("DHT: Received reply from a host we don't know: %v", p.raddr)
			if len(d.remoteNodes) < maxNodes {
				d.ping(p.raddr.String())
			}
			// XXX: Add this guy to a list of dubious hosts.
			return
		}
		// Fix the node ID.
		if node.id == "" {
			node.id = r.R.Id
			d.tree.insert(node)
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
			case "ping":
				// served its purpose, nothing else to be done.
				l4g.Trace("DHT: Received ping reply")
				totalRecvPingReply.Add(1)
			case "get_peers":
				d.processGetPeerResults(node, r)
			default:
				l4g.Info("DHT: Unknown query type: %v from %v", query.Type, p.raddr)
			}
			node.pastQueries[r.T] = query
			delete(node.pendingQueries, r.T)
		} else {
			l4g.Info("DHT: Unknown query id: %v", r.T)
		}
	case r.Y == "q":
		if _, ok := d.remoteNodes[p.raddr.String()]; !ok {
			// Another candidate for the routing table. See if it's reachable.
			if len(d.remoteNodes) < maxNodes {
				d.ping(p.raddr.String())
			}
		}
		switch r.Q {
		case "ping":
			d.replyPing(p.raddr, r)
		case "get_peers":
			d.replyGetPeers(p.raddr, r)
		case "find_node":
			d.replyFindNode(p.raddr, r)
		// When implementing a handler for
		// announce_peer, remember to change the
		// get_peers reply tokens to be meaningful.
		default:
			l4g.Warn("DHT: non-implemented handler for type %v", r.Q)
		}
	default:
		l4g.Info("DHT: Bogus DHT query from %v.", p.raddr)
	}
}

func (d *DhtEngine) routingTableCleanup() {
	t0 := time.Now()
	for _, n := range d.remoteNodes {
		if n.reachable {
			if len(n.pendingQueries) == 0 {
				goto PING
			}
			if time.Since(n.lastTime) > cleanupPeriod*2 {
				l4g.Trace("DHT: Old dude seen %v ago. Deleting.", time.Since(n.lastTime))
				d.kill(n)
				continue
			}
			if time.Since(n.lastTime).Nanoseconds() < cleanupPeriod.Nanoseconds()/2 {
				// Seen recently. Don't need to ping.
				continue
			}

		} else {
			// Not reachable.
			if len(n.pendingQueries) > 2 {
				// Didn't reply to 2 consecutive queries.
				l4g.Trace("DHT: Node never replied to ping. Deleting. %v", n.address)
				d.kill(n)
				continue
			}
		}
	PING:
		d.ping(n.address.String())
	}
	duration := time.Since(t0)
	// If this pauses the server for too long I may have to segment the cleanup.
	// 2000 nodes: it takes ~12ms
	// 4000 nodes: ~24ms.
	l4g.Info("DHT: Routing table cleanup took %v", duration)
}

func (d *DhtEngine) kill(n *DhtRemoteNode) {
	delete(d.remoteNodes, n.address.String())
	d.tree.cut(n.id, 0)
	totalKilledNodes.Add(1)
}

func (d *DhtEngine) newRemoteNode(id string, hostPort string) (r *DhtRemoteNode, err error) {
	address, err := net.ResolveUDPAddr("udp", hostPort)
	if err != nil {
		return nil, err
	}
	n, err := rand.Read(make([]byte, 1))
	if err != nil {
		return nil, err
	}
	r = &DhtRemoteNode{
		address:        address,
		lastQueryID:    n,
		id:             id,
		reachable:      false,
		pendingQueries: map[string]*queryType{},
		pastQueries:    map[string]*queryType{},
	}
	d.remoteNodes[hostPort] = r
	if id != "" {
		d.tree.insert(r)
	}
	totalNodes.Add(1)
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

func (d *DhtEngine) ping(address string) {
	// TODO: should translate to an IP first.
	r, err := d.getOrCreateRemoteNode(address)
	if err != nil {
		l4g.Info("ping error: %v", err)
		return
	}
	l4g.Debug("DHT: ping => %+v\n", address)
	t := r.newQuery("ping")

	queryArguments := map[string]interface{}{"id": d.nodeId}
	query := queryMessage{t, "q", "ping", queryArguments}
	sendMsg(d.conn, r.address, query)
	totalSentPing.Add(1)
}

func (d *DhtEngine) getPeersFrom(r *DhtRemoteNode, ih string) {
	totalSentGetPeers.Add(1)
	ty := "get_peers"
	transId := r.newQuery(ty)
	r.pendingQueries[transId].ih = ih
	queryArguments := map[string]interface{}{
		"id":        d.nodeId,
		"info_hash": ih,
	}
	query := queryMessage{transId, "q", ty, queryArguments}
	l4g.Trace(func() string {
		x := hashDistance(r.id, ih)
		return fmt.Sprintf("DHT sending get_peers. nodeID: %x , InfoHash: %x , distance: %x", r.id, ih, x)
	})
	sendMsg(d.conn, r.address, query)
}

func (d *DhtEngine) announcePeer(address *net.UDPAddr, ih string, token string) {
	r, err := d.getOrCreateRemoteNode(address.String())
	if err != nil {
		l4g.Trace("announcePeer:", err)
		return
	}
	ty := "announce_peer"
	l4g.Trace("DHT: announce_peer => %v %x %x\n", address, ih, token)
	transId := r.newQuery(ty)
	queryArguments := map[string]interface{}{
		"id":        d.nodeId,
		"info_hash": ih,
		"port":      d.port,
		"token":     token,
	}
	query := queryMessage{transId, "q", ty, queryArguments}
	sendMsg(d.conn, address, query)
}

func (d *DhtEngine) replyGetPeers(addr *net.UDPAddr, r responseType) {
	totalRecvGetPeers.Add(1)
	l4g.Info(func() string {
		x := hashDistance(r.A.InfoHash, d.nodeId)
		return fmt.Sprintf("DHT get_peers. Host: %v , nodeID: %x , InfoHash: %x , distance to me: %x", addr, r.A.Id, r.A.InfoHash, x)
	})

	if d.Logger != nil {
		d.Logger.GetPeers(addr, r.A.Id, r.A.InfoHash)
	}

	ih := r.A.InfoHash
	r0 := map[string]interface{}{"id": ih, "token": "blabla"}
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
		l4g.Trace("replyGetPeers: Giving peers! %v wanted %x, and we knew %d peers!", addr.String(), ih, len(peerContacts))
		reply.R["values"] = peerContacts
	} else {
		n := make([]string, 0, kNodes)
		for _, r := range d.tree.lookupFiltered(ih) {
			n = append(n, r.id+bencode.DottedPortToBinary(r.address.String()))
		}
		l4g.Trace("replyGetPeers: Nodes only. Giving %d", len(n))
		reply.R["nodes"] = strings.Join(n, "")
	}
	sendMsg(d.conn, addr, reply)
}

func (d *DhtEngine) replyFindNode(addr *net.UDPAddr, r responseType) {
	totalRecvFindNode.Add(1)
	l4g.Trace(func() string {
		x := hashDistance(r.A.Target, d.nodeId)
		return fmt.Sprintf("DHT find_node. Host: %v , nodeId: %x , target ID: %x , distance to me: %x", addr, r.A.Id, r.A.Target, x)
	})

	node := r.A.Target
	r0 := map[string]interface{}{"id": node}
	reply := replyMessage{
		T: r.T,
		Y: "r",
		R: r0,
	}

	// XXX we currently can't give out the peer contact. Probably requires processing announce_peer.
	// XXX If there was a total match, that guy is the last.
	neighbors := d.tree.lookup(node)
	n := make([]string, 0, kNodes)
	for _, r := range neighbors {
		n = append(n, r.id+bencode.DottedPortToBinary(r.address.String()))
	}
	l4g.Trace("replyFindNode: Nodes only. Giving %d", len(n))
	reply.R["nodes"] = strings.Join(n, "")
	sendMsg(d.conn, addr, reply)
}

func (d *DhtEngine) replyPing(addr *net.UDPAddr, response responseType) {
	l4g.Trace("DHT: reply ping => %v\n", addr)
	reply := replyMessage{
		T: response.T,
		Y: "r",
		R: map[string]interface{}{"id": d.nodeId},
	}
	sendMsg(d.conn, addr, reply)
}

// Process another node's response to a get_peers query. If the response
// contains peers, send them to the Torrent engine, our client, using the
// DhtEngine.PeersRequestResults channel. If it contains closest nodes, query
// them if we still need it. Also announce ourselves as a peer for that node,
// unless we are in supernode mode.
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
			l4g.Info("DHT: processGetPeerResults, totalPeers: %v", totalPeers.String())
			d.PeersRequestResults <- result
		}
	}
	if resp.R.Nodes != "" {
		for id, address := range parseNodesString(resp.R.Nodes) {
			// XXX
			// If it's in our routing table already, ignore it.
			if _, ok := d.remoteNodes[address]; ok {
				totalDupes.Add(1)
			} else {
				// And it is actually new. Interesting.
				l4g.Trace(func() string {
					x := hashDistance(query.ih, node.id)
					return fmt.Sprintf("DHT: Got new node reference: %x@%v from %x@%v. Distance: %x.", id, address, node.id, node.address, x)
				})
				if _, err := d.newRemoteNode(id, address); err == nil {
					if len(d.infoHashPeers[query.ih]) < d.numTargetPeers {
						d.getPeers(query.ih)
					}
				}
			}
		}
	}
}

func newNodeId() []byte {
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		l4g.Exit("nodeId rand:", err)
	}
	return b
}

var (
	totalNodes                   = expvar.NewInt("totalNodes")
	totalKilledNodes             = expvar.NewInt("totalKilledNodes")
	totalReachableNodes          = expvar.NewInt("totalReachableNodes")
	totalDupes                   = expvar.NewInt("totalDupes")
	totalPeers                   = expvar.NewInt("totalPeers")
	totalSentPing                = expvar.NewInt("totalSentPing")
	totalSentGetPeers            = expvar.NewInt("totalSentGetPeers")
	totalRecvGetPeers            = expvar.NewInt("totalRecvGetPeers")
	totalRecvGetPeersReply       = expvar.NewInt("totalRecvGetPeersReply")
	totalRecvPingReply           = expvar.NewInt("totalRecvPingReply")
	totalRecvFindNode            = expvar.NewInt("totalRecvFindNode")
	totalPacketsFromBlockedHosts = expvar.NewInt("totalPacketsFromBlockedHosts")
	totalDroppedPackets          = expvar.NewInt("totalDroppedPackets")
	totalRecv                    = expvar.NewInt("totalRecv")
)

func init() {
	l4g.Global.AddFilter("stdout", l4g.WARNING, l4g.NewConsoleLogWriter())
}
