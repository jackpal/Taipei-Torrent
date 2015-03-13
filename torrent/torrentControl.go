// torrentControl.go
package torrent

import (
	"errors"
	"log"
	"strconv"
)

type TorrentManager interface {
	Start(tc *TorrentControl) error
}

type TorrentControl struct {
	Flags           *TorrentFlags
	TorrentSessions map[string]*TorrentSession
	doneChan        chan<- *TorrentSession
}

//Add a torrent, by magnet uri, http link, or filepath.
//'start' determines if torrent contents start downloading immediately.
//Note: regardless of start bool, magnet uris and http links
//are fetched immediately for their tasty tasty metadata.
//Returns the resulting infohash if successful; "" and an error if not
func (tc *TorrentControl) AddTorrent(torr string, start bool) (string, error) {
	var ts *TorrentSession
	ts, err := NewTorrentSession(tc.Flags, torr, uint16(tc.Flags.Port))
	if err != nil {
		log.Println("Could not create torrent session.", err)
		return "", err
	}
	log.Printf("Creating torrent session for %x", ts.M.InfoHash)
	tc.TorrentSessions[ts.M.InfoHash] = ts
	go func(ts *TorrentSession) {
		ts.DoTorrent()
		tc.doneChan <- ts
	}(ts)
	return "", errors.New("Not implemented yet.")
}

//Returns a copy of the metadata for a particular torrent if successful;
//an zero-struct and error if not
func (tc *TorrentControl) GetMetaData(infohash string) (MetaInfo, error) {
	ts, ok := tc.TorrentSessions[infohash]
	if !ok {
		return MetaInfo{}, errors.New("Infohash not found.")
	}

	//TODO: Should probably make a new copy, just in case the manager wants to
	// mangle this one for some unfathomable reason
	return *ts.M, nil
}

//Returns the status of a particular torrent if successful;
//nil and an error if not.
//Also, if infohash == "", should return status info for Taipei-Torrent itself
func (tc *TorrentControl) GetStatus(infohash string) (map[string]string, error) {
	if infohash == "" {
		return nil, errors.New("Not implemented yet.")
	}

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
	infoH := make([]string, len(tc.TorrentSessions))
	i := 0
	for key := range tc.TorrentSessions {
		infoH[i] = key
		i++
	}
	return infoH
}

//Sets the pieces wanted for a particular torrent
//Returns nil if successful; an error if not
func (tc *TorrentControl) SetPiecesWanted(infohash string, wanted Bitset) error {
	return errors.New("Not implemented yet.")
}

//Returns the pieces requested that we have on hand (and len()==0 array is perfectly
//acceptable, and expected if we don't have any of the pieces requested;
//Nil and an error is returned if there's something wrong with the request.
//(bad infohash; wanted bitset is the wrong length, etc.)
func (tc *TorrentControl) GetPieces(infohash string, wanted Bitset) ([]chunk, error) {
	ts, ok := tc.TorrentSessions[infohash]
	if !ok {
		return nil, errors.New("Infohash not found.")
	}

	if wanted.Len() != ts.pieceSet.Len() {
		return nil, errors.New("Wanted bitset is wrong length.")
	}

	returnPieces := make([]chunk, 0, 32)
	for i := 0; i < wanted.Len(); i++ {
		if wanted.IsSet(i) && ts.pieceSet.IsSet(i) {
			globalOffset := int64(i) * ts.M.Info.PieceLength

			c := chunk{data: make([]byte, ts.pieceLength(i))}
			c.i = int64(i)
			_, err := ts.fileStore.ReadAt(c.data, globalOffset)
			if err != nil {
				log.Println("Error reading from file store:", err)
			} else {
				returnPieces = append(returnPieces, c)
			}
		}
	}

	return returnPieces, nil
}

//Adds pieces to the specified torrent.
//Might seem kinda strange, but it's useful if we're using other transfer
//mechanisms besides/instead of the bittorrent protocol.
func (tc *TorrentControl) SendPieces(infohash string, pieces []chunk) error {
	ts, ok := tc.TorrentSessions[infohash]
	if !ok {
		return errors.New("Infohash not found.")
	}

	for _, piece := range pieces {
		if !ts.pieceSet.IsSet(int(piece.i)) {
			globalOffset := piece.i * ts.M.Info.PieceLength
			_, err := ts.fileStore.WriteAt(piece.data, globalOffset)
			if err != nil {
				log.Println("Error writing to file store:", err)
			}
			if !ts.RecordPiece(uint32(piece.i), len(piece.data)) {
				log.Println("Got a bad piece from manager:", piece.i)
			}
		}
	}

	return nil
}

func (tc *TorrentControl) ResumeTorrent(infohash string) error {
	return errors.New("Not implemented yet.")
}

func (tc *TorrentControl) PauseTorrent(infohash string) error {
	return errors.New("Not implemented yet.")
}

func (tc *TorrentControl) StopTorrent(infohash string) error {
	return errors.New("Not implemented yet.")
}

func (tc *TorrentControl) RemoveTorrent(infohash string) error {
	return errors.New("Not implemented yet.")
}
