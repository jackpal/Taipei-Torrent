package main

import (
	"flag"
	"github.com/jackpal/Taipei-Torrent/torrent"
	"github.com/jackpal/Taipei-Torrent/tracker"
	"log"
	"os"
	"os/signal"
	"path"
	"runtime/pprof"
)

var (
	cpuprofile    = flag.String("cpuprofile", "", "If not empty, collects CPU profile samples and writes the profile to the given file before the program exits")
	memprofile    = flag.String("memprofile", "", "If not empty, writes memory heap allocations to the given file before the program exits")
	createTorrent = flag.String("createTorrent", "", "If not empty, creates a torrent file from the given root. Writes to stdout")
	createTracker = flag.String("createTracker", "", "Creates a tracker serving the given torrent file on the given address. Example --createTracker=:8080 to serve on port 8080.")
)

func main() {
	flag.Usage = usage
	flag.Parse()

	if *createTorrent != "" {
		err := torrent.WriteMetaInfoBytes(*createTorrent, os.Stdout)
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

	err := torrent.RunTorrents(args)
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
	for _, torrentFile := range torrentFiles {
		var metaInfo *torrent.MetaInfo
		metaInfo, err = torrent.GetMetaInfo(torrentFile)
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
