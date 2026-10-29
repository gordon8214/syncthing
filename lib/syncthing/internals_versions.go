// Copyright (C) 2026 The Syncthing Authors.
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this file,
// You can obtain one at https://mozilla.org/MPL/2.0/.

package syncthing

import (
	"errors"
	"time"

	"github.com/syncthing/syncthing/lib/fs"
	"github.com/syncthing/syncthing/lib/model"
	"github.com/syncthing/syncthing/lib/versioner"
)

// FileVersion describes one archived version of a file.
type FileVersion = versioner.FileVersion

var (
	// ErrNoVersioner is returned for folders without versioning.
	ErrNoVersioner = model.ErrNoVersioner
	// ErrVersionNotFound is returned when the requested version does not
	// exist (any more).
	ErrVersionNotFound = versioner.ErrVersionNotFound
	// ErrVersionsNotSupported is returned for versioners that cannot look up
	// versions (external versioning).
	ErrVersionsNotSupported = versioner.ErrRestorationNotSupported
)

type versionedModel interface {
	FileVersions(folder, name string) ([]versioner.FileVersion, error)
	OpenFileVersion(folder, name string, versionTime time.Time) (fs.File, error)
}

var errNoFileVersions = errors.New("model does not support file versions")

func (m *Internals) versioned() (versionedModel, error) {
	v, ok := m.model.(versionedModel)
	if !ok {
		return nil, errNoFileVersions
	}
	return v, nil
}

// FileVersions returns the versions of a file the folder's versioner keeps,
// newest first.
func (m *Internals) FileVersions(folderID, name string) ([]FileVersion, error) {
	v, err := m.versioned()
	if err != nil {
		return nil, err
	}
	return v.FileVersions(folderID, name)
}

// OpenFileVersion opens the version of a file archived at versionTime (a
// FileVersion's VersionTime), or returns ErrVersionNotFound. It may be called
// from within RunInFolderLoop.
func (m *Internals) OpenFileVersion(folderID, name string, versionTime time.Time) (fs.File, error) {
	v, err := m.versioned()
	if err != nil {
		return nil, err
	}
	return v.OpenFileVersion(folderID, name, versionTime)
}
