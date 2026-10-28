// Copyright (C) 2026 The Syncthing Authors.
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this file,
// You can obtain one at https://mozilla.org/MPL/2.0/.

package model

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/syncthing/syncthing/lib/config"
	"github.com/syncthing/syncthing/lib/protocol"
	"github.com/syncthing/syncthing/lib/scanner"
)

func setupCoordinatedFolder(t *testing.T, modify func(*config.FolderConfiguration)) (*testModel, config.FolderConfiguration) {
	t.Helper()
	w, fcfg := newDefaultCfgWrapper(t)
	fcfg.FilesystemType = config.FilesystemTypeBasic
	fcfg.Path = t.TempDir()
	if modify != nil {
		modify(&fcfg)
	}
	must(t, os.Mkdir(filepath.Join(fcfg.Path, ".stfolder"), 0o700))
	_, err := w.Modify(func(c *config.Configuration) { c.SetFolder(fcfg) })
	must(t, err)
	m := setupModel(t, w)
	t.Cleanup(func() { cleanupModel(m) })
	return m, fcfg
}

// publish replaces name with contents the way a coordinator does: through a
// temporary file and a rename, then builds and commits its index entry.
func publish(ctx context.Context, h FolderHandle, root, name string, contents []byte) (protocol.FileInfo, error) {
	tmp := filepath.Join(root, ".syncthing."+filepath.Base(name)+".tmp")
	if err := os.WriteFile(tmp, contents, 0o644); err != nil {
		return protocol.FileInfo{}, err
	}
	if err := os.Rename(tmp, filepath.Join(root, name)); err != nil {
		return protocol.FileInfo{}, err
	}
	fi, err := scanner.FileInfoFor(ctx, h.ScanConfig(), name, nil)
	if err != nil {
		return protocol.FileInfo{}, err
	}
	return fi, h.CommitLocal([]protocol.FileInfo{fi})
}

func TestRunInFolderLoopCommitsLikeAScan(t *testing.T) {
	m, fcfg := setupCoordinatedFolder(t, nil)
	must(t, os.WriteFile(filepath.Join(fcfg.Path, "a.txt"), []byte("one"), 0o644))
	must(t, m.ScanFolder(fcfg.ID))
	old, ok := m.testCurrentFolderFile(fcfg.ID, "a.txt")
	if !ok {
		t.Fatal("not scanned")
	}
	before, err := m.LocalSequence(fcfg.ID)
	must(t, err)

	err = m.RunInFolderLoop(fcfg.ID, func(ctx context.Context, h FolderHandle) error {
		_, err := publish(ctx, h, fcfg.Path, "a.txt", []byte("two two"))
		return err
	})
	must(t, err)

	cur, ok := m.testCurrentFolderFile(fcfg.ID, "a.txt")
	if !ok {
		t.Fatal("missing after commit")
	}
	if bytes.Equal(cur.BlocksHash, old.BlocksHash) || cur.Size != 7 {
		t.Fatalf("content not indexed: %v", cur)
	}
	if !cur.Version.GreaterEqual(old.Version) || cur.Version.Equal(old.Version) {
		t.Fatalf("version not advanced: %v -> %v", old.Version, cur.Version)
	}
	if !bytes.Equal(cur.PreviousBlocksHash, old.BlocksHash) {
		t.Fatal("previous blocks hash not recorded")
	}
	if cur.InConflictWith(old) {
		t.Fatal("new version conflicts with the one it replaced")
	}
	after, err := m.LocalSequence(fcfg.ID)
	must(t, err)
	if after <= before {
		t.Fatalf("sequence %d not after %d", after, before)
	}
	global, ok, err := m.CurrentGlobalFile(fcfg.ID, "a.txt")
	must(t, err)
	if !ok || !global.Version.Equal(cur.Version) {
		t.Fatal("global version is not the committed one")
	}

	// A scan finds nothing to change, so the committed entry is what a scan
	// would have produced.
	must(t, m.ScanFolder(fcfg.ID))
	rescanned, err := m.LocalSequence(fcfg.ID)
	must(t, err)
	if rescanned != after {
		t.Fatalf("scan changed the index after the commit (%d -> %d)", after, rescanned)
	}
}

func TestRunInFolderLoopCreatesAndDeletes(t *testing.T) {
	m, fcfg := setupCoordinatedFolder(t, nil)
	err := m.RunInFolderLoop(fcfg.ID, func(ctx context.Context, h FolderHandle) error {
		if err := h.Filesystem().Mkdir("d", 0o755); err != nil {
			return err
		}
		dir, err := scanner.FileInfoFor(ctx, h.ScanConfig(), "d", nil)
		if err != nil {
			return err
		}
		if err := h.CommitLocal([]protocol.FileInfo{dir}); err != nil {
			return err
		}
		_, err = publish(ctx, h, fcfg.Path, filepath.Join("d", "b"), nil)
		return err
	})
	must(t, err)
	dir, ok := m.testCurrentFolderFile(fcfg.ID, "d")
	if !ok || !dir.IsDirectory() {
		t.Fatal("directory not indexed")
	}
	file, ok := m.testCurrentFolderFile(fcfg.ID, "d/b")
	if !ok || file.Size != 0 || len(file.Blocks) != 1 {
		t.Fatalf("empty file not indexed: %v", file)
	}

	err = m.RunInFolderLoop(fcfg.ID, func(ctx context.Context, h FolderHandle) error {
		cur, ok, err := h.CurrentFile("d/b")
		if err != nil || !ok {
			return fmt.Errorf("current file: %v %v", ok, err)
		}
		if err := h.Filesystem().Remove("d/b"); err != nil {
			return err
		}
		deletion := h.DeletionOf(cur)
		if !deletion.Deleted || deletion.Version.Equal(cur.Version) {
			return errors.New("deletion entry not a new deleted version")
		}
		return h.CommitLocal([]protocol.FileInfo{deletion})
	})
	must(t, err)
	file, ok = m.testCurrentFolderFile(fcfg.ID, "d/b")
	if !ok || !file.Deleted {
		t.Fatal("deletion not indexed")
	}
	before, err := m.LocalSequence(fcfg.ID)
	must(t, err)
	must(t, m.ScanFolder(fcfg.ID))
	after, err := m.LocalSequence(fcfg.ID)
	must(t, err)
	if after != before {
		t.Fatal("scan changed the index after the deletion was committed")
	}
}

func TestRunInFolderLoopErrorsAreTheCallers(t *testing.T) {
	m, fcfg := setupCoordinatedFolder(t, nil)
	failure := errors.New("operation failed")
	err := m.RunInFolderLoop(fcfg.ID, func(context.Context, FolderHandle) error {
		return failure
	})
	if !errors.Is(err, failure) {
		t.Fatalf("got %v", err)
	}
	state, _, err := m.State(fcfg.ID)
	must(t, err)
	if state == FolderError.String() {
		t.Fatal("a failed operation put the folder into an error state")
	}
	if err := m.RunInFolderLoop("missing", func(context.Context, FolderHandle) error { return nil }); err == nil {
		t.Fatal("ran in a folder that doesn't exist")
	}
}

func TestRunInFolderLoopIgnoresAndReservedState(t *testing.T) {
	m, fcfg := setupCoordinatedFolder(t, nil)
	must(t, m.SetIgnores(fcfg.ID, []string{"*.tmp"}))
	err := m.RunInFolderLoop(fcfg.ID, func(_ context.Context, h FolderHandle) error {
		if !h.IsIgnored("x.tmp") || h.IsIgnored("x.txt") {
			return errors.New("ignore patterns not applied")
		}
		if h.HasVersioner() {
			return errors.New("versioner reported for a folder without versioning")
		}
		if err := h.Archive("x.txt"); err == nil {
			return errors.New("archived without a versioner")
		}
		return nil
	})
	must(t, err)
}

func TestRunInFolderLoopArchivesPlatformHistory(t *testing.T) {
	m, fcfg := setupCoordinatedFolder(t, func(c *config.FolderConfiguration) {
		c.Versioning = config.VersioningConfiguration{Type: "staggered", Params: map[string]string{"preservePlatformData": "true"}}
		c.IgnorePerms = true
	})
	must(t, os.WriteFile(filepath.Join(fcfg.Path, "file.txt"), []byte("old contents"), 0o644))
	must(t, m.ScanFolder(fcfg.ID))
	err := m.RunInFolderLoop(fcfg.ID, func(ctx context.Context, h FolderHandle) error {
		if !h.HasVersioner() {
			return errors.New("no versioner")
		}
		if err := h.Archive("file.txt"); err != nil {
			return err
		}
		_, err := publish(ctx, h, fcfg.Path, "file.txt", []byte("new contents"))
		return err
	})
	must(t, err)
	versions, err := m.folderVersioners[fcfg.ID].GetVersions()
	must(t, err)
	if len(versions["file.txt"]) != 1 {
		t.Fatalf("replaced version not archived: %v", versions)
	}
}

// Coordinated commits and scans of the same folder run one at a time; with
// -race this also checks the hook's own synchronization.
func TestRunInFolderLoopSerializedWithScans(t *testing.T) {
	m, fcfg := setupCoordinatedFolder(t, nil)
	const n = 20
	var wg sync.WaitGroup
	errs := make(chan error, 2*n)
	for i := range n {
		wg.Add(2)
		go func() {
			defer wg.Done()
			name := fmt.Sprintf("f%d", i)
			errs <- m.RunInFolderLoop(fcfg.ID, func(ctx context.Context, h FolderHandle) error {
				_, err := publish(ctx, h, fcfg.Path, name, []byte(name))
				return err
			})
		}()
		go func() {
			defer wg.Done()
			errs <- m.ScanFolder(fcfg.ID)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		must(t, err)
	}
	for i := range n {
		name := fmt.Sprintf("f%d", i)
		fi, ok := m.testCurrentFolderFile(fcfg.ID, name)
		if !ok || fi.Size != int64(len(name)) {
			t.Fatalf("%s not indexed: %v", name, fi)
		}
	}
}

func TestRunInFolderLoopStopsWithTheFolder(t *testing.T) {
	m, fcfg := setupCoordinatedFolder(t, nil)
	entered := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- m.RunInFolderLoop(fcfg.ID, func(ctx context.Context, _ FolderHandle) error {
			close(entered)
			<-ctx.Done()
			return ctx.Err()
		})
	}()
	<-entered
	m.cancel()
	<-m.stopped
	m.cancel = nil
	if err := <-done; err == nil {
		t.Fatal("cancelled operation reported success")
	}
}
