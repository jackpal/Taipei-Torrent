package main

import (
	"bytes"
	"io"
	"log"
	"net"
	"time"

	bencode "code.google.com/p/bencode-go"
)

const MAX_OUR_REQUESTS = 2
const MAX_PEER_REQUESTS = 10
const STANDARD_BLOCK_LENGTH = 16 * 1024

type peerMessage struct {
	peer    *peerState
	message []byte // nil means an error occurred
}

type peerState struct {
	address         string
	id              string
	writeChan       chan []byte
	writeChan2      chan []byte
	lastWriteTime   time.Time
	lastReadTime    time.Time
	have            *Bitset // What the peer has told us it has
	conn            net.Conn
	am_choking      bool // this client is choking the peer
	am_interested   bool // this client is interested in the peer
	peer_choking    bool // peer is choking this client
	peer_interested bool // peer is interested in this client
	peer_requests   map[uint64]bool
	our_requests    map[uint64]time.Time // What we requested, when we requested it

	theirExtensions map[string]int
}

func queueingWriter(in, out chan []byte) {
	queue := make(map[int][]byte)
	head, tail := 0, 0
L:
	for {
		if head == tail {
			select {
			case m, ok := <-in:
				if !ok {
					break L
				}
				queue[head] = m
				head++
			}
		} else {
			select {
			case m, ok := <-in:
				if !ok {
					break L
				}
				queue[head] = m
				head++
			case out <- queue[tail]:
				delete(queue, tail)
				tail++
			}
		}
	}
	// We throw away any messages waiting to be sent, including the
	// nil message that is automatically sent when the in channel is closed
	close(out)
}

func NewPeerState(conn net.Conn) *peerState {
	writeChan := make(chan []byte)
	writeChan2 := make(chan []byte)
	go queueingWriter(writeChan, writeChan2)
	return &peerState{writeChan: writeChan, writeChan2: writeChan2, conn: conn,
		am_choking: true, peer_choking: true,
		peer_requests: make(map[uint64]bool, MAX_PEER_REQUESTS),
		our_requests:  make(map[uint64]time.Time, MAX_OUR_REQUESTS)}
}

func (p *peerState) Close() {
	log.Println("Closing connection to ", p.address)
	p.conn.Close()
	// No need to close p.writeChan. Further writes to p.conn will just fail.
}

func (p *peerState) AddRequest(index, begin, length uint32) {
	if !p.am_choking && len(p.peer_requests) < MAX_PEER_REQUESTS {
		offset := (uint64(index) << 32) | uint64(begin)
		p.peer_requests[offset] = true
	}
}

func (p *peerState) CancelRequest(index, begin, length uint32) {
	offset := (uint64(index) << 32) | uint64(begin)
	if _, ok := p.peer_requests[offset]; ok {
		delete(p.peer_requests, offset)
	}
}

func (p *peerState) RemoveRequest() (index, begin, length uint32, ok bool) {
	for k, _ := range p.peer_requests {
		index, begin = uint32(k>>32), uint32(k)
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
			p.peer_requests = make(map[uint64]bool, MAX_PEER_REQUESTS)
		}
		p.sendOneCharMessage(b)
	}
}

func (p *peerState) SetInterested(interested bool) {
	if interested != p.am_interested {
		// log.Println("SetInterested", interested, p.address)
		p.am_interested = interested
		b := byte(3)
		if interested {
			b = 2
		}
		p.sendOneCharMessage(b)
	}
}

func (p *peerState) SendBitfield(bs *Bitset) {
	msg := make([]byte, len(bs.Bytes())+1)
	msg[0] = BITFIELD
	copy(msg[1:], bs.Bytes())
	p.sendMessage(msg)
}

func (p *peerState) SendExtensions(port int) {

	handshake := map[string]interface{}{
		"m": map[string]int{
			"ut_metadata": 1,
		},
		"v": "Taipei-Torrent dev",
	}

	var buf bytes.Buffer
	err := bencode.Marshal(&buf, handshake)
	if err != nil {
		//log.Println("Error when marshalling extension message")
		return
	}

	msg := make([]byte, 2+buf.Len())
	msg[0] = EXTENSION
	msg[1] = EXTENSION_HANDSHAKE
	copy(msg[2:], buf.Bytes())

	p.sendMessage(msg)
}

func (p *peerState) sendOneCharMessage(b byte) {
	// log.Println("ocm", b, p.address)
	p.sendMessage([]byte{b})
}

func (p *peerState) sendMessage(b []byte) {
	p.writeChan <- b
	p.lastWriteTime = time.Now()
}

func (p *peerState) keepAlive(now time.Time) {
	if now.Sub(p.lastWriteTime) >= 2*time.Minute {
		// log.Stderr("Sending keep alive", p)
		p.sendMessage([]byte{})
	}
}

// There's two goroutines per peer, one to read data from the peer, the other to
// send data to the peer.

func uint32ToBytes(buf []byte, n uint32) {
	buf[0] = byte(n >> 24)
	buf[1] = byte(n >> 16)
	buf[2] = byte(n >> 8)
	buf[3] = byte(n)
}

func writeNBOUint32(conn net.Conn, n uint32) (err error) {
	var buf []byte = make([]byte, 4)
	uint32ToBytes(buf, n)
	_, err = conn.Write(buf[0:])
	return
}

func bytesToUint32(buf []byte) uint32 {
	return (uint32(buf[0]) << 24) |
		(uint32(buf[1]) << 16) |
		(uint32(buf[2]) << 8) | uint32(buf[3])
}

func readNBOUint32(conn net.Conn) (n uint32, err error) {
	var buf [4]byte
	_, err = conn.Read(buf[0:])
	if err != nil {
		return
	}
	n = bytesToUint32(buf[0:])
	return
}

// This func is designed to be run as a goroutine. It
// listens for messages on a channel and sends them to a peer.

func (p *peerState) peerWriter(errorChan chan peerMessage) {
	// log.Println("Writing messages")
	for msg := range p.writeChan2 {
		// log.Println("Writing", len(msg), conn.RemoteAddr())
		err := writeNBOUint32(p.conn, uint32(len(msg)))
		if err != nil {
			goto exit
		}
		_, err = p.conn.Write(msg)
		if err != nil {
			// log.Println("Failed to write a message", p.address, len(msg), msg, err)
			goto exit
		}
	}
exit:
	// log.Println("peerWriter exiting")
	errorChan <- peerMessage{p, nil}
}

// This func is designed to be run as a goroutine. It
// listens for messages from the peer and forwards them to a channel.

func (p *peerState) peerReader(msgChan chan peerMessage) {
	// log.Println("Reading messages")
	for {
		var n uint32
		n, err := readNBOUint32(p.conn)
		if err != nil {
			break
		}
		if n > 130*1024 {
			// log.Println("Message size too large: ", n)
			break
		}

		var buf []byte
		if n == 0 {
			// keep-alive - we want an empty message
			buf = make([]byte, 1)
		} else {
			buf = make([]byte, n)
		}

		_, err = io.ReadFull(p.conn, buf)
		if err != nil {
			break
		}
		msgChan <- peerMessage{p, buf}
	}

	msgChan <- peerMessage{p, nil}
	// log.Println("peerReader exiting")
}

func (p *peerState) sendMetadataRequest(piece int) {
	log.Printf("Sending metadata request for piece %d to %s\n", piece, p.address)

	m := map[string]int{
		"msg_type": METADATA_REQUEST,
		"piece":    piece,
	}

	var raw bytes.Buffer
	err := bencode.Marshal(&raw, m)
	if err != nil {
		return
	}

	msg := make([]byte, raw.Len()+2)
	msg[0] = EXTENSION
	msg[1] = byte(p.theirExtensions["ut_metadata"])
	copy(msg[2:], raw.Bytes())

	p.sendMessage(msg)
}
