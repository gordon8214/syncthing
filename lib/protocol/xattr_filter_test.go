// Copyright (C) 2026 The Syncthing Authors.
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this file,
// You can obtain one at https://mozilla.org/MPL/2.0/.

//go:build linux || darwin || freebsd || netbsd

package protocol

import "testing"

func TestNativeXattrComparisonFilter(t *testing.T) {
	filter := func(attrs []Xattr) []Xattr {
		var result []Xattr
		for _, attr := range attrs {
			if attr.Name == "shared" {
				result = append(result, attr)
			}
		}
		return result
	}
	a := FileInfo{Name: "file"}
	a.Platform.SetXattrs([]Xattr{{Name: "local", Value: []byte("old")}, {Name: "shared", Value: []byte("value")}})
	b := FileInfo{Name: "file"}
	b.Platform.SetXattrs([]Xattr{{Name: "shared", Value: []byte("value")}})
	if a.IsEquivalent(b, 0) {
		t.Fatal("raw comparison ignored excluded metadata")
	}
	if !a.IsEquivalentOptional(b, FileInfoComparison{FilterNativeXattrs: filter}) {
		t.Fatal("excluded native metadata prevented convergence")
	}
	b.Platform.Xattrs()[0].Value = []byte("changed")
	if a.IsEquivalentOptional(b, FileInfoComparison{FilterNativeXattrs: filter}) {
		t.Fatal("ignored permitted metadata change")
	}
	// Applying the filter to a foreign platform would lose relay/history data.
	x := &XattrData{Xattrs: []Xattr{{Name: "local"}}}
	if xattrsEqualFiltered(x, nil, filter, false) {
		t.Fatal("filtered foreign metadata")
	}
}
