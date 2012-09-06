package dht

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path"
	"strings"
)

type DHTStore struct {
	// The rest of the stack uses string, but that confuses the json
	// Marshaller. []byte is more correct anyway.
	Id      []byte
	Port    int
	Remotes map[string][]byte // Key: IP, Value: node ID.
	path    string            // Empty if the store is disabled.
}

// mkdirStore() creates a directory to load and save the configuration from.
// Uses ~/.taipeitorrent if $HOME is set, otherwise falls back to
// /var/run/taipeitorrent.
func mkdirStore() string {
	dir := "/var/run/taipeitorrent"
	env := os.Environ()
	for _, e := range env {
		if strings.HasPrefix(e, "HOME=") {
			dir = strings.SplitN(e, "=", 2)[1]
			dir = path.Join(dir, ".taipeitorrent")
		}
	}
	// Ignore errors.
	os.MkdirAll(dir, 0750)

	if s, err := os.Stat(dir); err != nil {
		log.Fatal("stat config dir", err)
	} else if !s.IsDir() {
		log.Fatalf("Dir %v expected directory, got %v", dir, s)
	}
	return dir
}

func openStore(port int, enabled bool) (cfg *DHTStore) {
	cfg = &DHTStore{Port: port}
	if enabled {
		cfg.path = mkdirStore()

		// If a node is running in port 30610, the config should be in
		// ~/.taipeitorrent/dht-36010
		p := fmt.Sprintf("%v-%v", path.Join(cfg.path, "dht"), port)
		f, err := os.Open(p)
		if err != nil {
			// log.Println(err)
			return cfg
		}
		defer f.Close()

		if err = json.NewDecoder(f).Decode(cfg); err != nil {
			log.Println(err)
		}
	}
	return
}

// saveStore tries to safe the provided config in a safe way.
func saveStore(s DHTStore) {
	if s.path == "" {
		return
	}
	tmp, err := ioutil.TempFile(s.path, "taipeitorrent")
	if err != nil {
		log.Println("saveStore tempfile:", err)
		return
	}
	err = json.NewEncoder(tmp).Encode(s)
	// The file has to be closed already otherwise it can't be renamed on
	// Windows.
	tmp.Close()
	if err != nil {
		log.Println("saveStore json encoding:", err)
		return
	}

	// Write worked, so replace the existing file. That's atomic in Linux, but
	// not on Windows.
	p := fmt.Sprintf("%v-%v", s.path+"/dht", s.Port)
	if err := os.Rename(tmp.Name(), p); err != nil {
		// if os.IsExist(err) {
		// Not working for Windows:
		// http://code.google.com/p/go/issues/detail?id=3828

		// It's not possible to atomically rename files on Windows, so I
		// have to delete it and try again. If the program crashes between
		// the unlink and the rename operation, it lose the configuration,
		// unfortunately.
		if err := os.Remove(p); err != nil {
			log.Println("saveStore failed to remove the existing config:", err)
			return
		}
		if err := os.Rename(tmp.Name(), p); err != nil {
			log.Println("saveStore failed to rename file after deleting the original config:", err)
			return
		}
		// } else {
		// 	log.Println("saveStore failed when replacing existing config:", err)
		// }
	} else {
		log.Println("Saved DHT routing table to the filesystem.")
	}
}
