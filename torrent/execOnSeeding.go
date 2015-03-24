package torrent

import (
	"fmt"
	"log"
	"os/exec"
)

func (t *TorrentSession) execOnSeeding() {
	cmd := exec.Command(t.flags.ExecOnSeeding)
	cmd.Env = []string{
		fmt.Sprintf("TORRENT_FILE=%s", t.torrentFile),
		fmt.Sprintf("TORRENT_INFOHASH=%x", t.M.InfoHash),
	}
	starterr := cmd.Start()
	if starterr != nil {
		log.Printf("Error starting '%s': %v\n", t.flags.ExecOnSeeding, starterr)
		return
	}

	go func() {
		err := cmd.Wait()
		if err != nil {
			log.Printf("Error while executing '%s': %v\n", t.flags.ExecOnSeeding, err)
		} else {
			log.Printf("Executing finished on '%s'\n", t.flags.ExecOnSeeding)
		}
	}()
}
