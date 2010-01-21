package main

import (
	"io"
	"log"
	"net"
	"os"
	"time"
)


const MAX_OUR_REQUESTS = 2
const MAX_PEER_REQUESTS = 10
const STANDARD_BLOCK_LENGTH = 16 * 1024

type peerState struct {
	address         string
	id              string
	writeChan       chan []byte
	lastWriteTime   int64   // In seconds
	lastReadTime    int64   // In seconds
	have            *Bitset // What the peer has told us it has
	conn            net.Conn
	am_choking      bool // this client is choking the peer
	am_interested   bool // this client is interested in the peer
	peer_choking    bool // peer is choking this client
	peer_interested bool // peer is interested in this client
	peer_requests   map[uint64]bool
	our_requests    map[uint64]int64 // What we requested, when we requested it
}

func NewPeerState(conn net.Conn) *peerState {
	writeChan := make(chan []byte)
	return &peerState{writeChan: writeChan, conn: conn,
		am_choking: true, peer_choking: true,
		peer_requests: make(map[uint64]bool, MAX_PEER_REQUESTS),
		our_requests: make(map[uint64]int64, MAX_OUR_REQUESTS)}
}

func (p *peerState) Close() {
	p.conn.Close()
	close(p.writeChan)
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
		p.peer_requests[offset] = false, false
	}
}

func (p *peerState) RemoveRequest() (index, begin, length uint32, ok bool) {
	for k, _ := range (p.peer_requests) {
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
		// log.Stderr("SetInterested", interested, p.address)
		p.am_interested = interested
		b := byte(3)
		if interested {
			b = 2
		}
		p.sendOneCharMessage(b)
	}
}

func (p *peerState) sendOneCharMessage(b byte) {
	// log.Stderr("ocm", b, p.address)
	p.sendMessage([]byte{b})
}

func (p *peerState) sendMessage(b []byte) {
	p.writeChan <- b
	p.lastWriteTime = time.Seconds()
}

func (p *peerState) keepAlive(now int64) {
	if now-p.lastWriteTime >= 120 {
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
	// log.Stderr("Writing header.")
	_, err := conn.Write(header)
	if err != nil {
		return
	}
	// log.Stderr("Writing messages")
	for {
		select {
		case msg := <-msgChan:
			// log.Stderr("Writing", len(msg), conn.RemoteAddr())
			err = writeNBOUint32(conn, uint32(len(msg)))
			if err != nil {
				return
			}
			_, err = conn.Write(msg)
			if err != nil {
				log.Stderr("Failed to write a message", conn, len(msg), msg, err)
				return
			}
		}
	}
	// log.Stderr("peerWriter exiting")
}

type peerMessage struct {
	peer    *peerState
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
		if n > 130*1024 {
			// log.Stderr("Message size too large: ", n)
			goto exit
		}
		buf := make([]byte, n)
		_, err := io.ReadFull(conn, buf)
		if err != nil {
			goto exit
		}
		msgChan <- peerMessage{peer, buf}
	}

exit:
	conn.Close()
	msgChan <- peerMessage{peer, nil}
	// log.Stderr("peerWriter exiting")
}
