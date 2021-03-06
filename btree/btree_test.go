package btree

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"math/rand"
	"os"
	"testing"

	"github.com/msg555/ctrfs/blockcache"
	"github.com/msg555/ctrfs/blockfile"
)

func blockFileCreate(blockSize int) (*blockfile.BlockFile, error) {
	f, err := ioutil.TempFile("", "ctrfs-test")
	if err != nil {
		return nil, err
	}
	err = os.Remove(f.Name())
	if err != nil {
		f.Close()
		return nil, err
	}
	bf := &blockfile.BlockFile{
		Cache: blockcache.New(1000, blockSize),
		File:  f,
	}
	bf.Init()
	return bf, nil
}

func TestFuzz(t *testing.T) {
	bf, err := blockFileCreate(1000)
	if err != nil {
		t.Fatalf("unexpected error creating temp file '%s'", err)
	}

	tr := BTree{
		MaxKeySize: 4,
		EntrySize:  4,
		FanOut:     4,
	}
	err = tr.Open(bf)
	if err != nil {
		t.Fatal(err)
	}

	rng := rand.New(rand.NewSource(555))

	keyDomain := 1000
	valDomain := 10000
	treeRoot, err := tr.CreateEmpty(nil)
	if err != nil {
		t.Fatalf("unexpected error creating empty tree '%s'", err)
	}

	data := make(map[string]string)
	for i := 0; i < 50000; i++ {
		k := fmt.Sprintf("%d", rng.Int()%keyDomain)
		switch rng.Int() % 3 {
		case 0: // Find
			mpVal, mpOk := data[k]
			trVal, trInd, err := tr.Find(treeRoot, []byte(k))
			if err != nil {
				t.Fatalf("unexpected error with Find: '%s'", err)
			}
			if mpOk && trVal == nil {
				t.Fatalf("expected key but missing")
			}
			if !mpOk && trVal != nil {
				t.Fatalf("found key but should be missing")
			}
			if mpOk {
				if bytes.Compare([]byte(mpVal), trVal) != 0 {
					t.Fatalf("got unexpected value from Find")
				}

				// ByIndex
				byIndexKey, byIndexVal, err := tr.ByIndex(trInd)
				if err != nil {
					t.Fatalf("unexpected error with ByIndex: '%s'", err)
				}
				if bytes.Compare([]byte(k), byIndexKey) != 0 {
					t.Fatalf("got unexpected key from ByIndex")
				}
				if bytes.Compare([]byte(mpVal), byIndexVal) != 0 {
					t.Fatalf("got unexpected value from ByIndex")
				}
			}
		case 1: // Insert
			v := fmt.Sprintf("%04d", rng.Int()%valDomain)

			_, ok := data[k]
			data[k] = v

			if err := tr.Insert(nil, treeRoot, []byte(k), []byte(v), false); err != nil {
				if !ok || err != ErrorKeyAlreadyExists {
					t.Fatalf("unexpected error with Insert: '%s'", err)
				}
				if err := tr.Insert(nil, treeRoot, []byte(k), []byte(v), true); err != nil {
					t.Fatalf("unexpected error with Insert: '%s'", err)
				}
			} else {
				if ok {
					t.Fatalf("expected insert to fail as key already existed")
				}
			}
		case 2: // Delete
			_, ok := data[k]
			if ok {
				delete(data, k)
			}
			if err := tr.Delete(nil, treeRoot, []byte(k)); err != nil {
				if ok || err != ErrorKeyNotFound {
					t.Fatalf("unexpected error with Delete: '%s'", err)
				}
			}
		}
		if i%100 == 0 {
			count := 0
			failMsg := ""

			done, err := tr.Scan(treeRoot, nil, func(_ IndexType, key KeyType, val ValueType) bool {
				count++
				mpVal, mpOk := data[string(key)]
				if !mpOk {
					failMsg = "got key from Scan that doesn't exist"
					return false
				}
				if bytes.Compare(val, []byte(mpVal)) != 0 {
					failMsg = "got incorrect value from Scan"
					return false
				}
				return true
			})
			if err != nil {
				t.Fatalf("unexpected scan error: '%s'", err)
			}
			if !done {
				t.Fatalf("scan unexpectedly did not complete")
			}

			if failMsg != "" {
				t.Fatal(failMsg)
			}
			if count != len(data) {
				t.Fatalf("unexpected number of elements in tree, got=%d, wanted=%d", count, len(data))
			}

			count = 0
			var searchStart []byte
			for {
				first := true
				done, err := tr.Scan(treeRoot, searchStart, func(_ IndexType, key KeyType, val ValueType) bool {
					if !first {
						searchStart = dupBytes(key)
						return false
					}
					first = false

					count++
					mpVal, mpOk := data[string(key)]
					if !mpOk {
						failMsg = "got key from Scan that doesn't exist"
						return false
					}
					if bytes.Compare(val, []byte(mpVal)) != 0 {
						failMsg = "got incorrect value from Scan"
						return false
					}
					return true
				})
				if err != nil {
					t.Fatalf("unexpected scan error: '%s'", err)
				}
				if done {
					break
				}
			}

			if failMsg != "" {
				t.Fatal(failMsg)
			}
			if count != len(data) {
				t.Fatalf("unexpected number of elements in tree, got=%d, wanted=%d", count, len(data))
			}
		}
	}
}
