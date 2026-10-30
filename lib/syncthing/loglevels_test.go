// Copyright (C) 2026 The Syncthing Authors.
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this file,
// You can obtain one at https://mozilla.org/MPL/2.0/.

package syncthing

import (
	"bytes"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

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

func TestLogFacilitiesAndLines(t *testing.T) {
	before := LogLevels()
	descr := LogFacilities()["syncthing"]
	buf := new(bytes.Buffer)
	SetLogOutput(buf)
	t.Cleanup(func() {
		SetLogOutput(os.Stdout)
		for pkg, level := range before {
			SetLogLevel(pkg, level)
		}
		slogutil.RegisterPackage(descr)
	})

	// This test's package registers itself under its own name, replacing
	// the description the engine gave it
	RegisterLogFacility("Test facility")
	if got := LogFacilities()["syncthing"]; got != "Test facility" {
		t.Fatalf("description %q", got)
	}

	start := time.Now()
	SetLogLevel("syncthing", slog.LevelInfo)
	slog.Debug("A hidden line")
	SetLogLevel("syncthing", slog.LevelDebug)
	if LogLevels()["syncthing"] != slog.LevelDebug {
		t.Fatal("the level did not change")
	}
	slog.Debug("A shown line")

	var shown []LogLine
	for _, line := range LogLines(start) {
		if line.Facility == "syncthing" && strings.HasPrefix(line.Message, "A ") {
			shown = append(shown, line)
		}
	}
	if len(shown) != 1 || !strings.HasPrefix(shown[0].Message, "A shown line") || shown[0].Level != slog.LevelDebug {
		t.Fatalf("lines %+v", shown)
	}
	if out := buf.String(); !strings.Contains(out, "A shown line") || strings.Contains(out, "A hidden line") {
		t.Fatalf("output %q", out)
	}
}
