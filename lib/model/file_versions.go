// Copyright (C) 2026 The Syncthing Authors.
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this file,
// You can obtain one at https://mozilla.org/MPL/2.0/.

package model

import (
	"time"

	"github.com/syncthing/syncthing/lib/fs"
	"github.com/syncthing/syncthing/lib/versioner"
)

// ErrNoVersioner is returned for folders without versioning.
var ErrNoVersioner = errNoVersioner

// fileVersioner returns the folder's versioner, when it can look up the
// versions of one file.
func (m *model) fileVersioner(folder string) (versioner.FileVersioner, error) {
	m.mut.RLock()
	err := m.checkFolderRunningRLocked(folder)
	ver := m.folderVersioners[folder]
	m.mut.RUnlock()
	if err != nil {
		return nil, err
	}
	if ver == nil {
		return nil, ErrNoVersioner
	}
	fv, ok := versioner.AsFileVersioner(ver)
	if !ok {
		return nil, versioner.ErrRestorationNotSupported
	}
	return fv, nil
}

// FileVersions returns the versions of a file in the folder, newest first.
// It does not wait for the folder's serve loop.
func (m *model) FileVersions(folder, name string) ([]versioner.FileVersion, error) {
	fv, err := m.fileVersioner(folder)
	if err != nil {
		return nil, err
	}
	return fv.FileVersions(name)
}

// OpenFileVersion opens the version of a file in the folder archived at
// versionTime, or returns versioner.ErrVersionNotFound. It does not wait for
// the folder's serve loop.
func (m *model) OpenFileVersion(folder, name string, versionTime time.Time) (fs.File, error) {
	fv, err := m.fileVersioner(folder)
	if err != nil {
		return nil, err
	}
	return fv.OpenVersion(name, versionTime)
}
