// Copyright (C) 2015 The Syncthing Authors.
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this file,
// You can obtain one at https://mozilla.org/MPL/2.0/.

package versioner

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/syncthing/syncthing/lib/config"
	"github.com/syncthing/syncthing/lib/fs"
)

func TestTrashcanArchiveRestoreSwitcharoo(t *testing.T) {
	// This tests that trashcan versioner restoration correctly archives existing file, because trashcan versioner
	// files are untagged, archiving existing file to replace with a restored version technically should collide in
	// in names.
	tmpDir1 := t.TempDir()

	tmpDir2 := t.TempDir()

	cfg := config.FolderConfiguration{
		FilesystemType: config.FilesystemTypeBasic,
		Path:           tmpDir1,
		Versioning: config.VersioningConfiguration{
			FSType: config.FilesystemTypeBasic,
			FSPath: tmpDir2,
		},
	}
	folderFs := cfg.Filesystem()

	versionsFs := fs.NewFilesystem(fs.FilesystemTypeBasic, tmpDir2)

	writeFile(t, folderFs, "file", "A")

	versioner := newTrashcan(cfg)

	if err := versioner.Archive("file"); err != nil {
		t.Fatal(err)
	}

	if _, err := folderFs.Stat("file"); !fs.IsNotExist(err) {
		t.Fatal(err)
	}

	// Check versions
	versions, err := versioner.GetVersions()
	if err != nil {
		t.Fatal(err)
	}

	fileVersions := versions["file"]
	if len(fileVersions) != 1 {
		t.Fatalf("unexpected number of versions: %d != 1", len(fileVersions))
	}

	fileVersion := fileVersions[0]

	if !fileVersion.ModTime.Equal(fileVersion.VersionTime) {
		t.Error("time mismatch")
	}

	if content := readFile(t, versionsFs, "file"); content != "A" {
		t.Errorf("expected A got %s", content)
	}

	writeFile(t, folderFs, "file", "B")

	versionInfo, err := versionsFs.Stat("file")
	if err != nil {
		t.Fatal(err)
	}

	if !versionInfo.ModTime().Truncate(time.Second).Equal(fileVersion.ModTime) {
		t.Error("time mismatch")
	}

	if err := versioner.Restore("file", fileVersion.VersionTime); err != nil {
		t.Fatal(err)
	}

	if content := readFile(t, folderFs, "file"); content != "A" {
		t.Errorf("expected A got %s", content)
	}

	if content := readFile(t, versionsFs, "file"); content != "B" {
		t.Errorf("expected B got %s", content)
	}
}

func TestTrashcanRestoreDeletedFile(t *testing.T) {
	// This tests that the Trash Can restore function works correctly when the file
	// to be restored was deleted/nonexistent in the folder where the file/folder is
	// going to be restored in. (Issue: #7965)

	tmpDir1 := t.TempDir()

	tmpDir2 := t.TempDir()

	cfg := config.FolderConfiguration{
		FilesystemType: config.FilesystemTypeBasic,
		Path:           tmpDir1,
		Versioning: config.VersioningConfiguration{
			FSType: config.FilesystemTypeBasic,
			FSPath: tmpDir2,
		},
	}

	folderFs := cfg.Filesystem()

	versionsFs := fs.NewFilesystem(fs.FilesystemTypeBasic, tmpDir2)

	versioner := newTrashcan(cfg)

	writeFile(t, folderFs, "file", "Some content")

	if err := versioner.Archive("file"); err != nil {
		t.Fatal(err)
	}

	// Shouldn't be in the default folder anymore, thus "deleted"
	if _, err := folderFs.Stat("file"); !fs.IsNotExist(err) {
		t.Fatal(err)
	}

	// It should, however, be in the archive
	if _, err := versionsFs.Lstat("file"); fs.IsNotExist(err) {
		t.Fatal(err)
	}

	versions, err := versioner.GetVersions()
	if err != nil {
		t.Fatal(err)
	}

	fileVersions := versions["file"]
	if len(fileVersions) != 1 {
		t.Fatalf("unexpected number of versions: %d != 1", len(fileVersions))
	}

	fileVersion := fileVersions[0]

	if !fileVersion.ModTime.Equal(fileVersion.VersionTime) {
		t.Error("time mismatch")
	}

	// Restore the file from the archive.
	if err := versioner.Restore("file", fileVersion.VersionTime); err != nil {
		t.Fatal(err)
	}

	// The file should be correctly restored
	if content := readFile(t, folderFs, "file"); content != "Some content" {
		t.Errorf("expected A got %s", content)
	}

	// It should no longer be in the archive
	if _, err := versionsFs.Lstat("file"); !fs.IsNotExist(err) {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, filesystem fs.Filesystem, name string) string {
	t.Helper()
	fd, err := filesystem.Open(name)
	if err != nil {
		t.Fatal(err)
	}
	defer fd.Close()
	buf, err := io.ReadAll(fd)
	if err != nil {
		t.Fatal(err)
	}
	return string(buf)
}

func writeFile(t *testing.T, filesystem fs.Filesystem, name, content string) {
	fd, err := filesystem.OpenFile(name, fs.OptReadWrite|fs.OptCreate, 0o777)
	if err != nil {
		t.Fatal(err)
	}
	defer fd.Close()
	if err := fd.Truncate(int64(len(content))); err != nil {
		t.Fatal(err)
	}

	if n, err := fd.Write([]byte(content)); err != nil || n != len(content) {
		t.Fatal(n, len(content), err)
	}
}

func TestTrashcanCleanOut(t *testing.T) {
	testDir := t.TempDir()

	cfg := config.FolderConfiguration{
		FilesystemType: config.FilesystemTypeBasic,
		Path:           testDir,
		Versioning: config.VersioningConfiguration{
			Params: map[string]string{
				"cleanoutDays": "7",
			},
		},
	}

	fs := cfg.Filesystem()

	v := newTrashcan(cfg)

	testcases := map[string]bool{
		".stversions/file1":                     false,
		".stversions/file2":                     true,
		".stversions/keep1/file1":               false,
		".stversions/keep1/file2":               false,
		".stversions/keep2/file1":               false,
		".stversions/keep2/file2":               true,
		".stversions/keep3/keepsubdir/file1":    false,
		".stversions/remove/file1":              true,
		".stversions/remove/file2":              true,
		".stversions/remove/removesubdir/file1": true,
	}

	t.Run("trashcan versioner trashcan clean up", func(t *testing.T) {
		oldTime := time.Now().Add(-8 * 24 * time.Hour)
		for file, shouldRemove := range testcases {
			fs.MkdirAll(filepath.Dir(file), 0o777)

			writeFile(t, fs, file, "some content")

			if shouldRemove {
				if err := fs.Chtimes(file, oldTime, oldTime); err != nil {
					t.Fatal(err)
				}
			}
		}

		if err := v.Clean(context.Background()); err != nil {
			t.Fatal(err)
		}

		for file, shouldRemove := range testcases {
			_, err := fs.Lstat(file)
			if shouldRemove && !os.IsNotExist(err) {
				t.Error(file, "should have been removed")
			} else if !shouldRemove && err != nil {
				t.Error(file, "should not have been removed")
			}
		}

		if _, err := fs.Lstat(".stversions/keep3"); os.IsNotExist(err) {
			t.Error("directory with non empty subdirs should not be removed")
		}

		if _, err := fs.Lstat(".stversions/remove"); !os.IsNotExist(err) {
			t.Error("empty directory should have been removed")
		}
	})
}

func trashcanForRestoring(t *testing.T) (Versioner, fs.Filesystem, fs.Filesystem) {
	t.Helper()
	versions := t.TempDir()
	cfg := config.FolderConfiguration{
		FilesystemType: config.FilesystemTypeBasic,
		Path:           t.TempDir(),
		Versioning: config.VersioningConfiguration{
			FSType: config.FilesystemTypeBasic,
			FSPath: versions,
			Params: map[string]string{"cleanoutDays": "30"},
		},
	}
	return newTrashcan(cfg), cfg.Filesystem(), fs.NewFilesystem(fs.FilesystemTypeBasic, versions)
}

// Going through with a restore without having found the version would cost the
// file in the folder, and other devices would delete it as well.
func TestTrashcanRestoreOfAVersionThatIsGoneKeepsTheFile(t *testing.T) {
	versioner, folderFs, versionsFs := trashcanForRestoring(t)
	writeFile(t, folderFs, "file", "current")

	if err := versioner.Restore("file", time.Now().Add(-time.Hour)); !errors.Is(err, errNotFound) {
		t.Fatalf("got %v", err)
	}
	if content := readFile(t, folderFs, "file"); content != "current" {
		t.Fatalf("the file is now %q", content)
	}
	if entries, err := versionsFs.DirNames("."); err != nil || len(entries) != 0 {
		t.Fatalf("left in the versions: %v, %v", entries, err)
	}
}

func TestTrashcanRestoresAFileInADirectory(t *testing.T) {
	versioner, folderFs, versionsFs := trashcanForRestoring(t)
	if err := folderFs.MkdirAll("dir/sub", 0o777); err != nil {
		t.Fatal(err)
	}
	writeFile(t, folderFs, "dir/sub/file", "old")
	if err := versioner.Archive("dir/sub/file"); err != nil {
		t.Fatal(err)
	}
	writeFile(t, folderFs, "dir/sub/file", "current")

	versions, err := versioner.GetVersions()
	if err != nil {
		t.Fatal(err)
	}
	if err := versioner.Restore("dir/sub/file", versions["dir/sub/file"][0].VersionTime); err != nil {
		t.Fatal(err)
	}
	if content := readFile(t, folderFs, "dir/sub/file"); content != "old" {
		t.Fatalf("the file is now %q", content)
	}
	if content := readFile(t, versionsFs, "dir/sub/file"); content != "current" {
		t.Fatalf("the version is now %q", content)
	}
}

// A version from another type of versioner is listed, so it has to work too
func TestTrashcanRestoresATaggedVersion(t *testing.T) {
	versioner, folderFs, versionsFs := trashcanForRestoring(t)
	writeFile(t, folderFs, "file.txt", "current")
	stamp := time.Now().Add(-time.Hour).Truncate(time.Second)
	writeFile(t, versionsFs, TagFilename("file.txt", stamp.Format(TimeFormat)), "tagged")

	if err := versioner.Restore("file.txt", stamp); err != nil {
		t.Fatal(err)
	}
	if content := readFile(t, folderFs, "file.txt"); content != "tagged" {
		t.Fatalf("the file is now %q", content)
	}
	if content := readFile(t, versionsFs, "file.txt"); content != "current" {
		t.Fatalf("the version is now %q", content)
	}
}

// What a restore puts in the trash can has only just been archived, however old
// the file is. Other devices have taken the restored file, so this may be the
// last copy of it.
func TestTrashcanCleanKeepsWhatARestoreDisplaced(t *testing.T) {
	versioner, folderFs, versionsFs := trashcanForRestoring(t)
	writeFile(t, folderFs, "file", "old")
	if err := versioner.Archive("file"); err != nil {
		t.Fatal(err)
	}
	writeFile(t, folderFs, "file", "current")
	longAgo := time.Now().Add(-60 * 24 * time.Hour)
	if err := folderFs.Chtimes("file", longAgo, longAgo); err != nil {
		t.Fatal(err)
	}

	versions, err := versioner.GetVersions()
	if err != nil {
		t.Fatal(err)
	}
	if err := versioner.Restore("file", versions["file"][0].VersionTime); err != nil {
		t.Fatal(err)
	}
	if err := versioner.Clean(context.Background()); err != nil {
		t.Fatal(err)
	}
	if content := readFile(t, versionsFs, "file"); content != "current" {
		t.Fatalf("the version is now %q", content)
	}
}

// A version from another type of versioner has the time of the file, which
// says nothing about when it was archived
func TestTrashcanCleanGoesByTheTagOfAVersion(t *testing.T) {
	versioner, _, versionsFs := trashcanForRestoring(t)
	longAgo := time.Now().Add(-60 * 24 * time.Hour)
	recent := TagFilename("recent.txt", time.Now().Format(TimeFormat))
	expired := TagFilename("expired.txt", longAgo.Format(TimeFormat))
	for _, name := range []string{recent, expired} {
		writeFile(t, versionsFs, name, "content")
		if err := versionsFs.Chtimes(name, longAgo, longAgo); err != nil {
			t.Fatal(err)
		}
	}

	if err := versioner.Clean(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := versionsFs.Lstat(recent); err != nil {
		t.Errorf("a version archived just now is gone: %v", err)
	}
	if _, err := versionsFs.Lstat(expired); !fs.IsNotExist(err) {
		t.Errorf("an old version is still there: %v", err)
	}
}

// A number of days that doesn't fit a duration would wrap around and remove
// every version
func TestCleanoutDaysThatDoNotFitKeepVersions(t *testing.T) {
	for value, want := range map[string]int{
		"": 0, "none": 0, "7": 7, "106751": 106751, "106752": 0, "999999": 0,
	} {
		if got := parseCleanoutDays(value); got != want {
			t.Errorf("%q: got %d, want %d", value, got, want)
		}
	}
}

func TestRestoreOfAVersionThatIsGoneKeepsTheFile(t *testing.T) {
	cfg := config.FolderConfiguration{
		FilesystemType: config.FilesystemTypeBasic,
		Path:           t.TempDir(),
		Versioning: config.VersioningConfiguration{
			FSType: config.FilesystemTypeBasic,
			Params: map[string]string{"keep": "5"},
		},
	}
	versioner := newSimple(cfg)
	folderFs := cfg.Filesystem()
	writeFile(t, folderFs, "file", "current")

	if err := versioner.Restore("file", time.Now().Add(-time.Hour)); !errors.Is(err, errNotFound) {
		t.Fatalf("got %v", err)
	}
	if content := readFile(t, folderFs, "file"); content != "current" {
		t.Fatalf("the file is now %q", content)
	}
}

func TestPlatformHistoryPaths(t *testing.T) {
	for name, want := range map[string]bool{
		".stversions/platform/887fd559.json.gz":      true,
		".stversions/platform/887fd559.pending.json": true,
		"platform/887fd559.json.gz":                  false,
		"docs/.stversions/platform/a.json.gz":        false,
		".stversions/platform/deeper/a.json.gz":      false,
		".stversions/platform/notes.txt":             false,
		"file.txt":                                   false,
	} {
		if got := IsPlatformHistoryPath(filepath.FromSlash(name)); got != want {
			t.Errorf("%s: got %v", name, got)
		}
	}
}
