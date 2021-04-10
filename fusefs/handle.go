package fusefs

import (
	"io"

	"bazil.org/fuse"
	"github.com/go-errors/errors"

	"github.com/msg555/ctrfs/storage"
	"github.com/msg555/ctrfs/unix"
)

type Handle interface {
	Read(*fuse.ReadRequest) error
	Write(*fuse.WriteRequest) error
	Release(*fuse.ReleaseRequest) error
}

func (conn *Connection) handleReleaseRequest(req *fuse.ReleaseRequest) error {
	conn.handleLock.Lock()
	handle, ok := conn.handleMap[req.Handle]
	delete(conn.handleMap, req.Handle)
	conn.handleLock.Unlock()

	if !ok {
		return FuseError{
			source: errors.New("invalid file handle"),
			errno:  unix.EBADF,
		}
	}
	return handle.Release(req)
}

func (conn *Connection) handleReadRequest(req *fuse.ReadRequest) error {
	conn.handleLock.RLock()
	handle, ok := conn.handleMap[req.Handle]
	conn.handleLock.RUnlock()

	if !ok {
		return FuseError{
			source: errors.New("invalid file handle"),
			errno:  unix.EBADF,
		}
	}
	return handle.Read(req)
}

func (conn *Connection) handleWriteRequest(req *fuse.WriteRequest) error {
	conn.handleLock.RLock()
	handle, ok := conn.handleMap[req.Handle]
	conn.handleLock.RUnlock()

	if !ok {
		return FuseError{
			source: errors.New("invalid file handle"),
			errno:  unix.EBADF,
		}
	}
	return handle.Write(req)
}

func (conn *Connection) handleFlushRequest(req *fuse.FlushRequest) error {
	// Read only file system, flush does nothing
	req.Respond()
	return nil
}

func (conn *Connection) OpenHandle(handle Handle) fuse.HandleID {
	conn.handleLock.Lock()
	conn.lastHandleID++
	handleID := conn.lastHandleID
	conn.handleMap[handleID] = handle
	conn.handleLock.Unlock()
	return handleID
}

type FileHandleDir struct {
	Conn *Connection
	*storage.InodeData
}

func (h *FileHandleDir) Read(req *fuse.ReadRequest) error {
	if !req.Dir {
		return unix.EISDIR
	}
	if uint64(req.Offset) == DIRENT_OFFSET_EOF {
		req.Respond(&fuse.ReadResponse{})
		return nil
	}

	buf := make([]byte, req.Size)

	lastOffset := 0
	bufOffset := 0
	complete, err := h.Conn.Mount.ScanChildren(h.InodeData, uint64(req.Offset), func(offset uint64, inodeId storage.InodeId, name string, inode *storage.InodeData) bool {
		if bufOffset != 0 {
			updateDirEntryOffset(buf[lastOffset:], offset)
		}

		size := addDirEntry(buf[bufOffset:], name, inodeId, inode)
		if size == 0 {
			return false
		}
		lastOffset = bufOffset
		bufOffset += size
		return true
	})
	if err != nil {
		return err
	}

	if bufOffset > 0 && complete {
		updateDirEntryOffset(buf[lastOffset:], DIRENT_OFFSET_EOF)
	}

	req.Respond(&fuse.ReadResponse{
		Data: buf[:bufOffset],
	})
	return nil
}

func (h *FileHandleDir) Write(req *fuse.WriteRequest) error {
	return FuseError{
		source: errors.New("cannot write to dir"),
		errno:  unix.EBADF,
	}
}

func (h *FileHandleDir) Release(req *fuse.ReleaseRequest) error {
	req.Respond()
	return nil
}

type FileHandleReg struct {
	Conn *Connection
	storage.InodeId
	storage.FileView
}

func (h *FileHandleReg) Read(req *fuse.ReadRequest) error {
	if req.Dir {
		return unix.ENOTDIR
	}

	buf := make([]byte, req.Size)
	read, err := h.FileView.ReadAt(buf, req.Offset)
	if err != nil && err != io.EOF {
		return err
	}

	req.Respond(&fuse.ReadResponse{
		Data: buf[:read],
	})
	return nil
}

func (h *FileHandleReg) Write(req *fuse.WriteRequest) error {
	written, err := h.FileView.WriteAt(req.Data, req.Offset)
	if err != nil {
		return err
	}
	req.Respond(&fuse.WriteResponse{
		Size: written,
	})
	return nil
}

func (h *FileHandleReg) Release(req *fuse.ReleaseRequest) error {
	println("RELEASE", h)
	err := h.Conn.Mount.ReleaseView(h.InodeId)
	if err != nil {
		return err
	}
	req.Respond()
	return nil
}
