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
	RunInFolderLoop(ctx context.Context, folder string, fn func(context.Context, model.FolderHandle) error) error
	LocalIndexID(folder string) (protocol.IndexID, error)
	LocalSequence(folder string) (int64, error)
	DeviceFile(folder string, device protocol.DeviceID, file string) (protocol.FileInfo, bool, error)
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
// a scan or a pull of the folder, and returns its error. When ctx is done
// before the serve loop takes fn, it returns ctx's error and fn does not run.
// fn's context, derived from ctx, is also canceled when the folder stops.
func (m *Internals) RunInFolderLoop(ctx context.Context, folderID string, fn func(context.Context, FolderHandle) error) error {
	c, err := m.coordinated()
	if err != nil {
		return err
	}
	return c.RunInFolderLoop(ctx, folderID, fn)
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

// DeviceFileInfo returns the index entry another device last announced for
// path, which for a device that is away can be from long ago.
func (m *Internals) DeviceFileInfo(folderID string, device protocol.DeviceID, path string) (protocol.FileInfo, bool, error) {
	c, err := m.coordinated()
	if err != nil {
		return protocol.FileInfo{}, false, err
	}
	return c.DeviceFile(folderID, device, path)
}
