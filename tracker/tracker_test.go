package tracker

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"math"
	"os"
	"os/exec"
	"path"
	"strconv"
	"testing"
	"time"

	"github.com/jackpal/Taipei-Torrent/torrent"
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

func TestSwarm1(t *testing.T) {
	testSwarm(t, 1)
}

func TestSwarm10(t *testing.T) {
	testSwarm(t, 10)
}

func TestSwarm20(t *testing.T) {
	testSwarm(t, 20)
}

func TestSwarm50(t *testing.T) {
	testSwarm(t, 50)
}

func TestSwarm100(t *testing.T) {
	testSwarm(t, 100)
}

func testSwarm(t *testing.T, leechCount int) {
	err := runSwarm(leechCount)
	if err != nil {
		t.Fatal("Error running testSwarm", err)
	}
}

type prog struct {
	instanceName string
	dirName      string
	cmd          *exec.Cmd
}

func (p *prog) start(doneCh chan *prog) (err error) {
	log.Println("starting", p.instanceName)
	out := logWriter(p.instanceName)
	p.cmd.Stdout = &out
	p.cmd.Stderr = &out
	err = p.cmd.Start()
	if err != nil {
		return
	}
	go func() {
		p.cmd.Wait()
		doneCh <- p
	}()
	return
}

func (p *prog) kill() (err error) {
	err = p.cmd.Process.Kill()
	return
}

func newProg(instanceName string, dir string, command string, arg ...string) (p *prog) {
	cmd := helperCommands(append([]string{command}, arg...)...)
	return &prog{instanceName: instanceName, dirName: dir, cmd: cmd}
}

func runSwarm(leechCount int) (err error) {
	var rootDir string
	rootDir, err = ioutil.TempDir("", "swarm")
	if err != nil {
		return
	}
	log.Printf("Temporary directory: %s", rootDir)
	seedDir := path.Join(rootDir, "seed")
	err = os.Mkdir(seedDir, 0700)
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

	doneCh := make(chan *prog, 1)

	tracker := newTracker("tracker", ":8080", rootDir, torrentFile)
	err = tracker.start(doneCh)
	if err != nil {
		return
	}
	defer tracker.kill()
	time.Sleep(100 * time.Microsecond)

	var seed, leech *prog
	seed = newTorrentClient("seed", 0, torrentFile, seedDir, math.Inf(0))
	err = seed.start(doneCh)
	if err != nil {
		return
	}
	defer seed.kill()
	time.Sleep(50 * time.Microsecond)

	for l := 0; l < leechCount; l++ {
		leechDir := path.Join(rootDir, fmt.Sprintf("leech %d", l))
		err = os.Mkdir(leechDir, 0700)
		if err != nil {
			return
		}
		leech = newTorrentClient(fmt.Sprintf("leech%d", l), 0, torrentFile, leechDir, 0)
		err = leech.start(doneCh)
		if err != nil {
			return
		}
		defer leech.kill()
	}

	timeout := make(chan bool, 1)
	go func() {
		// It takes about 3.5 seconds to complete the test on my computer.
		time.Sleep(50 * time.Second)
		timeout <- true
	}()

	for doneCount := 0; doneCount < leechCount; doneCount++ {
		select {
		case <-timeout:
			err = fmt.Errorf("Timout exceeded")
		case donePeer := <-doneCh:
			if donePeer == tracker || donePeer == seed {
				err = fmt.Errorf("%v finished before all leeches. Should not have.", donePeer)
			}
			err = compareData(seedData, donePeer.dirName)
		}
		if err != nil {
			return
		}
		log.Printf("Done: %d of %d", (doneCount + 1), leechCount)
	}
	if err != nil {
		return
	}
	// All is good. Clean up
	os.RemoveAll(rootDir)

	return
}

func newTracker(name string, addr string, fileDir string, torrentFile string) (p *prog) {
	return newProg(name, fileDir, "tracker", addr, torrentFile)
}

func newTorrentClient(name string, port int, torrentFile string, fileDir string, ratio float64) (p *prog) {
	return newProg(name, fileDir, "client",
		fmt.Sprintf("%v", port),
		fileDir,
		fmt.Sprintf("%v", ratio),
		torrentFile)
}

func createTorrentFile(torrentFileName, root, announcePath string) (err error) {
	var metaInfo *torrent.MetaInfo
	metaInfo, err = torrent.CreateMetaInfoFromFileSystem(nil, root, "127.0.0.1:8080", 0, false)
	if err != nil {
		return
	}
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

func compareData(sourceName, copyDirName string) (err error) {
	_, base := path.Split(sourceName)
	copyName := path.Join(copyDirName, base)
	err = compare(sourceName, copyName)
	return
}

// Compare two files (or directories) for equality.
func compare(aName, bName string) (err error) {
	var aFileInfo, bFileInfo os.FileInfo
	aFileInfo, err = os.Stat(aName)
	if err != nil {
		return
	}
	bFileInfo, err = os.Stat(bName)
	if err != nil {
		return
	}
	aIsDir, bIsDir := aFileInfo.IsDir(), bFileInfo.IsDir()
	if aIsDir != bIsDir {
		return fmt.Errorf("%s.IsDir() == %v != %s.IsDir() == %v",
			aName, aIsDir,
			bName, bIsDir)
	}
	var aFile, bFile *os.File
	aFile, err = os.Open(aName)
	if err != nil {
		return
	}
	defer aFile.Close()
	bFile, err = os.Open(bName)
	if err != nil {
		return
	}
	defer bFile.Close()
	if !aIsDir {
		aSize, bSize := aFileInfo.Size(), bFileInfo.Size()
		if aSize != bSize {
			return fmt.Errorf("%s.Size() == %v != %s.Size() == %v",
				aName, aSize,
				bName, bSize)
		}
		var aBuf, bBuf bytes.Buffer
		bufferSize := int64(128 * 1024)
		for i := int64(0); i < aSize; i += bufferSize {
			toRead := bufferSize
			remainder := aSize - i
			if toRead > remainder {
				toRead = remainder
			}
			_, err = io.CopyN(&aBuf, aFile, toRead)
			if err != nil {
				return
			}
			_, err = io.CopyN(&bBuf, bFile, toRead)
			if err != nil {
				return
			}
			aBytes, bBytes := aBuf.Bytes(), bBuf.Bytes()
			for j := int64(0); j < toRead; j++ {
				a, b := aBytes[j], bBytes[j]
				if a != b {
					err = fmt.Errorf("%s[%d] %d != %d", aName, i+j, a, b)
					return
				}
			}
			aBuf.Reset()
			bBuf.Reset()
		}
	} else {
		var aNames, bNames []string
		aNames, err = aFile.Readdirnames(0)
		if err != nil {
			return
		}
		bNames, err = bFile.Readdirnames(0)
		if err != nil {
			return
		}
		if len(aNames) != len(bName) {
			err = fmt.Errorf("Directories %v and %v don't contain same number of files %d != %d",
				aName, bName, len(aNames), len(bNames))
		}
		for _, name := range aNames {
			err = compare(path.Join(aName, name), path.Join(bName, name))
			if err != nil {
				return
			}
		}
	}
	return
}

// type logWriter

type logWriter string

func (l logWriter) Write(p []byte) (n int, err error) {
	log.Println(l, string(p))
	n = len(p)
	return
}

// A test that's used to run multiple processes. From http://golang.org/src/pkg/os/exec/exec_test.go

func helperCommands(s ...string) *exec.Cmd {
	cs := []string{"-test.run=TestHelperProcess", "--"}
	cs = append(cs, s...)
	cmd := exec.Command(os.Args[0], cs...)
	cmd.Env = []string{"GO_WANT_HELPER_PROCESS=1"}
	return cmd
}

func TestHelperProcess(*testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}

	defer os.Exit(0)

	err := testHelperProcessImp(os.Args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error %v\n", err)
		os.Exit(3)
	}
}

func testHelperProcessImp(args []string) (err error) {
	for len(args) > 0 {
		if args[0] == "--" {
			args = args[1:]
			break
		}
		args = args[1:]
	}

	if len(args) == 0 {
		err = fmt.Errorf("No commands\n")
		return
	}

	cmd, args := args[0], args[1:]
	switch cmd {
	case "tracker":
		if len(args) < 2 {
			err = fmt.Errorf("tracker expected 2 or more args\n")
			return
		}
		addr, torrentFiles := args[0], args[1:]

		err = StartTracker(addr, torrentFiles)
		if err != nil {
			return
		}
	case "client":
		if len(args) < 4 {
			err = fmt.Errorf("client expected 4 or more args\n")
			return
		}
		portStr, fileDir, seedRatioStr, torrentFiles :=
			args[0], args[1], args[2], args[3:]
		var port uint64
		port, err = strconv.ParseUint(portStr, 10, 16)
		if err != nil {
			return
		}
		var seedRatio float64
		seedRatio, err = strconv.ParseFloat(seedRatioStr, 64)
		torrentFlags := torrent.TorrentFlags{
			Port:               int(port),
			FileDir:            fileDir,
			SeedRatio:          seedRatio,
			FileSystemProvider: torrent.OsFsProvider{},
			InitialCheck:       true,
			MaxActive:          1,
			ExecOnSeeding:      "",
			Cacher:             torrent.NewRamCacheProvider(1),
			MemoryPerTorrent:   4,
		}
		err = torrent.RunTorrents(&torrentFlags, torrentFiles)
		if err != nil {
			return
		}
	default:
		err = fmt.Errorf("Unknown command %q\n", cmd)
		return
	}
	return
}
