package main

import (
    "bytes"
    "crypto/sha1"
    "flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"strconv"
)

var torrent *string = flag.String("torrent", "", "URL or path to a torrent file")
var fileDir *string = flag.String("fileDir", "", "path to directory where files are stored")
var debugp *bool = flag.Bool("debug", false, "Turn on debugging")
var port *int = flag.Int("port", 0, "Port to listen on. Defaults to random.")
var useUPnP *bool = flag.Bool("useUPnP", false, "Use UPnP to open port in firewall.")

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
			return
		}
		err = nat.ForwardPort("TCP", listenPort, listenPort, "Taipei-Torrent", 0)
		if err != nil {
			return
		}
    }
    return
}

var kBitTorrentHeader = []byte{'\x13', 'B', 'i', 't', 'T', 'o', 'r', 
	'r', 'e', 'n', 't', ' ', 'p', 'r', 'o', 't', 'o', 'c', 'o', 'l'}
	
func string2Bytes(s string) []byte {
    return bytes.NewBufferString(s).Bytes()
}

type TorrentSession struct {
    m *MetaInfo
    si *SessionInfo
    ti *TrackerResponse
    fileStore FileStore
    peers map[string] *peerState
    peerMessageChan chan peerMessage
    pieceSet *Bitset
    totalPieces int
}

func NewTorrentSession(torrent string) (ts *TorrentSession, err os.Error) {
    t := &TorrentSession{peers: make(map[string] *peerState),
        peerMessageChan: make(chan peerMessage)}
	t.m, err = getMetaInfo(torrent)
	if err != nil {
		return
	}
	log.Stderr("Tracker: ", t.m.Announce, " Comment: ", t.m.Comment, " Encoding: ", t.m.Encoding)
    	
	fileStore, totalSize, err := NewFileStore(&t.m.Info, *fileDir)
	if err != nil {
	    return
	}
	t.fileStore = fileStore
	
	log.Stderr("Computing pieces left")
	good, bad, pieceSet, err := checkPieces(t.fileStore, totalSize, t.m)
	t.pieceSet = pieceSet
	t.totalPieces = int(good + bad)
	log.Stderr("Good pieces: ", good, " Bad pieces: ", bad)
	
	listenPort, err := chooseListenPort()
	if err != nil {
	    log.Stderr("Could not choose listen port.")
	    return
	}
	t.si = &SessionInfo{PeerId: peerId(), Port: listenPort, Left: bad * t.m.Info.PieceLength}
	t.ti, err = getTrackerInfo(t.m, t.si)
	if err != nil {
	    log.Stderr("Could not get tracker info.")
		return
	}
	
	log.Stderr("Torrent has ", t.ti.Complete, " seeders and ", t.ti.Incomplete, " leachers.")
    return t, err
}

func (t *TorrentSession) ConnectToPeer(peerAddress string) (err os.Error) {
	peer := binaryToDottedPort(peerAddress)
	log.Stderr("Connecting to ", peer)
	c, err := net.Dial("tcp", "", peer)
	if err != nil {
		return
	}
	
	log.Stderr("Connected to ", peer)
    ps := NewPeerState(c)
    ps.address = peer
    var header [68]byte
    copy(header[0:], kBitTorrentHeader[0:])
    copy(header[28:48], string2Bytes(t.m.InfoHash))
    copy(header[48:68], string2Bytes(t.si.PeerId))
    
    t.peers[peer] = ps
    go peerWriter(ps.conn, ps.writeChan, header[0:])
    go peerReader(ps.conn, ps, t.peerMessageChan)

    ps.writeChan <- []byte{1}
    ps.am_choking = false
    ps.writeChan <- []byte{2}
    ps.am_interested = true
    
    return
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
    peers := ts.ti.Peers
    if len(peers) < 6 {
        return os.NewError("No peers.")
    }
    err = ts.ConnectToPeer(peers[0:6])
	if err != nil {
		return
	}
    for pm := range(ts.peerMessageChan) {
	    peer, message := pm.peer, pm.message
	    err2 := ts.DoMessage(peer, message)
	    if err2 != nil {
	        log.Stderr("Error: ", err2)
			ts.ClosePeer(peer)
	    }
	}
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
				log.Stderr("choke")
				if len(message) != 1 {
					return os.NewError("Unexpected length")
				}
				p.peer_choking = true
			case 1:
				log.Stderr("unchoke")
				if len(message) != 1 {
					return os.NewError("Unexpected length")
				}
				p.peer_choking = false
				// TODO: Request something!
				
			case 2:
				log.Stderr("interested")
				if len(message) != 1 {
					return os.NewError("Unexpected length")
				}
				p.peer_interested = true
				// TODO: Consider unchoking
			case 3:
				log.Stderr("not interested")
				if len(message) != 1 {
					return os.NewError("Unexpected length")
				}
				p.peer_interested = false
			case 4:
				log.Stderr("have")
				if len(message) != 5 {
					return os.NewError("Unexpected length")
				}
				n := bytesToUint32(message[1:])
				if n < uint32(p.have.n) {
					p.have.Set(int(n))
				} else {
					return os.NewError("have index is out of range.")
				}
			case 5:
				log.Stderr("bitfield")
				if p.have != nil {
					return os.NewError("Late bitfield operation")
				}
				p.have = NewBitsetFromBytes(t.totalPieces, message[1:])
				if p.have == nil {
					return os.NewError("Invalid bitfield data.")
				}
			case 6:
				log.Stderr("request")
				if len(message) != 13 {
					return os.NewError("Unexpected length")
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
					return os.NewError("Unexpected length.")
				}
				p.AddRequest(index, begin, length)
			case 7:
				log.Stderr("piece")
				if len(message) != 9 + STANDARD_BLOCK_LENGTH {
					return os.NewError("Unexpected length")
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
					return os.NewError("Unexpected length.")
				}
				_, err := t.fileStore.WriteAt(message[9:],
					int64(index) * t.m.Info.PieceLength + int64(begin))
				if err != nil {
					return err
				}
				// TODO: Update "have" if nescessary
			case 8:
				log.Stderr("cancel")
				if len(message) != 13 {
					return os.NewError("Unexpected length")
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
					return os.NewError("Unexpected length.")
				}
				p.CancelRequest(index, begin, length)
			case 9:
				log.Stderr("listen-port")
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
	return
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
	log.Stderr("Writing header.")
	_, err := conn.Write(header)
	if err != nil {
	    return
	}
	log.Stderr("Writing messages")
	for msg := range(msgChan) {
        log.Stderr("Writing a message")
        err = writeNBOUint32(conn, uint32(len(msg)))
        if err != nil {
            return
        }
        _, err = conn.Write(msg)
        if err != nil {
            log.Stderr("Failed to write a message: ", err)
            return
        }
	}
	log.Stderr("peerWriter exiting")
}

type peerMessage struct {
    peer *peerState
    message []byte // nil when peer is closed
}

func peerReader(conn net.Conn, peer *peerState, msgChan chan peerMessage) {
    // TODO: Add two-minute timeout.
	log.Stderr("Reading header.")
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
	log.Stderr("Reading messages")
	for {
        var n uint32
        n, err = readNBOUint32(conn)
        if err != nil {
            goto exit
        }
        if n == 0 {
            // it's a keep-alive message. swallow it.
            continue
        } else if n > 64*1024 {
            log.Stderr("Message size too large: ", n)
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
	log.Stderr("peerWriter exiting")
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

