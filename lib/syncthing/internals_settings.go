// Copyright (C) 2026 The Syncthing Authors.
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this file,
// You can obtain one at https://mozilla.org/MPL/2.0/.

package syncthing

import (
	"time"

	"github.com/syncthing/syncthing/internal/db"
	"github.com/syncthing/syncthing/lib/model"
	"github.com/syncthing/syncthing/lib/protocol"
)

// FileError is an item a folder could not scan or synchronize, and why.
type FileError = model.FileError

// PendingDevice is a device that tried to connect without being configured.
type PendingDevice = db.ObservedDevice

// FolderErrors returns the items the folder could not scan or synchronize.
func (m *Internals) FolderErrors(folderID string) ([]FileError, error) {
	return m.model.FolderErrors(folderID)
}

// Override makes the state of a send-only folder the global one. It does
// nothing for another type of folder, or one that isn't running, and returns
// when it is done.
func (m *Internals) Override(folderID string) {
	m.model.Override(folderID)
}

// Revert removes what changed locally in a receive-only or receive-encrypted
// folder. It does nothing for another type of folder, or one that isn't
// running, and returns when it is done.
func (m *Internals) Revert(folderID string) {
	m.model.Revert(folderID)
}

// ReceiveOnlySize counts what changed locally in a receive-only folder.
func (m *Internals) ReceiveOnlySize(folderID string) (Counts, error) {
	return m.model.ReceiveOnlySize(folderID)
}

// FolderVersions returns every version the folder's versioner keeps, by the
// path of the file. It walks the whole archive, so it takes a while on a large
// one.
func (m *Internals) FolderVersions(folderID string) (map[string][]FileVersion, error) {
	return m.model.GetFolderVersions(folderID)
}

// RestoreFolderVersions restores the given version of each path and returns
// what failed by path. A file that is in the way is archived first.
func (m *Internals) RestoreFolderVersions(folderID string, versions map[string]time.Time) (map[string]error, error) {
	return m.model.RestoreFolderVersions(folderID, versions)
}

// PendingDevices returns the devices that tried to connect without being
// configured.
func (m *Internals) PendingDevices() (map[protocol.DeviceID]PendingDevice, error) {
	return m.model.PendingDevices()
}

// DismissPendingDevice forgets a pending device, until it tries again.
func (m *Internals) DismissPendingDevice(deviceID protocol.DeviceID) error {
	return m.model.DismissPendingDevice(deviceID)
}
