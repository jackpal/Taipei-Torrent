// KRPC helpers.
package taipei

import (
	"bytes"
	"jackpal/bencode"
	"log"
	"net"
	"os"
	"strconv"
	"time"
)

// Owned by the DHT engine.
type DhtRemoteNode struct {
	address *net.UDPAddr
	id      string
	// lastQueryID should be incremented after consumed. Based on the
	// protocol, it would be two letters, but I'm using 0-255, although
	// treated as string.
	lastQueryID    int
	pendingQueries map[string]*queryType // key: transaction ID
	pastQueries    map[string]*queryType // key: transaction ID
	localNode      *DhtEngine
	reachable      bool
	lastTime       time.Time
}

type queryType struct {
	Type string
	ih   string
}

const (
	NODE_ID_LEN         = 20
	NODE_CONTACT_LEN    = 26
	PEER_CONTACT_LEN    = 6
	MAX_UDP_PACKET_SIZE = 8192
	UDP_TIMEOUT         = 2e9 // two seconds
)

// The 'nodes' response is a string with fixed length contacts concatenated arbitrarily.
func parseNodesString(nodes string) (parsed map[string]string) {
	parsed = make(map[string]string)
	if len(nodes)%NODE_CONTACT_LEN > 0 {
		// TODO: Err
		log.Println("DHT: Invalid length of nodes.")
		log.Printf("DHT: Should be a multiple of %d, got %d", NODE_CONTACT_LEN, len(nodes))
		return
	}
	for i := 0; i < len(nodes); i += NODE_CONTACT_LEN {
		id := nodes[i : i+NODE_ID_LEN]
		address := binaryToDottedPort(nodes[i+NODE_ID_LEN : i+NODE_CONTACT_LEN])
		parsed[id] = address
	}
	//log.Printf("parsed: %+v", parsed)
	return

}

// encodedPing returns the bencoded string to be used for DHT ping queries.
func (r *DhtRemoteNode) encodedPing(transId string) (msg string, err error) {
	queryArguments := map[string]string{"id": r.localNode.peerID}
	msg, err = encodeMsg("ping", queryArguments, transId)
	return
}

// encodedGetPeers returns the bencoded string to be used for DHT get_peers queries.
func (r *DhtRemoteNode) encodedGetPeers(transId string, infohash string) (msg string, err error) {
	queryArguments := map[string]string{
		"id":        r.localNode.peerID,
		"info_hash": infohash,
	}
	msg, err = encodeMsg("get_peers", queryArguments, transId)
	return

}

func (r *DhtRemoteNode) newQuery(transType string) string {
	r.lastQueryID = (r.lastQueryID + 1) % 256
	t := strconv.Itoa(r.lastQueryID)
	r.pendingQueries[t] = &queryType{Type: transType}
	return t
}

type getPeersResponse struct {
	// TODO: argh, values can be a string depending on the client (e.g: original bittorrent).
	Values []string "values"
	Id     string   "id"
	Nodes  string   "nodes"
}

type responseType struct {
	T string           "t"
	Y string           "y"
	Q string           "q"
	R getPeersResponse "R"
}

// Sends a message to the remote node. msg should be the bencoded string ready to be sent in the wire.
// Clients usually run it as a goroutine, so it must not change mutable shared state.
func (r *DhtRemoteNode) sendMsg(msg string) (response responseType, err error) {
	laddr := &net.UDPAddr{Port: r.localNode.port}
	conn, err := net.DialUDP("udp", laddr, r.address)
	if conn == nil || err != nil {
		return
	}
	defer conn.Close()
	if _, err = conn.Write(bytes.NewBufferString(msg).Bytes()); err != nil {
		log.Println("DHT: node write failed", err)
		return
	}
	return
}

func (r *DhtRemoteNode) dialNode(ch chan net.Conn) {
	return
}

// Read responses from bencode-speaking nodes. Return the appropriate data structure.
func readResponse(p packetType) (response responseType, err error) {
	// The calls to bencode.Unmarshal() can be fragile.
	defer func() {
		if x := recover(); x != nil {
			log.Println("DHT: !!! Recovering from panic() after bencode.Unmarshal")
		}
	}()

	if e2 := bencode.Unmarshal(bytes.NewBuffer(p.b), &response); e2 == nil {
		err = nil
		return
	} else {
		log.Printf("DHT: unmarshal error, odd or partial data during UDP read? %+v, err=%s", p, e2)
	}
	return
}

func encodeMsg(queryType string, queryArguments map[string]string, transId string) (msg string, err error) {
	type structNested struct {
		T string            "t"
		Y string            "y"
		Q string            "q"
		A map[string]string "a"
	}
	query := structNested{transId, "q", queryType, queryArguments}
	var b bytes.Buffer
	if err = bencode.Marshal(&b, query); err != nil {
		log.Println("DHT: bencode error:", err)
		return
	}
	msg = string(b.Bytes())
	return
}

type packetType struct {
	b     []byte
	raddr net.Addr
}

func listen(listenPort int) (socket *net.UDPConn, err error) {
	log.Printf("DHT: Listening for peers on port: %d\n", listenPort)
	listener, err := net.ListenPacket("udp", ":"+strconv.Itoa(listenPort))
	if err != nil {
		log.Println("DHT: Listen failed:", err)
	}
	if listener != nil {
		socket = listener.(*net.UDPConn)
		socket.SetTimeout(UDP_TIMEOUT)
	}
	return
}

// Read from UDP socket, writes slice of byte into channel.
func readFromSocket(socket *net.UDPConn, conChan chan packetType) {
	socket.SetReadTimeout(0)
	for {
		// Unfortunately this won't read directly into a buffer, so I have to set a fixed "fake" buffer.
		b := make([]byte, MAX_UDP_PACKET_SIZE)
		n, addr, err := socket.ReadFrom(b)
		b = b[0:n]
		if n == MAX_UDP_PACKET_SIZE {
			log.Printf("DHT: Warning. Received packet with len >= %d, some data may have been discarded.\n", MAX_UDP_PACKET_SIZE)
		}
		if err == nil {
			p := packetType{b, addr}
			conChan <- p
			continue
		}
		if e, ok := err.(*net.OpError); ok && e.Err == os.EAGAIN {
			continue
		}
		if n == 0 {
			log.Println("DHT: readResponse: got n == 0. Err:", err)
			continue
		}
	}
}
