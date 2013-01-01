package main

import (
	"fmt"
	"log"
	"net"
	"time"
)

// Implement the NAT-PMP protocol, typically supported by Apple routers.
// See http://tools.ietf.org/html/draft-cheshire-nat-pmp-03
//
// TODO:
//  + Register for changes to the external address.
//  + Re-register port mapping when router reboots.
//  + A mechanism for keeping a port mapping registered.

const NAT_PMP_PORT = 5351

const NAT_TRIES = 9

const NAT_INITIAL_MS = 250

type natPMPClient struct {
	gateway net.IP
}

func NewNatPMP(gateway net.IP) (nat NAT) {
	return &natPMPClient{gateway}
}

func (n *natPMPClient) GetExternalAddress() (addr net.IP, err error) {
	msg := make([]byte, 2)
	msg[0] = 0 // Version 0
	msg[1] = 0 // OP Code 0
	response, err := n.rpc(msg, 12)
	if err != nil {
		return
	}
	addr = net.IPv4(response[8], response[9], response[10], response[11])
	return
}

func (n *natPMPClient) AddPortMapping(protocol string, externalPort, internalPort int,
	description string, timeout int) (mappedExternalPort int, err error) {
	if timeout <= 0 {
		err = fmt.Errorf("timeout must not be <= 0")
		return
	}
	return n.addPortMapping(protocol, externalPort, internalPort, timeout)
}

func (n *natPMPClient) addPortMapping(protocol string, externalPort, internalPort int, lifetime int) (mappedExternalPort int, err error) {
	var opcode byte
	if protocol == "udp" {
		opcode = 1
	} else if protocol == "tcp" {
		opcode = 2
	} else {
		err = fmt.Errorf("unknown protocol %v", protocol)
		return
	}
	msg := make([]byte, 12)
	msg[0] = 0 // Version 0
	msg[1] = opcode
	writeNetworkOrderUint16(msg[4:6], uint16(internalPort))
	writeNetworkOrderUint16(msg[6:8], uint16(externalPort))
	writeNetworkOrderUint32(msg[8:12], uint32(lifetime))
	result, err := n.rpc(msg, 16)
	if err != nil {
		return
	}
	mappedExternalPort = int(readNetworkOrderUint16(result[10:12]))
	return
}

func (n *natPMPClient) deletePortMapping(protocol string, internalPort int) (err error) {
	// The RFC says to destroy a mapping, send an add-port with
	// an internalPort of the internal port to destroy, an external port of zero and a time of zero.
	_, err = n.addPortMapping(protocol, 0, internalPort, 0)
	return
}

func (n *natPMPClient) DeletePortMapping(protocol string, externalPort, internalPort int) (err error) {
	//
	return n.deletePortMapping(protocol, internalPort)
}

func (n *natPMPClient) rpc(msg []byte, resultSize int) (result []byte, err error) {
	var server net.UDPAddr
	server.IP = n.gateway
	server.Port = NAT_PMP_PORT
	conn, err := net.DialUDP("udp", nil, &server)
	if err != nil {
		return
	}
	defer conn.Close()

	result = make([]byte, resultSize)

	needNewDeadline := true

	var tries uint
	for tries = 0; tries < NAT_TRIES; {
		if needNewDeadline {
			err = conn.SetDeadline(time.Now().Add((NAT_INITIAL_MS << tries) * time.Millisecond))
			if err != nil {
				return
			}
			needNewDeadline = false
		}
		_, err = conn.Write(msg)
		if err != nil {
			return
		}
		var bytesRead int
		var remoteAddr *net.UDPAddr
		bytesRead, remoteAddr, err = conn.ReadFromUDP(result)
		if err != nil {
			if err.(net.Error).Timeout() {
				tries++
				needNewDeadline = true
				continue
			}
			return
		}
		if !remoteAddr.IP.Equal(n.gateway) {
			log.Printf("Ignoring packet because IPs differ:", remoteAddr, n.gateway)
			// Ignore this packet.
			// Continue without increasing retransmission timeout or deadline.
			continue
		}
		if bytesRead != resultSize {
			err = fmt.Errorf("unexpected result size %d, expected %d", bytesRead, resultSize)
			return
		}
		if result[0] != 0 {
			err = fmt.Errorf("unknown protocol version %d", result[0])
			return
		}
		expectedOp := msg[1] | 0x80
		if result[1] != expectedOp {
			err = fmt.Errorf("Unexpected opcode %d. Expected %d", result[1], expectedOp)
			return
		}
		resultCode := readNetworkOrderUint16(result[2:4])
		if resultCode != 0 {
			err = fmt.Errorf("Non-zero result code %d", resultCode)
			return
		}
		// If we got here the RPC is good.
		return
	}
	err = fmt.Errorf("Timed out trying to contact gateway")
	return
}

func writeNetworkOrderUint16(buf []byte, d uint16) {
	buf[0] = byte(d >> 8)
	buf[1] = byte(d)
}

func writeNetworkOrderUint32(buf []byte, d uint32) {
	buf[0] = byte(d >> 24)
	buf[1] = byte(d >> 16)
	buf[2] = byte(d >> 8)
	buf[3] = byte(d)
}

func readNetworkOrderUint16(buf []byte) uint16 {
	return (uint16(buf[0]) << 8) | uint16(buf[1])
}
