package taipei

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

func getString(m map[string]interface{}, k string) string {
	if v, ok := m[k]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func getMetaInfo(torrent string) (metaInfo *MetaInfo, err os.Error) {
	var input io.ReadCloser
	if strings.HasPrefix(torrent, "http:") {
		// 6g compiler bug prevents us from writing r, _, err :=
		var r *http.Response
		if r, _, err = http.Get(torrent); err != nil {
			return
		}
		input = r.Body
	} else {
		if input, err = os.Open(torrent, os.O_RDONLY, 0666); err != nil {
			return
		}
	}

	// We need to calcuate the sha1 of the Info map, including every value in the
	// map. The easiest way to do this is to read the data using the Decode
	// API, and then pick through it manually.
	var m interface{}
	m, err = bencode.Decode(input)
	input.Close()
	if err != nil {
		err = os.NewError("Couldn't parse torrent file phase 1: " + err.String())
		return
	}

	topMap, ok := m.(map[string]interface{})
	if !ok {
		err = os.NewError("Couldn't parse torrent file phase 2.")
		return
	}

	infoMap, ok := topMap["info"]
	if !ok {
		err = os.NewError("Couldn't parse torrent file. info")
		return
	}
	var b bytes.Buffer
	if err = bencode.Marshal(&b, infoMap); err != nil {
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
	m2.Announce = getString(topMap, "announce")
	m2.CreationDate = getString(topMap, "creation date")
	m2.Comment = getString(topMap, "comment")
	m2.CreatedBy = getString(topMap, "created by")
	m2.Encoding = getString(topMap, "encoding")

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
		log.Println(reason)
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
