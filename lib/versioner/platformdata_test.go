// Copyright (C) 2026 The Syncthing Authors.
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this file,
// You can obtain one at https://mozilla.org/MPL/2.0/.
package versioner

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/syncthing/syncthing/lib/config"
	"github.com/syncthing/syncthing/lib/fs"
	"github.com/syncthing/syncthing/lib/protocol"
)

func historyVersioner(t *testing.T) *staggered {
	t.Helper()
	cfg := config.FolderConfiguration{Path: t.TempDir(), FilesystemType: config.FilesystemTypeBasic,
		Versioning: config.VersioningConfiguration{Type: "staggered", Params: map[string]string{"preservePlatformData": "true"}}}
	v := newStaggered(cfg).(*staggered)
	return v
}
func historyPD(value byte) protocol.PlatformData {
	return protocol.PlatformData{Darwin: &protocol.XattrData{Xattrs: []protocol.Xattr{
		{Name: "com.apple.ResourceFork", Value: bytes.Repeat([]byte{value}, 128*1024)},
		{Name: "com.apple.metadata:_kMDItemUserTags", Value: []byte{value, 42}},
	}}}
}
func writeHistoryFile(t *testing.T, v *staggered, name, value string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(v.folderFs.URI(), name), []byte(value), 0o600); err != nil {
		t.Fatal(err)
	}
}
func onlyVersion(t *testing.T, v *staggered, name string) FileVersion {
	t.Helper()
	versions, err := v.GetVersions()
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 1 || len(versions[name]) != 1 {
		t.Fatalf("unexpected versions: %v", versions)
	}
	return versions[name][0]
}
func TestPlatformHistoryArchiveRestore(t *testing.T) {
	v := historyVersioner(t)
	pd := historyPD(1)
	writeHistoryFile(t, v, "test.txt", "original")
	if err := v.ArchiveWithPlatformData("test.txt", pd, false); err != nil {
		t.Fatal(err)
	}
	if _, err := v.folderFs.Stat("test.txt"); !fs.IsNotExist(err) {
		t.Fatalf("live still exists: %v", err)
	}
	ver := onlyVersion(t, v, "test.txt")
	writeHistoryFile(t, v, "test.txt", "replacement")
	if err := v.RestoreWithPlatformData("test.txt", ver.VersionTime, historyPD(2)); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(v.folderFs.URI(), "test.txt"))
	if err != nil || string(body) != "original" {
		t.Fatalf("body %q: %v", body, err)
	}
	// Simulate restarting after rename but before scan/index acknowledgement.
	restarted := &staggered{folderFs: v.folderFs, versionsFs: v.versionsFs, preservePlatformData: true, platformMut: sync.Mutex{}}
	got, ok, err := restarted.RestoredPlatformData("test.txt")
	if err != nil || !ok || !reflect.DeepEqual(pd, got) {
		t.Fatalf("metadata mismatch: %v, %v", ok, err)
	}
	if err := v.CommitRestoredPlatformData("test.txt", historyPD(9)); err == nil {
		t.Fatal("accepted wrong index metadata")
	}
	if err := v.CommitRestoredPlatformData("test.txt", pd); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := v.RestoredPlatformData("test.txt"); err != nil || ok {
		t.Fatalf("pending after acknowledgement: %v %v", ok, err)
	}
}
func TestPlatformHistoryMetadataOnlyAndCleanup(t *testing.T) {
	v := historyVersioner(t)
	writeHistoryFile(t, v, "tags.txt", "unchanged body")
	if err := v.ArchiveWithPlatformData("tags.txt", historyPD(3), true); err != nil {
		t.Fatal(err)
	}
	if body, err := os.ReadFile(filepath.Join(v.folderFs.URI(), "tags.txt")); err != nil || string(body) != "unchanged body" {
		t.Fatal("copy changed live file", err)
	}
	if _, ok, err := readPlatformRef(v.folderFs, "tags.txt"); err != nil || ok {
		t.Fatalf("live copy marked: %v %v", ok, err)
	}
	onlyVersion(t, v, "tags.txt")
	entries, err := v.versionsFs.DirNames(platformRoot)
	if err != nil || len(entries) != 1 {
		t.Fatalf("payloads %v %v", entries, err)
	}
	for i := range v.interval {
		v.interval[i].end = -1
	}
	// Explicitly expire all versions, avoiding a clock-dependent sleep.
	if err := clean(context.Background(), v.versionsFs, func(v []string, _ time.Time) []string { return v }, true); err != nil {
		t.Fatal(err)
	}
	entries, err = v.versionsFs.DirNames(platformRoot)
	if err != nil || len(entries) != 0 {
		t.Fatalf("payload leaked %v %v", entries, err)
	}
}
func TestPlatformHistoryCorruptRestoreLeavesLive(t *testing.T) {
	v := historyVersioner(t)
	writeHistoryFile(t, v, "corrupt.txt", "original")
	if err := v.ArchiveWithPlatformData("corrupt.txt", historyPD(1), false); err != nil {
		t.Fatal(err)
	}
	ver := onlyVersion(t, v, "corrupt.txt")
	entries, _ := v.versionsFs.DirNames(platformRoot)
	if err := v.versionsFs.Remove(filepath.Join(platformRoot, entries[0])); err != nil {
		t.Fatal(err)
	}
	writeHistoryFile(t, v, "corrupt.txt", "live")
	if err := v.RestoreWithPlatformData("corrupt.txt", ver.VersionTime, historyPD(2)); err == nil {
		t.Fatal("accepted missing metadata")
	}
	body, err := os.ReadFile(filepath.Join(v.folderFs.URI(), "corrupt.txt"))
	if err != nil || string(body) != "live" {
		t.Fatalf("live changed %q %v", body, err)
	}
}
func TestPlatformHistoryEmptyAttributes(t *testing.T) {
	v := historyVersioner(t)
	writeHistoryFile(t, v, "empty.txt", "body")
	if err := v.ArchiveWithPlatformData("empty.txt", protocol.PlatformData{}, false); err != nil {
		t.Fatal(err)
	}
	ver := onlyVersion(t, v, "empty.txt")
	if err := v.RestoreWithPlatformData("empty.txt", ver.VersionTime, historyPD(2)); err != nil {
		t.Fatal(err)
	}
	pd, ok, err := v.RestoredPlatformData("empty.txt")
	if err != nil || !ok || !reflect.DeepEqual(pd, protocol.PlatformData{}) {
		t.Fatalf("empty attributes lost %v %v", ok, err)
	}
}

type failHistoryRename struct {
	fs.Filesystem
	fail bool
}

func (f *failHistoryRename) Rename(old, new string) error {
	if f.fail && strings.HasPrefix(old, DefaultPath+string(filepath.Separator)) && new == "file" {
		f.fail = false
		return errors.New("injected publish failure")
	}
	return f.Filesystem.Rename(old, new)
}
func TestPlatformHistoryFailedPublishKeepsLive(t *testing.T) {
	v := historyVersioner(t)
	writeHistoryFile(t, v, "file", "historical")
	if err := v.ArchiveWithPlatformData("file", historyPD(1), false); err != nil {
		t.Fatal(err)
	}
	ver := onlyVersion(t, v, "file")
	writeHistoryFile(t, v, "file", "live")
	v.folderFs = &failHistoryRename{Filesystem: v.folderFs, fail: true}
	if err := v.RestoreWithPlatformData("file", ver.VersionTime, historyPD(2)); err == nil {
		t.Fatal("injection failed")
	}
	body, err := os.ReadFile(filepath.Join(v.folderFs.URI(), "file"))
	if err != nil || string(body) != "live" {
		t.Fatalf("failed restore lost live body %q %v", body, err)
	}
	if _, pending, err := v.RestoredPlatformData("file"); err != nil || pending {
		t.Fatalf("failed restore marked live: %v %v", pending, err)
	}
	// The selected historical version must still be complete and restorable.
	if err := v.RestoreWithPlatformData("file", ver.VersionTime, historyPD(2)); err != nil {
		t.Fatal(err)
	}
}
func TestPlatformHistoryCopyPreservesExecutable(t *testing.T) {
	v := historyVersioner(t)
	writeHistoryFile(t, v, "exec", "executable")
	if err := v.folderFs.Chmod("exec", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := v.ArchiveWithPlatformData("exec", historyPD(1), true); err != nil {
		t.Fatal(err)
	}
	ver := onlyVersion(t, v, "exec")
	if err := v.RestoreWithPlatformData("exec", ver.VersionTime, historyPD(2)); err != nil {
		t.Fatal(err)
	}
	info, err := v.folderFs.Stat("exec")
	if err != nil || info.Mode()&0o777 != 0o755 {
		t.Fatalf("executable mode lost %v %v", info, err)
	}
}

func TestPlatformHistoryReclaimsOrphansProtectsPending(t *testing.T) {
	v := historyVersioner(t)
	orphan, err := v.savePlatformData(historyPD(9))
	if err != nil {
		t.Fatal(err)
	}
	writeHistoryFile(t, v, "file", "history")
	if err := v.ArchiveWithPlatformData("file", historyPD(1), false); err != nil {
		t.Fatal(err)
	}
	ver := onlyVersion(t, v, "file")
	if err := v.RestoreWithPlatformData("file", ver.VersionTime, historyPD(2)); err != nil {
		t.Fatal(err)
	}
	// An interrupted temporary pending record is not a published record and
	// must not block reconciliation.
	temporary, err := v.versionsFs.Create(platformPending(orphan) + ".tmp")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = temporary.Write([]byte("{")); err != nil {
		t.Fatal(err)
	}
	if err = temporary.Close(); err != nil {
		t.Fatal(err)
	}
	entries, err := v.versionsFs.DirNames(platformRoot)
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-48 * time.Hour)
	for _, name := range entries {
		if err := v.versionsFs.Chtimes(filepath.Join(platformRoot, name), old, old); err != nil {
			t.Fatal(err)
		}
	}
	if err := v.reconcilePlatformData(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := v.versionsFs.Stat(platformBlob(orphan)); !fs.IsNotExist(err) {
		t.Fatalf("orphan retained: %v", err)
	}
	if pd, ok, err := v.RestoredPlatformData("file"); err != nil || !ok || !reflect.DeepEqual(pd, historyPD(1)) {
		t.Fatalf("pending restore collected: %v %v", ok, err)
	}
}

func TestPlatformHistoryFirstArchiveInSubdirectory(t *testing.T) {
	v := historyVersioner(t)
	if err := v.folderFs.MkdirAll("nested/deeper", 0o700); err != nil {
		t.Fatal(err)
	}
	writeHistoryFile(t, v, "nested/deeper/file", "original")
	if err := v.ArchiveWithPlatformData("nested/deeper/file", historyPD(1), false); err != nil {
		t.Fatal(err)
	}
	ver := onlyVersion(t, v, "nested/deeper/file")
	if err := v.RestoreWithPlatformData("nested/deeper/file", ver.VersionTime, historyPD(2)); err != nil {
		t.Fatal(err)
	}
	pd, ok, err := v.RestoredPlatformData("nested/deeper/file")
	if err != nil || !ok || !reflect.DeepEqual(pd, historyPD(1)) {
		t.Fatalf("nested metadata mismatch %v %v", ok, err)
	}
}
