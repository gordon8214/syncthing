// Copyright (C) 2026 The Syncthing Authors.
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this file,
// You can obtain one at https://mozilla.org/MPL/2.0/.

//go:build !darwin

package fs

// IsXattrSyncable leaves other platforms' native attribute policies unchanged.
func IsXattrSyncable(string) bool { return true }
