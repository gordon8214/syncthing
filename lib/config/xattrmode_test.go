// Copyright (C) 2026 The Syncthing Authors.
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this file,
// You can obtain one at https://mozilla.org/MPL/2.0/.

package config

import (
	"encoding/json"
	"encoding/xml"
	"testing"

	"github.com/syncthing/syncthing/lib/build"
)

func TestMacOSXattrMode(t *testing.T) {
	for _, mode := range []MacOSXattrMode{MacOSXattrSyncable, MacOSXattrAll} {
		want := XattrFilter{MacOSMode: mode}
		for _, format := range []string{"json", "xml"} {
			t.Run(format, func(t *testing.T) {
				var data []byte
				var err error
				var got XattrFilter
				if format == "json" {
					data, err = json.Marshal(want)
					if err == nil {
						err = json.Unmarshal(data, &got)
					}
				} else {
					data, err = xml.Marshal(want)
					if err == nil {
						err = xml.Unmarshal(data, &got)
					}
				}
				if err != nil || got.MacOSMode != mode {
					t.Fatalf("round trip %s: %+v %v", data, got, err)
				}
			})
		}
	}
	var got XattrFilter
	if err := json.Unmarshal([]byte(`{}`), &got); err != nil {
		t.Fatal(err)
	}
	if got.MacOSMode != MacOSXattrSyncable {
		t.Fatal("old configuration did not select syncable")
	}
	if got.Permit("com.example.unmarked") == build.IsDarwin {
		t.Fatal("incorrect native default")
	}
	if !got.Permit("com.apple.ResourceFork") {
		t.Fatal("resource forks excluded")
	}
	if err := json.Unmarshal([]byte(`{"macOSMode":"unknown"}`), &got); err == nil {
		t.Fatal("accepted unknown mode")
	}
	if err := xml.Unmarshal([]byte(`<XattrFilter><macOSMode>unknown</macOSMode></XattrFilter>`), &got); err == nil {
		t.Fatal("accepted unknown XML mode")
	}
	if err := json.Unmarshal([]byte(`{"macOSMode":1}`), &got); err == nil {
		t.Fatal("accepted numeric mode")
	}
	exception := XattrFilter{Entries: []XattrFilterEntry{{Match: "com.example.unmarked", Permit: true}}}
	if !exception.Permit("com.example.unmarked") || exception.Permit("com.apple.ResourceFork") {
		t.Fatal("explicit rules no longer use default deny")
	}
}
