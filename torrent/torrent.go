package torrent

import (
	"bufio"
	"bytes"
	"crypto/sha1"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"math"
	"math/rand"
	"net"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	bencode "code.google.com/p/bencode-go"
	"github.com/nictuku/dht"
	"github.com/nictuku/nettools"
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
	PORT      // Not implemented. For DHT support.
	EXTENSION = 20
)

const (
	EXTENSION_HANDSHAKE = iota
)

const (
	METADATA_REQUEST = iota
	METADATA_DATA
	METADATA_REJECT
)

// Should be overriden by flag. Not thread safe.
var gateway string
var fileDir string
var useDHT bool
var trackerLessMode bool
var seedRatio float64

func init() {
	flag.StringVar(&fileDir, "fileDir", ".", "path to directory where files are stored")
	// If the port is 0, picks up a random port - but the DHT will keep
	// running on port 0 because ListenUDP doesn't do that.
	// Don't use port 6881 which blacklisted by some trackers.
	flag.BoolVar(&useDHT, "useDHT", false, "Use DHT to get peers.")
	flag.BoolVar(&trackerLessMode, "trackerLessMode", false, "Do not get peers from the tracker. Good for "+
		"testing the DHT mode.")
	flag.StringVar(&gateway, "gateway", "", "IP Address of gateway.")
	flag.Float64Var(&seedRatio, "seedRatio", math.Inf(0), "Seed until ratio >= this value before quitting.")
}

func peerId() string {
	sid := "-tt" + strconv.Itoa(os.Getpid()) + "_" + strconv.FormatInt(rand.Int63(), 10)
	return sid[0:20]
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
	M                    *MetaInfo
	si                   *SessionInfo
	ti                   *TrackerResponse
	torrentHeader        []byte
	fileStore            FileStore
	trackerReportChan    chan ClientStatusReport
	trackerInfoChan      chan *TrackerResponse
	hintNewPeerChan      chan string
	addPeerChan          chan *btConn
	peers                map[string]*peerState
	peerMessageChan      chan peerMessage
	pieceSet             *Bitset // The pieces we have
	totalPieces          int
	totalSize            int64
	lastPieceLength      int
	goodPieces           int
	activePieces         map[int]*ActivePiece
	heartbeat            chan bool
	dht                  *dht.DHT
	quit                 chan bool
	trackerLessMode      bool
	torrentFile          string
	chokePolicy          ChokePolicy
	chokePolicyHeartbeat <-chan time.Time
}

func NewTorrentSession(torrent string, listenPort uint16) (ts *TorrentSession, err error) {
	t := &TorrentSession{
		peers:                make(map[string]*peerState),
		peerMessageChan:      make(chan peerMessage),
		activePieces:         make(map[int]*ActivePiece),
		quit:                 make(chan bool),
		torrentFile:          torrent,
		chokePolicy:          &ClassicChokePolicy{},
		chokePolicyHeartbeat: time.Tick(10 * time.Second),
	}
	fromMagnet := strings.HasPrefix(torrent, "magnet:")
	t.M, err = GetMetaInfo(torrent)
	if err != nil {
		return
	}
	dhtAllowed := useDHT && t.M.Info.Private == 0
	if dhtAllowed {
		// TODO: UPnP UDP port mapping.
		cfg := dht.NewConfig()
		cfg.Port = int(listenPort)
		cfg.NumTargetPeers = TARGET_NUM_PEERS
		if t.dht, err = dht.New(cfg); err != nil {
			log.Println("DHT node creation error", err)
			return
		}
		go t.dht.Run()
	}

	t.si = &SessionInfo{
		PeerId:        peerId(),
		Port:          listenPort,
		UseDHT:        dhtAllowed,
		FromMagnet:    fromMagnet,
		HaveTorrent:   false,
		ME:            &MetaDataExchange{},
		OurExtensions: map[int]string{1: "ut_metadata"},
	}
	t.setHeader()

	if !t.si.FromMagnet {
		t.load()
	}
	return t, err
}

func (t *TorrentSession) reload(metadata string) {
	var info InfoDict
	err := bencode.Unmarshal(bytes.NewReader([]byte(metadata)), &info)
	if err != nil {
		log.Println("Error when reloading torrent: ", err)
		return
	}

	t.M.Info = info
	t.load()
}

func (t *TorrentSession) load() {
	var err error

	log.Printf("Tracker: %v, Comment: %v, InfoHash: %x, Encoding: %v, Private: %v",
		t.M.AnnounceList, t.M.Comment, t.M.InfoHash, t.M.Encoding, t.M.Info.Private)
	if e := t.M.Encoding; e != "" && e != "UTF-8" {
		return
	}

	if t.M.Announce == "" {
		t.trackerLessMode = true
	} else {
		t.trackerLessMode = trackerLessMode
	}

	ext := ".torrent"
	dir := fileDir
	if len(t.M.Info.Files) != 0 {
		torrentName := t.M.Info.Name
		if torrentName == "" {
			torrentName = filepath.Base(t.torrentFile)
		}
		// canonicalize the torrent path and make sure it doesn't start with ".."
		torrentName = path.Clean("/" + torrentName)
		dir += torrentName
		if dir[len(dir)-len(ext):] == ext {
			dir = dir[:len(dir)-len(ext)]
		}
	}

	var fileSystem FileSystem
	fileSystem, err = NewOSFileSystem(dir)
	if err != nil {
		return
	}

	t.fileStore, t.totalSize, err = NewFileStore(&t.M.Info, fileSystem)
	if err != nil {
		return
	}
	t.lastPieceLength = int(t.totalSize % t.M.Info.PieceLength)
	if t.lastPieceLength == 0 { // last piece is a full piece
		t.lastPieceLength = int(t.M.Info.PieceLength)
	}

	start := time.Now()
	good, bad, pieceSet, err := checkPieces(t.fileStore, t.totalSize, t.M)
	end := time.Now()
	log.Printf("Computed missing pieces (%.2f seconds)", end.Sub(start).Seconds())
	if err != nil {
		return
	}
	t.pieceSet = pieceSet
	t.totalPieces = good + bad
	t.goodPieces = good

	left := uint64(bad) * uint64(t.M.Info.PieceLength)
	if !t.pieceSet.IsSet(t.totalPieces - 1) {
		left = left - uint64(t.M.Info.PieceLength) + uint64(t.lastPieceLength)
	}
	t.si.Left = left

	log.Println("Good pieces:", good, "Bad pieces:", bad, "Bytes left:", left)

	t.si.HaveTorrent = true
}

func (t *TorrentSession) pieceLength(piece int) int {
	if piece < t.totalPieces-1 {
		return int(t.M.Info.PieceLength)
	}
	return t.lastPieceLength
}

func (t *TorrentSession) fetchTrackerInfo(event string) {
	m, si := t.M, t.si
	log.Println("Stats: Uploaded", si.Uploaded, "Downloaded", si.Downloaded, "Left", si.Left)
	t.trackerReportChan <- ClientStatusReport{
		event, m.InfoHash, si.PeerId, si.Port, si.Uploaded, si.Downloaded, si.Left}
}

func (ts *TorrentSession) setHeader() {
	header := make([]byte, 68)
	copy(header, kBitTorrentHeader[0:])
	if ts.si.UseDHT {
		header[27] = header[27] | 0x01
	}
	// Support Extension Protocol (BEP-0010)
	header[25] |= 0x10
	copy(header[28:48], string2Bytes(ts.M.InfoHash))
	copy(header[48:68], string2Bytes(ts.si.PeerId))
	ts.torrentHeader = header
}

func (ts *TorrentSession) Header() (header []byte) {
	return ts.torrentHeader
}

// Try to connect if the peer is not already in our peers.
// Can be called from any goroutine.
func (ts *TorrentSession) HintNewPeer(peer string) {
	ts.hintNewPeerChan <- peer
}

func (ts *TorrentSession) hintNewPeerImp(peer string) {
	if ts.mightAcceptPeer(peer) {
		go ts.connectToPeer(peer)
	}
}

func (ts *TorrentSession) mightAcceptPeer(peer string) bool {
	if len(ts.peers) < MAX_NUM_PEERS {
		if _, ok := ts.peers[peer]; !ok {
			return true
		}
	}
	return false
}

func (ts *TorrentSession) connectToPeer(peer string) {
	conn, err := proxyNetDial("tcp", peer)
	if err != nil {
		// log.Println("Failed to connect to", peer, err)
		return
	}

	_, err = conn.Write(ts.Header())
	if err != nil {
		log.Println("Failed to send header to", peer, err)
		return
	}

	theirheader, err := readHeader(conn)
	if err != nil {
		return
	}

	peersInfoHash := string(theirheader[8:28])
	id := string(theirheader[28:48])

	btconn := &btConn{
		header:   theirheader,
		Infohash: peersInfoHash,
		id:       id,
		conn:     conn,
	}
	// log.Println("Connected to", peer)
	ts.AddPeer(btconn)
}

func (t *TorrentSession) AcceptNewPeer(btconn *btConn) {
	_, err := btconn.conn.Write(t.Header())
	if err != nil {
		return
	}
	t.AddPeer(btconn)
}

// Can be called from any goroutine
func (t *TorrentSession) AddPeer(btconn *btConn) {
	t.addPeerChan <- btconn
}

func (t *TorrentSession) addPeerImp(btconn *btConn) {
	for _, p := range t.peers {
		if p.id == btconn.id {
			return
		}
	}

	theirheader := btconn.header

	peer := btconn.conn.RemoteAddr().String()
	// log.Println("Adding peer", peer)
	if len(t.peers) >= MAX_NUM_PEERS {
		log.Println("We have enough peers. Rejecting additional peer", peer)
		btconn.conn.Close()
		return
	}
	ps := NewPeerState(btconn.conn)
	ps.address = peer
	ps.id = btconn.id
	if t.si.UseDHT {
		// If 128, then it supports DHT.
		if int(theirheader[7])&0x01 == 0x01 {
			// It's OK if we know this node already. The DHT engine will
			// ignore it accordingly.
			go t.dht.AddNode(ps.address)
		}
	}

	// By default, a peer has no pieces. If it has pieces, it should send
	// a BITFIELD message as a first message
	ps.have = NewBitset(t.totalPieces)

	t.peers[peer] = ps
	go ps.peerWriter(t.peerMessageChan)
	go ps.peerReader(t.peerMessageChan)

	if int(theirheader[5])&0x10 == 0x10 {
		ps.SendExtensions(t.si.Port)
	} else {
		ps.SendBitfield(t.pieceSet)
	}
}

func (t *TorrentSession) ClosePeer(peer *peerState) {
	if t.si.ME != nil && !t.si.ME.Transferring {
		t.si.ME.Transferring = false
	}

	log.Println("Closing peer", peer.address)
	_ = t.removeRequests(peer)
	peer.Close()
	delete(t.peers, peer.address)
}

func (t *TorrentSession) deadlockDetector() {
	// Wait for a heartbeat before we start deadlock detection.
	// This handle the case where it takes a long time to find
	// a tracker.
	<-t.heartbeat
	lastHeartbeat := time.Now()
	for {
		select {
		case <-t.heartbeat:
			lastHeartbeat = time.Now()
		case <-time.After(15 * time.Second):
			age := time.Now().Sub(lastHeartbeat)
			log.Println("Starvation or deadlock of main thread detected. Look in the stack dump for what DoTorrent() is currently doing.")
			log.Println("Last heartbeat", age.Seconds(), "seconds ago")
			panic("Killed by deadlock detector")
		}
	}
}

func (t *TorrentSession) Quit() (err error) {
	t.quit <- true
	return
}

func (t *TorrentSession) Shutdown() (err error) {
	for _, peer := range t.peers {
		t.ClosePeer(peer)
	}
	if t.dht != nil {
		t.dht.Stop()
	}
	return
}

func (t *TorrentSession) DoTorrent() {
	t.heartbeat = make(chan bool, 1)
	go t.deadlockDetector()
	log.Println("Fetching torrent.")
	heartbeatChan := time.Tick(1 * time.Second)
	keepAliveChan := time.Tick(60 * time.Second)
	var retrackerChan <-chan time.Time
	t.hintNewPeerChan = make(chan string)
	t.addPeerChan = make(chan *btConn)
	if !t.trackerLessMode {
		// Start out polling tracker every 20 seconds until we get a response.
		// Maybe be exponential backoff here?
		retrackerChan = time.Tick(20 * time.Second)
		t.trackerInfoChan = make(chan *TrackerResponse)
		t.trackerReportChan = make(chan ClientStatusReport)
		startTrackerClient(t.M.Announce, t.M.AnnounceList, t.trackerInfoChan, t.trackerReportChan)
	}

	if t.si.UseDHT {
		t.dht.PeersRequest(t.M.InfoHash, true)
	}

	if !t.trackerLessMode && t.si.HaveTorrent {
		t.fetchTrackerInfo("started")
	}

	defer t.Shutdown()

	for {
		var peersRequestResults chan map[dht.InfoHash][]string
		peersRequestResults = nil
		if t.dht != nil {
			peersRequestResults = t.dht.PeersRequestResults
		}
		select {
		case <-t.chokePolicyHeartbeat:
			t.chokePeers()
		case hintNewPeer := <-t.hintNewPeerChan:
			t.hintNewPeerImp(hintNewPeer)
		case btconn := <-t.addPeerChan:
			t.addPeerImp(btconn)
		case <-retrackerChan:
			if !t.trackerLessMode {
				t.fetchTrackerInfo("")
			}
		case dhtInfoHashPeers := <-peersRequestResults:
			newPeerCount := 0
			// key = infoHash. The torrent client currently only
			// supports one download at a time, so let's assume
			// it's the case.
			for _, peers := range dhtInfoHashPeers {
				for _, peer := range peers {
					peer = dht.DecodePeerAddress(peer)
					if t.mightAcceptPeer(peer) {
						newPeerCount++
						if t.si.HaveTorrent {
							go t.connectToPeer(peer)
						}
					}
				}
			}
			// log.Println("Contacting", newPeerCount, "new peers (thanks DHT!)")
		case ti := <-t.trackerInfoChan:
			t.ti = ti
			log.Println("Torrent has", t.ti.Complete, "seeders and", t.ti.Incomplete, "leachers.")
			if !t.trackerLessMode {
				newPeerCount := 0
				{
					peers := t.ti.Peers
					if len(peers) > 0 {
						const peerLen = 6
						log.Println("Tracker gave us", len(peers)/peerLen, "peers")
						for i := 0; i < len(peers); i += peerLen {
							peer := nettools.BinaryToDottedPort(peers[i : i+peerLen])
							if t.mightAcceptPeer(peer) {
								newPeerCount++
								go t.connectToPeer(peer)
							}
						}
					}
				}
				{
					peers6 := t.ti.Peers6
					if len(peers6) > 0 {
						const peerLen = 18
						log.Println("Tracker gave us", len(peers6)/peerLen, "IPv6 peers")
						for i := 0; i < len(peers6); i += peerLen {
							peerEntry := peers6[i : i+peerLen]
							host := net.IP(peerEntry[0:16])
							port := int((uint(peerEntry[16]) << 8) | uint(peerEntry[17]))
							peer := net.JoinHostPort(host.String(), strconv.Itoa(port))
							if t.mightAcceptPeer(peer) {
								newPeerCount++
								go t.connectToPeer(peer)
							}
						}
					}
				}
				log.Println("Contacting", newPeerCount, "new peers")
			}

			interval := t.ti.Interval
			minInterval := uint(120)
			maxInterval := uint(24 * 3600)
			if interval < minInterval {
				interval = minInterval
			} else if interval > maxInterval {
				interval = maxInterval
			}
			log.Println("..checking again in", interval, "seconds.")
			retrackerChan = time.Tick(time.Duration(interval) * time.Second)

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
		case <-heartbeatChan:
			t.heartbeat <- true
			ratio := float64(0.0)
			if t.si.Downloaded > 0 {
				ratio = float64(t.si.Uploaded) / float64(t.si.Downloaded)
			}
			log.Println("Peers:", len(t.peers), "downloaded:", t.si.Downloaded,
				"uploaded:", t.si.Uploaded, "ratio", ratio)
			log.Println("good, total", t.goodPieces, t.totalPieces)
			if t.goodPieces == t.totalPieces && ratio >= seedRatio {
				log.Println("Achieved target seed ratio", seedRatio)
				return
			}
			if len(t.peers) < TARGET_NUM_PEERS && t.goodPieces < t.totalPieces {
				if t.si.UseDHT {
					go t.dht.PeersRequest(t.M.InfoHash, true)
				}
				if !t.trackerLessMode {
					if t.ti == nil || t.ti.Complete > 100 {
						t.fetchTrackerInfo("")
					}
				}
			}
		case <-keepAliveChan:
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

		case <-t.quit:
			log.Println("Quitting torrent session")
			return
		}
	}
}

func (t *TorrentSession) chokePeers() (err error) {
	log.Printf("Choking peers")
	peers := t.peers
	chokers := make([]Choker, 0, len(peers))
	for _, peer := range peers {
		if peer.peer_interested {
			peer.computeDownloadRate()
			log.Printf("%s %g bps", peer.address, peer.DownloadBPS())
			chokers = append(chokers, Choker(peer))
		}
	}
	var unchokeCount int
	unchokeCount, err = t.chokePolicy.Choke(chokers)
	if err != nil {
		return
	}
	for i, c := range chokers {
		shouldChoke := i >= unchokeCount
		if peer, ok := c.(*peerState); ok {
			if shouldChoke != peer.am_choking {
				log.Printf("Changing choke status %v -> %v", peer.address, shouldChoke)
				peer.SetChoke(shouldChoke)
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
		pieceLength := t.pieceLength(piece)
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
	opcode := byte(REQUEST)
	if !request {
		opcode = byte(CANCEL)
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
		t.si.Downloaded += uint64(length)
		if v.isComplete() {
			delete(t.activePieces, int(piece))
			ok, err = checkPiece(t.fileStore, t.totalSize, t.M, int(piece))
			if !ok || err != nil {
				log.Println("Closing peer that sent a bad piece", piece, p.id, err)
				p.Close()
				return
			}
			t.si.Left -= uint64(v.pieceLength)
			t.pieceSet.Set(int(piece))
			t.goodPieces++
			var percentComplete float32 = 0
			if t.totalPieces > 0 {
				percentComplete = float32(t.goodPieces*100) / float32(t.totalPieces)
			}
			log.Println("Have", t.goodPieces, "of", t.totalPieces,
				"pieces", percentComplete, "% complete.")
			if t.goodPieces == t.totalPieces {
				if !t.trackerLessMode {
					t.fetchTrackerInfo("completed")
				}
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
						haveMsg[0] = HAVE
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
		begin := int(k & 0xffffffff)
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
			block := int(k&0xffffffff) / STANDARD_BLOCK_LENGTH
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
	if len(message) == 0 { // keep alive
		return
	}

	if t.si.HaveTorrent {
		err = t.generalMessage(message, p)
	} else {
		err = t.extensionMessage(message, p)
	}
	return
}

func (t *TorrentSession) extensionMessage(message []byte, p *peerState) (err error) {
	if message[0] == EXTENSION {
		err := t.DoExtension(message[1:], p)
		if err != nil {
			log.Printf("Failed extensions for %s: %s\n", p.address, err)
		}
	}
	return
}

func (t *TorrentSession) generalMessage(message []byte, p *peerState) (err error) {
	messageId := message[0]

	switch messageId {
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
		if !p.can_receive_bitfield {
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
		if int64(begin) >= t.M.Info.PieceLength {
			return errors.New("begin out of range.")
		}
		if int64(begin)+int64(length) > t.M.Info.PieceLength {
			return errors.New("begin + length out of range.")
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
		if int64(begin) >= t.M.Info.PieceLength {
			return errors.New("begin out of range.")
		}
		if int64(begin)+int64(length) > t.M.Info.PieceLength {
			return errors.New("begin + length out of range.")
		}
		if length > 128*1024 {
			return errors.New("Block length too large.")
		}
		globalOffset := int64(index)*t.M.Info.PieceLength + int64(begin)
		_, err = t.fileStore.WriteAt(message[9:], globalOffset)
		if err != nil {
			return err
		}
		p.creditDownload(int64(length))
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
		if int64(begin) >= t.M.Info.PieceLength {
			return errors.New("begin out of range.")
		}
		if int64(begin)+int64(length) > t.M.Info.PieceLength {
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
			return fmt.Errorf("Unexpected length for port message: %d", len(message))
		}
		go t.dht.AddNode(p.address)
	case EXTENSION:
		err := t.DoExtension(message[1:], p)
		if err != nil {
			log.Printf("Failed extensions for %s: %s\n", p.address, err)
		}

		if t.si.HaveTorrent {
			p.SendBitfield(t.pieceSet)
		}
	default:
		return errors.New(fmt.Sprintf("Uknown message id: %d\n", messageId))
	}

	if messageId != EXTENSION {
		p.can_receive_bitfield = false
	}

	return
}

type ExtensionHandshake struct {
	M      map[string]int "m"
	P      uint16         "p"
	V      string         "v"
	Yourip string         "yourip"
	Ipv6   string         "ipv6"
	Ipv4   string         "ipv4"
	Reqq   uint16         "reqq"

	MetadataSize uint "metadata_size"
}

func (t *TorrentSession) DoExtension(msg []byte, p *peerState) (err error) {

	var h ExtensionHandshake
	if msg[0] == EXTENSION_HANDSHAKE {
		err = bencode.Unmarshal(bytes.NewReader(msg[1:]), &h)
		if err != nil {
			log.Println("Error when unmarshaling extension handshake")
			return err
		}

		p.theirExtensions = make(map[string]int)
		for name, code := range h.M {
			p.theirExtensions[name] = code
		}

		if t.si.HaveTorrent || t.si.ME != nil && t.si.ME.Transferring {
			return
		}

		// Fill metadata info
		if h.MetadataSize != uint(0) {
			nPieces := uint(math.Ceil(float64(h.MetadataSize) / float64(16*1024)))
			t.si.ME.Pieces = make([][]byte, nPieces)
		}

		if _, ok := p.theirExtensions["ut_metadata"]; ok {
			t.si.ME.Transferring = true
			p.sendMetadataRequest(0)
		}

	} else if ext, ok := t.si.OurExtensions[int(msg[0])]; ok {
		switch ext {
		case "ut_metadata":
			t.DoMetadata(msg[1:], p)
		default:
			log.Println("Unknown extension: ", ext)
		}
	} else {
		log.Println("Unknown extension: ", int(msg[0]))
	}

	return nil
}

type MetadataMessage struct {
	MsgType   uint8 "msg_type"
	Piece     uint  "piece"
	TotalSize uint  "total_size"
}

func (t *TorrentSession) DoMetadata(msg []byte, p *peerState) {
	// We need a buffered reader because the raw data is put directly
	// after the bencoded data, and a simple reader will get all its bytes
	// eaten. A buffered reader will keep a reference to where the
	// bdecoding ended.
	br := bufio.NewReader(bytes.NewReader(msg))
	var message MetadataMessage
	err := bencode.Unmarshal(br, &message)
	if err != nil {
		log.Println("Error when parsing metadata: ", err)
		return
	}

	mt := message.MsgType
	switch mt {
	case METADATA_REQUEST:
		//TODO: Answer to metadata request
	case METADATA_DATA:

		var piece bytes.Buffer
		_, err := io.Copy(&piece, br)
		if err != nil {
			log.Println("Error when getting metadata piece: ", err)
			return
		}
		t.si.ME.Pieces[message.Piece] = piece.Bytes()

		finished := true
		for idx, data := range t.si.ME.Pieces {
			if len(data) == 0 {
				p.sendMetadataRequest(idx)
				finished = false
			}
		}

		if !finished {
			break
		}

		log.Println("Finished downloading metadata!")
		var full bytes.Buffer
		for _, piece := range t.si.ME.Pieces {
			full.Write(piece)
		}
		b := full.Bytes()

		// Verify sha
		sha := sha1.New()
		sha.Write(b)
		actual := string(sha.Sum(nil))
		if actual != t.M.InfoHash {
			log.Println("Invalid metadata")
			log.Printf("Expected %s, got %s\n", t.M.InfoHash, actual)
		}

		metadata := string(b)
		err = saveMetaInfo(metadata)
		if err != nil {
			return
		}
		t.reload(metadata)
	case METADATA_REJECT:
		log.Printf("%d didn't want to send piece %d\n", p.address, message.Piece)
	default:
		log.Println("Didn't understand metadata extension type: ", mt)
	}
}

func (t *TorrentSession) sendRequest(peer *peerState, index, begin, length uint32) (err error) {
	if !peer.am_choking {
		// log.Println("Sending block", index, begin, length)
		buf := make([]byte, length+9)
		buf[0] = PIECE
		uint32ToBytes(buf[1:5], index)
		uint32ToBytes(buf[5:9], begin)
		_, err = t.fileStore.ReadAt(buf[9:],
			int64(index)*t.M.Info.PieceLength+int64(begin))
		if err != nil {
			return
		}
		peer.sendMessage(buf)
		t.si.Uploaded += uint64(length)
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
