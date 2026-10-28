// Copyright (C) 2026 The Syncthing Authors.
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this file,
// You can obtain one at https://mozilla.org/MPL/2.0/.

//go:build darwin

package fs

import (
	"bytes"
	"errors"
	"fmt"
	"github.com/syncthing/syncthing/lib/protocol"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestResourceForkXattrAcrossBufferBoundary(t *testing.T) {
	for _, size := range []int{1, 512, 1023, 1024, 1025, 4096, 128 * 1024, 256 * 1024} {
		for _, name := range []string{"com.apple.ResourceFork", "user.test-large"} {
			t.Run(fmt.Sprintf("%s/%d", name, size), func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "file")
				if err := os.WriteFile(path, []byte("file contents"), 0o600); err != nil {
					t.Fatal(err)
				}
				want := make([]byte, size)
				for i := range want {
					want[i] = byte(i % 251)
				}
				if err := unix.Lsetxattr(path, name, want, 0); err != nil {
					t.Fatal(err)
				}
				got, err := getXattr(path, name)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(got, want) {
					t.Fatalf("xattr data differs: got %d bytes, want %d", len(got), len(want))
				}
			})
		}
	}
}

type resourceForkFilter struct{ limit int }

func (resourceForkFilter) Permit(name string) bool      { return name == "com.apple.ResourceFork" }
func (f resourceForkFilter) GetMaxSingleEntrySize() int { return f.limit }
func (resourceForkFilter) GetMaxTotalSize() int         { return 0 }
func TestResourceForkXattrShrink(t *testing.T) {
	for _, size := range []int{0, 512, 4096, 128 * 1024} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "file")
			body := []byte("data fork must survive")
			if err := os.WriteFile(path, body, 0o600); err != nil {
				t.Fatal(err)
			}
			f := NewFilesystem(FilesystemTypeBasic, root)
			old := bytes.Repeat([]byte{0xa5}, 256*1024)
			if err := unix.Lsetxattr(path, "com.apple.ResourceFork", old, 0); err != nil {
				t.Fatal(err)
			}
			want := bytes.Repeat([]byte{0x42}, size)
			if err := f.SetXattr("file", []protocol.Xattr{{Name: "com.apple.ResourceFork", Value: want}}, resourceForkFilter{}); err != nil {
				t.Fatal(err)
			}
			got, err := getXattr(path, "com.apple.ResourceFork")
			if size == 0 && errors.Is(err, unix.ENOATTR) {
				got = nil
				err = nil
			}
			if err != nil || !bytes.Equal(got, want) {
				t.Fatalf("shrink got %d bytes, want %d: %v", len(got), len(want), err)
			}
			got, err = os.ReadFile(path)
			if err != nil || !bytes.Equal(got, body) {
				t.Fatal("changed data fork", err)
			}
		})
	}
}

func TestResourceForkXattrShrinkFilteredOldValue(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "file")
	if err := os.WriteFile(path, []byte("body"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := unix.Lsetxattr(path, "com.apple.ResourceFork", bytes.Repeat([]byte{1}, 4096), 0); err != nil {
		t.Fatal(err)
	}
	want := bytes.Repeat([]byte{2}, 512)
	if err := NewFilesystem(FilesystemTypeBasic, root).SetXattr("file", []protocol.Xattr{{Name: "com.apple.ResourceFork", Value: want}}, resourceForkFilter{limit: 1024}); err != nil {
		t.Fatal(err)
	}
	got, err := getXattr(path, "com.apple.ResourceFork")
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("filtered old fork retained tail: %d %v", len(got), err)
	}
}
