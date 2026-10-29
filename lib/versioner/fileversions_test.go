// Copyright (C) 2026 The Syncthing Authors.
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this file,
// You can obtain one at https://mozilla.org/MPL/2.0/.

package versioner

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/syncthing/syncthing/lib/config"
)

func versionAt(t *testing.T, s string) time.Time {
	t.Helper()
	tm, err := time.ParseInLocation(TimeFormat, s, time.Local)
	if err != nil {
		t.Fatal(err)
	}
	return tm
}

func TestFileVersionsSimple(t *testing.T) {
	cfg := config.FolderConfiguration{
		FilesystemType: config.FilesystemTypeBasic,
		Path:           t.TempDir(),
		Versioning:     config.VersioningConfiguration{Type: "simple"},
	}
	v, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	fv, ok := AsFileVersioner(v)
	if !ok {
		t.Fatal("simple versioning cannot list the versions of a file")
	}
	versionsFs := versionerFsFromFolderCfg(cfg)

	// No archive yet
	if versions, err := fv.FileVersions("dir/file.txt"); err != nil || len(versions) != 0 {
		t.Fatalf("versions before archiving: %v, %v", versions, err)
	}

	if err := versionsFs.MkdirAll("dir", 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, versionsFs, "dir/file~20260101-120000.txt", "first")
	writeFile(t, versionsFs, "dir/file~20260102-120000.txt", "second!")
	writeFile(t, versionsFs, "dir/file~garbage.txt", "unparsable")
	writeFile(t, versionsFs, "dir/other~20260101-120000.txt", "other file")
	writeFile(t, versionsFs, "dir/file~20260103-120000.md", "other extension")
	if err := versionsFs.MkdirAll("dir/file~20260104-120000.txt", 0o755); err != nil {
		t.Fatal(err)
	}

	versions, err := fv.FileVersions("dir/file.txt")
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 2 {
		t.Fatalf("got %d versions, want 2: %v", len(versions), versions)
	}
	if !versions[0].VersionTime.Equal(versionAt(t, "20260102-120000")) || versions[0].Size != 7 {
		t.Errorf("newest version: %+v", versions[0])
	}
	if !versions[1].VersionTime.Equal(versionAt(t, "20260101-120000")) || versions[1].Size != 5 {
		t.Errorf("oldest version: %+v", versions[1])
	}

	fd, err := fv.OpenVersion("dir/file.txt", versions[1].VersionTime)
	if err != nil {
		t.Fatal(err)
	}
	defer fd.Close()
	// The open version stays readable when cleanup removes it
	if err := versionsFs.Remove("dir/file~20260101-120000.txt"); err != nil {
		t.Fatal(err)
	}
	if body, err := io.ReadAll(fd); err != nil || string(body) != "first" {
		t.Fatalf("version contents %q: %v", body, err)
	}

	if _, err := fv.OpenVersion("dir/file.txt", versionAt(t, "20260101-120000")); !errors.Is(err, ErrVersionNotFound) {
		t.Errorf("opening a removed version: %v", err)
	}
	if _, err := fv.OpenVersion("dir/file.txt", versionAt(t, "20260105-120000")); !errors.Is(err, ErrVersionNotFound) {
		t.Errorf("opening an unknown version: %v", err)
	}
}

func TestFileVersionsTrashcan(t *testing.T) {
	cfg := config.FolderConfiguration{
		FilesystemType: config.FilesystemTypeBasic,
		Path:           t.TempDir(),
		Versioning:     config.VersioningConfiguration{Type: "trashcan"},
	}
	v, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	fv, ok := AsFileVersioner(v)
	if !ok {
		t.Fatal("trashcan versioning cannot list the versions of a file")
	}
	folderFs := cfg.Filesystem()
	writeFile(t, folderFs, "file", "A")
	if err := v.Archive("file"); err != nil {
		t.Fatal(err)
	}

	versions, err := fv.FileVersions("file")
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 1 || !versions[0].VersionTime.Equal(versions[0].ModTime) || versions[0].Size != 1 {
		t.Fatalf("trashcan versions: %+v", versions)
	}
	fd, err := fv.OpenVersion("file", versions[0].VersionTime)
	if err != nil {
		t.Fatal(err)
	}
	defer fd.Close()
	if body, err := io.ReadAll(fd); err != nil || string(body) != "A" {
		t.Fatalf("version contents %q: %v", body, err)
	}
}

func TestFileVersionsPlatformHistory(t *testing.T) {
	v := historyVersioner(t)
	writeHistoryFile(t, v, "test.txt", "original")
	if err := v.ArchiveWithPlatformData("test.txt", historyPD(1), false); err != nil {
		t.Fatal(err)
	}
	fv, ok := AsFileVersioner(v)
	if !ok {
		t.Fatal("staggered versioning cannot list the versions of a file")
	}
	versions, err := fv.FileVersions("test.txt")
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 1 || versions[0].Size != int64(len("original")) {
		t.Fatalf("versions: %+v", versions)
	}
	if !versions[0].VersionTime.Equal(onlyVersion(t, v, "test.txt").VersionTime) {
		t.Errorf("version time %v differs from GetVersions", versions[0].VersionTime)
	}
	fd, err := fv.OpenVersion("test.txt", versions[0].VersionTime)
	if err != nil {
		t.Fatal(err)
	}
	defer fd.Close()
	if body, err := io.ReadAll(fd); err != nil || string(body) != "original" {
		t.Fatalf("version contents %q: %v", body, err)
	}
	// The platform metadata store in the versions directory holds no versions
	blobs, err := v.versionsFs.DirNames(platformRoot)
	if err != nil || len(blobs) == 0 {
		t.Fatalf("no platform store: %v", err)
	}
	for _, name := range []string{".stversions", filepath.Join(platformRoot, blobs[0])} {
		if versions, err := fv.FileVersions(name); err != nil || len(versions) != 0 {
			t.Fatalf("versions of %s: %v, %v", name, versions, err)
		}
	}
}

func TestFileVersionsNames(t *testing.T) {
	cfg := config.FolderConfiguration{
		FilesystemType: config.FilesystemTypeBasic,
		Path:           t.TempDir(),
		Versioning:     config.VersioningConfiguration{Type: "simple"},
	}
	v, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	fv, _ := AsFileVersioner(v)
	versionsFs := versionerFsFromFolderCfg(cfg)

	if err := versionsFs.MkdirAll(".", 0o755); err != nil {
		t.Fatal(err)
	}
	// A tilde in the name or extension, an old-style tag, and the same time in both styles
	writeFile(t, versionsFs, "a~b~20260101-120000.txt", "tilde in the name")
	writeFile(t, versionsFs, "c~20260101-120000.txt~1", "tilde in the extension")
	writeFile(t, versionsFs, "d.txt~20260101-120000", "old style")
	writeFile(t, versionsFs, "d~20260101-120000.txt", "new style")
	writeFile(t, versionsFs, "d~20260102-120000.txt", "newer")
	// An untagged file is not a version without the trashcan, nor is a live file named like a version of another
	writeFile(t, versionsFs, "e.txt", "untagged")
	writeFile(t, versionsFs, "f~20260101-120000.txt", "version of f.txt")

	for name, want := range map[string][]string{
		"a~b.txt":               {"tilde in the name"},
		"c.txt~1":               {"tilde in the extension"},
		"d.txt":                 {"newer", "new style"},
		"e.txt":                 nil,
		"f~20260101-120000.txt": nil,
		"f.txt":                 {"version of f.txt"},
		"missing/f.txt":         nil,
		".stfolder/f.txt":       nil,
		".stversions/d.txt":     nil,
	} {
		versions, err := fv.FileVersions(name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		var got []string
		for _, version := range versions {
			fd, err := fv.OpenVersion(name, version.VersionTime)
			if err != nil {
				t.Fatalf("%s at %v: %v", name, version.VersionTime, err)
			}
			body, _ := io.ReadAll(fd)
			fd.Close()
			got = append(got, string(body))
		}
		if !slices.Equal(got, want) {
			t.Errorf("versions of %s: %q, want %q", name, got, want)
		}
	}
}

func TestFileVersionsNotThroughSymlinksOrFiles(t *testing.T) {
	cfg := config.FolderConfiguration{
		FilesystemType: config.FilesystemTypeBasic,
		Path:           t.TempDir(),
		Versioning:     config.VersioningConfiguration{Type: "trashcan"},
	}
	v, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	fv, _ := AsFileVersioner(v)
	versionsFs := versionerFsFromFolderCfg(cfg)
	if err := versionsFs.MkdirAll(".", 0o755); err != nil {
		t.Fatal(err)
	}

	// A directory of the versions tree that links elsewhere
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret"), []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := versionsFs.CreateSymlink(outside, "linked"); err != nil {
		t.Fatal(err)
	}
	// A deleted file where the folder now has a directory of that name
	writeFile(t, versionsFs, "notes", "deleted file")

	for _, name := range []string{"linked/secret", "notes/a.txt"} {
		versions, err := fv.FileVersions(name)
		if err != nil || len(versions) != 0 {
			t.Errorf("versions of %s: %v, %v", name, versions, err)
		}
		if _, err := fv.OpenVersion(name, time.Now()); !errors.Is(err, ErrVersionNotFound) {
			t.Errorf("opening a version of %s: %v", name, err)
		}
	}
}

func TestFileVersionsExternal(t *testing.T) {
	cfg := config.FolderConfiguration{
		FilesystemType: config.FilesystemTypeBasic,
		Path:           t.TempDir(),
		Versioning:     config.VersioningConfiguration{Type: "external", Params: map[string]string{"command": "true"}},
	}
	v, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := AsFileVersioner(v); ok {
		t.Fatal("external versioning claims to list versions")
	}
}
