// torrentControl.go
package torrent

import (
	"errors"
	"strconv"
)

type TorrentManager interface {
	Start() error
}

type TorrentControl struct {
	TorrentSessions map[string]*TorrentSession
}

//Add a torrent, by magnet uri, http link, or filepath.
//'start' determines if torrent contents start downloading immediately.
//Note: regardless of start bool, magnet uris and http links
//are fetched immediately for their tasty tasty metadata.
//Returns the resulting infohash if successful; "" and an error if not
func (tc *TorrentControl) AddTorrent(torr string, start bool) (string, error) {
	return "", nil
}

//Returns a copy of the metadata for a particular torrent if successful;
//an zero-struct and error if not
func (tc *TorrentControl) GetMetaData(infohash string) (MetaInfo, error) {
	return MetaInfo{}, nil
}

//Returns the status of a particular torrent if successful;
//nil and an error if not.
//Also, if infohash == "", should return status info for Taipei-Torrent itself
func (tc *TorrentControl) GetStatus(infohash string) (map[string]string, error) {
	ts, ok := tc.TorrentSessions[infohash]
	if !ok {
		return nil, errors.New("Infohash not found.")
	}
	var percentComplete float64 = 0
	if ts.totalPieces > 0 {
		percentComplete = float64(ts.goodPieces*100) / float64(ts.totalPieces)
	}
	status := make(map[string]string)
	status["Percent"] = strconv.FormatFloat(percentComplete, 'f', 1, 64)
	status["Name"] = ts.torrentFile
	return status, nil
}

//Returns an array of infostrings for all the torrents
func (tc *TorrentControl) GetTorrentList() []string {
	infoH := make([]string, 0, len(tc.TorrentSessions))

	for key := range tc.TorrentSessions {
		infoH = append(infoH, key)
	}

	return infoH
}

//Sets the pieces wanted for a particular torrent
//Returns nil if successful; an error if not
func (tc *TorrentControl) SetPiecesWanted(infohash string, wanted Bitset) error {
	return nil
}

//Returns the pieces requested that we have on hand (and len()==0 array is perfectly
//acceptable, and expected if we don't have any of the pieces requested;
//An error is returned if there's something wrong with the request.
//(bad infohash; wanted bitset is the wrong length, etc.)
func (tc *TorrentControl) GetPieces(infohash string, wanted Bitset) ([]chunk, error) {
	return nil, nil
}

//Adds pieces to the specified torrent.
//Might seem kinda strange, but it's useful if we're using other transfer
//mechanisms besides/instead of the bittorrent protocol.
func (tc *TorrentControl) SendPieces(infohash string, pieces []chunk) error {
	return nil
}

func (tc *TorrentControl) ResumeTorrent(infohash string) error {
	return nil
}

func (tc *TorrentControl) PauseTorrent(infohash string) error {
	return nil
}

func (tc *TorrentControl) StopTorrent(infohash string) error {
	return nil
}

func (tc *TorrentControl) RemoveTorrent(infohash string) error {
	return nil
}
