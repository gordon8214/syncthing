// Copyright (C) 2026 The Syncthing Authors.
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this file,
// You can obtain one at https://mozilla.org/MPL/2.0/.

package config

import "fmt"

// MacOSXattrMode selects the default policy when no explicit filter rules exist.
// The zero value intentionally applies Apple's sync policy to old configurations.
type MacOSXattrMode int

const (
	MacOSXattrSyncable MacOSXattrMode = iota
	MacOSXattrAll
)

func (m MacOSXattrMode) MarshalText() ([]byte, error) {
	switch m {
	case MacOSXattrSyncable:
		return []byte("syncable"), nil
	case MacOSXattrAll:
		return []byte("all"), nil
	default:
		return nil, fmt.Errorf("invalid macOS extended attribute mode: %d", m)
	}
}

func (m *MacOSXattrMode) UnmarshalText(text []byte) error {
	switch string(text) {
	case "", "syncable":
		*m = MacOSXattrSyncable
	case "all":
		*m = MacOSXattrAll
	default:
		return fmt.Errorf("invalid macOS extended attribute mode: %q", text)
	}
	return nil
}
