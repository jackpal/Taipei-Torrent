package dht

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"strings"
)

var configFilePrefix string

func init() {
	configFilePrefix = mkdirStore()
}

// mkdirStore() creates a directory to load and save the configuration from.
// Uses ~/.taipeitorrent if $HOME is set, otherwise falls back to
// /var/run/taipeitorrent. It's very much UNIX only at the moment, but making
// it generic should be fairly easy.
func mkdirStore() string {
	dir := "/var/run/taipeitorrent"
	env := os.Environ()
	for _, e := range env {
		if strings.HasPrefix(e, "HOME=") {
			dir = strings.SplitN(e, "=", 2)[1] + "/.taipeitorrent"
		}
	}
	// Ignore errors.
	os.Mkdir(dir, 0750)

	if s, err := os.Stat(dir); err != nil {
		log.Fatal("stat config dir", err)
	} else if !s.IsDir() {
		log.Fatalf("Dir %v expected directory, got %v", dir, s)
	}
	return dir
}

type DhtStore struct {
	// The rest of the stack uses string, but that confuses the json
	// Marshaller. []byte is more correct anyway.
	Id      []byte
	Port    int
	Remotes map[string][]byte // Key: IP, Value: node ID.
}

func openStore(port int) (cfg *DhtStore) {
	cfg = &DhtStore{}

	// If a node is running in port 30610, the config should be in
	// ~/.taipeitorrent/dht-36010
	p := fmt.Sprintf("%v-%v", configFilePrefix+"/dht", port)
	f, err := os.Open(p)
	if err != nil {
		// log.Println(err)
		return cfg
	}
	defer f.Close()

	if err = json.NewDecoder(f).Decode(cfg); err != nil {
		log.Println(err)
	}
	return
}

// saveStore tries to safe the provided config in a safe way.
func saveStore(s DhtStore) {

	tmp, err := ioutil.TempFile(configFilePrefix, "taipeitorrent")
	if err != nil {
		log.Println("saveStore tempfile:", err)
		return
	}
	defer tmp.Close()

	enc := json.NewEncoder(tmp)
	if err = enc.Encode(s); err != nil {
		log.Println("saveStore json encoding:", err)
		return
	}

	// Write worked, so now we can replace the existing file, which is atomic AFAIK.
	p := fmt.Sprintf("%v-%v", configFilePrefix+"/dht", s.Port)
	if err := os.Rename(tmp.Name(), p); err != nil {
		log.Println("saveStore failed when replacing existing config:", err)
	}
	log.Println("SAVED.")
}
