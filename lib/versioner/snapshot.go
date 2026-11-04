// Copyright (C) 2026 The Syncthing Authors.
// SPDX-License-Identifier: MPL-2.0

package versioner

import (
	"errors"
	"path/filepath"
	"time"

	"github.com/syncthing/syncthing/lib/fs"
	"github.com/syncthing/syncthing/lib/protocol"
)

// ErrVersionMetadataMissing distinguishes an incomplete archive from an explicitly
// captured empty attribute set. It must never authorize a content-only restore.
var ErrVersionMetadataMissing = errors.New("version has no captured Mac metadata")

// VersionSnapshotter opens content and its unfiltered, archived metadata together.
// The caller owns the file. The metadata is detached from the versioner's storage.
type VersionSnapshotter interface {
	OpenVersionSnapshot(string, time.Time) (fs.File, protocol.PlatformData, error)
}

func AsVersionSnapshotter(v Versioner) (VersionSnapshotter, bool) {
	if wrapped, ok := v.(*versionerWithErrorContext); ok {
		v = wrapped.Versioner
	}
	s, ok := v.(VersionSnapshotter)
	return s, ok
}

func (v *staggered) OpenVersionSnapshot(name string, stamp time.Time) (fs.File, protocol.PlatformData, error) {
	v.platformMut.Lock()
	defer v.platformMut.Unlock()
	var empty protocol.PlatformData
	if !v.preservePlatformData {
		return nil, empty, ErrVersionMetadataMissing
	}
	file, err := openFileVersion(v.versionsFs, name, stamp, false)
	if err != nil {
		return nil, empty, err
	}
	// File.Name is relative to the filesystem, including through its wrappers.
	path := file.Name()
	if filepath.IsAbs(path) {
		path, err = filepath.Rel(v.versionsFs.URI(), path)
	}
	if err != nil {
		file.Close()
		return nil, empty, err
	}
	ref, found, err := readPlatformRef(v.versionsFs, path)
	if err == nil && !found {
		err = ErrVersionMetadataMissing
	}
	var metadata protocol.PlatformData
	if err == nil {
		metadata, err = v.loadPlatformData(ref)
	}
	if err == nil && metadata.Darwin == nil {
		err = ErrVersionMetadataMissing
	}
	if err != nil {
		file.Close()
		return nil, empty, err
	}
	return file, metadata, nil
}
