// Copyright (C) 2026 The Syncthing Authors.
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this file,
// You can obtain one at https://mozilla.org/MPL/2.0/.

package config

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

func TestFolderCopyXattrFilter(t *testing.T) {
	for _, entries := range [][]XattrFilterEntry{
		nil,
		{},
		{{Match: "com.example.default", Permit: true}, {Match: "*", Permit: false}},
	} {
		t.Run(fmt.Sprintf("entries=%v", entries), func(t *testing.T) {
			original := FolderConfiguration{XattrFilter: XattrFilter{
				MacOSMode:          MacOSXattrAll,
				Entries:            entries,
				MaxSingleEntrySize: 2048,
				MaxTotalSize:       8192,
			}}
			copied := original.Copy()
			if !slices.Equal(copied.XattrFilter.Entries, entries) ||
				(copied.XattrFilter.Entries == nil) != (entries == nil) ||
				copied.XattrFilter.MacOSMode != original.XattrFilter.MacOSMode ||
				copied.XattrFilter.MaxSingleEntrySize != original.XattrFilter.MaxSingleEntrySize ||
				copied.XattrFilter.MaxTotalSize != original.XattrFilter.MaxTotalSize {
				t.Fatalf("copy changed filter: got %+v, want %+v", copied.XattrFilter, original.XattrFilter)
			}
			if len(entries) == 0 {
				return
			}
			copied.XattrFilter.Entries[0] = XattrFilterEntry{Match: "com.example.copy", Permit: false}
			if got := original.XattrFilter.Entries[0]; got != (XattrFilterEntry{Match: "com.example.default", Permit: true}) {
				t.Fatalf("editing copy changed original: %+v", got)
			}
			original.XattrFilter.Entries[1].Match = "com.example.original"
			if got := copied.XattrFilter.Entries[1].Match; got != "*" {
				t.Fatalf("editing original changed copy: %q", got)
			}
		})
	}
}

func TestReadJSONXattrFilters(t *testing.T) {
	data := fmt.Sprintf(`{
		"version": %d,
		"defaults": {"folder": {"xattrFilter": {"entries": [
			{"match": "com.example.default", "permit": true}
		]}}},
		"folders": [
			{"id": "first", "path": "first", "xattrFilter": {"entries": [{"match": "com.example.first", "permit": false}]}},
			{"id": "second", "path": "second", "xattrFilter": {"entries": [{"match": "com.example.second", "permit": true}]}},
			{"id": "inherited", "path": "inherited"},
			{"id": "inherited-again", "path": "inherited-again"},
			{"id": "empty", "path": "empty", "xattrFilter": {"entries": []}}
		]
	}`, CurrentVersion)
	cfg, err := ReadJSON(strings.NewReader(data), device1)
	if err != nil {
		t.Fatal(err)
	}

	filters := []XattrFilter{cfg.Defaults.Folder.XattrFilter}
	for _, id := range []string{"first", "second", "inherited", "inherited-again", "empty"} {
		folder, _, ok := cfg.Folder(id)
		if !ok {
			t.Fatalf("missing folder %q", id)
		}
		filters = append(filters, folder.XattrFilter)
	}
	want := [][]XattrFilterEntry{
		{{Match: "com.example.default", Permit: true}},
		{{Match: "com.example.first", Permit: false}},
		{{Match: "com.example.second", Permit: true}},
		{{Match: "com.example.default", Permit: true}},
		{{Match: "com.example.default", Permit: true}},
		{},
	}
	if len(filters) != len(want) {
		t.Fatalf("got %d filters, want %d", len(filters), len(want))
	}
	for i, filter := range filters {
		if !slices.Equal(filter.Entries, want[i]) {
			t.Fatalf("filter %d: got %+v, want %+v", i, filter.Entries, want[i])
		}
	}
	for i, filter := range filters {
		if len(filter.Entries) == 0 {
			continue
		}
		filter.Entries[0].Match = "changed"
		for j, other := range filters {
			if j > i && !slices.Equal(other.Entries, want[j]) {
				t.Fatalf("editing filter %d changed filter %d: %+v", i, j, other.Entries)
			}
		}
	}
}
