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

	"golang.org/x/text/unicode/norm"

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

func writeTestFile(t *testing.T, fsys fs.Filesystem, name string, size int) []byte {
	t.Helper()
	data := bytes.Repeat([]byte("y"), size)
	fd, err := fsys.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	defer fd.Close()
	if _, err := fd.Write(data); err != nil {
		t.Fatal(err)
	}
	return data
}

func TestFileInfoForProtocolBlockSizes(t *testing.T) {
	testFs := fs.NewFilesystem(fs.FilesystemTypeBasic, t.TempDir())
	short := protocol.LocalDeviceID.Short()
	hashed := func(data []byte, blockSize int) []protocol.BlockInfo {
		t.Helper()
		blocks, err := Blocks(context.Background(), bytes.NewReader(data), blockSize, int64(len(data)), nil)
		if err != nil {
			t.Fatal(err)
		}
		return blocks
	}

	// A file renamed to a path without a current entry keeps its one block,
	// hashed with 256 KiB blocks: the entry gets the protocol size that
	// holds it, not the block's own size.
	data := writeTestFile(t, testFs, "renamed", 200<<10)
	cfg := Config{Filesystem: testFs, ShortID: short}
	got, err := FileInfoFor(context.Background(), cfg, "renamed", hashed(data, 256<<10))
	if err != nil || got.RawBlockSize != 256<<10 {
		t.Fatalf("one block of 200 KiB: block size %d, %v", got.RawBlockSize, err)
	}

	// Nor smaller than BlockSizeFor's, which retains the current size.
	data = writeTestFile(t, testFs, "small", 100<<10)
	cfg.CurrentFiler = fixedCurrentFiler{"small": {Name: "small", RawBlockSize: 256 << 10}}
	got, err = FileInfoFor(context.Background(), cfg, "small", hashed(data, 128<<10))
	if err != nil || got.RawBlockSize != 256<<10 {
		t.Fatalf("one block below a retained size: block size %d, %v", got.RawBlockSize, err)
	}

	// A current entry with a block size outside the protocol's doesn't pass
	// it on to a hashed file.
	writeTestFile(t, testFs, "odd", 150<<10)
	cfg.CurrentFiler = fixedCurrentFiler{"odd": {Name: "odd", RawBlockSize: 200 << 10}}
	got, err = FileInfoFor(context.Background(), cfg, "odd", nil)
	if err != nil || got.RawBlockSize != 256<<10 || len(got.Blocks) != 1 {
		t.Fatalf("hashed after an odd current size: %v %v", got, err)
	}

	// A list of several blocks of another size is refused.
	data = writeTestFile(t, testFs, "several", 300<<10)
	cfg.CurrentFiler = nil
	if got, err := FileInfoFor(context.Background(), cfg, "several", hashed(data, 200<<10)); err == nil {
		t.Fatalf("accepted 200 KiB blocks: %v", got)
	}

	// As is one block larger than the largest block size.
	fd, err := testFs.Create("huge")
	if err != nil {
		t.Fatal(err)
	}
	size := int64(protocol.MaxBlockSize + 1)
	if err := fd.Truncate(size); err != nil {
		t.Fatal(err)
	}
	fd.Close()
	huge := []protocol.BlockInfo{{Offset: 0, Size: int(size), Hash: make([]byte, 32)}}
	if got, err := FileInfoFor(context.Background(), cfg, "huge", huge); err == nil {
		t.Fatalf("accepted a block larger than the largest block size: %v", got.RawBlockSize)
	}
}

// otherForm returns name in the Unicode normalization form the platform's
// file names don't use.
func otherForm(name string) string {
	if normalizePath(name) == norm.NFD.String(name) {
		return norm.NFC.String(name)
	}
	return norm.NFD.String(name)
}

func TestFileInfoForNames(t *testing.T) {
	testFs := fs.NewFilesystem(fs.FilesystemTypeBasic, t.TempDir())
	native := normalizePath("café.txt")
	if native == otherForm(native) {
		t.Fatal("test name has a single normalization form")
	}
	writeTestFile(t, testFs, native, 10)
	cfg := Config{Filesystem: testFs, ShortID: protocol.LocalDeviceID.Short()}

	// A name in the other form, or not clean, gets the form a scan gives it.
	for _, name := range []string{native, otherForm(native), "./" + otherForm(native), "sub/../" + native} {
		got, err := FileInfoFor(context.Background(), cfg, name, nil)
		if err != nil {
			t.Fatalf("%q: %v", name, err)
		}
		if got.Name != native {
			t.Errorf("%q: named %q, a scan names it %q", name, got.Name, native)
		}
	}
	walked := walkDir(testFs, ".", nil, nil, 0)
	if len(walked) != 1 || walked[0].Name != native {
		t.Fatalf("walk produced %v", walked)
	}

	// Names a scan skips are refused, also when the item exists.
	if err := testFs.Mkdir(".stfolder", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := testFs.Mkdir(".stversions", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := testFs.Mkdir("sub", 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{".stignore", ".stversions/old", ".syncthing.file.tmp", "~syncthing~file.tmp", "sub/.syncthing.file.tmp"} {
		writeTestFile(t, testFs, name, 1)
	}
	for _, name := range []string{
		".stfolder", ".stignore", ".stversions", ".stversions/old", ".syncthing.file.tmp", "~syncthing~file.tmp",
		"sub/.syncthing.file.tmp", ".", "", "..", "../" + native, "/" + native, "\xff",
	} {
		if got, err := FileInfoFor(context.Background(), cfg, name, nil); err == nil {
			t.Errorf("%q: accepted as %q", name, got.Name)
		}
	}
}

func TestFileInfoForKeepsTheCurrentEntry(t *testing.T) {
	testFs := fs.NewFilesystem(fs.FilesystemTypeBasic, t.TempDir())
	writeTestFile(t, testFs, "file", 10)
	short := protocol.LocalDeviceID.Short()
	// A vector with this device's counter, which Update raises in place
	cur := protocol.FileInfo{Name: "file", Type: protocol.FileInfoTypeFile, Version: protocol.Vector{}.Update(short).Update(42)}
	before := cur.Version.Copy()
	cfg := Config{Filesystem: testFs, ShortID: short, CurrentFiler: fixedCurrentFiler{"file": cur}}
	for range 2 {
		got, err := FileInfoFor(context.Background(), cfg, "file", nil)
		if err != nil {
			t.Fatal(err)
		}
		if got.Version.Compare(before) != protocol.Greater {
			t.Fatalf("version %v not after %v", got.Version, before)
		}
	}
	if !cur.Version.Equal(before) || cur.Version.Counter(short) != before.Counter(short) {
		t.Fatalf("current entry's version changed from %v to %v", before, cur.Version)
	}
}
