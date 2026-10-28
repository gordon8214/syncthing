// Copyright (C) 2026 The Syncthing Authors.
//
// This Source Code Form is subject to the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this file,
// You can obtain one at https://mozilla.org/MPL/2.0/.

package versioner

import (
	"reflect"
	"testing"

	"github.com/syncthing/syncthing/lib/config"
	"github.com/syncthing/syncthing/lib/protocol"
)

func nativeHistoryPD() protocol.PlatformData {
	return protocol.PlatformData{
		Darwin: &protocol.XattrData{Xattrs: []protocol.Xattr{
			{Name: "com.example.private", Value: []byte("historical private")},
			{Name: "com.example.shared#S", Value: []byte("historical shared")},
		}},
		Linux: &protocol.XattrData{Xattrs: []protocol.Xattr{
			{Name: "user.foreign", Value: []byte("foreign history")},
		}},
	}
}

func TestPlatformHistoryNativeXattrPolicy(t *testing.T) {
	for _, tc := range []struct {
		name       string
		sync, send bool
		mode       config.MacOSXattrMode
		wantNative int
	}{
		{name: "syncable", sync: true, wantNative: 1},
		{name: "all", sync: true, mode: config.MacOSXattrAll, wantNative: 2},
		{name: "send-only", send: true, wantNative: 1},
		{name: "disabled", wantNative: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.FolderConfiguration{
				Path: t.TempDir(), FilesystemType: config.FilesystemTypeBasic,
				SyncXattrs: tc.sync, SendXattrs: tc.send,
				XattrFilter: config.XattrFilter{MacOSMode: tc.mode},
				Versioning:  config.VersioningConfiguration{Type: "staggered", Params: map[string]string{"preservePlatformData": "true"}},
			}
			v := newStaggered(cfg).(*staggered)
			raw := nativeHistoryPD()
			writeHistoryFile(t, v, "file", "historical body")
			if err := v.ArchiveWithPlatformData("file", raw, false); err != nil {
				t.Fatal(err)
			}
			ver := onlyVersion(t, v, "file")
			if err := v.RestoreWithPlatformData("file", ver.VersionTime, protocol.PlatformData{}); err != nil {
				t.Fatal(err)
			}
			got, pending, err := v.RestoredPlatformData("file")
			if err != nil || !pending || len(got.Darwin.Xattrs) != tc.wantNative {
				t.Fatalf("native restore policy: %+v %v %v", got, pending, err)
			}
			if !reflect.DeepEqual(got.Linux, raw.Linux) {
				t.Fatal("filtered foreign history")
			}
			ref, _, err := readPlatformRef(v.folderFs, "file")
			if err != nil {
				t.Fatal(err)
			}
			saved, err := v.loadPlatformData(ref)
			if err != nil || !reflect.DeepEqual(saved, raw) {
				t.Fatal("modified historical blob", err)
			}
			if tc.wantNative == 1 {
				if got.Darwin.Xattrs[0].Name != "com.example.shared#S" {
					t.Fatal("kept excluded attribute")
				}
				if err := v.CommitRestoredPlatformData("file", raw); err == nil {
					t.Fatal("acknowledged unfiltered publication")
				}
				if _, pending, err := v.RestoredPlatformData("file"); err != nil || !pending {
					t.Fatal("lost pending restore after rejected acknowledgment")
				}
			}
			if err := v.CommitRestoredPlatformData("file", got); err != nil {
				t.Fatal(err)
			}
			if _, pending, err := v.RestoredPlatformData("file"); err != nil || pending {
				t.Fatal("filtered restore not acknowledged", err)
			}
		})
	}
}

func TestPlatformHistoryCopiesXattrFilter(t *testing.T) {
	cfg := config.FolderConfiguration{Path: t.TempDir(), SyncXattrs: true,
		FilesystemType: config.FilesystemTypeBasic,
		XattrFilter:    config.XattrFilter{Entries: []config.XattrFilterEntry{{Match: "com.example.private", Permit: true}}},
	}
	v := newStaggered(cfg).(*staggered)
	cfg.XattrFilter.Entries[0].Permit = false
	got := v.filterRestoredPlatformData(nativeHistoryPD())
	if len(got.Darwin.Xattrs) != 1 || got.Darwin.Xattrs[0].Name != "com.example.private" {
		t.Fatal("constructor shared mutable filter entries")
	}
}
