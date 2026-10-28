// Copyright (C) 2026 The Syncthing Authors.
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this file,
// You can obtain one at https://mozilla.org/MPL/2.0/.

package fs

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/syncthing/syncthing/lib/protocol"
	"golang.org/x/sys/unix"
)

type policyTestFilter struct {
	all           bool
	single, total int
}

func (f policyTestFilter) Permit(name string) bool    { return f.all || IsXattrSyncable(name) }
func (f policyTestFilter) GetMaxSingleEntrySize() int { return f.single }
func (f policyTestFilter) GetMaxTotalSize() int       { return f.total }

func TestXattrNativeSyncPolicy(t *testing.T) {
	for _, name := range []string{"com.apple.FinderInfo", "com.apple.ResourceFork", "com.apple.metadata:_kMDItemUserTags", "com.apple.metadata:kMDItemFinderComment", "com.apple.TextEncoding", "com.apple.quarantine", "com.example.test#S"} {
		if !IsXattrSyncable(name) {
			t.Errorf("excluded %s", name)
		}
	}
	for _, name := range []string{"com.example.unmarked", "com.apple.provenance", "com.apple.macl", "com.apple.decmpfs", "com.example.test#SN", "com.example.test#SB", "com.example.test#Ss"} {
		if IsXattrSyncable(name) {
			t.Errorf("included %s", name)
		}
	}
}

func TestXattrReceivePolicy(t *testing.T) {
	for _, directory := range []bool{false, true} {
		root := t.TempDir()
		path := filepath.Join(root, "item")
		if directory {
			if err := os.Mkdir(path, 0700); err != nil {
				t.Fatal(err)
			}
		} else if err := os.WriteFile(path, []byte("body"), 0600); err != nil {
			t.Fatal(err)
		}
		ffs := NewFilesystem(FilesystemTypeBasic, root)
		all := policyTestFilter{all: true}
		initial := []protocol.Xattr{{Name: "com.example.private", Value: []byte("local")}, {Name: "com.example.shared#S", Value: []byte("old")}, {Name: "com.example.deleted#S", Value: []byte("delete")}}
		if err := ffs.SetXattr("item", initial, all); err != nil {
			t.Fatal(err)
		}
		if err := ffs.Chmod("item", 0400); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = ffs.Chmod("item", 0700) })
		incoming := []protocol.Xattr{{Name: "com.example.private", Value: []byte("remote")}, {Name: "com.example.shared#S", Value: []byte("new")}, {Name: "com.example.injected", Value: []byte("remote")}}
		if err := WithOwnerWrite(ffs, "item", func() error { return ffs.SetXattr("item", incoming, policyTestFilter{}) }); err != nil {
			t.Fatal(err)
		}
		for name, want := range map[string]string{"com.example.private": "local", "com.example.shared#S": "new"} {
			got, err := getXattr(path, name)
			if err != nil || string(got) != want {
				t.Fatalf("%s: %q %v", name, got, err)
			}
		}
		for _, name := range []string{"com.example.deleted#S", "com.example.injected"} {
			if _, err := getXattr(path, name); !os.IsNotExist(err) && !isMissingTestXattr(err) {
				t.Fatalf("attribute %s exists: %v", name, err)
			}
		}
		info, err := ffs.Lstat("item")
		if err != nil || info.Mode()&0777 != 0400 {
			t.Fatalf("permissions changed: %v %v", info, err)
		}
	}
}
func isMissingTestXattr(err error) bool { return errors.Is(err, unix.ENOATTR) }

func TestXattrFilterLimits(t *testing.T) {
	attrs := []protocol.Xattr{{Name: "z#S", Value: []byte("last")}, {Name: "a#S", Value: bytes.Repeat([]byte{1}, 100)}, {Name: "b#S", Value: []byte("ok")}, {Name: "private", Value: []byte("local")}}
	got := FilterXattrs(attrs, policyTestFilter{single: 20, total: 12})
	if len(got) != 2 || got[0].Name != "b#S" || got[1].Name != "z#S" {
		t.Fatalf("limits counted rejected entries: %v", got)
	}
	for _, discarded := range got[len(got):cap(got)] {
		if discarded.Value != nil {
			t.Fatal("retained rejected value in backing array")
		}
	}
	if attrs[0].Name != "z#S" {
		t.Fatal("mutated caller's metadata")
	}
}
