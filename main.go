package main

import (
	"flag"
	"log"
	"os"
	"os/signal"
	"runtime/pprof"
)

var (
	cpuprofile = flag.String("cpuprofile", "", "write cpu profile to file")
	memprofile = flag.String("memprofile", "", "write mem profile to file")
)

var torrent string

func main() {
	flag.Usage = usage
	flag.Parse()

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

	torrent = args[0]

	log.Println("Starting.")
	ts, err := NewTorrentSession(torrent)
	if err != nil {
		log.Println("Could not create torrent session.", err)
		return
	}

	go func() {
		<-listenSigInt()
		ts.Quit()
	}()

	err = ts.DoTorrent()
	if err != nil {
		log.Println("Failed: ", err)
	} else {
		log.Println("Done")
	}

	if *memprofile != "" {
		memf, err := os.Create(*memprofile)
		if err != nil {
			log.Fatal(err)
		}
		pprof.WriteHeapProfile(memf)
	}

}

func usage() {
	log.Printf("usage: Taipei-Torrent [options] (torrent-file | torrent-url)")

	flag.PrintDefaults()
	os.Exit(2)
}

func listenSigInt() chan os.Signal {
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, os.Kill)
	return c
}
