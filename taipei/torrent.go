package taipei

import (
	"bytes"
	"crypto/sha1"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net"
	"net/url"
	"os"
	"strconv"
	"time"
)

const (
	MAX_NUM_PEERS    = 60
	TARGET_NUM_PEERS = 15
)

// BitTorrent message types. Sources:
// http://bittorrent.org/beps/bep_0003.html
// http://wiki.theory.org/BitTorrentSpecification
const (
	CHOKE = iota
	UNCHOKE
	INTERESTED
	NOT_INTERESTED
	HAVE
	BITFIELD
	REQUEST
	PIECE
	CANCEL
	PORT // Not implemented. For DHT support.
)

// Should be overriden by flag. Not thread safe.
var port int
var useUPnP bool
var fileDir string
var useDHT bool
var trackerLessMode bool

func init() {
	flag.StringVar(&fileDir, "fileDir", ".", "path to directory where files are stored")
	flag.IntVar(&port, "port", 0, "Port to listen on. Defaults to random.")
	flag.BoolVar(&useUPnP, "useUPnP", false, "Use UPnP to open port in firewall.")
	flag.BoolVar(&useDHT, "useDHT", false, "Use DHT to get peers (NOT WORKING).")
	flag.BoolVar(&trackerLessMode, "trackerLessMode", false, "Do not get peers from the tracker. Good for "+
		"testing the DHT mode.")
}

func peerId() string {
	sid := "-tt" + strconv.Itoa(os.Getpid()) + "_" + strconv.FormatInt(rand.Int63(), 10)
	return sid[0:20]
}

func binaryToDottedPort(port string) string {
	return fmt.Sprintf("%d.%d.%d.%d:%d", port[0], port[1], port[2], port[3],
		(uint16(port[4])<<8)|uint16(port[5]))
}

func chooseListenPort() (listenPort int, err error) {
	listenPort = port
	if useUPnP {
		log.Println("Using UPnP to open port.")
		// TODO: Look for ports currently in use. Handle collisions.
		var nat NAT
		nat, err = Discover()
		if err != nil {
			log.Println("Unable to discover NAT:", err)
			return
		}
		// TODO: Check if the port is already mapped by someone else.
		err2 := nat.DeletePortMapping("TCP", listenPort)
		if err2 != nil {
			log.Println("Unable to delete port mapping", err2)
		}
		err = nat.AddPortMapping("TCP", listenPort, listenPort,
			"Taipei-Torrent port "+strconv.Itoa(listenPort), 0)
		if err != nil {
			log.Println("Unable to forward listen port", err)
			return
		}
	}
	return
}

func listenForPeerConnections(listenPort int, conChan chan net.Conn) {
	listenString := ":" + strconv.Itoa(listenPort)
	log.Println("Listening for peers on port:", listenString)
	listener, err := net.Listen("tcp", listenString)
	if err != nil {
		log.Println("Listen failed:", err)
		return
	}
	for {
		var conn net.Conn
		conn, err = listener.Accept()
		if err != nil {
			log.Println("Listener failed:", err)
		} else {
			// log.Println("A peer contacted us", conn.RemoteAddr().String())
			conChan <- conn
		}
	}
}

var kBitTorrentHeader = []byte{'\x13', 'B', 'i', 't', 'T', 'o', 'r',
	'r', 'e', 'n', 't', ' ', 'p', 'r', 'o', 't', 'o', 'c', 'o', 'l'}

func string2Bytes(s string) []byte { return bytes.NewBufferString(s).Bytes() }

type ActivePiece struct {
	downloaderCount []int // -1 means piece is already downloaded
	pieceLength     int
}

func (a *ActivePiece) chooseBlockToDownload(endgame bool) (index int) {
	if endgame {
		return a.chooseBlockToDownloadEndgame()
	}
	return a.chooseBlockToDownloadNormal()
}

func (a *ActivePiece) chooseBlockToDownloadNormal() (index int) {
	for i, v := range a.downloaderCount {
		if v == 0 {
			a.downloaderCount[i]++
			return i
		}
	}
	return -1
}

func (a *ActivePiece) chooseBlockToDownloadEndgame() (index int) {
	index, minCount := -1, -1
	for i, v := range a.downloaderCount {
		if v >= 0 && (minCount == -1 || minCount > v) {
			index, minCount = i, v
		}
	}
	if index > -1 {
		a.downloaderCount[index]++
	}
	return
}

func (a *ActivePiece) recordBlock(index int) (requestCount int) {
	requestCount = a.downloaderCount[index]
	a.downloaderCount[index] = -1
	return
}

func (a *ActivePiece) isComplete() bool {
	for _, v := range a.downloaderCount {
		if v != -1 {
			return false
		}
	}
	return true
}

type TorrentSession struct {
	m               *MetaInfo
	si              *SessionInfo
	ti              *TrackerResponse
	fileStore       FileStore
	trackerInfoChan chan *TrackerResponse
	peers           map[string]*peerState
	peerMessageChan chan peerMessage
	pieceSet        *Bitset // The pieces we have
	totalPieces     int
	totalSize       int64
	lastPieceLength int
	goodPieces      int
	activePieces    map[int]*ActivePiece
	lastHeartBeat   time.Time
	dht             *DhtEngine
}

func NewTorrentSession(torrent string) (ts *TorrentSession, err error) {

	var listenPort int
	if listenPort, err = chooseListenPort(); err != nil {
		log.Println("Could not choose listen port.")
		log.Println("Peer connectivity will be affected.")
	}
	t := &TorrentSession{peers: make(map[string]*peerState),
		peerMessageChan: make(chan peerMessage),
		activePieces:    make(map[int]*ActivePiece)}
	t.m, err = getMetaInfo(torrent)
	if err != nil {
		return
	}
	log.Printf("Tracker: %v, Comment: %v, InfoHash: %x, Encoding: %v", t.m.Announce, t.m.Comment, t.m.InfoHash, t.m.Encoding)
	if e := t.m.Encoding; e != "" && e != "UTF-8" {
		log.Println("Unknown encoding", e)
		err = errors.New("Unknown encoding")
		return
	}

	fileStore, totalSize, err := NewFileStore(&t.m.Info, fileDir)
	if err != nil {
		return
	}
	t.fileStore = fileStore
	t.totalSize = totalSize
	t.lastPieceLength = int(t.totalSize % t.m.Info.PieceLength)

	log.Println("Computing pieces left")
	start := time.Now()
	good, bad, pieceSet, err := checkPieces(t.fileStore, totalSize, t.m)
	end := time.Now()
	log.Println("Took", end.Sub(start).Seconds(), "seconds")
	if err != nil {
		return
	}
	t.pieceSet = pieceSet
	t.totalPieces = int(good + bad)
	t.goodPieces = int(good)
	log.Println("Good pieces:", good, "Bad pieces:", bad)

	left := bad * t.m.Info.PieceLength
	if !t.pieceSet.IsSet(t.totalPieces - 1) {
		left = left - t.m.Info.PieceLength + int64(t.lastPieceLength)
	}
	t.si = &SessionInfo{PeerId: peerId(), Port: listenPort, Left: left}
	// TODO: Don't use DHT if torrent is private. 
	if useDHT {
		// TODO: UPnP UDP port mapping.
		if t.dht, err = NewDhtNode(t.si.PeerId, listenPort); err != nil {
			log.Println("DHT node creation error", err)
			return
		}
		go t.dht.DoDht()
	}
	return t, err
}

func (t *TorrentSession) fetchTrackerInfo(event string) {
	m, si := t.m, t.si
	log.Println("Stats: Uploaded", si.Uploaded, "Downloaded", si.Downloaded, "Left", si.Left)
	// We build the URL in two steps because of a bug on the ARM go compiler.
	// A single long concatenation would break compilation for ARM.
	u := m.Announce + "?" +
		"info_hash=" + url.QueryEscape(m.InfoHash) +
		"&peer_id=" + si.PeerId +
		"&port=" + strconv.Itoa(si.Port)
	u += "&uploaded=" + strconv.FormatInt(si.Uploaded, 10) +
		"&downloaded=" + strconv.FormatInt(si.Downloaded, 10) +
		"&left=" + strconv.FormatInt(si.Left, 10) +
		"&compact=1"
	if event != "" {
		u += "&event=" + event
	}
	ch := t.trackerInfoChan
	go func() {
		ti, err := getTrackerInfo(u)
		if ti == nil || err != nil {
			log.Println("Could not fetch tracker info:", err)
		} else {
			ch <- ti
		}
	}()
}

func connectToPeer(peer string, ch chan net.Conn) {
	// log.Println("Connecting to", peer)
	conn, err := net.Dial("tcp", peer)
	if err != nil {
		// log.Println("Failed to connect to", peer, err)
	} else {
		// log.Println("Connected to", peer)
		ch <- conn
	}
}

func (t *TorrentSession) AddPeer(conn net.Conn) {
	peer := conn.RemoteAddr().String()
	// log.Println("Adding peer", peer)
	if len(t.peers) >= MAX_NUM_PEERS {
		log.Println("We have enough peers. Rejecting additional peer", peer)
		conn.Close()
	}
	ps := NewPeerState(conn)
	ps.address = peer
	var header [68]byte
	copy(header[0:], kBitTorrentHeader[0:])
	// TODO: Announce DHT support when we're ready.
	if useDHT {
		header[27] = header[27] | 0x01
	}
	copy(header[28:48], string2Bytes(t.m.InfoHash))
	copy(header[48:68], string2Bytes(t.si.PeerId))

	t.peers[peer] = ps
	go ps.peerWriter(t.peerMessageChan, header[0:])
	go ps.peerReader(t.peerMessageChan)
	ps.SetChoke(false) // TODO: better choke policy
}

func (t *TorrentSession) ClosePeer(peer *peerState) {
	log.Println("Closing peer", peer.address)
	_ = t.removeRequests(peer)
	peer.Close()
	delete(t.peers, peer.address)
}

func (t *TorrentSession) deadlockDetector() {
	for {
		time.Sleep(15 * time.Second)
		age := time.Now().Sub(t.lastHeartBeat)
		if age > 15*time.Second {
			log.Println("Starvation or deadlock of main thread detected. Look in the stack dump for what DoTorrent() is currently doing.")
			log.Println("Last heartbeat", age.Seconds(), "seconds ago")
			panic("Killed by deadlock detector")
		}
	}
}

func (t *TorrentSession) DoTorrent() (err error) {
	t.lastHeartBeat = time.Now()
	go t.deadlockDetector()
	log.Println("Fetching torrent.")
	rechokeChan := time.Tick(1 * time.Second)
	// Start out polling tracker every 20 seconds untill we get a response.
	// Maybe be exponential backoff here?
	retrackerChan := time.Tick(20 * time.Second)
	keepAliveChan := time.Tick(60 * time.Second)
	t.trackerInfoChan = make(chan *TrackerResponse)

	conChan := make(chan net.Conn)
	go listenForPeerConnections(t.si.Port, conChan)

	DhtPeersRequestResults := make(chan map[string][]string)
	if useDHT {
		DhtPeersRequestResults = t.dht.PeersRequestResults
		go t.dht.PeersRequest(t.m.InfoHash)
	}

	t.fetchTrackerInfo("started")

	for {
		select {
		case _ = <-retrackerChan:
			if !trackerLessMode {
				t.fetchTrackerInfo("")
			}
		case dhtInfoHashPeers := <-DhtPeersRequestResults:
			newPeerCount := 0
			// key = infoHash. The torrent client currently only
			// supports one download at a time, so let's assume
			// it's the case.
			for _, peers := range dhtInfoHashPeers {
				for _, peer := range peers {
					peer = binaryToDottedPort(peer)
					if _, ok := t.peers[peer]; !ok {
						newPeerCount++
						go connectToPeer(peer, conChan)
					}
				}
			}
			log.Println("Contacting", newPeerCount, "new peers (thanks DHT!!)")
		case ti := <-t.trackerInfoChan:
			t.ti = ti
			log.Println("Torrent has", t.ti.Complete, "seeders and", t.ti.Incomplete, "leachers.")
			if !trackerLessMode {
				peers := t.ti.Peers
				log.Println("Tracker gave us", len(peers)/6, "peers")
				newPeerCount := 0
				for i := 0; i < len(peers); i += 6 {
					peer := binaryToDottedPort(peers[i : i+6])
					if _, ok := t.peers[peer]; !ok {
						newPeerCount++
						go connectToPeer(peer, conChan)
					}
				}
				log.Println("Contacting", newPeerCount, "new peers")
				interval := t.ti.Interval
				if interval < 120 {
					interval = 120
				} else if interval > 24*3600 {
					interval = 24 * 3600
				}
				log.Println("..checking again in", interval, "seconds.")
				retrackerChan = time.Tick(interval * time.Second)
				log.Println("Contacting", newPeerCount, "new peers")
			}
			interval := t.ti.Interval
			if interval < 120 {
				interval = 120
			} else if interval > 24*3600 {
				interval = 24 * 3600
			}
			log.Println("..checking again in", interval.String())
			retrackerChan = time.Tick(interval * time.Second)

		case pm := <-t.peerMessageChan:
			peer, message := pm.peer, pm.message
			peer.lastReadTime = time.Now()
			err2 := t.DoMessage(peer, message)
			if err2 != nil {
				if err2 != io.EOF {
					log.Println("Closing peer", peer.address, "because", err2)
				}
				t.ClosePeer(peer)
			}
		case conn := <-conChan:
			t.AddPeer(conn)
		case _ = <-rechokeChan:
			// TODO: recalculate who to choke / unchoke
			t.lastHeartBeat = time.Now()
			ratio := float64(0.0)
			if t.si.Downloaded > 0 {
				ratio = float64(t.si.Uploaded) / float64(t.si.Downloaded)
			}
			log.Println("Peers:", len(t.peers), "downloaded:", t.si.Downloaded,
				"uploaded:", t.si.Uploaded, "ratio", ratio)
			// TODO: Remove this hack when we support DHT and/or PEX
			// In a large well-seeded swarm, try to maintain a reasonable number of peers.
			log.Println("good, total", t.goodPieces, t.totalPieces)
			if len(t.peers) < TARGET_NUM_PEERS && t.goodPieces < t.totalPieces {
				if useDHT {
					go t.dht.PeersRequest(t.m.InfoHash)
				}
				if !trackerLessMode {
					if t.ti == nil || t.ti.Complete > 100 {
						t.fetchTrackerInfo("")
					}
				}
			}
		case _ = <-keepAliveChan:
			now := time.Now()
			for _, peer := range t.peers {
				if peer.lastReadTime.Second() != 0 && now.Sub(peer.lastReadTime) > 3*time.Minute {
					// log.Println("Closing peer", peer.address, "because timed out.")
					t.ClosePeer(peer)
					continue
				}
				err2 := t.doCheckRequests(peer)
				if err2 != nil {
					if err2 != io.EOF {
						log.Println("Closing peer", peer.address, "because", err2)
					}
					t.ClosePeer(peer)
					continue
				}
				peer.keepAlive(now)
			}
		}
	}
	return
}

func (t *TorrentSession) RequestBlock(p *peerState) (err error) {
	for k, _ := range t.activePieces {
		if p.have.IsSet(k) {
			err = t.RequestBlock2(p, k, false)
			if err != io.EOF {
				return
			}
		}
	}
	// No active pieces. (Or no suitable active pieces.) Pick one
	piece := t.ChoosePiece(p)
	if piece < 0 {
		// No unclaimed pieces. See if we can double-up on an active piece
		for k, _ := range t.activePieces {
			if p.have.IsSet(k) {
				err = t.RequestBlock2(p, k, true)
				if err != io.EOF {
					return
				}
			}
		}
	}
	if piece >= 0 {
		pieceLength := int(t.m.Info.PieceLength)
		if piece == t.totalPieces-1 {
			pieceLength = t.lastPieceLength
		}
		pieceCount := (pieceLength + STANDARD_BLOCK_LENGTH - 1) / STANDARD_BLOCK_LENGTH
		t.activePieces[piece] = &ActivePiece{make([]int, pieceCount), pieceLength}
		return t.RequestBlock2(p, piece, false)
	} else {
		p.SetInterested(false)
	}
	return
}

func (t *TorrentSession) ChoosePiece(p *peerState) (piece int) {
	n := t.totalPieces
	start := rand.Intn(n)
	piece = t.checkRange(p, start, n)
	if piece == -1 {
		piece = t.checkRange(p, 0, start)
	}
	return
}

func (t *TorrentSession) checkRange(p *peerState, start, end int) (piece int) {
	for i := start; i < end; i++ {
		if (!t.pieceSet.IsSet(i)) && p.have.IsSet(i) {
			if _, ok := t.activePieces[i]; !ok {
				return i
			}
		}
	}
	return -1
}

func (t *TorrentSession) RequestBlock2(p *peerState, piece int, endGame bool) (err error) {
	v := t.activePieces[piece]
	block := v.chooseBlockToDownload(endGame)
	if block >= 0 {
		t.requestBlockImp(p, piece, block, true)
	} else {
		return io.EOF
	}
	return
}

// Request or cancel a block
func (t *TorrentSession) requestBlockImp(p *peerState, piece int, block int, request bool) {
	begin := block * STANDARD_BLOCK_LENGTH
	req := make([]byte, 13)
	opcode := byte(6)
	if !request {
		opcode = byte(8) // Cancel
	}
	length := STANDARD_BLOCK_LENGTH
	if piece == t.totalPieces-1 {
		left := t.lastPieceLength - begin
		if left < length {
			length = left
		}
	}
	// log.Println("Requesting block", piece, ".", block, length, request)
	req[0] = opcode
	uint32ToBytes(req[1:5], uint32(piece))
	uint32ToBytes(req[5:9], uint32(begin))
	uint32ToBytes(req[9:13], uint32(length))
	requestIndex := (uint64(piece) << 32) | uint64(begin)
	if !request {
		delete(p.our_requests, requestIndex)
	} else {
		p.our_requests[requestIndex] = time.Now()
	}
	p.sendMessage(req)
	return
}

func (t *TorrentSession) RecordBlock(p *peerState, piece, begin, length uint32) (err error) {
	block := begin / STANDARD_BLOCK_LENGTH
	// log.Println("Received block", piece, ".", block)
	requestIndex := (uint64(piece) << 32) | uint64(begin)
	delete(p.our_requests, requestIndex)
	v, ok := t.activePieces[int(piece)]
	if ok {
		requestCount := v.recordBlock(int(block))
		if requestCount > 1 {
			// Someone else has also requested this, so send cancel notices
			for _, peer := range t.peers {
				if p != peer {
					if _, ok := peer.our_requests[requestIndex]; ok {
						t.requestBlockImp(peer, int(piece), int(block), false)
						requestCount--
					}
				}
			}
		}
		t.si.Downloaded += int64(length)
		if v.isComplete() {
			delete(t.activePieces, int(piece))
			ok, err = checkPiece(t.fileStore, t.totalSize, t.m, int(piece))
			if !ok || err != nil {
				log.Println("Ignoring bad piece", piece, err)
				return
			}
			t.si.Left -= int64(v.pieceLength)
			t.pieceSet.Set(int(piece))
			t.goodPieces++
			log.Println("Have", t.goodPieces, "of", t.totalPieces, "pieces.")
			if t.goodPieces == t.totalPieces {
				t.fetchTrackerInfo("completed")
				// TODO: Drop connections to all seeders.
			}
			for _, p := range t.peers {
				if p.have != nil {
					if p.have.IsSet(int(piece)) {
						// We don't do anything special. We rely on the caller
						// to decide if this peer is still interesting.
					} else {
						// log.Println("...telling ", p)
						haveMsg := make([]byte, 5)
						haveMsg[0] = 4
						uint32ToBytes(haveMsg[1:5], uint32(piece))
						p.sendMessage(haveMsg)
					}
				}
			}
		}
	} else {
		log.Println("Received a block we already have.", piece, block, p.address)
	}
	return
}

func (t *TorrentSession) doChoke(p *peerState) (err error) {
	p.peer_choking = true
	err = t.removeRequests(p)
	return
}

func (t *TorrentSession) removeRequests(p *peerState) (err error) {
	for k, _ := range p.our_requests {
		piece := int(k >> 32)
		begin := int(k)
		block := begin / STANDARD_BLOCK_LENGTH
		// log.Println("Forgetting we requested block ", piece, ".", block)
		t.removeRequest(piece, block)
	}
	p.our_requests = make(map[uint64]time.Time, MAX_OUR_REQUESTS)
	return
}

func (t *TorrentSession) removeRequest(piece, block int) {
	v, ok := t.activePieces[piece]
	if ok && v.downloaderCount[block] > 0 {
		v.downloaderCount[block]--
	}
}

func (t *TorrentSession) doCheckRequests(p *peerState) (err error) {
	now := time.Now()
	for k, v := range p.our_requests {
		if now.Sub(v).Seconds() > 30 {
			piece := int(k >> 32)
			block := int(k) / STANDARD_BLOCK_LENGTH
			// log.Println("timing out request of", piece, ".", block)
			t.removeRequest(piece, block)
		}
	}
	return
}

func (t *TorrentSession) DoMessage(p *peerState, message []byte) (err error) {
	if message == nil {
		return io.EOF // The reader or writer goroutine has exited
	}
	if len(p.id) == 0 {
		// This is the header message from the peer.
		if useDHT {
			// If 128, then it supports DHT.
			if int(message[7])&0x01 == 0x01 {
				candidate := &DhtNodeCandidate{id: p.id, address: p.address}
				// It's OK if we know this node already. The DHT engine will
				// ignore it accordingly.
				go t.dht.RemoteNodeAcquaintance(candidate)
			}
		}

		peersInfoHash := string(message[8:28])
		if peersInfoHash != t.m.InfoHash {
			return errors.New("this peer doesn't have the right info hash")
		}
		p.id = string(message[28:48])
	} else {
		if len(message) == 0 { // keep alive
			return
		}
		messageId := message[0]
		// Message 5 is optional, but must be sent as the first message.
		if p.have == nil && messageId != 5 {
			// Fill out the have bitfield
			p.have = NewBitset(t.totalPieces)
		}
		switch id := message[0]; id {
		case CHOKE:
			// log.Println("choke", p.address)
			if len(message) != 1 {
				return errors.New("Unexpected length")
			}
			err = t.doChoke(p)
		case UNCHOKE:
			// log.Println("unchoke", p.address)
			if len(message) != 1 {
				return errors.New("Unexpected length")
			}
			p.peer_choking = false
			for i := 0; i < MAX_OUR_REQUESTS; i++ {
				err = t.RequestBlock(p)
				if err != nil {
					return
				}
			}
		case INTERESTED:
			// log.Println("interested", p)
			if len(message) != 1 {
				return errors.New("Unexpected length")
			}
			p.peer_interested = true
			// TODO: Consider unchoking
		case NOT_INTERESTED:
			// log.Println("not interested", p)
			if len(message) != 1 {
				return errors.New("Unexpected length")
			}
			p.peer_interested = false
		case HAVE:
			if len(message) != 5 {
				return errors.New("Unexpected length")
			}
			n := bytesToUint32(message[1:])
			if n < uint32(p.have.n) {
				p.have.Set(int(n))
				if !p.am_interested && !t.pieceSet.IsSet(int(n)) {
					p.SetInterested(true)
				}
			} else {
				return errors.New("have index is out of range.")
			}
		case BITFIELD:
			// log.Println("bitfield", p.address)
			if p.have != nil {
				return errors.New("Late bitfield operation")
			}
			p.have = NewBitsetFromBytes(t.totalPieces, message[1:])
			if p.have == nil {
				return errors.New("Invalid bitfield data.")
			}
			t.checkInteresting(p)
		case REQUEST:
			// log.Println("request", p.address)
			if len(message) != 13 {
				return errors.New("Unexpected message length")
			}
			index := bytesToUint32(message[1:5])
			begin := bytesToUint32(message[5:9])
			length := bytesToUint32(message[9:13])
			if index >= uint32(p.have.n) {
				return errors.New("piece out of range.")
			}
			if !t.pieceSet.IsSet(int(index)) {
				return errors.New("we don't have that piece.")
			}
			if int64(begin) >= t.m.Info.PieceLength {
				return errors.New("begin out of range.")
			}
			if int64(begin)+int64(length) > t.m.Info.PieceLength {
				return errors.New("begin + length out of range.")
			}
			if length != STANDARD_BLOCK_LENGTH {
				return errors.New("Unexpected block length.")
			}
			// TODO: Asynchronous
			// p.AddRequest(index, begin, length)
			return t.sendRequest(p, index, begin, length)
		case PIECE:
			// piece
			if len(message) < 9 {
				return errors.New("unexpected message length")
			}
			index := bytesToUint32(message[1:5])
			begin := bytesToUint32(message[5:9])
			length := len(message) - 9
			if index >= uint32(p.have.n) {
				return errors.New("piece out of range.")
			}
			if t.pieceSet.IsSet(int(index)) {
				// We already have that piece, keep going
				break
			}
			if int64(begin) >= t.m.Info.PieceLength {
				return errors.New("begin out of range.")
			}
			if int64(begin)+int64(length) > t.m.Info.PieceLength {
				return errors.New("begin + length out of range.")
			}
			if length > 128*1024 {
				return errors.New("Block length too large.")
			}
			globalOffset := int64(index)*t.m.Info.PieceLength + int64(begin)
			_, err = t.fileStore.WriteAt(message[9:], globalOffset)
			if err != nil {
				return err
			}
			t.RecordBlock(p, index, begin, uint32(length))
			err = t.RequestBlock(p)
		case CANCEL:
			// log.Println("cancel")
			if len(message) != 13 {
				return errors.New("Unexpected message length")
			}
			index := bytesToUint32(message[1:5])
			begin := bytesToUint32(message[5:9])
			length := bytesToUint32(message[9:13])
			if index >= uint32(p.have.n) {
				return errors.New("piece out of range.")
			}
			if !t.pieceSet.IsSet(int(index)) {
				return errors.New("we don't have that piece.")
			}
			if int64(begin) >= t.m.Info.PieceLength {
				return errors.New("begin out of range.")
			}
			if int64(begin)+int64(length) > t.m.Info.PieceLength {
				return errors.New("begin + length out of range.")
			}
			if length != STANDARD_BLOCK_LENGTH {
				return errors.New("Unexpected block length.")
			}
			p.CancelRequest(index, begin, length)
		case PORT:
			// TODO: Implement this message.
			// We see peers sending us 16K byte messages here, so
			// it seems that we don't understand what this is.
			if len(message) != 3 {
				return errors.New(fmt.Sprintf("Unexpected length for port message:", len(message)))
			}
			candidate := &DhtNodeCandidate{id: p.id, address: p.address}
			go t.dht.RemoteNodeAcquaintance(candidate)
		default:
			return errors.New("Uknown message id")
		}
	}
	return
}

func (t *TorrentSession) sendRequest(peer *peerState, index, begin, length uint32) (err error) {
	if !peer.am_choking {
		// log.Println("Sending block", index, begin)
		buf := make([]byte, length+9)
		buf[0] = 7
		uint32ToBytes(buf[1:5], index)
		uint32ToBytes(buf[5:9], begin)
		_, err = t.fileStore.ReadAt(buf[9:],
			int64(index)*t.m.Info.PieceLength+int64(begin))
		if err != nil {
			return
		}
		peer.sendMessage(buf)
		t.si.Uploaded += int64(length)
	}
	return
}

func (t *TorrentSession) checkInteresting(p *peerState) {
	p.SetInterested(t.isInteresting(p))
}

func (t *TorrentSession) isInteresting(p *peerState) bool {
	for i := 0; i < t.totalPieces; i++ {
		if !t.pieceSet.IsSet(i) && p.have.IsSet(i) {
			return true
		}
	}
	return false
}

// TODO: See if we can overlap IO with computation

func checkPieces(fs FileStore, totalLength int64, m *MetaInfo) (good, bad int64, goodBits *Bitset, err error) {
	pieceLength := m.Info.PieceLength
	numPieces := (totalLength + pieceLength - 1) / pieceLength
	goodBits = NewBitset(int(numPieces))
	ref := m.Info.Pieces
	if len(ref) != int(numPieces*sha1.Size) {
		err = errors.New("Incorrect Info.Pieces length")
		return
	}
	currentSums, err := computeSums(fs, totalLength, m.Info.PieceLength)
	if err != nil {
		return
	}
	for i := int64(0); i < numPieces; i++ {
		base := i * sha1.Size
		end := base + sha1.Size
		if checkEqual(ref[base:end], currentSums[base:end]) {
			good++
			goodBits.Set(int(i))
		} else {
			bad++
		}
	}
	return
}

func checkEqual(ref string, current []byte) bool {
	for i := 0; i < len(current); i++ {
		if ref[i] != current[i] {
			return false
		}
	}
	return true
}

func computeSums(fs FileStore, totalLength int64, pieceLength int64) (sums []byte, err error) {
	numPieces := (totalLength + pieceLength - 1) / pieceLength
	sums = make([]byte, sha1.Size*numPieces)
	hasher := sha1.New()
	piece := make([]byte, pieceLength)
	for i := int64(0); i < numPieces; i++ {
		if i == numPieces-1 {
			piece = piece[0 : totalLength-i*pieceLength]
		}
		_, err = fs.ReadAt(piece, i*pieceLength)
		if err != nil {
			return
		}
		hasher.Reset()
		_, err = hasher.Write(piece)
		if err != nil {
			return
		}
		copy(sums[i*sha1.Size:], hasher.Sum(nil))
	}
	return
}

func checkPiece(fs FileStore, totalLength int64, m *MetaInfo, pieceIndex int) (good bool, err error) {
	ref := m.Info.Pieces
	currentSum, err := computePieceSum(fs, totalLength, m.Info.PieceLength, pieceIndex)
	if err != nil {
		return
	}
	base := pieceIndex * sha1.Size
	end := base + sha1.Size
	good = checkEqual(ref[base:end], currentSum)
	return
}

func computePieceSum(fs FileStore, totalLength int64, pieceLength int64, pieceIndex int) (sum []byte, err error) {
	numPieces := (totalLength + pieceLength - 1) / pieceLength
	hasher := sha1.New()
	piece := make([]byte, pieceLength)
	if int64(pieceIndex) == numPieces-1 {
		piece = piece[0 : totalLength-int64(pieceIndex)*pieceLength]
	}
	_, err = fs.ReadAt(piece, int64(pieceIndex)*pieceLength)
	if err != nil {
		return
	}
	_, err = hasher.Write(piece)
	if err != nil {
		return
	}
	sum = hasher.Sum(nil)
	return
}
