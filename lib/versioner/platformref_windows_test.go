// Copyright (C) 2026 The Syncthing Authors.
// SPDX-License-Identifier: MPL-2.0
package versioner

import (
	"golang.org/x/sys/windows"
	"strings"
	"testing"
)

func TestPlatformHistoryExtendedPaths(t *testing.T) {
	for path, expected := range map[string]string{
		`C:\folder\file`:        `\\?\C:\folder\file`,
		`\\server\share\file`:   `\\?\UNC\server\share\file`,
		`UNC\server\share\file`: `\\?\UNC\server\share\file`,
		`\\?\C:\folder\file`:    `\\?\C:\folder\file`,
	} {
		if actual := extendedPlatformPath(path); actual != expected {
			t.Errorf("%q: %q != %q", path, actual, expected)
		}
	}
}

func TestPlatformHistoryFailedReferenceReplacement(t *testing.T) {
	v := historyVersioner(t)
	writeHistoryFile(t, v, "file", "body")
	ref := platformRef{ID: strings.Repeat("a", 32)}
	if err := writePlatformRef(v.folderFs, "file", ref); err != nil {
		t.Fatal(err)
	}

	if err := writePlatformRefData(v.folderFs, "file", make([]byte, platformRefLimit+1)); err == nil {
		t.Fatal("accepted oversized reference")
	}
	handle, err := openPlatformRef(v.folderFs, "file", true)
	if err != nil {
		t.Fatal(err)
	}
	// A malformed native update must fail without changing the existing EA.
	var iosb windows.IO_STATUS_BLOCK
	malformed := []byte{0, 0, 0, 0, 0, 255, 255, 255}
	err = windows.NtSetEaFile(handle, &iosb, &malformed[0], uint32(len(malformed)))
	windows.CloseHandle(handle)
	if err == nil {
		t.Fatal("accepted malformed native reference update")
	}

	if got, ok, err := readPlatformRef(v.folderFs, "file"); err != nil || !ok || got != ref {
		t.Fatalf("valid reference changed: %+v %v %v", got, ok, err)
	}
}

func TestPlatformHistoryReferenceReplacement(t *testing.T) {
	v := historyVersioner(t)
	writeHistoryFile(t, v, "file", "body")
	for _, restore := range []bool{false, true, false} {
		ref := platformRef{ID: strings.Repeat("a", 32), Restore: restore}
		if err := writePlatformRef(v.folderFs, "file", ref); err != nil {
			t.Fatal(err)
		}
		if got, ok, err := readPlatformRef(v.folderFs, "file"); err != nil || !ok || got != ref {
			t.Fatalf("replacement: %+v %v %v", got, ok, err)
		}
	}
}
