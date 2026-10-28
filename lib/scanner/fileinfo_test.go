// Copyright (C) 2026 The Syncthing Authors.
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this file,
// You can obtain one at https://mozilla.org/MPL/2.0/.

package scanner

import (
	"bytes"
	"context"
	"testing"

	"github.com/syncthing/syncthing/lib/fs"
	"github.com/syncthing/syncthing/lib/protocol"
)

type fixedCurrentFiler map[string]protocol.FileInfo

func (c fixedCurrentFiler) CurrentFile(name string) (protocol.FileInfo, bool) {
	f, ok := c[name]
	return f, ok
}

func TestFileInfoForMatchesWalk(t *testing.T) {
	tmp := t.TempDir()
	testFs := fs.NewFilesystem(fs.FilesystemTypeBasic, tmp)
	fd, err := testFs.Create("file")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fd.Write(bytes.Repeat([]byte("x"), 300<<10)); err != nil {
		t.Fatal(err)
	}
	fd.Close()

	walked := walkDir(testFs, ".", nil, nil, 0)
	cfg, cancel := testConfig()
	defer cancel()
	cfg.Filesystem = testFs
	cfg.ScanOwnership = true
	var want protocol.FileInfo
	for _, f := range walked {
		if f.Name == "file" {
			want = f
		}
	}
	if want.Name == "" {
		t.Fatal("walk did not find the file")
	}

	got, err := FileInfoFor(context.Background(), cfg, "file", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.BlocksHash, want.BlocksHash) || got.Size != want.Size || got.RawBlockSize != want.RawBlockSize {
		t.Fatalf("got %v, walk produced %v", got, want)
	}
	if !got.IsEquivalentOptional(want, protocol.FileInfoComparison{}) || got.ModifiedBy != want.ModifiedBy {
		t.Fatalf("not equivalent: %v %v", got, want)
	}

	// Precomputed blocks are used as they are, and checked against the file.
	again, err := FileInfoFor(context.Background(), cfg, "file", got.Blocks)
	if err != nil || !bytes.Equal(again.BlocksHash, got.BlocksHash) {
		t.Fatalf("precomputed blocks: %v", err)
	}
	if _, err := FileInfoFor(context.Background(), cfg, "file", got.Blocks[:1]); err == nil {
		t.Fatal("accepted a block list for a shorter file")
	}

	// A list with another block size, as a renamed file keeps, is used with
	// its own block size.
	larger, err := Blocks(context.Background(), bytes.NewReader(bytes.Repeat([]byte("x"), 300<<10)), 256<<10, 300<<10, nil)
	if err != nil {
		t.Fatal(err)
	}
	kept, err := FileInfoFor(context.Background(), cfg, "file", larger)
	if err != nil || kept.RawBlockSize != 256<<10 || len(kept.Blocks) != 2 {
		t.Fatalf("kept block list: %v %v", kept, err)
	}
}

func TestFileInfoForNewVersion(t *testing.T) {
	tmp := t.TempDir()
	testFs := fs.NewFilesystem(fs.FilesystemTypeBasic, tmp)
	fd, err := testFs.Create("file")
	if err != nil {
		t.Fatal(err)
	}
	fd.Write([]byte("contents"))
	fd.Close()
	short := protocol.LocalDeviceID.Short()
	cur := protocol.FileInfo{
		Name:         "file",
		Type:         protocol.FileInfoTypeFile,
		Version:      protocol.Vector{}.Update(42),
		BlocksHash:   []byte("previous"),
		RawBlockSize: protocol.MinBlockSize * 2,
	}
	cfg := Config{Filesystem: testFs, ShortID: short, CurrentFiler: fixedCurrentFiler{"file": cur}}
	got, err := FileInfoFor(context.Background(), cfg, "file", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.New || !got.Version.GreaterEqual(cur.Version) || got.Version.Counter(short) == 0 {
		t.Fatalf("not a new version of the current one: %v", got.Version)
	}
	if !bytes.Equal(got.PreviousBlocksHash, cur.BlocksHash) || got.ModifiedBy != short {
		t.Fatal("previous blocks hash or modifier not set")
	}
	if got.RawBlockSize != protocol.MinBlockSize*2 {
		t.Fatalf("block size %d not retained", got.RawBlockSize)
	}
	if err := testFs.Mkdir("dir", 0o755); err != nil {
		t.Fatal(err)
	}
	dir, err := FileInfoFor(context.Background(), cfg, "dir", nil)
	if err != nil || !dir.IsDirectory() || !dir.New {
		t.Fatalf("directory: %v %v", dir, err)
	}
}
