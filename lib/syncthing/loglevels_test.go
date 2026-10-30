// Copyright (C) 2026 The Syncthing Authors.
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this file,
// You can obtain one at https://mozilla.org/MPL/2.0/.

package syncthing

import (
	"bytes"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/syncthing/syncthing/internal/slogutil"
)

func TestSetLogLevels(t *testing.T) {
	t.Cleanup(slogutil.SaveLevels())

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

func TestLogLevelsAndLines(t *testing.T) {
	t.Cleanup(slogutil.SaveLevels())
	buf := new(bytes.Buffer)
	prev := SetLogOutput(buf)
	t.Cleanup(func() { SetLogOutput(prev) })

	start := time.Now()
	SetLogLevel("syncthing", slog.LevelInfo)
	slog.Debug("A hidden line")
	SetLogLevel("syncthing", slog.LevelDebug)
	if LogLevels()["syncthing"] != slog.LevelDebug {
		t.Fatal("the level did not change")
	}
	slog.Debug("A shown line")
	SetLogOutput(prev)

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

func TestLogWarnings(t *testing.T) {
	t.Cleanup(slogutil.SaveLevels())
	prev := SetLogOutput(io.Discard)
	t.Cleanup(func() { SetLogOutput(prev) })

	start := time.Now()
	SetLogLevel("syncthing", slog.LevelDebug)
	slog.Warn("A warning before a flood")
	for range 1001 {
		slog.Debug("A line of the flood")
	}

	for _, line := range LogLines(start) {
		if strings.HasPrefix(line.Message, "A warning") {
			t.Fatal("the flood kept the warning in LogLines; it needs more lines")
		}
	}
	warnings := LogWarnings(start)
	if len(warnings) != 1 || !strings.HasPrefix(warnings[0].Message, "A warning before a flood") || warnings[0].Facility != "syncthing" {
		t.Fatalf("warnings %+v", warnings)
	}
}
