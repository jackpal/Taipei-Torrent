package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
)

var configFilePrefix string

func init() {
	configFilePrefix = mkdirConfig() + "/dht"
}

// mkdirConfig() creates a directory to load and save the configuration from.
// Uses ~/.taipeitorrent if $HOME is set, otherwise falls back to
// /var/tmp/taipeitorrent.
func mkdirConfig() string {
	dir := "/var/tmp/taipeitorrent"
	env := os.Environ()
	for _, e := range env {
		if strings.HasPrefix(e, "HOME=") {
			dir = strings.SplitN(e, "=", 2)[1] + "/.taipeitorrent"
		}
	}
	if err := os.Mkdir(dir, 0750); err != nil {
		log.Println(err)
	}
	if err := os.Chdir(dir); err != nil {
		log.Fatal("chdir", err)
	}
	return dir
}

type DhtConfig struct {
	Id      string
	Port    int
	Address string
	Remotes map[string][]byte // Key: IP, Value: node ID.
}

func openConfig(port int) (cfg *DhtConfig) {
	cfg = &DhtConfig{}

	// If a node is running in port 30610, the config should be in
	// ~/.taipeitorrent/dht-36010
	p := fmt.Sprintf("%v-%v", configFilePrefix, port)
	f, err := os.Open(p)
	if err != nil {
		log.Println(err)
		return cfg
	}
	defer f.Close()

	if err = json.NewDecoder(f).Decode(cfg); err != nil {
		log.Println(err)
	}
	return
}

func saveConfig(s DhtConfig) {
	p := fmt.Sprintf("%v-%v", configFilePrefix, s.Port)
	log.Println("Saving config to ", p)
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE, 0660)
	if err != nil {
		log.Println(err)
		return
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	if err = enc.Encode(s); err != nil {
		log.Println(err)
	}

}
