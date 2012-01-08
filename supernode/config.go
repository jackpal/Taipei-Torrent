package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
)

type DhtConfig struct {

	Id	string
	Port	int
	Address	string
	Remotes	map[string][]byte
}

func openConfig(port int) (cfg *DhtConfig, err error) {
	p := fmt.Sprintf("/var/tmp/dht-%v", port)
	f, err := os.Open(p)
	if err != nil {
		log.Println(err)
		return nil, err
	}
	defer f.Close()
	cfg = &DhtConfig{}
	err = json.NewDecoder(f).Decode(cfg)
	return
}

func saveConfig(s DhtConfig) {
	p := fmt.Sprintf("/var/tmp/dht-%v", s.Port)
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


