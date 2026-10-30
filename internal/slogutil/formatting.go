// Copyright (C) 2025 The Syncthing Authors.
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this file,
// You can obtain one at https://mozilla.org/MPL/2.0/.

package slogutil

import (
	"cmp"
	"context"
	"io"
	"log/slog"
	"path"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

type LineFormat struct {
	TimestampFormat string
	LevelString     bool
	LevelSyslog     bool
}

type formattingOptions struct {
	LineFormat

	outMut       sync.Mutex // guards out and LineFormat, and serializes writes to out
	out          io.Writer
	recs         []*lineRecorder
	timeOverride time.Time
}

type formattingHandler struct {
	attrs  []slog.Attr
	groups []string
	opts   *formattingOptions
}

func SetLineFormat(f LineFormat) {
	globalFormatter.outMut.Lock()
	globalFormatter.LineFormat = f
	globalFormatter.outMut.Unlock()
}

// SetOutput sets where log lines are written, instead of standard output,
// and returns the previous output; a nil w discards them. Lines are written
// one at a time with a lock held, so w need not be safe for concurrent use,
// but it must not log (which would deadlock), and a slow w holds up every
// goroutine that logs.
func SetOutput(w io.Writer) io.Writer {
	globalFormatter.outMut.Lock()
	defer globalFormatter.outMut.Unlock()
	prev := globalFormatter.out
	globalFormatter.out = w
	return prev
}

var _ slog.Handler = (*formattingHandler)(nil)

func (h *formattingHandler) Enabled(_ context.Context, level slog.Level) bool {
	// Handle drops records below the level of the package that logs them
	return level >= globalLevels.Min()
}

func (h *formattingHandler) Handle(_ context.Context, rec slog.Record) error {
	fr := runtime.CallersFrames([]uintptr{rec.PC})
	var logAttrs []any
	var pkgName, typeName string
	fram, _ := fr.Next()
	if fram.Function != "" {
		pkgName, typeName = funcNameToPkg(fram.Function)
	}
	// A record from an unknown package, such as one from the log package,
	// gets the default level
	lvl := globalLevels.Get(pkgName)
	if lvl > rec.Level {
		// Logging not enabled at the record's level
		return nil
	}
	if pkgName != "" {
		logAttrs = append(logAttrs, slog.String("pkg", pkgName))
		if lvl <= slog.LevelDebug {
			// We are debugging, add additional source line data
			if typeName != "" {
				logAttrs = append(logAttrs, slog.String("type", typeName))
			}
			logAttrs = append(logAttrs, slog.Group("src", slog.String("file", path.Base(fram.File)), slog.Int("line", fram.Line)))
		}
	}

	var prefix string
	if len(h.groups) > 0 {
		prefix = strings.Join(h.groups, ".") + "."
	}

	// Build the message string.
	var sb strings.Builder
	sb.WriteString(rec.Message)

	// Collect all the attributes, adding the handler prefix.
	attrs := make([]slog.Attr, 0, rec.NumAttrs()+len(h.attrs)+1)
	rec.Attrs(func(attr slog.Attr) bool {
		attr.Key = prefix + attr.Key
		attrs = append(attrs, attr)
		return true
	})
	attrs = append(attrs, h.attrs...)
	attrs = append(attrs, slog.Group("log", logAttrs...))

	// Expand and format attributes
	var attrCount int
	for _, attr := range attrs {
		for _, attr := range expandAttrs("", attr) {
			appendAttr(&sb, "", attr, &attrCount)
		}
	}
	if attrCount > 0 {
		sb.WriteRune(')')
	}

	line := Line{
		When:    cmp.Or(h.opts.timeOverride, rec.Time),
		Message: sb.String(),
		Level:   rec.Level,
		Package: pkgName,
	}

	// If there is a recorder, record the line.
	for _, rec := range h.opts.recs {
		rec.record(line)
	}

	// If there's an output, print the line.
	h.opts.outMut.Lock()
	if h.opts.out != nil {
		_, _ = line.WriteTo(h.opts.out, h.opts.LineFormat)
	}
	h.opts.outMut.Unlock()
	return nil
}

func expandAttrs(prefix string, a slog.Attr) []slog.Attr {
	if prefix != "" {
		a.Key = prefix + "." + a.Key
	}
	val := a.Value.Resolve()
	if val.Kind() != slog.KindGroup {
		return []slog.Attr{a}
	}
	var attrs []slog.Attr
	for _, attr := range val.Group() {
		attrs = append(attrs, expandAttrs(a.Key, attr)...)
	}
	return attrs
}

func appendAttr(sb *strings.Builder, prefix string, a slog.Attr, attrCount *int) {
	const confusables = ` "()[]{},`
	if a.Key == "" {
		return
	}
	sb.WriteRune(' ')
	if *attrCount == 0 {
		sb.WriteRune('(')
	}
	sb.WriteString(prefix)
	sb.WriteString(a.Key)
	sb.WriteRune('=')
	v := a.Value.Resolve().String()
	if v == "" || strings.ContainsAny(v, confusables) {
		v = strconv.Quote(v)
	}
	sb.WriteString(v)
	*attrCount++
}

func (h *formattingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if len(h.groups) > 0 {
		prefix := strings.Join(h.groups, ".") + "."
		for i := range attrs {
			attrs[i].Key = prefix + attrs[i].Key
		}
	}
	return &formattingHandler{
		attrs:  append(h.attrs, attrs...),
		groups: h.groups,
		opts:   h.opts,
	}
}

func (h *formattingHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	return &formattingHandler{
		attrs:  h.attrs,
		groups: append([]string{name}, h.groups...),
		opts:   h.opts,
	}
}

func funcNameToPkg(fn string) (string, string) {
	fn = strings.ToLower(fn)
	fn = strings.TrimPrefix(fn, "github.com/syncthing/syncthing/lib/")
	fn = strings.TrimPrefix(fn, "github.com/syncthing/syncthing/internal/")

	// The package path ends at the first dot after its last slash: other
	// modules' paths contain dots (t-shaped.nl/sushitrain/v2/src.Func), and
	// the linker writes a dot in the last element as %2e.
	dirLen := strings.LastIndex(fn, "/") + 1
	pkgTypFn := strings.Split(fn[dirLen:], ".") // [package, type, method] or [package, function]
	pkg := strings.ReplaceAll(fn[:dirLen+len(pkgTypFn[0])], "%2e", ".")
	if len(pkgTypFn) <= 2 {
		return pkg, ""
	}

	// Remove parenthesis and asterisk from the type name
	typ := strings.TrimLeft(strings.TrimRight(pkgTypFn[1], ")"), "(*")
	// Skip certain type names that add no value
	typ = strings.TrimSuffix(typ, "service")
	switch typ {
	case pkg, "", "serveparams":
		return pkg, ""
	default:
		return pkg, typ
	}
}
