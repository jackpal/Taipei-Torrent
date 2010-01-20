package main

import (
	"bytes"
	"crypto/sha1"
	"io"
	"io/ioutil"
	"jackpal/http"
	"jackpal/bencode"
	"log"
	"os"
	"strings"
)

type FileDict struct {
	Length int64
	Path   []string
	Md5sum string
}

type InfoDict struct {
	PieceLength int64 "piece length"
	Pieces      string
	Private     int64
	Name        string
	// Single File Mode
	Length int64
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

func getMetaInfo(torrent string) (metaInfo *MetaInfo, err os.Error) {
	var input io.ReadCloser
	if strings.HasPrefix(torrent, "http:") {
		// 6g compiler bug prevents us from writing r, _, err :=
		var r *http.Response
		r, _, err = http.Get(torrent)
		input = r.Body
	} else {
		input, err = os.Open(torrent, os.O_RDONLY, 0666)
	}
	if err != nil {
		return
	}
	var m initialMetaInfo
	err = bencode.Unmarshal(input, &m)
	input.Close()
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

func getTrackerInfo(url string) (tr *TrackerResponse, err os.Error) {
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
