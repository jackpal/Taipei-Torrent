package main

import (
	"flag"
	"log"
	"math"
	"os"
	"os/signal"
	"path"
	"runtime/pprof"

	socks "github.com/hailiang/gosocks"
	"github.com/jackpal/Taipei-Torrent/torrent"
	"github.com/jackpal/Taipei-Torrent/tracker"
)

var (
	cpuprofile    = flag.String("cpuprofile", "", "If not empty, collects CPU profile samples and writes the profile to the given file before the program exits")
	memprofile    = flag.String("memprofile", "", "If not empty, writes memory heap allocations to the given file before the program exits")
	createTorrent = flag.String("createTorrent", "", "If not empty, creates a torrent file from the given root. Writes to stdout")
	createTracker = flag.String("createTracker", "", "Creates a tracker serving the given torrent file on the given address. Example --createTracker=:8080 to serve on port 8080.")

	port                = flag.Int("port", 7777, "Port to listen on. 0 means pick random port. Note that 6881 is blacklisted by some trackers.")
	fileDir             = flag.String("fileDir", ".", "path to directory where files are stored")
	seedRatio           = flag.Float64("seedRatio", math.Inf(0), "Seed until ratio >= this value before quitting.")
	useDeadlockDetector = flag.Bool("useDeadlockDetector", false, "Panic and print stack dumps when the program is stuck.")
	useLPD              = flag.Bool("useLPD", false, "Use Local Peer Discovery")
	useUPnP             = flag.Bool("useUPnP", false, "Use UPnP to open port in firewall.")
	useNATPMP           = flag.Bool("useNATPMP", false, "Use NAT-PMP to open port in firewall.")
	gateway             = flag.String("gateway", "", "IP Address of gateway.")
	useDHT              = flag.Bool("useDHT", false, "Use DHT to get peers.")
	trackerlessMode     = flag.Bool("trackerlessMode", false, "Do not get peers from the tracker. Good for testing DHT mode.")
	proxyAddress        = flag.String("proxyAddress", "", "Address of a SOCKS5 proxy to use.")
)

func parseTorrentFlags() *torrent.TorrentFlags {
	return &torrent.TorrentFlags{
		Dial:                dialerFromFlags(),
		Port:                *port,
		FileDir:             *fileDir,
		SeedRatio:           *seedRatio,
		UseDeadlockDetector: *useDeadlockDetector,
		UseLPD:              *useLPD,
		UseDHT:              *useDHT,
		UseUPnP:             *useUPnP,
		UseNATPMP:           *useNATPMP,
		TrackerlessMode:     *trackerlessMode,
		// IP address of gateway
		Gateway: *gateway,
	}
}

func dialerFromFlags() torrent.Dialer {
	if len(*proxyAddress) > 0 {
		return socks.DialSocksProxy(socks.SOCKS5, string(*proxyAddress))
	}
	return nil
}

func main() {
	flag.Usage = usage
	flag.Parse()

	if *createTorrent != "" {
		err := torrent.WriteMetaInfoBytes(*createTorrent, *createTracker, os.Stdout)
		if err != nil {
			log.Fatal("Could not create torrent file:", err)
		}
		return
	}

	if *createTracker != "" {
		err := startTracker(*createTracker, flag.Args())
		if err != nil {
			log.Fatal("Tracker returned error:", err)
		}
		return
	}

	args := flag.Args()
	narg := flag.NArg()
	if narg != 1 {
		if narg < 1 {
			log.Println("Too few arguments. Torrent file or torrent URL required.")
		} else {
			log.Printf("Too many arguments. (Expected 1): %v", args)
		}
		usage()
	}

	torrentFlags := parseTorrentFlags()

	if *cpuprofile != "" {
		cpuf, err := os.Create(*cpuprofile)
		if err != nil {
			log.Fatal(err)
		}
		pprof.StartCPUProfile(cpuf)
		defer pprof.StopCPUProfile()
	}

	if *memprofile != "" {
		defer func(file string) {
			memf, err := os.Create(file)
			if err != nil {
				log.Fatal(err)
			}
			pprof.WriteHeapProfile(memf)
		}(*memprofile)
	}

	log.Println("Starting.")

	err := torrent.RunTorrents(torrentFlags, args)
	if err != nil {
		log.Fatal("Could not run torrents", args, err)
	}
}

func usage() {
	log.Printf("usage: torrent.Torrent [options] (torrent-file | torrent-url)")

	flag.PrintDefaults()
	os.Exit(2)
}

func startTracker(addr string, torrentFiles []string) (err error) {
	t := tracker.NewTracker()
	// TODO(jackpal) Allow caller to choose port number
	t.Addr = addr
	dial := dialerFromFlags()
	for _, torrentFile := range torrentFiles {
		var metaInfo *torrent.MetaInfo
		metaInfo, err = torrent.GetMetaInfo(dial, torrentFile)
		if err != nil {
			return
		}
		name := metaInfo.Info.Name
		if name == "" {
			name = path.Base(torrentFile)
		}
		err = t.Register(metaInfo.InfoHash, name)
		if err != nil {
			return
		}
	}
	go func() {
		quitChan := listenSigInt()
		select {
		case <-quitChan:
			log.Printf("got control-C")
			t.Quit()
		}
	}()

	err = t.ListenAndServe()
	if err != nil {
		return
	}
	return
}

func listenSigInt() chan os.Signal {
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, os.Kill)
	return c
}
