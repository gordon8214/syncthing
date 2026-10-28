// Copyright (C) 2026 The Syncthing Authors.
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this file,
// You can obtain one at https://mozilla.org/MPL/2.0/.

package syncthing

import (
	"log/slog"

	"github.com/syncthing/syncthing/internal/slogutil"
)

// SetLogLevels sets the level of all packages' log messages, as the
// syncthing command does from STLOGLEVEL, then that of single packages from
// overrides, in the format of STTRACE: a comma separated list of package
// names, each optionally followed by a colon and a level, such as
// "model,protocol:WARN". A package named without a level logs at debug
// level, as does one with a level that doesn't parse, which is logged as a
// warning. Packages that overrides doesn't name keep any level set before.
//
// The levels apply to the log handler the engine installs as slog's default,
// which an application embedding the engine also logs through.
func SetLogLevels(level slog.Level, overrides string) {
	slogutil.SetDefaultLevel(level)
	slogutil.SetLevelOverrides(overrides)
}
