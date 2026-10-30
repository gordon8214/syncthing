// Copyright (C) 2025 The Syncthing Authors.
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this file,
// You can obtain one at https://mozilla.org/MPL/2.0/.

package slogutil

import (
	"bytes"
	"context"
	"log/slog"
	"maps"
	"strings"
	"testing"
	"time"
)

func TestFormattingHandler(t *testing.T) {
	buf := new(bytes.Buffer)
	h := &formattingHandler{
		opts: &formattingOptions{
			LineFormat:   DefaultLineFormat,
			out:          buf,
			timeOverride: time.Unix(1234567890, 0).In(time.UTC),
		},
	}

	l := slog.New(h).With("a", "a")
	l.Info("A basic info line", "attr1", "val with spaces", "attr2", 2, "attr3", `val"quote`)
	l.Info("A basic info line", "attr1", "paren)thesis")
	l.Info("An info line with an empty value", "attr1", "")
	l.Info("An info line with grouped values", "attr1", "val1", slog.Group("foo", "attr2", 2, slog.Group("bar", "attr3", "3")))

	l2 := l.WithGroup("foo")
	l2.Info("An info line with grouped values via logger", "attr1", "val1", "attr2", 2)

	l3 := l2.WithGroup("bar")
	l3.Info("An info line with nested grouped values via logger", "attr1", "val1", "attr2", 2)

	l3.Debug("A debug entry")
	l3.Warn("A warning entry")
	l3.Error("An error")

	exp := `
2009-02-13 23:31:30 INF A basic info line (attr1="val with spaces" attr2=2 attr3="val\"quote" a=a log.pkg=slogutil)
2009-02-13 23:31:30 INF A basic info line (attr1="paren)thesis" a=a log.pkg=slogutil)
2009-02-13 23:31:30 INF An info line with an empty value (attr1="" a=a log.pkg=slogutil)
2009-02-13 23:31:30 INF An info line with grouped values (attr1=val1 foo.attr2=2 foo.bar.attr3=3 a=a log.pkg=slogutil)
2009-02-13 23:31:30 INF An info line with grouped values via logger (foo.attr1=val1 foo.attr2=2 a=a log.pkg=slogutil)
2009-02-13 23:31:30 INF An info line with nested grouped values via logger (bar.foo.attr1=val1 bar.foo.attr2=2 a=a log.pkg=slogutil)
2009-02-13 23:31:30 WRN A warning entry (a=a log.pkg=slogutil)
2009-02-13 23:31:30 ERR An error (a=a log.pkg=slogutil)`

	if strings.TrimSpace(buf.String()) != strings.TrimSpace(exp) {
		t.Log(buf.String())
		t.Log(exp)
		t.Error("mismatch")
	}
}

func TestFuncNameToPkg(t *testing.T) {
	cases := []struct {
		fn, pkg, typ string
	}{
		{"github.com/syncthing/syncthing/lib/model.(*folder).Scan", "model", "folder"},
		{"github.com/syncthing/syncthing/lib/model.(*model).Serve.func1", "model", ""},
		{"github.com/syncthing/syncthing/lib/api.(*service).Serve", "api", ""},
		{"github.com/syncthing/syncthing/lib/relay/client.(*staticClient).Serve", "relay/client", "staticclient"},
		{"github.com/syncthing/syncthing/internal/db/sqlite.(*DB).Update", "db/sqlite", "db"},
		{"github.com/syncthing/syncthing/lib/fs.init.0", "fs", "init"},
		{"main.main", "main", ""},
		// Paths outside the module keep their dots
		{"t-shaped.nl/sushitrain/v2/src.(*Client).Start", "t-shaped.nl/sushitrain/v2/src", "client"},
		{"t-shaped.nl/sushitrain/v2/src.LogInfo", "t-shaped.nl/sushitrain/v2/src", ""},
		{"github.com/thejerf/suture/v4.(*Supervisor).Serve", "github.com/thejerf/suture/v4", "supervisor"},
		{"gopkg.in/yaml%2ev3.(*parser).parse", "gopkg.in/yaml.v3", "parser"},
	}
	for _, tc := range cases {
		if pkg, typ := funcNameToPkg(tc.fn); pkg != tc.pkg || typ != tc.typ {
			t.Errorf("%s: got %q, %q; expected %q, %q", tc.fn, pkg, typ, tc.pkg, tc.typ)
		}
	}
}

func TestFormattingHandlerRecordsPackage(t *testing.T) {
	rec := &lineRecorder{level: slog.LevelDebug}
	h := &formattingHandler{opts: &formattingOptions{recs: []*lineRecorder{rec}}}
	slog.New(h).Info("A line")
	lines := rec.Since(time.Time{})
	if len(lines) != 1 || lines[0].Package != "slogutil" {
		t.Fatalf("recorded %+v", lines)
	}
}

func TestFormattingHandlerLevels(t *testing.T) {
	t.Cleanup(SaveLevels())
	ctx := context.Background()
	rec := &lineRecorder{level: -1000}
	h := &formattingHandler{opts: &formattingOptions{recs: []*lineRecorder{rec}}}

	// Records below every package's level are dropped before Handle
	SetDefaultLevel(slog.LevelWarn)
	if h.Enabled(ctx, slog.LevelInfo) {
		t.Error("INFO is enabled below every level")
	}
	SetPackageLevel("model", slog.LevelDebug)
	if !h.Enabled(ctx, slog.LevelDebug) {
		t.Error("DEBUG is not enabled with a package at DEBUG")
	}

	// A record without a known caller, as from the log package, gets the
	// default level
	_ = h.Handle(ctx, slog.NewRecord(time.Now(), slog.LevelInfo, "An unknown info line", 0))
	_ = h.Handle(ctx, slog.NewRecord(time.Now(), slog.LevelWarn, "An unknown warning", 0))
	lines := rec.Since(time.Time{})
	if len(lines) != 1 || lines[0].Message != "An unknown warning" || lines[0].Package != "" {
		t.Fatalf("recorded %+v", lines)
	}
}

func TestSaveLevels(t *testing.T) {
	restore := SaveLevels()
	t.Cleanup(restore)
	before := PackageLevels()
	SetDefaultLevel(slog.LevelError)
	SetPackageLevel("model", slog.LevelDebug)
	restore()
	if after := PackageLevels(); !maps.Equal(after, before) {
		t.Errorf("restored %v, expected %v", after, before)
	}
	if globalLevels.Min() > slog.LevelInfo {
		t.Errorf("minimum level %v after restoring", globalLevels.Min())
	}
}
