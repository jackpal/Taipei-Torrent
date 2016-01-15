package torrent

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"log"
	"math/rand"
	"net"
	"net/url"
	"strconv"
	"time"

	"golang.org/x/net/proxy"
)

// Code to talk to trackers.
// Implements:
//  BEP 12 Multitracker Metadata Extension
//  BEP 15 UDP Tracker Protocol

type ClientStatusReport struct {
	Event      string
	InfoHash   string
	PeerID     string
	Port       uint16
	Uploaded   uint64
	Downloaded uint64
	Left       uint64
}

func startTrackerClient(dialer proxy.Dialer, announce string, announceList [][]string, trackerInfoChan chan *TrackerResponse, reports chan ClientStatusReport) {
	if announce != "" && announceList == nil {
		// Convert the plain announce into an announceList to simplify logic
		announceList = [][]string{[]string{announce}}
	}

	if announceList != nil {
		announceList = shuffleAnnounceList(announceList)
	}

	// Discard status old status reports if they are produced more quickly than they can
	// be consumed.
	recentReports := make(chan ClientStatusReport)
	go func() {
	outerLoop:
		for {
			// Wait until we have a report.
			recentReport := <-reports
			for {
				select {
				case recentReport = <-reports:
					// discard the old report, keep the new one.
					continue
				case recentReports <- recentReport:
					// send the latest report, then wait for new report.
					continue outerLoop
				}
			}
		}
	}()

	go func() {
		for report := range recentReports {
			tr := queryTrackers(dialer, announceList, report)
			if tr != nil {
				trackerInfoChan <- tr
			}
		}
	}()
}

// Deep copy announcelist and shuffle each level.
func shuffleAnnounceList(announceList [][]string) (result [][]string) {
	result = make([][]string, len(announceList))
	for i, level := range announceList {
		result[i] = shuffleAnnounceListLevel(level)
	}
	return
}

func shuffleAnnounceListLevel(level []string) (shuffled []string) {
	items := len(level)
	shuffled = make([]string, items)
	perm := rand.Perm(items)
	for i, v := range perm {
		shuffled[v] = level[i]
	}
	return
}

func queryTrackers(dialer proxy.Dialer, announceList [][]string, report ClientStatusReport) (tr *TrackerResponse) {
	for _, level := range announceList {
		for i, tracker := range level {
			var err error
			tr, err = queryTracker(dialer, report, tracker)
			if err == nil {
				// Move successful tracker to front of slice for next announcement
				// cycle.
				copy(level[1:i+1], level[0:i])
				level[0] = tracker
				return
			}
		}
	}
	log.Println("Error: Did not successfully contact a tracker:", announceList)
	return
}

func queryTracker(dialer proxy.Dialer, report ClientStatusReport, trackerUrl string) (tr *TrackerResponse, err error) {
	u, err := url.Parse(trackerUrl)
	if err != nil {
		log.Println("Error: Invalid announce URL(", trackerUrl, "):", err)
		return
	}
	switch u.Scheme {
	case "http":
		fallthrough
	case "https":
		return queryHTTPTracker(dialer, report, u)
	case "udp":
		return queryUDPTracker(report, u)
	default:
		errorMessage := fmt.Sprintf("Unknown scheme %v in %v", u.Scheme, trackerUrl)
		log.Println(errorMessage)
		return nil, errors.New(errorMessage)
	}
}

func queryHTTPTracker(dialer proxy.Dialer, report ClientStatusReport, u *url.URL) (tr *TrackerResponse, err error) {
	uq := u.Query()
	uq.Add("info_hash", report.InfoHash)
	uq.Add("peer_id", report.PeerID)
	uq.Add("port", strconv.FormatUint(uint64(report.Port), 10))
	uq.Add("uploaded", strconv.FormatUint(report.Uploaded, 10))
	uq.Add("downloaded", strconv.FormatUint(report.Downloaded, 10))
	uq.Add("left", strconv.FormatUint(report.Left, 10))
	uq.Add("compact", "1")

	// Don't report IPv6 address, the user might prefer to keep
	// that information private when communicating with IPv4 hosts.
	if false {
		ipv6Address, err := findLocalIPV6AddressFor(u.Host)
		if err == nil {
			log.Println("our ipv6", ipv6Address)
			uq.Add("ipv6", ipv6Address)
		}
	}

	if report.Event != "" {
		uq.Add("event", report.Event)
	}

	// This might reorder the existing query string in the Announce url
	// This might break some broken trackers that don't parse URLs properly.

	u.RawQuery = uq.Encode()

	tr, err = getTrackerInfo(dialer, u.String())
	if tr == nil || err != nil {
		log.Println("Error: Could not fetch tracker info:", err)
	} else if tr.FailureReason != "" {
		log.Println("Error: Tracker returned failure reason:", tr.FailureReason)
		err = fmt.Errorf("tracker failure %s", tr.FailureReason)
	}
	return
}

func findLocalIPV6AddressFor(hostAddr string) (local string, err error) {
	// Figure out our IPv6 address to talk to a given host.
	host, hostPort, err := net.SplitHostPort(hostAddr)
	if err != nil {
		host = hostAddr
		hostPort = "1234"
	}
	dummyAddr := net.JoinHostPort(host, hostPort)
	log.Println("Looking for host ", dummyAddr)
	conn, err := net.Dial("udp6", dummyAddr)
	if err != nil {
		log.Println("No IPV6 for host ", host, err)
		return "", err
	}
	defer conn.Close()
	localAddr := conn.LocalAddr()
	local, _, err = net.SplitHostPort(localAddr.String())
	if err != nil {
		local = localAddr.String()
	}
	return
}

func queryUDPTracker(report ClientStatusReport, u *url.URL) (tr *TrackerResponse, err error) {
	serverAddr, err := net.ResolveUDPAddr("udp", u.Host)
	if err != nil {
		return
	}
	con, err := net.DialUDP("udp", nil, serverAddr)
	if err != nil {
		return
	}
	defer func() { con.Close() }()

	var connectionID uint64
	for retry := uint(0); retry < uint(8); retry++ {
		err = con.SetDeadline(time.Now().Add(15 * (1 << retry) * time.Second))
		if err != nil {
			return
		}

		connectionID, err = connectToUDPTracker(con)
		if err == nil {
			break
		}
		if nerr, ok := err.(net.Error); ok && nerr.Timeout() {
			continue
		}
		if err != nil {
			return
		}
	}

	return getAnnouncementFromUDPTracker(con, connectionID, report)
}

func connectToUDPTracker(con *net.UDPConn) (connectionID uint64, err error) {
	var connectionRequest_connectionID uint64 = 0x41727101980
	var action uint32 = 0
	transactionID := rand.Uint32()

	connectionRequest := new(bytes.Buffer)
	err = binary.Write(connectionRequest, binary.BigEndian, connectionRequest_connectionID)
	if err != nil {
		return
	}
	err = binary.Write(connectionRequest, binary.BigEndian, action)
	if err != nil {
		return
	}
	err = binary.Write(connectionRequest, binary.BigEndian, transactionID)
	if err != nil {
		return
	}

	_, err = con.Write(connectionRequest.Bytes())
	if err != nil {
		return
	}

	connectionResponseBytes := make([]byte, 16)

	var connectionResponseLen int
	connectionResponseLen, err = con.Read(connectionResponseBytes)
	if err != nil {
		return
	}
	if connectionResponseLen != 16 {
		err = fmt.Errorf("Unexpected response size %d", connectionResponseLen)
		return
	}
	connectionResponse := bytes.NewBuffer(connectionResponseBytes)
	var connectionResponseAction uint32
	err = binary.Read(connectionResponse, binary.BigEndian, &connectionResponseAction)
	if err != nil {
		return
	}
	if connectionResponseAction != 0 {
		err = fmt.Errorf("Unexpected response action %d", connectionResponseAction)
		return
	}
	var connectionResponseTransactionID uint32
	err = binary.Read(connectionResponse, binary.BigEndian, &connectionResponseTransactionID)
	if err != nil {
		return
	}
	if connectionResponseTransactionID != transactionID {
		err = fmt.Errorf("Unexpected response transactionID %x != %x",
			connectionResponseTransactionID, transactionID)
		return
	}

	err = binary.Read(connectionResponse, binary.BigEndian, &connectionID)
	if err != nil {
		return
	}
	return
}

func getAnnouncementFromUDPTracker(con *net.UDPConn, connectionID uint64, report ClientStatusReport) (tr *TrackerResponse, err error) {
	transactionID := rand.Uint32()

	announcementRequest := new(bytes.Buffer)
	err = binary.Write(announcementRequest, binary.BigEndian, connectionID)
	if err != nil {
		return
	}
	var action uint32 = 1
	err = binary.Write(announcementRequest, binary.BigEndian, action)
	if err != nil {
		return
	}
	err = binary.Write(announcementRequest, binary.BigEndian, transactionID)
	if err != nil {
		return
	}
	err = binary.Write(announcementRequest, binary.BigEndian, []byte(report.InfoHash))
	if err != nil {
		return
	}
	err = binary.Write(announcementRequest, binary.BigEndian, []byte(report.PeerID))
	if err != nil {
		return
	}
	err = binary.Write(announcementRequest, binary.BigEndian, report.Downloaded)
	if err != nil {
		return
	}
	err = binary.Write(announcementRequest, binary.BigEndian, report.Left)
	if err != nil {
		return
	}
	err = binary.Write(announcementRequest, binary.BigEndian, report.Uploaded)
	if err != nil {
		return
	}
	var event uint32 = 0
	switch report.Event {
	case "":
		event = 0
	case "completed":
		event = 1
	case "started":
		event = 2
	case "stopped":
		event = 3
	default:
		err = fmt.Errorf("Unknown event string %v", report.Event)
		return
	}
	err = binary.Write(announcementRequest, binary.BigEndian, event)
	if err != nil {
		return
	}
	var ipAddress uint32 = 0
	err = binary.Write(announcementRequest, binary.BigEndian, ipAddress)
	if err != nil {
		return
	}
	var key uint32 = 0
	err = binary.Write(announcementRequest, binary.BigEndian, key)
	if err != nil {
		return
	}

	const peerRequestCount = 10
	var numWant uint32 = peerRequestCount
	err = binary.Write(announcementRequest, binary.BigEndian, numWant)
	if err != nil {
		return
	}
	err = binary.Write(announcementRequest, binary.BigEndian, report.Port)
	if err != nil {
		return
	}

	_, err = con.Write(announcementRequest.Bytes())
	if err != nil {
		return
	}

	const minimumResponseLen = 20
	const peerDataSize = 6
	expectedResponseLen := minimumResponseLen + peerDataSize*peerRequestCount
	responseBytes := make([]byte, expectedResponseLen)

	var responseLen int
	responseLen, err = con.Read(responseBytes)
	if err != nil {
		return
	}
	if responseLen < minimumResponseLen {
		err = fmt.Errorf("Unexpected response size %d", responseLen)
		return
	}
	response := bytes.NewBuffer(responseBytes)
	var responseAction uint32
	err = binary.Read(response, binary.BigEndian, &responseAction)
	if err != nil {
		return
	}
	if action != 1 {
		err = fmt.Errorf("Unexpected response action %d", action)
		return
	}
	var responseTransactionID uint32
	err = binary.Read(response, binary.BigEndian, &responseTransactionID)
	if err != nil {
		return
	}
	if transactionID != responseTransactionID {
		err = fmt.Errorf("Unexpected response transactionID %x", responseTransactionID)
		return
	}
	var interval uint32
	err = binary.Read(response, binary.BigEndian, &interval)
	if err != nil {
		return
	}
	var leechers uint32
	err = binary.Read(response, binary.BigEndian, &leechers)
	if err != nil {
		return
	}
	var seeders uint32
	err = binary.Read(response, binary.BigEndian, &seeders)
	if err != nil {
		return
	}

	peerCount := (responseLen - minimumResponseLen) / peerDataSize
	peerDataBytes := make([]byte, peerDataSize*peerCount)
	err = binary.Read(response, binary.BigEndian, &peerDataBytes)
	if err != nil {
		return
	}

	tr = &TrackerResponse{
		Interval:   uint(interval),
		Complete:   uint(seeders),
		Incomplete: uint(leechers),
		Peers:      string(peerDataBytes)}
	return
}
