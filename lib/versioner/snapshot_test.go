// Copyright (C) 2026 The Syncthing Authors.
// SPDX-License-Identifier: MPL-2.0

package versioner

import (
	"errors"
	"io"
	"reflect"
	"testing"

	"github.com/syncthing/syncthing/lib/protocol"
)

func TestVersionSnapshotMetadata(t *testing.T) {
	for _, pd := range []protocol.PlatformData{historyPD(7), {Darwin: &protocol.XattrData{}}, {}} {
		v := historyVersioner(t)
		writeHistoryFile(t, v, "file", "archived")
		if err := v.ArchiveWithPlatformData("file", pd, false); err != nil {
			t.Fatal(err)
		}
		stamp := onlyVersion(t, v, "file").VersionTime
		file, got, err := v.OpenVersionSnapshot("file", stamp)
		if pd.Darwin == nil {
			if !errors.Is(err, ErrVersionMetadataMissing) {
				t.Fatalf("missing metadata: %v", err)
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		content, err := io.ReadAll(file)
		file.Close()
		if err != nil || string(content) != "archived" || !reflect.DeepEqual(got, pd) {
			t.Fatalf("snapshot: %q %#v %v", content, got, err)
		}
		if _, err := v.folderFs.Lstat("file"); err == nil {
			t.Fatal("snapshot restored live file")
		}
	}
}
