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
		log.Printf("[ %s ] Error starting '%s': %v\n", t.M.Info.Name, t.flags.ExecOnSeeding, starterr)
		return
	}

	go func() {
		err := cmd.Wait()
		if err != nil {
			log.Printf("[ %s ] Error while executing '%s': %v\n", t.M.Info.Name, t.flags.ExecOnSeeding, err)
		} else {
			log.Printf("[ %s ] Executing finished on '%s'\n", t.M.Info.Name, t.flags.ExecOnSeeding)
		}
	}()
}
