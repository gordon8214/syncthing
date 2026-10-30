// Copyright (C) 2026 The Syncthing Authors.
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this file,
// You can obtain one at https://mozilla.org/MPL/2.0/.

package syncthing

import (
	"io"
	"log/slog"
	"time"

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

// LogFacilities returns the description of each log facility, by name. A
// facility is a package whose level can be set on its own: the engine's
// packages that log, and those that RegisterLogFacility added.
func LogFacilities() map[string]string {
	return slogutil.PackageDescrs()
}

// LogLevels returns the level of each log facility, by name.
func LogLevels() map[string]slog.Level {
	return slogutil.PackageLevels()
}

// SetLogLevel sets the level of one package's log messages, as the web GUI
// does. It takes effect at once.
func SetLogLevel(facility string, level slog.Level) {
	slogutil.SetPackageLevel(facility, level)
}

// RegisterLogFacility adds the package that calls it to the log facilities,
// with a description, so that an application embedding the engine can list
// its own package with the engine's. A package's name is its import path in
// lower case, less the module path for the engine's own packages.
func RegisterLogFacility(descr string) {
	slogutil.RegisterCallerPackage(descr, 1)
}

// SetLogOutput sets where the log handler writes formatted lines, instead
// of standard output. The handler writes one line at a time, so w need not
// be safe for concurrent use.
func SetLogOutput(w io.Writer) {
	slogutil.SetOutput(w)
}

// A LogLine is a message the log handler logged.
type LogLine struct {
	When     time.Time
	Level    slog.Level
	Facility string // the package that logged it, if known
	Message  string // including its attributes
}

// LogLines returns the lines logged after since, oldest first. The handler
// keeps the last thousand lines, of every level that was enabled for the
// package logging it at the time, as the web GUI's log shows them.
func LogLines(since time.Time) []LogLine {
	recorded := slogutil.GlobalRecorder.Since(since)
	lines := make([]LogLine, len(recorded))
	for i, line := range recorded {
		lines[i] = LogLine{
			When:     line.When,
			Level:    line.Level,
			Facility: line.Package,
			Message:  line.Message,
		}
	}
	return lines
}
