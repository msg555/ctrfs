package btree

import (
	"errors"
)

var ErrorKeyNotFound = errors.New("key not found")
var ErrorRootImmutable = errors.New("root block is immutable")

func (tr *BTree) Delete(tag interface{}, treeIndex TreeIndex, key KeyType) error {
	if tr.blocks.IsBlockReadOnly(treeIndex) {
		return ErrorRootImmutable
	}
	err := tr.validateKey(key)
	if err != nil {
		return err
	}

	block, _, _, err := tr.deleteHelper(tag, treeIndex, key)
	defer tr.blocks.GetCache().Pool.Put(block)
	if err != nil {
		return err
	}

	if tr.getBlockSize(block) == 0 {
		// Root has emptied out, copy child into root node and delete child.
		childIndex := tr.getBlockChild(block, 0)

		_, err = tr.blocks.Read(childIndex, block)
		if err != nil {
			return err
		}

		err = tr.blocks.Write(tag, treeIndex, block)
		if err != nil {
			return err
		}

		if tr.blocks.IsBlockReadOnly(childIndex) {
			return nil
		}
		return tr.blocks.Free(childIndex)
	}

	return tr.blocks.Write(tag, treeIndex, block)
}

func (tr *BTree) deleteHelper(tag interface{}, treeIndex TreeIndex, key KeyType) ([]byte, KeyType, ValueType, error) {
	cache := tr.blocks.GetCache()
	block := cache.Pool.Get().([]byte)

	if treeIndex == 0 {
		return block, nil, nil, ErrorKeyNotFound
	}

	_, err := tr.blocks.Read(treeIndex, block)
	if err != nil {
		return block, nil, nil, err
	}

	var insertInd int
	var match bool
	if key == nil {
		// nil key means we want to delete max element
		insertInd = tr.getBlockSize(block)
		if tr.getBlockChild(block, insertInd) == 0 {
			insertInd--
			match = true
		}
	} else {
		insertInd, match, err = tr.searchBlock(block, key)
		if err != nil {
			return block, nil, nil, err
		}
	}

	childTree := tr.getBlockChild(block, insertInd)
	var childBlock []byte
	var deletedKey KeyType
	var deletedValue ValueType

	blockSize := tr.getBlockSize(block)
	if match {
		if childTree == 0 {
			if key == nil {
				// we only need to copy the deleted key/value if we were doing a deleted
				// max operation.
				deletedKey = dupBytes(tr.getNodeKey(tr.getNodeSlice(block, insertInd)))
				deletedValue = dupBytes(tr.getNodeValue(tr.getNodeSlice(block, insertInd)))
			}

			// Shift over elements on top of deleted element, no children to shift.
			for i := insertInd; i+1 < blockSize; i++ {
				copy(tr.getNodeSlice(block, i), tr.getNodeSlice(block, i+1))
			}
			tr.setBlockSize(block, blockSize-1)

			return block, deletedKey, deletedValue, nil
		}

		childBlock, deletedKey, deletedValue, err = tr.deleteHelper(tag, childTree, nil)
		defer cache.Pool.Put(childBlock)
		if err != nil {
			return block, nil, nil, err
		}

		tr.setNode(tr.getNodeSlice(block, insertInd), deletedKey, deletedValue)
	} else {
		childBlock, deletedKey, deletedValue, err = tr.deleteHelper(tag, childTree, key)
		defer cache.Pool.Put(childBlock)
		if err != nil {
			return block, nil, nil, err
		}
	}

	childBlockSize := tr.getBlockSize(childBlock)
	if childBlockSize*2 >= tr.FanOut {
		// Child size is fine
		childTree, err := tr.copyUpBlock(tag, childTree, childBlock)
		if err != nil {
			return block, nil, nil, err
		}

		tr.setBlockChild(block, insertInd, childTree)
		return block, deletedKey, deletedValue, nil
	}

	// Child is too small, match up with sibling
	sibIndex := insertInd - 1
	if sibIndex < 0 {
		sibIndex = insertInd + 1
	}
	sibTree := tr.getBlockChild(block, sibIndex)

	sibBlock := cache.Pool.Get().([]byte)
	defer cache.Pool.Put(sibBlock)

	_, err = tr.blocks.Read(sibTree, sibBlock)
	if err != nil {
		return block, nil, nil, err
	}
	sibBlockSize := tr.getBlockSize(sibBlock)

	// Normalize so that childX refers to the larger sibling
	if insertInd < sibIndex {
		sibIndex, insertInd = insertInd, sibIndex
		sibBlock, childBlock = childBlock, sibBlock
		sibTree, childTree = childTree, sibTree
		sibBlockSize, childBlockSize = childBlockSize, sibBlockSize
	}

	if childBlockSize+sibBlockSize >= tr.FanOut {
		// Rebalance nodes with sibling block
		var childTrees []TreeIndex
		var nodeKeys []KeyType
		var nodeValues []ValueType
		for i := 0; i <= sibBlockSize; i++ {
			childTrees = append(childTrees, tr.getBlockChild(sibBlock, i))
			if i < sibBlockSize {
				nodeKeys = append(nodeKeys, tr.getNodeKey(tr.getNodeSlice(sibBlock, i)))
				nodeValues = append(nodeValues, tr.getNodeValue(tr.getNodeSlice(sibBlock, i)))
			}
		}
		nodeKeys = append(nodeKeys, tr.getNodeKey(tr.getNodeSlice(block, sibIndex)))
		nodeValues = append(nodeValues, tr.getNodeValue(tr.getNodeSlice(block, sibIndex)))
		for i := 0; i <= childBlockSize; i++ {
			childTrees = append(childTrees, tr.getBlockChild(childBlock, i))
			if i < childBlockSize {
				nodeKeys = append(nodeKeys, tr.getNodeKey(tr.getNodeSlice(childBlock, i)))
				nodeValues = append(nodeValues, tr.getNodeValue(tr.getNodeSlice(childBlock, i)))
			}
		}

		// Calculate new block sizes
		sibBlockSize = (len(nodeKeys) - 1) / 2
		childBlockSize = len(nodeKeys) - 1 - sibBlockSize

		sibBlock = cache.Pool.Get().([]byte)
		defer cache.Pool.Put(sibBlock)

		// Copy values back into blocks
		tr.setBlockSize(sibBlock, sibBlockSize)
		for i := 0; i <= sibBlockSize; i++ {
			tr.setBlockChild(sibBlock, i, childTrees[i])
			if i < sibBlockSize {
				tr.setNode(tr.getNodeSlice(sibBlock, i), nodeKeys[i], nodeValues[i])
			}
		}

		childBlock = cache.Pool.Get().([]byte)
		defer cache.Pool.Put(childBlock)

		tr.setBlockSize(childBlock, childBlockSize)
		for i := 0; i <= childBlockSize; i++ {
			tr.setBlockChild(childBlock, i, childTrees[sibBlockSize+1+i])
			if i < childBlockSize {
				tr.setNode(tr.getNodeSlice(childBlock, i), nodeKeys[sibBlockSize+1+i], nodeValues[sibBlockSize+1+i])
			}
		}

		sibTree, err = tr.copyUpBlock(tag, sibTree, sibBlock)
		if err != nil {
			return block, nil, nil, err
		}

		childTree, err = tr.copyUpBlock(tag, childTree, childBlock)
		if err != nil {
			return block, nil, nil, err
		}

		tr.setNode(tr.getNodeSlice(block, sibIndex), nodeKeys[sibBlockSize], nodeValues[sibBlockSize])
		tr.setBlockChild(block, sibIndex, sibTree)
		tr.setBlockChild(block, sibIndex+1, childTree)

		return block, deletedKey, deletedValue, nil
	}

	// Merge with sibling block
	copy(tr.getNodeSlice(sibBlock, sibBlockSize), tr.getNodeSlice(block, sibIndex))
	for i := 0; i <= childBlockSize; i++ {
		tr.setBlockChild(sibBlock, sibBlockSize+1+i, tr.getBlockChild(childBlock, i))
		if i < childBlockSize {
			copy(tr.getNodeSlice(sibBlock, sibBlockSize+1+i), tr.getNodeSlice(childBlock, i))
		}
	}
	tr.setBlockSize(sibBlock, sibBlockSize+childBlockSize+1)

	sibTree, err = tr.copyUpBlock(tag, sibTree, sibBlock)
	if err != nil {
		return block, nil, nil, err
	}

	for i := sibIndex; i+1 < blockSize; i++ {
		tr.setBlockChild(block, i+1, tr.getBlockChild(block, i+2))
		copy(tr.getNodeSlice(block, i), tr.getNodeSlice(block, i+1))
	}
	tr.setBlockSize(block, blockSize-1)

	return block, deletedKey, deletedValue, nil
}

func (tr *BTree) FreeTree(treeIndex TreeIndex, ignoreReadOnly bool) error {
	if tr.blocks.IsBlockReadOnly(treeIndex) {
		if ignoreReadOnly {
			return nil
		}
		return errors.New("attempt to free read only block")
	}

	cache := tr.blocks.GetCache()
	block := cache.Pool.Get().([]byte)
	defer cache.Pool.Put(block)

	_, err := tr.blocks.Read(treeIndex, block)
	if err != nil {
		return err
	}

	numBlocks := tr.getBlockSize(block)
	for i := 0; i <= numBlocks; i++ {
		childIndex := tr.getBlockChild(block, i)
		if childIndex != 0 {
			if err := tr.FreeTree(childIndex, ignoreReadOnly); err != nil {
				return err
			}
		}
	}
	return nil
}
