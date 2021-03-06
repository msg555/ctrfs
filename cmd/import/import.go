package main

import (
	"encoding/hex"
	"fmt"
	"log"
	"os"

	"github.com/go-errors/errors"

	"github.com/msg555/ctrfs/storage"
)

func help() {
	fmt.Printf("%s (dir|tar) file [file ...]\n", os.Args[0])
}

func main() {
	if len(os.Args) < 3 {
		help()
		os.Exit(1)
	}

	mode := os.Args[1]
	if mode != "dir" && mode != "tar" {
		help()
		os.Exit(1)
	}

	sc, err := storage.OpenDefaultStorageContext()
	if err != nil {
		log.Fatal(err)
	}

	for _, file := range os.Args[2:] {
		if file == "-" {
			file = "/dev/stdin"
		}

		var err error
		var nd *storage.StorageNode
		if mode == "dir" {
			nd, err = sc.ImportPath(file)
		} else {
			var f *os.File
			f, err = os.Open(file)
			if err == nil {
				nd, err = sc.ImportTar(f)
				f.Close()
			}
		}
		if err != nil {
			gerr, ok := err.(*errors.Error)
			if ok {
				log.Fatalf("improt of '%s' failed: %s\n%s", file, err, gerr.ErrorStack())
			} else {
				log.Fatalf("import of '%s' failed: %s", file, err)
			}
		} else {
			fmt.Printf("imported '%s' as %s\n", file, hex.EncodeToString(nd.NodeAddress[:]))
		}
	}

	err = sc.Close()
	if err != nil {
		log.Fatalf("failed shutting down storage: %s", err)
	}
}
