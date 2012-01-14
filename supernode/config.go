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

type DhtConfig struct {
	Id      string
	Port    int
	Address string
	Remotes map[string][]byte
}

func openConfig(port int) (cfg *DhtConfig) {
	cfg = &DhtConfig{}

	p := fmt.Sprintf("%v-%v", configFilePrefix, port)
	f, err := os.Open(p)
	if err != nil {
		log.Println(err)
		return cfg
	}
	defer f.Close()
	err = json.NewDecoder(f).Decode(cfg)
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

func mkdirConfig() string {
	home := "/var/tmp"
	env := os.Environ()
	for _, e := range env {
		if strings.HasPrefix(e, "HOME=") {
			home = strings.SplitN(e, "=", 2)[1]
		}
	}
	dir := home + "/taipeitorrent"
	if err := os.Mkdir(dir, 0750); err != nil {
		log.Println(err)
	}
	if err := os.Chdir(dir); err != nil {
		log.Fatal("chdir", err)
	}
	return dir
}
