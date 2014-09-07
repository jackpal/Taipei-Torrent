package torrent

import (
	"reflect"
	"testing"
)

type magnetTest struct {
	uri        string
	infoHashes []string
}

func TestParseMagnet(t *testing.T) {
	uris := []magnetTest{
		{uri: "magnet:?xt=urn:btih:bbb6db69965af769f664b6636e7914f8735141b3&dn=Ubuntu-12.04-desktop-i386.iso&tr=udp%3A%2F%2Ftracker.openbittorrent.com%3A80&tr=udp%3A%2F%2Ftracker.publicbt.com%3A80&tr=udp%3A%2F%2Ftracker.istole.it%3A6969&tr=udp%3A%2F%2Ftracker.ccc.de%3A80", infoHashes: []string{"bbb6db69965af769f664b6636e7914f8735141b3"}},
	}

	for _, u := range uris {
		m, err := parseMagnet(u.uri)
		if err != nil {
			t.Errorf("ParseMagnet failed for uri %v: %v", u.uri, err)
		}
		if !reflect.DeepEqual(u.infoHashes, m.InfoHashes) {
			t.Errorf("ParseMagnet failed, wanted %v, got %v", u.infoHashes, m.InfoHashes)
		}
	}
}
