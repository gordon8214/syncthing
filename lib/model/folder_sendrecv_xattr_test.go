// Copyright (C) 2026 The Syncthing Authors.
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this file,
// You can obtain one at https://mozilla.org/MPL/2.0/.

//go:build !windows

package model

import (
	"bytes"
	"errors"
	"os"
	"syscall"
	"testing"

	"github.com/syncthing/syncthing/lib/config"
	"github.com/syncthing/syncthing/lib/fs"
	"github.com/syncthing/syncthing/lib/protocol"
)

// Writing extended attributes needs write access to the item on macOS (and for user attributes on Linux), so
// read-only items must get theirs before, or despite, their final permissions.

var readOnlyTestXattrs = []protocol.Xattr{{Name: "user.synctrain-test-hash#S", Value: []byte("0123456789abcdef")}}

func readOnlyXattrFolder(t *testing.T) (*sendReceiveFolder, fs.Filesystem) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("permissions don't restrict root")
	}
	ffs := fs.NewFilesystem(fs.FilesystemTypeBasic, t.TempDir())
	probe := "probe"
	writeFile(t, ffs, probe, nil)
	if err := ffs.SetXattr(probe, readOnlyTestXattrs, config.XattrFilter{}); errors.Is(err, fs.ErrXattrsNotSupported) ||
		errors.Is(err, syscall.EOPNOTSUPP) {
		t.Skip("xattrs not supported")
	} else if err != nil {
		t.Fatal(err)
	}
	f := &sendReceiveFolder{
		folder: &folder{
			FolderConfiguration: config.FolderConfiguration{SyncXattrs: true},
			mtimefs:             ffs,
		},
	}
	return f, ffs
}

func readOnlyTestFile(name string) protocol.FileInfo {
	return protocol.FileInfo{
		Name:        name,
		Type:        protocol.FileInfoTypeFile,
		Permissions: 0o400,
		ModifiedS:   1234567890,
		Platform: protocol.PlatformData{
			Linux:  &protocol.XattrData{Xattrs: readOnlyTestXattrs},
			Darwin: &protocol.XattrData{Xattrs: readOnlyTestXattrs},
		},
	}
}

func checkReadOnlyXattrs(t *testing.T, ffs fs.Filesystem, name string, mode fs.FileMode) {
	t.Helper()
	info, err := ffs.Lstat(name)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode() & fs.ModePerm; got != mode {
		t.Errorf("%s has mode %o, expected %o", name, got, mode)
	}
	attrs, err := ffs.GetXattr(name, config.XattrFilter{})
	if err != nil {
		t.Fatal(err)
	}
	for _, xa := range attrs {
		if xa.Name == readOnlyTestXattrs[0].Name && bytes.Equal(xa.Value, readOnlyTestXattrs[0].Value) {
			return
		}
	}
	t.Errorf("%s lacks its xattr: %v", name, attrs)
}

func TestPerformFinishReadOnlyFileWithXattrs(t *testing.T) {
	f, ffs := readOnlyXattrFolder(t)
	file := readOnlyTestFile("pack.pack")
	tempName := fs.TempName(file.Name)
	writeFile(t, ffs, tempName, []byte("content"))

	dbUpdateChan := make(chan dbUpdateJob, 1)
	scanChan := make(chan string, 1)
	if err := f.performFinish(file, protocol.FileInfo{}, false, tempName, dbUpdateChan, scanChan); err != nil {
		t.Fatal(err)
	}
	checkReadOnlyXattrs(t, ffs, file.Name, 0o400)
}

func TestSetPlatformDataOnReadOnlyItems(t *testing.T) {
	f, ffs := readOnlyXattrFolder(t)

	// An existing read-only file, as a metadata-only change finds it
	file := readOnlyTestFile("object")
	writeFile(t, ffs, file.Name, []byte("content"))
	if err := ffs.Chmod(file.Name, 0o400); err != nil {
		t.Fatal(err)
	}
	if err := f.setPlatformData(&file, file.Name); err != nil {
		t.Fatal(err)
	}
	checkReadOnlyXattrs(t, ffs, file.Name, 0o400)

	// An existing read-only directory
	dir := readOnlyTestFile("objects")
	dir.Type = protocol.FileInfoTypeDirectory
	if err := ffs.Mkdir(dir.Name, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ffs.Chmod(dir.Name, 0o700) })
	if err := f.setPlatformData(&dir, dir.Name); err != nil {
		t.Fatal(err)
	}
	checkReadOnlyXattrs(t, ffs, dir.Name, 0o500)
}
