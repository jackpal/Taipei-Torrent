package tracker

import (
	"bufio"
	"fmt"
	"github.com/jackpal/Taipei-Torrent/torrent"
	"io/ioutil"
	"log"
	"math"
	"os"
	"os/exec"
	"path"
	"testing"
	"time"
)

func TestScrapeURL(t *testing.T) {
	tests := []struct{ announce, scrape string }{
		{"", ""},
		{"foo", ""},
		{"x/announce", "x/scrape"},
		{"x/announce?ad#3", "x/scrape?ad#3"},
		{"announce/x", ""},
	}
	for _, test := range tests {
		scrape := ScrapePattern(test.announce)
		if scrape != test.scrape {
			t.Errorf("ScrapeURL(%#v) = %#v. Expected %#v", test.announce, scrape, test.scrape)
		}
	}
}

func TestSwarm(t *testing.T) {
	// End-to-end test of transfering a file from one torrent client to another.
	err := testSwarm(t)
	if err != nil {
		t.Fatal("Error running testSwarm", err)
	}
}

func testSwarm(t *testing.T) (err error) {
	var rootDir string
	rootDir, err = ioutil.TempDir("", "swarm")
	if err != nil {
		return
	}
	log.Printf("Temporary directory: %s", rootDir)
	defer os.RemoveAll(rootDir)
	seedDir := path.Join(rootDir, "seed")
	err = os.Mkdir(seedDir, 0700)
	if err != nil {
		return
	}
	leechDir := path.Join(rootDir, "leech")
	err = os.Mkdir(leechDir, 0700)
	if err != nil {
		return
	}
	seedData := path.Join(seedDir, "data")
	err = createDataFile(seedData, 1024*1024)
	if err != nil {
		return
	}
	torrentFile := path.Join(rootDir, "testSwarm.torrent")
	err = createTorrentFile(torrentFile, seedData, "127.0.0.1:8080/announce")
	if err != nil {
		return
	}

	var tracker, seed, leech *exec.Cmd
	var trackerCh, seedCh, leechCh chan error
	tracker, trackerCh, err = startTracker(":8080", torrentFile)
	if err != nil {
		return
	}

	defer kill(tracker)
	seed, seedCh, err = startTorrentClient("seed", 7000, torrentFile, seedDir, math.Inf(0))
	if err != nil {
		return
	}
	defer kill(seed)
	leech, leechCh, err = startTorrentClient("leech", 7001, torrentFile, leechDir, 0)
	if err != nil {
		return
	}
	defer kill(leech)

	timeout := make(chan bool, 1)
	go func() {
		time.Sleep(10 * time.Second)
		timeout <- true
	}()
	select {
	case <-timeout:
		err = fmt.Errorf("Timout exceeded")
	case err = <-leechCh:
	case err = <-seedCh:
		if err == nil {
			err = fmt.Errorf("Seed finished. Should not have.")
		}
	case err = <-trackerCh:
		if err == nil {
			err = fmt.Errorf("Tracker finished. Should not have.")
		}
	}
	if err != nil {
		return
	}
	// Compare seed and leech versions of data file
	return
}

func startTracker(addr string, trackerFile string) (cmd *exec.Cmd, ech chan error, err error) {
	cmd = exec.Command("Taipei-Torrent", fmt.Sprintf("-createTracker=%v", addr), trackerFile)
	ech, err = startCmd("tracker", cmd)
	return
}

func startTorrentClient(name string, port int, trackerFile string, fileDir string, ratio float64) (cmd *exec.Cmd, ech chan error, err error) {
	cmd = exec.Command("Taipei-Torrent",
		fmt.Sprintf("-port=%v", port),
		fmt.Sprintf("-fileDir=%v", fileDir),
		fmt.Sprintf("-seedRatio=%v", ratio),
		trackerFile)
	ech, err = startCmd(name, cmd)
	return
}

func startCmd(name string, cmd *exec.Cmd) (ech chan error, err error) {
	log.Println("starting", name)
	out := logWriter(name)
	cmd.Stdout = &out
	cmd.Stderr = &out
	err = cmd.Start()
	if err != nil {
		return
	}
	ech = make(chan error, 1)
	go func() {
		err := cmd.Wait()
		ech <- err
	}()
	return
}

func kill(cmd *exec.Cmd) (err error) {
	err = cmd.Process.Kill()
	return
}

func createTorrentFile(torrentFileName, root, announcePath string) (err error) {
	var metaInfo *torrent.MetaInfo
	metaInfo, err = torrent.CreateMetaInfoFromFileSystem(nil, root, 0, false)
	if err != nil {
		return
	}
	metaInfo.Announce = "127.0.0.1:8080/announce"
	metaInfo.CreatedBy = "testSwarm"
	var torrentFile *os.File
	torrentFile, err = os.Create(torrentFileName)
	if err != nil {
		return
	}
	defer torrentFile.Close()
	err = metaInfo.Bencode(torrentFile)
	if err != nil {
		return
	}
	return
}

func createDataFile(name string, length int64) (err error) {
	if (length & 3) != 0 {
		return fmt.Errorf("createDataFile only supports length that is a multiple of 4. Not %d", length)
	}
	var file *os.File
	file, err = os.Create(name)
	if err != nil {
		return
	}
	defer file.Close()
	err = file.Truncate(length)
	if err != nil {
		return
	}
	w := bufio.NewWriter(file)
	b := make([]byte, 4)
	for i := int64(0); i < length; i += 4 {
		b[0] = byte(i >> 24)
		b[1] = byte(i >> 16)
		b[2] = byte(i >> 8)
		b[3] = byte(i)
		_, err = w.Write(b)
		if err != nil {
			return
		}
	}
	return
}

type logWriter string

func (l logWriter) Write(p []byte) (n int, err error) {
	log.Println(l, string(p))
	n = len(p)
	return
}
