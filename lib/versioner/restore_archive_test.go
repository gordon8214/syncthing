// Copyright (C) 2026 Gordon
// SPDX-License-Identifier: MPL-2.0

package versioner

import (
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
	"testing/synctest"
	"time"

	"github.com/syncthing/syncthing/lib/config"
)

func TestStaggeredRestoreArchiveRetention(t *testing.T) {
	for _, platform := range []bool{false, true} {
		t.Run("platform="+strconv.FormatBool(platform), func(t *testing.T) {
			for _, restore := range []bool{false, true} {
				t.Run("restore="+strconv.FormatBool(restore), func(t *testing.T) {
					cfg := config.FolderConfiguration{
						Path: t.TempDir(), FilesystemType: config.FilesystemTypeBasic,
						Versioning: config.VersioningConfiguration{Type: "staggered", Params: map[string]string{
							"preservePlatformData": strconv.FormatBool(platform),
						}},
					}
					regular, err := New(cfg)
					if err != nil {
						t.Fatal(err)
					}
					synctest.Test(t, func(t *testing.T) {
						archive := func(v Versioner, value byte) {
							t.Helper()
							if pv, ok := WithPlatformData(v); ok {
								err = pv.ArchiveWithPlatformData("file.txt", historyPD(value), false)
							} else {
								err = v.Archive("file.txt")
							}
							if err != nil {
								t.Fatal(err)
							}
						}
						write := func(contents string) {
							t.Helper()
							if err := os.WriteFile(filepath.Join(cfg.Path, "file.txt"), []byte(contents), 0o640); err != nil {
								t.Fatal(err)
							}
						}
						time.Sleep(time.Second - time.Nanosecond)
						selectedTime := time.Now().Truncate(time.Second)
						write("selected")
						archive(regular, 1)
						write("current")
						time.Sleep(time.Nanosecond)
						currentTime := time.Now().Truncate(time.Second)
						archiver := regular
						if restore {
							archiver, err = NewForRestore(cfg)
							if err != nil {
								t.Fatal(err)
							}
						}
						archive(archiver, 2)
						if _, err := os.Stat(filepath.Join(cfg.Path, "file.txt")); !os.IsNotExist(err) {
							t.Fatal("archiving did not displace the live file", err)
						}
						selectedPath := filepath.Join(cfg.Path, DefaultPath, TagFilename("file.txt", selectedTime.Format(TimeFormat)))
						if data, err := os.ReadFile(selectedPath); err != nil || string(data) != "selected" {
							t.Fatalf("selected archive: %q, %v", data, err)
						}
						currentPath := filepath.Join(cfg.Path, DefaultPath, TagFilename("file.txt", currentTime.Format(TimeFormat)))
						if restore {
							if data, err := os.ReadFile(currentPath); err != nil || string(data) != "current" {
								t.Fatalf("displaced live archive: %q, %v", data, err)
							}
							if platform {
								snapshotter, ok := AsVersionSnapshotter(regular)
								if !ok {
									t.Fatal("missing snapshot support")
								}
								file, metadata, err := snapshotter.OpenVersionSnapshot("file.txt", currentTime)
								if err != nil {
									t.Fatal(err)
								}
								file.Close()
								if !reflect.DeepEqual(metadata, historyPD(2)) {
									t.Fatal("displaced live metadata was not preserved")
								}
							}
							// Skipping immediate pruning does not change scheduled retention,
							// even when Clean is called on the private restore archiver itself.
							if err := archiver.Clean(t.Context()); err != nil {
								t.Fatal(err)
							}
						}
						if _, err := os.Stat(currentPath); !os.IsNotExist(err) {
							t.Fatal("normal staggered retention changed", err)
						}
					})
				})
			}
		})
	}
}

func TestNewForRestoreRejectsInvalidVersioning(t *testing.T) {
	if _, err := NewForRestore(config.FolderConfiguration{}); err == nil {
		t.Fatal("accepted an unknown versioner")
	}
}
