package main

import (
	"bytes"
	"crypto/sha1"
	"io/ioutil"
	"http"
	"jackpal/bencode"
	"log"
	"os"
	"strconv"
)

type FileDict struct {
	Length int64
	Path   []string
	Md5sum string
}

type InfoDict struct {
	PieceLength uint64 "piece length"
	Pieces      string
	Private     int64
	Name        string
	// Single File Mode
	Length uint64
	Md5sum string
	// Multiple File mode
	Files []FileDict
}

type MetaInfo struct {
	Info         InfoDict
	InfoHash     string
	Announce     string
	CreationDate string "creation date"
	Comment      string
	CreatedBy    string "created by"
	Encoding     string
}

type initialMetaInfo struct {
	Info         map[string]interface{} // allows us to compute the sha1
	Announce     string
	CreationDate string "creation date"
	Comment      string
	CreatedBy    string "created by"
	Encoding     string
}

func getMetaInfo(url string) (metaInfo *MetaInfo, err os.Error) {
	r, _, err := http.Get(url)
	if err != nil {
		return
	}
	defer r.Body.Close()
	var m initialMetaInfo
	err = bencode.Unmarshal(r.Body, &m)
	if err != nil {
		return
	}
	var b bytes.Buffer
	if err = bencode.Marshal(&b, m.Info); err != nil {
		return
	}
	hash := sha1.New()
	hash.Write(b.Bytes())

	var m2 MetaInfo
	err = bencode.Unmarshal(&b, &m2.Info)
	if err != nil {
		return
	}

	m2.InfoHash = string(hash.Sum())
	m2.Announce = m.Announce
	m2.CreationDate = m.CreationDate
	m2.Comment = m.Comment
	m2.CreatedBy = m.CreatedBy
	m2.Encoding = m.Encoding

	metaInfo = &m2
	return
}

type TrackerResponse struct {
	FailureReason  string "failure reason"
	WarningMessage string "warning message"
	Interval       int
	MinInterval    int    "min interval"
	TrackerId      string "tracker id"
	Complete       int
	Incomplete     int
	Peers          string
}

type SessionInfo struct {
	PeerId     string
	Port       int
	Uploaded   int64
	Downloaded int64
	Left       int64
}

func getTrackerInfo(m *MetaInfo, si *SessionInfo) (tr *TrackerResponse, err os.Error) {
	url := m.Announce + "?" +
		"info_hash=" + http.URLEscape(m.InfoHash) +
		"&peer_id=" + si.PeerId +
		"&port=" + strconv.Itoa(si.Port) +
		"&uploaded=" + strconv.Itoa64(si.Uploaded) +
		"&downloaded=" + strconv.Itoa64(si.Downloaded) +
		"&left=12" + strconv.Itoa64(si.Left) +
		"&compact=1" +
		"&event=started"
	r, _, err := http.Get(url)
	if err != nil {
		return
	}
	defer r.Body.Close()
	if r.StatusCode >= 400 {
		data, _ := ioutil.ReadAll(r.Body)
		reason := "Bad Request " + string(data)
		log.Stderr(reason)
		err = os.NewError(reason)
		return
	}
	var tr2 TrackerResponse
	err = bencode.Unmarshal(r.Body, &tr2)
	r.Body.Close()
	if err != nil {
		return
	}
	tr = &tr2
	return
}
