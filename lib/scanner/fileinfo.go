// Copyright (C) 2026 The Syncthing Authors.
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this file,
// You can obtain one at https://mozilla.org/MPL/2.0/.

package scanner

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"unicode/utf8"

	"github.com/syncthing/syncthing/lib/fs"
	"github.com/syncthing/syncthing/lib/protocol"
)

// BlockSizeFor returns the block size a scan uses for a file of the given
// size: the size protocol.BlockSize picks, unless the current version's block
// size is within a factor of two of it, which is then retained.
func BlockSizeFor(size int64, curFile protocol.FileInfo, hasCurFile bool) int {
	blockSize := protocol.BlockSize(size)
	if hasCurFile {
		curBlockSize := curFile.BlockSize()
		if blockSize > curBlockSize && blockSize/curBlockSize <= 2 {
			// New block size is larger, but not more than twice larger.
			// Retain.
			blockSize = curBlockSize
		} else if curBlockSize > blockSize && curBlockSize/blockSize <= 2 {
			// Old block size is larger, but not more than twice larger.
			// Retain.
			blockSize = curBlockSize
		}
	}
	return blockSize
}

// NameFor returns name, a path relative to the folder root, in the form a
// scan reports it and the folder's index returns it: clean, with the
// platform's path separators and Unicode normalization (NFD on macOS and iOS,
// NFC elsewhere). Names from other devices are NFC, which a scan on macOS
// never reports. It fails for a name no scan reports: one that is not valid
// UTF-8 or not a path below the folder root.
func NameFor(name string) (string, error) {
	if !utf8.ValidString(name) {
		return "", fmt.Errorf("%q: %w", name, errUTF8Invalid)
	}
	native := filepath.Clean(normalizePath(name))
	if native == "." || !filepath.IsLocal(native) {
		return "", fmt.Errorf("%q: %w", name, errNotInFolder)
	}
	return native, nil
}

var (
	errNotInFolder = errors.New("not a path below the folder root")
	errNotIndexed  = errors.New("scans skip internal and temporary files")
)

// FileInfoFor returns the FileInfo a scan with cfg would produce for the
// regular file or directory at name, as a new local version of the entry
// cfg.CurrentFiler returns. It reports an entry even when it is unchanged.
//
// The entry is named as NameFor converts name, which may differ from name: in
// a scan, a name in another form is renamed or skipped. Internal and
// temporary files, which a scan skips, are refused.
//
// For a regular file, blocks may hold the file's block list, for instance
// one hashed with BlockSizeFor's block size or the list of a file it was
// renamed from; when nil, the file is hashed. The caller is responsible for
// the file not changing between that and committing the result. The entry
// always has a protocol block size: a list of several blocks must use one,
// and a list of one block gets the smallest that holds the block and is at
// least BlockSizeFor's.
func FileInfoFor(ctx context.Context, cfg Config, name string, blocks []protocol.BlockInfo) (protocol.FileInfo, error) {
	if cfg.CurrentFiler == nil {
		cfg.CurrentFiler = noCurrentFiler{}
	}
	w := &walker{cfg}

	name, err := NameFor(name)
	if err != nil {
		return protocol.FileInfo{}, err
	}
	if fs.IsInternal(name) || fs.IsTemporary(name) {
		return protocol.FileInfo{}, fmt.Errorf("%s: %w", name, errNotIndexed)
	}
	info, err := cfg.Filesystem.Lstat(name)
	if err != nil {
		return protocol.FileInfo{}, err
	}
	if !info.IsRegular() && !info.IsDir() {
		return protocol.FileInfo{}, fmt.Errorf("%s: not a regular file or directory", name)
	}
	curFile, hasCurFile := cfg.CurrentFiler.CurrentFile(name)
	// The new version is built on the current one's vector, which the
	// CurrentFiler may share with others (Update changes it in place).
	curFile.Version = curFile.Version.Copy()

	f, err := CreateFileInfo(info, name, cfg.Filesystem, cfg.ScanOwnership, cfg.ScanXattrs, cfg.XattrFilter)
	if err != nil {
		return protocol.FileInfo{}, err
	}
	f = w.updateFileInfo(f, curFile)
	f.NoPermissions = cfg.IgnorePerms
	f.New = !hasCurFile
	if hasCurFile && curFile.ShouldConflict() && !f.ShouldConflict() {
		// As in walkRegular and walkDir: we haven't taken the versions of
		// others into account.
		f.Version = f.Version.DropOthers(cfg.ShortID)
	}
	if info.IsDir() {
		return f, nil
	}

	if cfg.RestoredPlatformData != nil {
		pd, restored, err := cfg.RestoredPlatformData(name)
		if err != nil {
			return protocol.FileInfo{}, err
		}
		if restored {
			f.Platform = pd
		}
	}

	blockSize := BlockSizeFor(info.Size(), curFile, hasCurFile)
	if len(blocks) > 1 {
		// The block size of a longer list is that of its blocks
		blockSize = blocks[0].Size
		if !slices.Contains(protocol.BlockSizes, blockSize) {
			return protocol.FileInfo{}, fmt.Errorf("%s: block size %d is not a protocol block size", name, blockSize)
		}
	} else {
		if len(blocks) == 1 {
			// Any block size that holds the one block describes the list
			blockSize = max(blockSize, blocks[0].Size)
		}
		// The smallest protocol block size that is at least that (a current
		// entry may have kept another size)
		i := slices.IndexFunc(protocol.BlockSizes, func(size int) bool { return size >= blockSize })
		if i < 0 {
			return protocol.FileInfo{}, fmt.Errorf("%s: block of %d bytes is larger than any protocol block size", name, blockSize)
		}
		blockSize = protocol.BlockSizes[i]
	}
	f.RawBlockSize = int32(blockSize)
	if blocks == nil {
		if blocks, err = HashFile(ctx, cfg.Folder, cfg.Filesystem, name, blockSize, nil); err != nil {
			return protocol.FileInfo{}, err
		}
	} else if err := checkBlocks(blocks, info.Size(), blockSize); err != nil {
		return protocol.FileInfo{}, fmt.Errorf("%s: %w", name, err)
	}
	f.Blocks = blocks
	f.BlocksHash = protocol.BlocksHash(blocks)
	f.Size = 0
	for _, b := range blocks {
		f.Size += int64(b.Size)
	}
	return f, nil
}

// checkBlocks reports whether blocks is a block list of a file with the given
// size and block size, which is what Blocks returns.
func checkBlocks(blocks []protocol.BlockInfo, size int64, blockSize int) error {
	if size == 0 {
		if len(blocks) != 1 || blocks[0].Size != 0 {
			return errors.New("block list does not describe an empty file")
		}
		return nil
	}
	var offset int64
	for _, b := range blocks {
		if b.Offset != offset || b.Size <= 0 || b.Size > blockSize || len(b.Hash) == 0 {
			return errors.New("block list does not match the file")
		}
		if b.Size < blockSize && offset+int64(b.Size) != size {
			return errors.New("block list does not match the file")
		}
		offset += int64(b.Size)
	}
	if offset != size {
		return errors.New("block list does not match the file size")
	}
	return nil
}
