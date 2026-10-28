// Copyright (C) 2026 The Syncthing Authors.
//
// This Source Code Form is subject to the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this file,
// You can obtain one at https://mozilla.org/MPL/2.0/.

//go:build linux || darwin

package model

import (
	"errors"
	"testing"

	"github.com/syncthing/syncthing/lib/build"
	"github.com/syncthing/syncthing/lib/config"
	"github.com/syncthing/syncthing/lib/fs"
	"github.com/syncthing/syncthing/lib/protocol"
	"github.com/syncthing/syncthing/lib/scanner"
)

func TestForeignXattrsDoNotPreventDiskComparison(t *testing.T) {
	ffs := fs.NewFilesystem(fs.FilesystemTypeBasic, t.TempDir())
	writeFile(t, ffs, "file", []byte("body"))
	stat, err := ffs.Lstat("file")
	must(t, err)
	filter := config.XattrFilter{}
	item, err := scanner.CreateFileInfo(stat, "file", ffs, false, true, filter)
	must(t, err)
	foreign := &protocol.XattrData{Xattrs: []protocol.Xattr{{Name: "foreign", Value: []byte("metadata")}}}
	if build.IsDarwin {
		item.Platform.Linux = foreign
	} else {
		item.Platform.Darwin = foreign
	}
	folder := &sendReceiveFolder{folder: &folder{FolderConfiguration: config.FolderConfiguration{SyncXattrs: true}, mtimefs: ffs}}
	scans := make(chan string, 1)
	must(t, folder.scanIfItemChanged("file", stat, item, true, false, scans))
	if len(scans) != 0 {
		t.Fatal("foreign metadata scheduled a redundant scan")
	}
	must(t, ffs.SetXattr("file", []protocol.Xattr{{Name: "user.test#S", Value: []byte("native change")}}, filter))
	if err := folder.scanIfItemChanged("file", stat, item, true, false, scans); !errors.Is(err, errModified) {
		t.Fatalf("native attribute change was hidden: %v", err)
	}
}
