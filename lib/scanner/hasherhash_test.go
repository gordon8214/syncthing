// Copyright (C) 2026 The Syncthing Authors.
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this file,
// You can obtain one at https://mozilla.org/MPL/2.0/.

package scanner

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"hash"
	"io"
	"sync/atomic"
	"testing"

	"github.com/syncthing/syncthing/lib/fs"
	"github.com/syncthing/syncthing/lib/protocol"
)

// A hash that takes its data the way a hash set from outside can: through
// ReadFrom, which can fail halfway.
type readingHash struct {
	hash.Hash
	read     *atomic.Int64
	failNext *atomic.Bool
}

func (h readingHash) ReadFrom(r io.Reader) (int64, error) {
	if h.failNext.CompareAndSwap(true, false) {
		n, _ := io.CopyN(struct{ io.Writer }{h.Hash}, r, 1024)
		return n, errors.New("failed halfway")
	}
	n, err := io.Copy(struct{ io.Writer }{h.Hash}, r)
	h.read.Add(n)
	return n, err
}

func writeHasherTestFile(t *testing.T, tfs fs.Filesystem, name string, size int) protocol.FileInfo {
	t.Helper()
	fd, err := tfs.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	defer fd.Close()
	data := make([]byte, size)
	for i := range data {
		data[i] = byte(i % 251)
	}
	if _, err := fd.Write(data); err != nil {
		t.Fatal(err)
	}
	return protocol.FileInfo{Name: name, Size: int64(size), RawBlockSize: protocol.MinBlockSize}
}

func TestHasherHash(t *testing.T) {
	tfs := fs.NewFilesystem(fs.FilesystemTypeBasic, t.TempDir())
	broken := writeHasherTestFile(t, tfs, "broken", 3*protocol.MinBlockSize)
	file := writeHasherTestFile(t, tfs, "file", 3*protocol.MinBlockSize+1)

	want, err := HashFile(t.Context(), "folder", tfs, file.Name, file.BlockSize(), nil)
	if err != nil {
		t.Fatal(err)
	}

	type contextKey struct{}
	ctx := context.WithValue(t.Context(), contextKey{}, true)

	var made, read atomic.Int64
	var failNext atomic.Bool
	failNext.Store(true)
	SetHasherHash(func(hashCtx context.Context) hash.Hash {
		if hashCtx.Value(contextKey{}) == nil {
			t.Error("the hash wasn't given the hasher's context")
		}
		made.Add(1)
		return readingHash{sha256.New(), &read, &failNext}
	})
	t.Cleanup(func() { SetHasherHash(nil) })

	inbox := make(chan protocol.FileInfo)
	outbox := make(chan ScanResult, 2)
	newParallelHasher(ctx, "folder", tfs, 1, outbox, inbox, nil, nil)
	inbox <- broken
	inbox <- file
	close(inbox)

	// What failed halfway must not reach the hash of the next file
	if first := <-outbox; first.Err == nil || first.Path != broken.Name {
		t.Errorf("first result should be the failure on %s, got %v for %s", broken.Name, first.Err, first.File.Name)
	}
	second := <-outbox
	if second.Err != nil {
		t.Fatal(second.Err)
	}
	if !bytes.Equal(protocol.BlocksHash(second.File.Blocks), protocol.BlocksHash(want)) {
		t.Errorf("blocks through the hash %v differ from %v", second.File.Blocks, want)
	}
	if got := read.Load(); got != file.Size {
		t.Errorf("the hash read %d bytes, want %d", got, file.Size)
	}
	if got := made.Load(); got != 1 {
		t.Errorf("made %d hashes for one hasher", got)
	}
}

func TestHasherHashUnset(t *testing.T) {
	SetHasherHash(func(context.Context) hash.Hash {
		t.Error("the hash was used after it was unset")
		return sha256.New()
	})
	SetHasherHash(nil)

	tfs := fs.NewFilesystem(fs.FilesystemTypeBasic, t.TempDir())
	inbox := make(chan protocol.FileInfo)
	outbox := make(chan ScanResult, 1)
	newParallelHasher(t.Context(), "folder", tfs, 1, outbox, inbox, nil, nil)
	inbox <- writeHasherTestFile(t, tfs, "file", protocol.MinBlockSize)
	close(inbox)
	if result := <-outbox; result.Err != nil {
		t.Fatal(result.Err)
	}
}
