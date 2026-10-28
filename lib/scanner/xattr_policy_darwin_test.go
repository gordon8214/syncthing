// Copyright (C) 2026 The Syncthing Authors.
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this file,
// You can obtain one at https://mozilla.org/MPL/2.0/.

package scanner

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/syncthing/syncthing/lib/config"
	"github.com/syncthing/syncthing/lib/fs"
	"github.com/syncthing/syncthing/lib/protocol"
	"golang.org/x/sys/unix"
)

func TestXattrPolicyIndexMigration(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "file")
	if err := os.WriteFile(path, []byte("body"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := unix.Lsetxattr(path, "com.example.private", []byte("local"), 0); err != nil {
		t.Fatal(err)
	}
	if err := unix.Lsetxattr(path, "com.example.shared#S", []byte("shared"), 0); err != nil {
		t.Fatal(err)
	}
	cfg := Config{Filesystem: fs.NewFilesystem(fs.FilesystemTypeBasic, root), ShortID: protocol.LocalDeviceID.Short(), Hashers: 1, ProgressTickIntervalS: -1, ScanXattrs: true, XattrFilter: config.XattrFilter{MacOSMode: config.MacOSXattrAll}}
	walk := func() []protocol.FileInfo {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var files []protocol.FileInfo
		for res := range Walk(ctx, cfg) {
			if res.Err != nil {
				t.Fatal(res.Err)
			}
			files = append(files, res.File)
		}
		return files
	}
	old := walk()
	if len(old) != 1 || len(old[0].Platform.Xattrs()) != 2 {
		t.Fatalf("old index: %v", old)
	}
	oldVersion := old[0].Version.Copy()
	cfg.CurrentFiler = fakeCurrentFiler{"file": old[0]}
	cfg.XattrFilter = config.XattrFilter{}
	migrated := walk()
	if len(migrated) != 1 || len(migrated[0].Platform.Xattrs()) != 1 {
		t.Fatalf("migration: %v", migrated)
	}
	if migrated[0].Version.Equal(oldVersion) {
		t.Fatal("migration not published")
	}
	cfg.CurrentFiler = fakeCurrentFiler{"file": migrated[0]}
	if next := walk(); len(next) != 0 {
		t.Fatal("migration repeated")
	}
	if err := unix.Lsetxattr(path, "com.example.private", []byte("changed locally"), 0); err != nil {
		t.Fatal(err)
	}
	if next := walk(); len(next) != 0 {
		t.Fatal("excluded metadata caused update")
	}
	if err := unix.Lsetxattr(path, "com.example.shared#S", []byte("updated"), 0); err != nil {
		t.Fatal(err)
	}
	if next := walk(); len(next) != 1 {
		t.Fatal("syncable metadata change lost")
	}
	cfg.XattrFilter = config.XattrFilter{MacOSMode: config.MacOSXattrAll}
	if next := walk(); len(next) != 1 || len(next[0].Platform.Xattrs()) != 2 {
		t.Fatal("all mode failed to recover local metadata")
	}
}
