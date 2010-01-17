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

func doTorrent() (err os.Error) {
	log.Stderr("Fetching torrent.")
	m, err := getMetaInfo(*torrent)
	if err != nil {
		return
	}
	log.Stderr("Tracker: ", m.Announce, " Comment: ", m.Comment, " Encoding: ", m.Encoding)
	
	fileStore, totalSize, err := NewFileStore(&m.Info, *fileDir)
	if err != nil {
	    return
	}
	defer fileStore.Close()
	
	log.Stderr("Computing pieces left")
	good, bad, pieceSet, err := checkPieces(fileStore, totalSize, m)
	totalPieces := good + bad
	log.Stderr("Good pieces: ", good, " Bad pieces: ", bad)
	
	listenPort, err := chooseListenPort()
	if err != nil {
	    return
	}
	si := &SessionInfo{PeerId: peerId(), Port: listenPort, Left: bad * m.Info.PieceLength}

	tr, err := getTrackerInfo(m, si)
	if err != nil {
		return
	}
	
	log.Stderr("Torrent has ", tr.Complete, " seeders and ", tr.Incomplete, " leachers.")
    peers := tr.Peers
    if len(peers) < 6 {
        err = os.NewError("No peers.")
        return
    }
	peer := binaryToDottedPort(peers[0:6])
	log.Stderr("Connecting to ", peer)
	c, err := net.Dial("tcp", "", peer)
	if err != nil {
		return
	}
	
	peerMessageChan := make(chan peerMessage)
    writeChan := make(chan []byte)
    
    ps := &peerState{writeChan: writeChan, conn: c}
    var header [68]byte
    copy(header[0:], kBitTorrentHeader[0:])
    copy(header[28:48], string2Bytes(m.InfoHash))
    copy(header[48:68], string2Bytes(si.PeerId))
    go peerWriter(ps.conn, ps.writeChan, header[0:])
    go peerReader(ps.conn, ps, peerMessageChan)
	for pm := range(peerMessageChan) {
	    log.Stderr("Peer message: ", pm)
	    peer, message := pm.peer, pm.message
	    if len(peer.id) == 0 {
	    	// This is the header message from the peer.
	    	peersInfoHash := string(message[8:28])
	    	if peersInfoHash != m.InfoHash {
	    	    log.Stderr("this peer doesn't have the right info hash")
	    	    peer.Close()
	    	    continue
	    	}
	    	peer.id = string(message[28:48])
	    } else {
	        messageId := message[0]
	        // Message 5 is optional, but must be sent as the first message.
	        if ps.have == nil && messageId != 5 {
	        	// Fill out the have bitfield
	        	ps.have = NewBitset(int(totalPieces))
	        }
	        switch id := message[0]; id {
	            case 0:
	                log.Stderr("choke")
	                if len(message) != 1 {
	                    log.Stderr("Unexpected length")
	                    peer.Close()
	                    break
	                }
	                ps.choked = true
	            case 1:
	                log.Stderr("unchoke")
	                if len(message) != 1 {
	                    log.Stderr("Unexpected length")
	                    peer.Close()
	                    break
	                }
	                ps.choked = false
	            case 2:
	                log.Stderr("interested")
	                if len(message) != 1 {
	                    log.Stderr("Unexpected length")
	                    peer.Close()
	                    break
	                }
	                ps.interested = true
	            case 3:
	                log.Stderr("not interested")
	                if len(message) != 1 {
	                    log.Stderr("Unexpected length")
	                    peer.Close()
	                    break
	                }
	                ps.interested = false
	            case 4:
	                log.Stderr("have")
	                if len(message) != 5 {
	                    log.Stderr("Unexpected length")
	                    peer.Close()
	                    break
	                }
	                n := bytesToUint32(message[1:])
	                if n < uint32(ps.have.n) {
	                    ps.have.Set(int(n))
	                } else {
	                    log.Stderr("have index is out of range.")
	                    peer.Close()
	                }
	            case 5:
	                log.Stderr("bitfield")
	                if ps.have != nil {
	                    log.Stderr("Late bitfield operation")
	                    peer.Close()
	                    break
	                }
	                ps.have = NewBitsetFromBytes(int(totalPieces), message[1:])
	                if ps.have == nil {
	                    log.Stderr("Invalid bitfield data.")
	                    peer.Close()
	                    break
	                }
	            case 6:
	                log.Stderr("request")
	                if len(message) != 13 {
	                    log.Stderr("Unexpected length")
	                    peer.Close()
	                    break
	                }
	                index := bytesToUint32(message[1:5])
	                begin := bytesToUint32(message[5:9])
	                length := bytesToUint32(message[9:13])
	                if index >= uint32(ps.have.n) {
	                    log.Stderr("piece out of range.")
	                    peer.Close()
	                    break
	                }
	                if ! pieceSet.IsSet(int(index)) {
	                    log.Stderr("we don't have that piece.")
	                    peer.Close()
	                    break
	                }
	                if int64(begin) >= m.Info.PieceLength {
	                    log.Stderr("begin out of range.")
	                    peer.Close()
	                    break
	                }
	                if int64(begin) + int64(length) > m.Info.PieceLength {
	                    log.Stderr("begin + length out of range.")
	                    peer.Close()
	                    break
	                }
	                if length != 16384 {
	                    log.Stderr("Unexpected length.")
	                    peer.Close()
	                    break
	                }
	                buf := make([]byte, length + 9)
	                buf[0] = 7
	                copy(buf[1:9], message[1:9])
	                _, err := fileStore.ReadAt(buf[9:],
	                	int64(index) * m.Info.PieceLength + int64(begin))
	                if err != nil {
	                    log.Stderr("Couldn't read data.", err)
	                    peer.Close()
	                    break
	                }
	                peer.writeChan <- buf
	            case 7:
	                log.Stderr("piece")
	                if len(message) != 9 + 16384 {
	                    log.Stderr("Unexpected length")
	                    peer.Close()
	                    break
	                }
	                index := bytesToUint32(message[1:5])
	                begin := bytesToUint32(message[5:9])
	                length := len(message) - 9
	                if index >= uint32(ps.have.n) {
	                    log.Stderr("piece out of range.")
	                    peer.Close()
	                    break
	                }
	                if pieceSet.IsSet(int(index)) {
	                    log.Stderr("we already have that piece.")
	                    // Not a hanging offense, keep going
	                    break
	                }
	                if int64(begin) >= m.Info.PieceLength {
	                    log.Stderr("begin out of range.")
	                    peer.Close()
	                    break
	                }
	                if int64(begin) + int64(length) > m.Info.PieceLength {
	                    log.Stderr("begin + length out of range.")
	                    peer.Close()
	                    break
	                }
	                if length != 16384 {
	                    log.Stderr("Unexpected length.")
	                    peer.Close()
	                    break
	                }
	                _, err := fileStore.WriteAt(message[9:],
	                	int64(index) * m.Info.PieceLength + int64(begin))
	                if err != nil {
	                    log.Stderr("Couldn't write data.", err)
	                    peer.Close()
	                    break
	                }
	            case 8:
	                log.Stderr("cancel")
	                if len(message) != 13 {
	                    log.Stderr("Unexpected length")
	                    peer.Close()
	                    break
	                }
	                // TODO: unify parsing and checking with request
	                // TODO: Do something with this method.
	            case 9:
	                log.Stderr("listen-port")
	                if len(message) != 3 {
	                    log.Stderr("Unexpected length")
	                    peer.Close()
	                    break
	                }
	            default:
	            	log.Stderr("Uknown message id ", id)
	            	peer.Close()
	        }
	    }
	}
	return
}

type peerState struct {
    id string
    writeChan chan []byte
    have *Bitset
    conn net.Conn
    choked bool
    interested bool
}

func (p *peerState) Close() {
    p.conn.Close()
    close(p.writeChan)
}

type torrentState struct {
    peers map[string] *peerState
}

// There's two goroutines per peer, one to read data from the peer, the other to
// send data to the peer.

func writeNBOUint32(conn net.Conn, n uint32) (err os.Error) {
    var buf [4]byte
    buf[0] = byte(n >> 24)
    buf[1] = byte(n >> 16)
    buf[2] = byte(n >> 8)
    buf[3] = byte(n)
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
        log.Stderr("Reading a message")
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

