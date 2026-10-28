// Copyright (C) 2026 The Syncthing Authors.
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this file,
// You can obtain one at https://mozilla.org/MPL/2.0/.

package fs

import (
	"sync"

	"github.com/ebitengine/purego"
)

var preserveXattrForIntent = sync.OnceValue(func() func(string, uint32) int32 {
	var preserve func(string, uint32) int32
	// This public libc API is available on every supported macOS/iOS release.
	// Resolving it once also supports standalone macOS builds without CGO.
	purego.RegisterLibFunc(&preserve, purego.RTLD_DEFAULT, "xattr_preserve_for_intent")
	return preserve
})

// IsXattrSyncable includes both encoded flags and the OS's legacy-name defaults.
func IsXattrSyncable(name string) bool {
	const syncIntent = 4 // XATTR_OPERATION_INTENT_SYNC in <xattr_flags.h>
	return preserveXattrForIntent()(name, syncIntent) != 0
}
