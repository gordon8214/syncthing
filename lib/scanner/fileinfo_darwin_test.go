// Copyright (C) 2026 The Syncthing Authors.
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this file,
// You can obtain one at https://mozilla.org/MPL/2.0/.

package scanner

import (
	"bytes"
	"context"
	"testing"

	"golang.org/x/text/unicode/norm"

	"github.com/syncthing/syncthing/lib/fs"
	"github.com/syncthing/syncthing/lib/protocol"
)

// A file created with a precomposed (NFC) name, as other devices and APIs
// name it, is indexed under its decomposed (NFD) name on macOS, by a scan
// and by FileInfoFor alike.
func TestFileInfoForPrecomposedName(t *testing.T) {
	testFs := fs.NewFilesystem(fs.FilesystemTypeBasic, t.TempDir())
	nfc := norm.NFC.String("crème brûlée.txt")
	nfd := norm.NFD.String(nfc)
	if nfc == nfd {
		t.Fatal("test name has a single normalization form")
	}
	writeTestFile(t, testFs, nfc, 1000)
	cfg := Config{Filesystem: testFs, ShortID: protocol.LocalDeviceID.Short()}

	got, err := FileInfoFor(context.Background(), cfg, nfc, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != nfd {
		t.Fatalf("named %q, not the decomposed %q", got.Name, nfd)
	}

	walked := walkDir(testFs, ".", nil, nil, 0)
	if len(walked) != 1 {
		t.Fatalf("walk produced %v", walked)
	}
	if walked[0].Name != got.Name || !bytes.Equal(walked[0].BlocksHash, got.BlocksHash) {
		t.Fatalf("walk produced %q (%x), FileInfoFor %q (%x)", walked[0].Name, walked[0].BlocksHash, got.Name, got.BlocksHash)
	}
}
