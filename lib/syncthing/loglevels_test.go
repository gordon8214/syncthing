// Copyright (C) 2026 The Syncthing Authors.
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this file,
// You can obtain one at https://mozilla.org/MPL/2.0/.

package syncthing

import (
	"log/slog"
	"testing"

	"github.com/syncthing/syncthing/internal/slogutil"
)

func TestSetLogLevels(t *testing.T) {
	before := slogutil.PackageLevels()
	t.Cleanup(func() {
		slogutil.SetDefaultLevel(slog.LevelInfo)
		for pkg, level := range before {
			slogutil.SetPackageLevel(pkg, level)
		}
	})

	SetLogLevels(slog.LevelWarn, " model:ERROR, protocol,,scanner:nonsense")
	want := map[string]slog.Level{
		"model":     slog.LevelError, // given a level
		"protocol":  slog.LevelDebug, // named alone
		"scanner":   slog.LevelDebug, // a level that doesn't parse
		"versioner": slog.LevelWarn,  // the default
	}
	levels := slogutil.PackageLevels()
	for pkg, level := range want {
		if got, ok := levels[pkg]; !ok || got != level {
			t.Errorf("%s logs at %v (known %v), expected %v", pkg, got, ok, level)
		}
	}

	// A later call changes the default, and keeps the overrides it doesn't
	// name.
	SetLogLevels(slog.LevelInfo, "")
	levels = slogutil.PackageLevels()
	if levels["versioner"] != slog.LevelInfo || levels["model"] != slog.LevelError {
		t.Errorf("after resetting the default: %v", levels)
	}
}
