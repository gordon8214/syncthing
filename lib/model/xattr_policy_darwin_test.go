// Copyright (C) 2026 The Syncthing Authors.
//
// This Source Code Form is subject to the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this file,
// You can obtain one at https://mozilla.org/MPL/2.0/.

package model

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/syncthing/syncthing/lib/config"
	"github.com/syncthing/syncthing/lib/protocol"
	"golang.org/x/sys/unix"
)

func TestXattrReceiveOnlyIndexConverges(t *testing.T) {
	w, cfg := newDefaultCfgWrapper(t)
	cfg.FilesystemType = config.FilesystemTypeBasic
	cfg.Path = t.TempDir()
	cfg.Type = config.FolderTypeReceiveOnly
	cfg.SyncXattrs = true
	cfg.XattrFilter = config.XattrFilter{MacOSMode: config.MacOSXattrAll}
	must(t, os.Mkdir(filepath.Join(cfg.Path, ".stfolder"), 0o700))
	_, err := w.Modify(func(c *config.Configuration) { c.SetFolder(cfg) })
	must(t, err)
	m := setupModel(t, w)
	defer cleanupModel(m)
	conn := addFakeConn(m, device1, cfg.ID)
	path := filepath.Join(cfg.Path, "file")
	must(t, os.WriteFile(path, []byte("body"), 0o600))
	must(t, unix.Lsetxattr(path, "com.example.private", []byte("local"), 0))
	must(t, unix.Lsetxattr(path, "com.example.shared#S", []byte("shared"), 0))
	must(t, m.ScanFolder(cfg.ID))
	fi, ok := m.testCurrentFolderFile(cfg.ID, "file")
	if !ok {
		t.Fatal("file missing")
	}
	fi.LocalFlags = 0
	fi.Version = protocol.Vector{}.Update(device1.Short())
	must(t, m.Index(conn, &protocol.Index{Folder: cfg.ID, Files: []protocol.FileInfo{fi}}))
	runner, ok := m.folderRunners.Get(cfg.ID)
	if !ok {
		t.Fatal("folder runner missing")
	}
	folder := runner.(*receiveOnlyFolder)
	must(t, folder.doInSync(func(context.Context) error {
		// Seed a pre-upgrade local index identical to the unfiltered peer.
		if err := folder.updateLocalsFromPulling([]protocol.FileInfo{fi}); err != nil {
			return err
		}
		folder.XattrFilter = config.XattrFilter{}
		return nil
	}))
	var sequence int64
	for i := range 3 {
		must(t, m.ScanFolder(cfg.ID))
		local, ok := m.testCurrentFolderFile(cfg.ID, "file")
		if !ok || local.IsReceiveOnlyChanged() || len(local.Platform.Xattrs()) != 1 {
			t.Fatalf("scan %d did not reconcile filtered metadata: %+v", i, local)
		}
		if i > 0 && local.Sequence != sequence {
			t.Fatalf("scan %d changed sequence %d -> %d", i, sequence, local.Sequence)
		}
		sequence = local.Sequence
	}
	global, ok, err := m.sdb.GetDeviceFile(cfg.ID, device1, "file")
	must(t, err)
	if !ok || len(global.Platform.Xattrs()) != 2 {
		t.Fatal("mutated remote index metadata")
	}
	private, err := unix.Lgetxattr(path, "com.example.private", nil)
	if err != nil || private != len("local") {
		t.Fatal("removed excluded local attribute", err)
	}
}
