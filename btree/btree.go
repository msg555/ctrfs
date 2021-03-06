// Implements a simple, immutable B-tree implementation on disk featuring fixed
// length keys and values. Keys are strings that have variable length not
// exceeding MaxKeyLength. Values are fixed length byte arrays of size
// RecordLength.
// Has a simple API for constructing the B-tree all at once or by writing
// it record by record in sorted order.

/*
TODO: Add offset support by annotating elements with a 64-bit unique ID (e.g.
insertion counter).

Support resume by caching the position (sort index) of unique IDs recently
requested. then we may need to look a little forward and backwards to find the
starting position.
*/

package btree

import (
	"encoding/binary"

	"github.com/go-errors/errors"

	"github.com/msg555/ctrfs/blockfile"
)

var bo = binary.LittleEndian

type KeyType = []byte
type ValueType = []byte
type IndexType = int64
type TreeIndex = int64

type KeyValuePair struct {
	Key   KeyType
	Value ValueType
}

// Represents a B-tree structure. Set the public variables then call Init().
// You can then start querying or writing records to the tree.
type BTree struct {
	// Maximum size in bytes of a key
	MaxKeySize int

	// Size in bytes of a value
	EntrySize int

	// The number of keys stored at each B-tree node. Each node has up to FanOut+1
	// child nodes. FanOut must be set to an even number. Set FanOut to 0 to have
	// it automatically set to the largest possible when calling Open().
	FanOut int

	// Number of bytes in total for an entry
	nodeSize int

	// Underlying block store to keep b-tree nodes
	blocks blockfile.BlockAllocator
}

/*
Block:
	A macro-node in a B-tree containing up to FanOut "nodes"
Node:
	A single key/value entry stored within a block

Block Layout
	nodes  uint32
	height uint32 - leaf nodes have height=0

	... 0 <= i <= tr.FanOut
	child[i] uint64

	... 0 <= i < tr.FanOut (set only for i < nodes)
	keylen[i] uint32
	key[i]    [MaxKeySize]byte
	value[i]  [EntrySize]byte
*/

func dupBytes(arr []byte) []byte {
	result := make([]byte, len(arr))
	copy(result, arr)
	return result
}

// Open a B-tree with a single writable block allocator.
func (tr *BTree) Open(bf blockfile.BlockAllocator) error {
	if tr.FanOut%2 == 1 {
		return errors.New("tree fan out must be even")
	}

	fanOut := tr.FanOut
	nodeSize := 4 + tr.MaxKeySize + tr.EntrySize
	if fanOut == 0 {
		fanOut = (bf.GetBlockSize() - 12) / (8 + nodeSize)
		fanOut = fanOut &^ 1
		if fanOut <= 0 {
			return errors.New("insufficiently sized block allocator")
		}
	}
	blockSize := 12 + (8+nodeSize)*fanOut
	if bf.GetBlockSize() < blockSize {
		return errors.New("insufficiently sized block allocator")
	}

	tr.FanOut = fanOut
	tr.nodeSize = nodeSize
	tr.blocks = bf
	return nil
}

func (tr *BTree) validateKey(key KeyType) error {
	if len(key) == 0 {
		return errors.New("empty key")
	}
	if len(key) > tr.MaxKeySize {
		return errors.New("key length too long")
	}
	return nil
}

func (tr *BTree) validateValue(value ValueType) error {
	if len(value) != tr.EntrySize {
		return errors.New("value size incorrect")
	}
	return nil
}

func (tr *BTree) copyUpBlock(tag interface{}, index TreeIndex, block []byte) (TreeIndex, error) {
	var err error
	if tr.getBlockSize(block) == 0 {
		return 0, errors.New("cannot save block with size 0")
	}

	if tr.blocks.IsBlockReadOnly(index) {
		index, err = tr.blocks.Allocate(tag)
		if err != nil {
			return 0, err
		}
	}

	err = tr.blocks.Write(tag, index, block)
	if err != nil {
		return 0, err
	}
	return index, nil
}

func (tr *BTree) getBlockSize(block []byte) int {
	return int(bo.Uint32(block))
}

func (tr *BTree) setBlockSize(block []byte, size int) {
	bo.PutUint32(block, uint32(size))
}

func (tr *BTree) getBlockChild(block []byte, childInd int) TreeIndex {
	if childInd < 0 || childInd > tr.FanOut {
		panic("child index out of range")
	}
	return TreeIndex(bo.Uint64(block[4+8*childInd:]))
}

func (tr *BTree) setBlockChild(block []byte, childInd int, childTr TreeIndex) {
	if childInd < 0 || childInd > tr.FanOut {
		panic("child index out of range")
	}
	bo.PutUint64(block[4+8*childInd:], uint64(childTr))
}

func (tr *BTree) getNodeSlice(block []byte, i int) []byte {
	posStart := 4 + 8*(tr.FanOut+1) + i*tr.nodeSize
	return block[posStart : posStart+tr.nodeSize]
}

func (tr *BTree) getNodeKey(node []byte) KeyType {
	keylen := int(bo.Uint32(node))
	if keylen > tr.MaxKeySize {
		return nil
	}
	return node[4 : 4+keylen]
}

func (tr *BTree) getNodeValue(node []byte) ValueType {
	return node[4+tr.MaxKeySize : tr.nodeSize]
}

func (tr *BTree) setNode(node []byte, key KeyType, value ValueType) {
	bo.PutUint32(node, uint32(len(key)))
	copy(node[4:], key)
	copy(node[4+tr.MaxKeySize:], value)
}

func (tr *BTree) CreateEmpty(tag interface{}) (TreeIndex, error) {
	return tr.blocks.Allocate(tag)
}
