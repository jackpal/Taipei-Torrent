package main

import (
	"flag"
	"log"
	"math"
	"math/rand"
	"os"
	"os/signal"
	"path"
	"runtime/debug"
	"runtime/pprof"
	"time"

	"github.com/jackpal/Taipei-Torrent/torrent"
	"github.com/jackpal/Taipei-Torrent/tracker"
	"golang.org/x/net/proxy"
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
	initialCheck        = flag.Bool("initialCheck", true, "Do an initial hash check on files when adding torrents.")
	useSFTP             = flag.String("useSFTP", "", "SFTP connection string, to store torrents over SFTP. e.g. 'username:password@192.168.1.25:22/path/'")
	useRamCache         = flag.Int("useRamCache", 0, "Size in MiB of cache in ram, to reduce traffic on torrent storage.")
	useHdCache          = flag.Int("useHdCache", 0, "Size in MiB of cache in OS temp directory, to reduce traffic on torrent storage.")
	execOnSeeding       = flag.String("execOnSeeding", "", "Command to execute when torrent has fully downloaded and has begun seeding.")
	quickResume         = flag.Bool("quickResume", false, "Save torrenting data to resume faster. '-initialCheck' should be set to false, to prevent hash check on resume.")
	maxActive           = flag.Int("maxActive", 16, "How many torrents should be active at a time. Torrents added beyond this value are queued.")
	memoryPerTorrent    = flag.Int("memoryPerTorrent", -1, "Maximum memory (in MiB) per torrent used for Active Pieces. 0 means minimum. -1 (default) means unlimited.")
)

func parseTorrentFlags() (flags *torrent.TorrentFlags, err error) {
	dialer, err := dialerFromFlags()
	if err != nil {
		return
	}
	flags = &torrent.TorrentFlags{
		Dial:                dialer,
		Port:                portFromFlags(),
		FileDir:             *fileDir,
		SeedRatio:           *seedRatio,
		UseDeadlockDetector: *useDeadlockDetector,
		UseLPD:              *useLPD,
		UseDHT:              *useDHT,
		UseUPnP:             *useUPnP,
		UseNATPMP:           *useNATPMP,
		TrackerlessMode:     *trackerlessMode,
		// IP address of gateway
		Gateway:            *gateway,
		InitialCheck:       *initialCheck,
		FileSystemProvider: fsproviderFromFlags(),
		Cacher:             cacheproviderFromFlags(),
		ExecOnSeeding:      *execOnSeeding,
		QuickResume:        *quickResume,
		MaxActive:          *maxActive,
		MemoryPerTorrent:   *memoryPerTorrent,
	}
	return
}

func portFromFlags() int {
	if *port != 0 {
		return *port
	}

	rr := rand.New(rand.NewSource(time.Now().UnixNano()))
	return rr.Intn(48000) + 1025
}

func cacheproviderFromFlags() torrent.CacheProvider {
	if (*useRamCache) > 0 && (*useHdCache) > 0 {
		log.Panicln("Only one cache at a time, please.")
	}

	if (*useRamCache) > 0 {
		return torrent.NewRamCacheProvider(*useRamCache)
	}

	if (*useHdCache) > 0 {
		return torrent.NewHdCacheProvider(*useHdCache)
	}
	return nil
}

func fsproviderFromFlags() torrent.FsProvider {
	if len(*useSFTP) > 0 {
		return torrent.NewSftpFsProvider(*useSFTP)
	}
	return torrent.OsFsProvider{}
}

func dialerFromFlags() (proxy.Dialer, error) {
	if len(*proxyAddress) > 0 {
		return proxy.SOCKS5("tcp", string(*proxyAddress), nil, &proxy.Direct)
	}
	return proxy.FromEnvironment(), nil
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
	if narg < 1 {
		log.Println("Too few arguments. Torrent file or torrent URL required.")
		usage()
	}

	torrentFlags, err := parseTorrentFlags()
	if err != nil {
		log.Fatal("Could not parse flags:", err)
	}

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

	if (*memoryPerTorrent) >= 0 { //User is worried about memory use.
		debug.SetGCPercent(20) //Set the GC to clear memory more often.
	}

	log.Println("Starting.")

	err = torrent.RunTorrents(torrentFlags, args)
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
	dial, err := dialerFromFlags()
	if err != nil {
		return
	}
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
