// Copyright (C) 2026 The Syncthing Authors.
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this file,
// You can obtain one at https://mozilla.org/MPL/2.0/.

package syncthing

import (
	"context"
	"errors"

	"github.com/syncthing/syncthing/lib/model"
	"github.com/syncthing/syncthing/lib/protocol"
)

// FolderHandle is the access RunInFolderLoop gives to a folder.
type FolderHandle = model.FolderHandle

type coordinatedModel interface {
	RunInFolderLoop(folder string, fn func(context.Context, model.FolderHandle) error) error
	LocalIndexID(folder string) (protocol.IndexID, error)
	LocalSequence(folder string) (int64, error)
}

var errNotCoordinated = errors.New("model does not support coordinated changes")

func (m *Internals) coordinated() (coordinatedModel, error) {
	c, ok := m.model.(coordinatedModel)
	if !ok {
		return nil, errNotCoordinated
	}
	return c, nil
}

// RunInFolderLoop runs fn in the folder's serve loop, where it cannot overlap
// a scan or a pull of the folder, and returns its error.
func (m *Internals) RunInFolderLoop(folderID string, fn func(context.Context, FolderHandle) error) error {
	c, err := m.coordinated()
	if err != nil {
		return err
	}
	return c.RunInFolderLoop(folderID, fn)
}

// LocalIndexID returns the ID of this device's index of the folder, which
// changes when the index is reset.
func (m *Internals) LocalIndexID(folderID string) (protocol.IndexID, error) {
	c, err := m.coordinated()
	if err != nil {
		return 0, err
	}
	return c.LocalIndexID(folderID)
}

// LocalSequence returns the sequence of this device's latest change to the
// folder's index.
func (m *Internals) LocalSequence(folderID string) (int64, error) {
	c, err := m.coordinated()
	if err != nil {
		return 0, err
	}
	return c.LocalSequence(folderID)
}

// LocalFileInfo returns this device's index entry for path.
func (m *Internals) LocalFileInfo(folderID, path string) (protocol.FileInfo, bool, error) {
	return m.model.CurrentFolderFile(folderID, path)
}
