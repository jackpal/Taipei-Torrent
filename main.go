package main

import (
    "bytes"
    "crypto/sha1"
    "flag"
	"fmt"
	"jackpal/http"
	"io"
	"log"
	"net"
	"os"
	"rand"
	"strconv"
	"time"
)

var torrent *string = flag.String("torrent", "", "URL or path to a torrent file")
var fileDir *string = flag.String("fileDir", "", "path to directory where files are stored")
var debugp *bool = flag.Bool("debug", false, "Turn on debugging")
var port *int = flag.Int("port", 0, "Port to listen on. Defaults to random.")
var useUPnP *bool = flag.Bool("useUPnP", false, "Use UPnP to open port in firewall.")

const NS_PER_S = 1000000000

func peerId() string {
	sid := "Taipei_tor_" + strconv.Itoa(os.Getpid()) + "______________"
	return sid[0:20]
}

func binaryToDottedPort(port string) string {
	return fmt.Sprintf("%d.%d.%d.%d:%d", port[0], port[1], port[2], port[3],
		(uint16(port[4])<<8)|uint16(port[5]))
}

func chooseListenPort() (listenPort int, err os.Error) {
    listenPort = *port
    if *useUPnP {
        // TODO: Look for ports currently in use. Handle collisions.
        var nat NAT
        nat, err = Discover()
		if err != nil {
		    log.Stderr("Unable to discover NAT")
			return
		}
		err = nat.ForwardPort("TCP", listenPort, listenPort, "Taipei-Torrent", 0)
		if err != nil {
		    log.Stderr("Unable to forward listen port")
			return
		}
    }
    return
}

func listenForPeerConnections(listenPort int, conChan chan net.Conn) {
    listenString := ":" + strconv.Itoa(listenPort)
    log.Stderr("Listening for peers on port:", listenString)
    listener, err := net.Listen("tcp", listenString)
	if err != nil {
		log.Stderr("Listen failed:", err)
		return
	}
	for {
        conn, err := listener.Accept()
        if err != nil {
            log.Stderr("Listener failed:", err)
        } else {
            conChan <- conn
        }
    }
}

var kBitTorrentHeader = []byte{'\x13', 'B', 'i', 't', 'T', 'o', 'r', 
	'r', 'e', 'n', 't', ' ', 'p', 'r', 'o', 't', 'o', 'c', 'o', 'l'}
	
func string2Bytes(s string) []byte {
    return bytes.NewBufferString(s).Bytes()
}

type ActivePiece struct {
    downloaderCount []int // -1 means piece is already downloaded
}

func (a *ActivePiece) chooseBlockToDownload() (index int) {
    for i,v := range(a.downloaderCount) {
        if v == 0 {
            return i
        }
    }
    return -1
}

func (a *ActivePiece) chooseBlockToDownloadEndgame() (index int) {
    index, minCount := -1, -1
    for i,v := range(a.downloaderCount) {
        if minCount == -1 || minCount > v {
            index, minCount = i, v
        }
    }
    return
}

func (a *ActivePiece) recordBlock(index int) {
    a.downloaderCount[index] = -1
}

func (a *ActivePiece) isComplete() bool {
    for _,v := range(a.downloaderCount) {
        if v != -1 {
            return false
        }
    }
    return true
}

type TorrentSession struct {
    m *MetaInfo
    si *SessionInfo
    ti *TrackerResponse
    fileStore FileStore
    peers map[string] *peerState
    peerMessageChan chan peerMessage
    pieceSet *Bitset // The pieces we have
    totalPieces int
    goodPieces int
    activePieces map[int] *ActivePiece
}

func NewTorrentSession(torrent string) (ts *TorrentSession, err os.Error) {
    t := &TorrentSession{peers: make(map[string] *peerState),
        peerMessageChan: make(chan peerMessage),
        activePieces: make(map[int] *ActivePiece)}
	t.m, err = getMetaInfo(torrent)
	if err != nil {
		return
	}
	log.Stderr("Tracker:", t.m.Announce, "Comment:", t.m.Comment, "Encoding:", t.m.Encoding)
    	
	fileStore, totalSize, err := NewFileStore(&t.m.Info, *fileDir)
	if err != nil {
	    return
	}
	t.fileStore = fileStore
	
	log.Stderr("Computing pieces left")
	good, bad, pieceSet, err := checkPieces(t.fileStore, totalSize, t.m)
	t.pieceSet = pieceSet
	t.totalPieces = int(good + bad)
	t.goodPieces = int(good)
	log.Stderr("Good pieces:", good, "Bad pieces:", bad)
	
	listenPort, err := chooseListenPort()
	if err != nil {
	    log.Stderr("Could not choose listen port.")
	    return
	}
	t.si = &SessionInfo{PeerId: peerId(), Port: listenPort, Left: bad * t.m.Info.PieceLength}
    return t, err
}

func (t *TorrentSession) fetchTrackerInfo(ch chan *TrackerResponse) {
    m, si := t.m, t.si
    log.Stderr("Stats: Uploaded", si.Uploaded, "Downloaded", si.Downloaded, "Left", si.Left)
	url := m.Announce + "?" +
		"info_hash=" + http.URLEscape(m.InfoHash) +
		"&peer_id=" + si.PeerId +
		"&port=" + strconv.Itoa(si.Port) +
		"&uploaded=" + strconv.Itoa64(si.Uploaded) +
		"&downloaded=" + strconv.Itoa64(si.Downloaded) +
		"&left=" + strconv.Itoa64(si.Left) +
		"&compact=1"
    if t.ti == nil {
		url += "&event=started"
    }
    go func () {
        ti, err := getTrackerInfo(url)
        if err != nil {
            log.Stderr("Could not fetch tracker info:", err)
        } else {
            ch <- ti
        }
    } ()
}
    
func connectToPeer(peer string, ch chan net.Conn) {
	// log.Stderr("Connecting to", peer)
	conn, err := net.Dial("tcp", "", peer)
	if err != nil {
	    // log.Stderr("Failed to connect to", peer, err)
	} else {
        ch <- conn
    }
}

func (t *TorrentSession) AddPeer(conn net.Conn) {
    peer := conn.RemoteAddr().String()
	// log.Stderr("Connected to", peer)
    ps := NewPeerState(conn)
    ps.address = peer
    var header [68]byte
    copy(header[0:], kBitTorrentHeader[0:])
    copy(header[28:48], string2Bytes(t.m.InfoHash))
    copy(header[48:68], string2Bytes(t.si.PeerId))
    
    t.peers[peer] = ps
    go peerWriter(ps.conn, ps.writeChan, header[0:])
    go peerReader(ps.conn, ps, t.peerMessageChan)
    ps.SetChoke(false) // TODO: better choke policy
}

func (t *TorrentSession) ClosePeer(peer *peerState) {
    peer.Close()
    t.peers[peer.address] = peer,false
}

func doTorrent() (err os.Error) {
	log.Stderr("Fetching torrent.")
	ts, err := NewTorrentSession(*torrent)
	if err != nil {
		return
	}
    rechokeChan := time.Tick(10 * NS_PER_S)
    // Start out polling tracker every 20 seconds untill we get a response.
    // Maybe be exponential backoff here?
    retrackerChan := time.Tick(20 * NS_PER_S)
    trackerInfoChan := make(chan *TrackerResponse)
    
    conChan := make(chan net.Conn)
    
    go listenForPeerConnections(ts.si.Port, conChan)
    
    ts.fetchTrackerInfo(trackerInfoChan)
    
    for {
        select {
        case _ = <- retrackerChan:
            ts.fetchTrackerInfo(trackerInfoChan)
        case ti := <- trackerInfoChan:
            ts.ti = ti
            log.Stderr("Torrent has", ts.ti.Complete, "seeders and", ts.ti.Incomplete, "leachers.")
            peers := ts.ti.Peers
			for i := 0; i < len(peers); i += 6 {
	            peer := binaryToDottedPort(peers[i:i+6])
	            if _, ok := ts.peers[peer]; !ok {
				    go connectToPeer(peer, conChan)
				}
			}
			interval := ts.ti.Interval
			if interval < 120 {
			    interval = 120
			} else if interval > 24 * 3600 {
			    interval = 24 * 3600
			}
			log.Stderr("..checking again in", interval, "seconds.")
			retrackerChan = time.Tick(int64(interval) * NS_PER_S)

        case pm := <- ts.peerMessageChan:
			peer, message := pm.peer, pm.message
			err2 := ts.DoMessage(peer, message)
			if err2 != nil {
				if err2 != os.EOF {
				    log.Stderr("Closing peer", peer, "because", err2)
				}
				ts.ClosePeer(peer)
		    }
		case conn := <- conChan:
		    ts.AddPeer(conn)
		case _ = <-rechokeChan:
		    // TODO: recalculate who to choke / unchoke
		}
	}
	return
}

func (t *TorrentSession) RequestBlock(p *peerState) (err os.Error) {
    for k,_ := range(t.activePieces) {
        if p.have.IsSet(k) {
            err = t.RequestBlock2(p, k)
            if err != os.EOF {
                return
            }
        }
    }
    // No active pieces. (Or no suitable active pieces.) Pick one
    piece := t.ChoosePiece(p)
    if piece >= 0 {
		pieceCount := int(t.m.Info.PieceLength / STANDARD_BLOCK_LENGTH)
		t.activePieces[piece] = &ActivePiece{make([]int, pieceCount)}
		return t.RequestBlock2(p, piece)
	} else {
	    // TODO: no longer interesting
	}
	return
}

func (t *TorrentSession) ChoosePiece(p *peerState) (piece int) {
    n := t.totalPieces
    start := rand.Intn(n)
    for i := start; i < n; i++ {
        if (! t.pieceSet.IsSet(i)) && p.have.IsSet(i) {
            return i
        }
    }
    for i := 0; i < start; i++ {
        if (! t.pieceSet.IsSet(i)) && p.have.IsSet(i) {
            return i
        }
    }
    return -1
}

func (t *TorrentSession) RequestBlock2(p *peerState, piece int) (err os.Error) {
    v := t.activePieces[piece]
	block := v.chooseBlockToDownload()
	if block >= 0 {
	    // log.Stderr("Requesting block ", strconv.Itoa(piece), ".", strconv.Itoa(block))
		v.downloaderCount[block]++
		begin := block * STANDARD_BLOCK_LENGTH
		req := make([]byte, 13)
		req[0] = 6
		uint32ToBytes(req[1:5], uint32(piece))
		uint32ToBytes(req[5:9], uint32(begin))
		uint32ToBytes(req[9:13], STANDARD_BLOCK_LENGTH)
		p.our_requests[(uint64(piece) << 32) | uint64(begin)] = true
		p.writeChan <- req
	} else {
	    return os.EOF
	}
	return
}

func (t *TorrentSession) RecordBlock(p *peerState, piece, begin uint32) (err os.Error) {
    block := begin / STANDARD_BLOCK_LENGTH
	// log.Stderr("Received block ", strconv.Itoa(int(piece)), ".", strconv.Itoa(int(block)))
    p.our_requests[(uint64(piece) << 32) | uint64(begin)] = false, false
    v, ok := t.activePieces[int(piece)]
    if ok {
		v.recordBlock(int(block))
		t.si.Downloaded += STANDARD_BLOCK_LENGTH
		if v.isComplete() {
		    t.activePieces[int(piece)] = v, false
		    // TODO: Check if the hash for this piece is good or not.
		    t.si.Left -= t.m.Info.PieceLength
		    t.pieceSet.Set(int(piece))
		    t.goodPieces++
		    log.Stderr("Have", t.goodPieces, "of", t.totalPieces, "blocks.")
		    for _,p := range(t.peers) {
		        if p.have != nil {
		            if p.have.IsSet(int(piece)) {
		                // Check if this peer is still interesting
		                t.checkInteresting(p)
		            } else {
						// log.Stderr("...telling ", p)
						haveMsg := make([]byte, 5)
						haveMsg[0] = 4
						uint32ToBytes(haveMsg[1:5], uint32(piece))
						p.writeChan <- haveMsg
					}
			    }
		    }
		}
	}
    return
}

func (t *TorrentSession) doChoke(p *peerState)(err os.Error) {
	p.peer_choking = true
	for k,_ := range(p.our_requests) {
	    piece := int(k >> 32)
	    begin := int(k)
	    block := begin / STANDARD_BLOCK_LENGTH
	    // log.Stderr("Forgetting we requested block ", piece, ".", block)
	    v, ok := t.activePieces[piece]
	    if ok && v.downloaderCount[int(block)] > 0 {
	        v.downloaderCount[int(block)]--
	    }
	}
	p.our_requests = make(map[uint64] bool, MAX_REQUESTS)
	return
}

func (t *TorrentSession) DoMessage(p *peerState, message []byte) (err os.Error) {
	if len(p.id) == 0 {
		// This is the header message from the peer.
		if message == nil {
		    return os.EOF
		}
		peersInfoHash := string(message[8:28])
		if peersInfoHash != t.m.InfoHash {
			return os.NewError("this peer doesn't have the right info hash")
		}
		p.id = string(message[28:48])
	} else {
	    if len(message) == 0 {
	        return os.EOF
	    }
		messageId := message[0]
		// Message 5 is optional, but must be sent as the first message.
		if p.have == nil && messageId != 5 {
			// Fill out the have bitfield
			p.have = NewBitset(t.totalPieces)
		}
		switch id := message[0]; id {
			case 0:
				// log.Stderr("choke")
				if len(message) != 1 {
					return os.NewError("Unexpected length")
				}
				err = t.doChoke(p)
			case 1:
				// log.Stderr("unchoke")
				if len(message) != 1 {
					return os.NewError("Unexpected length")
				}
				p.peer_choking = false
				for i := 0; i <  MAX_REQUESTS; i++ {
				    err = t.RequestBlock(p)
				    if err != nil {
				        return
				    }
				}
			case 2:
				// log.Stderr("interested")
				if len(message) != 1 {
					return os.NewError("Unexpected length")
				}
				p.peer_interested = true
				// TODO: Consider unchoking
			case 3:
				// log.Stderr("not interested")
				if len(message) != 1 {
					return os.NewError("Unexpected length")
				}
				p.peer_interested = false
			case 4:
				if len(message) != 5 {
					return os.NewError("Unexpected length")
				}
				n := bytesToUint32(message[1:])
				if n < uint32(p.have.n) {
					p.have.Set(int(n))
					if ! p.am_interested && ! t.pieceSet.IsSet(int(n)) {
					    p.SetInterested(true)
					}
				} else {
					return os.NewError("have index is out of range.")
				}
			case 5:
				// log.Stderr("bitfield")
				if p.have != nil {
					return os.NewError("Late bitfield operation")
				}
				p.have = NewBitsetFromBytes(t.totalPieces, message[1:])
				if p.have == nil {
					return os.NewError("Invalid bitfield data.")
				}
				t.checkInteresting(p)
			case 6:
				// log.Stderr("request")
				if len(message) != 13 {
					return os.NewError("Unexpected message length")
				}
				index := bytesToUint32(message[1:5])
				begin := bytesToUint32(message[5:9])
				length := bytesToUint32(message[9:13])
				if index >= uint32(p.have.n) {
					return os.NewError("piece out of range.")
				}
				if ! t.pieceSet.IsSet(int(index)) {
					return os.NewError("we don't have that piece.")
				}
				if int64(begin) >= t.m.Info.PieceLength {
					return os.NewError("begin out of range.")
				}
				if int64(begin) + int64(length) > t.m.Info.PieceLength {
					return os.NewError("begin + length out of range.")
				}
				if length != STANDARD_BLOCK_LENGTH {
					return os.NewError("Unexpected block length.")
				}
				// TODO: Asynchronous
				// p.AddRequest(index, begin, length)
				return t.sendRequest(p, index, begin, length)
			case 7:
			    // piece
				if len(message) != 9 + STANDARD_BLOCK_LENGTH {
					return os.NewError("unexpected message length")
				}
				index := bytesToUint32(message[1:5])
				begin := bytesToUint32(message[5:9])
				length := len(message) - 9
				if index >= uint32(p.have.n) {
					return os.NewError("piece out of range.")
				}
				if t.pieceSet.IsSet(int(index)) {
					// We already have that piece, keep going
					break
				}
				if int64(begin) >= t.m.Info.PieceLength {
					return os.NewError("begin out of range.")
				}
				if int64(begin) + int64(length) > t.m.Info.PieceLength {
					return os.NewError("begin + length out of range.")
				}
				if length != STANDARD_BLOCK_LENGTH {
					return os.NewError("Unexpected block length.")
				}
				globalOffset := int64(index) * t.m.Info.PieceLength + int64(begin)
				_, err = t.fileStore.WriteAt(message[9:], globalOffset)
				if err != nil {
					return err
				}
				t.RecordBlock(p, index, begin)
				err = t.RequestBlock(p)
			case 8:
				log.Stderr("cancel")
				if len(message) != 13 {
					return os.NewError("Unexpected message length")
				}
				index := bytesToUint32(message[1:5])
				begin := bytesToUint32(message[5:9])
				length := bytesToUint32(message[9:13])
				if index >= uint32(p.have.n) {
					return os.NewError("piece out of range.")
				}
				if ! t.pieceSet.IsSet(int(index)) {
					return os.NewError("we don't have that piece.")
				}
				if int64(begin) >= t.m.Info.PieceLength {
					return os.NewError("begin out of range.")
				}
				if int64(begin) + int64(length) > t.m.Info.PieceLength {
					return os.NewError("begin + length out of range.")
				}
				if length != STANDARD_BLOCK_LENGTH {
					return os.NewError("Unexpected block length.")
				}
				p.CancelRequest(index, begin, length)
			case 9:
				// TODO: Implement this message.
				// We see peers sending us 16K byte messages here, so
				// it seems that we don't understand what this is.
				log.Stderr("port len=", len(message))
				if len(message) != 3 {
					return os.NewError("Unexpected length")
				}
			default:
				return os.NewError("Uknown message id")
		}
	}
	return
}

func (t *TorrentSession) sendRequest(peer *peerState,
    index, begin, length uint32) (err os.Error) {
    if ! peer.am_choking {
        // log.Stderr("Sending block", index, begin)
		buf := make([]byte, length + 9)
		buf[0] = 7
		uint32ToBytes(buf[1:5], index)
		uint32ToBytes(buf[5:9], begin)
		_, err = t.fileStore.ReadAt(buf[9:],
			int64(index) * t.m.Info.PieceLength + int64(begin))
		if err != nil {
			return
		}
		peer.writeChan <- buf
		t.si.Uploaded += STANDARD_BLOCK_LENGTH
	}
	return
}

func (t *TorrentSession) checkInteresting(p *peerState) {
    p.SetInterested(t.isInteresting(p))
}

func (t *TorrentSession) isInteresting(p *peerState) bool {
    for i := 0; i < t.totalPieces; i++ {
		if ! t.pieceSet.IsSet(i) && p.have.IsSet(i) {
			return true
		}
	}
	return false
}

const MAX_REQUESTS = 10
const STANDARD_BLOCK_LENGTH = 16*1024

type peerState struct {
    address string
    id string
    writeChan chan []byte
    have *Bitset // What the peer has told us it has
    conn net.Conn
    am_choking bool // this client is choking the peer
	am_interested bool // this client is interested in the peer
    peer_choking bool // peer is choking this client
    peer_interested bool // peer is interested in this client
    peer_requests map[uint64] bool
    our_requests map[uint64] bool
}

func NewPeerState(conn net.Conn) *peerState {
    writeChan := make(chan []byte)
    return &peerState{writeChan: writeChan, conn: conn,
    	am_choking: true, peer_choking: true,
    	peer_requests: make(map[uint64] bool, MAX_REQUESTS),
    	our_requests: make(map[uint64] bool, MAX_REQUESTS)}
}

func (p *peerState) Close() {
    p.conn.Close()
    close(p.writeChan)
}

func (p *peerState) AddRequest(index, begin, length uint32) {
	if !p.am_choking && len(p.peer_requests) < MAX_REQUESTS {
		offset := (uint64(index) << 32) | uint64(begin)
		p.peer_requests[offset] = true
	}
}

func (p *peerState) CancelRequest(index, begin, length uint32) {
    offset := (uint64(index) << 32) | uint64(begin)
    if _, ok := p.peer_requests[offset]; ok {
        p.peer_requests[offset] = false, false
    }
}

func (p *peerState) RemoveRequest() (index, begin, length uint32, ok bool) {
    for k,_ := range(p.peer_requests) {
        index, begin = uint32(k >> 32), uint32(k)
        length = STANDARD_BLOCK_LENGTH
        ok = true
        return
    }
    return
}

func (p *peerState) SetChoke(choke bool) {
    if choke != p.am_choking {
		p.am_choking = choke
		b := byte(1)
		if choke {
		    b = 0
		    p.peer_requests = make(map[uint64] bool, MAX_REQUESTS)
		}
		p.sendOneCharMessage(b)
	}
}

func (p *peerState) SetInterested(interested bool) {
    if interested != p.am_interested {
        p.am_interested = interested
        b := byte(3)
        if interested {
            b = 2
        }
        p.sendOneCharMessage(b)
    }
}

func (p *peerState) sendOneCharMessage(b byte) {
    p.writeChan <- []byte{b}
}

// There's two goroutines per peer, one to read data from the peer, the other to
// send data to the peer.

func uint32ToBytes(buf []byte, n uint32) {
    buf[0] = byte(n >> 24)
    buf[1] = byte(n >> 16)
    buf[2] = byte(n >> 8)
    buf[3] = byte(n)
}

func writeNBOUint32(conn net.Conn, n uint32) (err os.Error) {
    var buf [4]byte
	uint32ToBytes(&buf, n)
    _, err = conn.Write(buf[0:])
    return
}

func bytesToUint32(buf []byte) uint32 {
    return (uint32(buf[0]) << 24) |
        (uint32(buf[1]) << 16) |
        (uint32(buf[2]) << 8) | uint32(buf[3])
}

func readNBOUint32(conn net.Conn) (n uint32, err os.Error) {
    var buf [4]byte
    _, err = conn.Read(buf[0:])
    if err != nil {
        return
    }
    n = bytesToUint32(buf[0:])
    return
}

func peerWriter(conn net.Conn, msgChan chan []byte, header []byte) {
    // TODO: Add one-minute keep-alive messages.
	// log.Stderr("Writing header.")
	_, err := conn.Write(header)
	if err != nil {
	    return
	}
	// log.Stderr("Writing messages")
	// TODO: keep-alive
	for msg := range(msgChan) {
	    // log.Stderr("Writing", len(msg), conn.RemoteAddr())
        err = writeNBOUint32(conn, uint32(len(msg)))
        if err != nil {
            return
        }
        _, err = conn.Write(msg)
        if err != nil {
            log.Stderr("Failed to write a message", msg, err)
            return
        }
	}
	// log.Stderr("peerWriter exiting")
}

type peerMessage struct {
    peer *peerState
    message []byte // nil when peer is closed
}

func peerReader(conn net.Conn, peer *peerState, msgChan chan peerMessage) {
    // TODO: Add two-minute timeout.
	// log.Stderr("Reading header.")
	var header [68]byte
	_, err := conn.Read(header[0:1])
	if err != nil {
	    goto exit
	}
	if header[0] != 19 {
	    goto exit
	}
	_, err = conn.Read(header[1:20])
	if err != nil {
	    goto exit
	}
	if string(header[1:20]) != "BitTorrent protocol" {
	    goto exit
	}
	// Read rest of header
	_, err = conn.Read(header[20:])
	if err != nil {
	    goto exit
	}
	msgChan <- peerMessage{peer, header[20:]}
	// log.Stderr("Reading messages")
	for {
        var n uint32
        n, err = readNBOUint32(conn)
        if err != nil {
            goto exit
        }
        if n == 0 {
            // it's a keep-alive message. swallow it.
            // log.Stderr("Received keep-alive message.")
            continue
        } else if n > 64*1024 {
            // log.Stderr("Message size too large: ", n)
            goto exit
        }
        buf := make([]byte, n)
        _, err :=  io.ReadFull(conn, buf)
        if err != nil {
            goto exit
        }
        msgChan <- peerMessage{peer, buf}
	}
	
exit:
	conn.Close()
    msgChan <- peerMessage {peer, nil}
	// log.Stderr("peerWriter exiting")
}


func checkPieces(fs FileStore, totalLength int64, m *MetaInfo) (good, bad int64, goodBits *Bitset, err os.Error) {
	currentSums, err := computeSums(fs, totalLength, m.Info.PieceLength)
	if err != nil {
	    return
	}
	pieceLength := m.Info.PieceLength
    numPieces := (totalLength + pieceLength - 1) / pieceLength
    goodBits = NewBitset(int(numPieces))
    ref := m.Info.Pieces
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
	
func computeSums(fs FileStore, totalLength int64, pieceLength int64) (sums []byte, err os.Error) {
    numPieces := (totalLength + pieceLength - 1) / pieceLength;
    sums = make([]byte, sha1.Size * numPieces)
    hasher := sha1.New()
    piece := make([]byte, pieceLength)
    for i := int64(0); i < numPieces; i++ {
        _, err := fs.ReadAt(piece, i * pieceLength)
        if err != nil {
            return
        }
        hasher.Reset()
        _, err = hasher.Write(piece)
        if err != nil {
            return
        }
        copy(sums[i * sha1.Size:], hasher.Sum())
    }
    return
}

func main() {
	// testBencode()
	// testUPnP()
    flag.Parse()
	log.Stderr("Starting.")
	err := doTorrent()
	if err != nil {
	    log.Stderr("Failed: ", err)
	} else {
	    log.Stderr("Done")
	}
}

