// Copyright (C) 2026 The Syncthing Authors.
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this file,
// You can obtain one at https://mozilla.org/MPL/2.0/.

package model

import (
	"context"
	"errors"
	"time"

	"github.com/syncthing/syncthing/lib/config"
	"github.com/syncthing/syncthing/lib/fs"
	"github.com/syncthing/syncthing/lib/protocol"
	"github.com/syncthing/syncthing/lib/scanner"
)

// FolderHandle gives a function run by RunInFolderLoop access to a folder
// while its scanner and puller are held, so that the function can change the
// folder on disk and index the result without racing them. A handle is valid
// only during that call.
type FolderHandle interface {
	// Filesystem returns the folder's filesystem, which records modification
	// times the underlying filesystem cannot keep.
	Filesystem() fs.Filesystem
	ShortID() protocol.ShortID
	ModTimeWindow() time.Duration
	// ScanConfig returns the configuration a scan of the folder uses, for
	// building index entries with scanner.FileInfoFor.
	ScanConfig() scanner.Config
	CurrentFile(name string) (protocol.FileInfo, bool, error)
	GlobalFile(name string) (protocol.FileInfo, bool, error)
	// IsIgnored reports whether the folder's ignore patterns, as loaded when
	// the function was called, exclude name.
	IsIgnored(name string) bool
	// HasVersioner reports whether the folder archives replaced and deleted
	// files.
	HasVersioner() bool
	// Archive moves the file at name to the folder's versions, as the puller
	// does before replacing or deleting it.
	Archive(name string) error
	// DeletionOf returns the index entry recording the deletion of cur, as a
	// scan that finds it missing does.
	DeletionOf(cur protocol.FileInfo) protocol.FileInfo
	// CommitLocal records files as new local versions, as a scan does: they
	// are indexed and announced to other devices.
	CommitLocal(files []protocol.FileInfo) error
	// Scan scans subs, as a scan of the folder does.
	Scan(ctx context.Context, subs []string) error
}

var errNotCoordinated = errors.New("folder does not support coordinated changes")

// baseFolder returns the folder a runner is built on.
func (f *folder) baseFolder() *folder {
	return f
}

// runCoordinated runs fn in the serve loop, once the initial scan has
// finished. The error fn returns is the caller's: it does not put the folder
// into an error state.
func (f *folder) runCoordinated(fn func(context.Context) error) error {
	select {
	case <-f.initialScanFinished:
	case <-f.done:
		return context.Canceled
	}
	var opErr error
	err := f.doInSync(func(ctx context.Context) error {
		if err := f.getHealthErrorAndLoadIgnores(); err != nil {
			opErr = err
			return err
		}
		opErr = fn(ctx)
		return ctx.Err()
	})
	if opErr != nil {
		return opErr
	}
	return err
}

// commitLocal indexes files as local changes. It must run in the serve loop.
func (f *folder) commitLocal(files []protocol.FileInfo) error {
	if f.Type == config.FolderTypeReceiveEncrypted {
		return errNotCoordinated
	}
	batch := NewFileInfoBatch(func(fs []protocol.FileInfo) error {
		if err := f.getHealthErrorWithoutIgnores(); err != nil {
			return err
		}
		return f.updateLocalsFromScanning(fs)
	})
	for _, file := range files {
		file.LocalFlags = f.localFlags
		file.Sequence = 0
		batch.Append(file)
		if err := batch.FlushIfFull(); err != nil {
			return err
		}
	}
	if err := batch.Flush(); err != nil {
		return err
	}
	f.SchedulePull()
	return nil
}

type folderHandle struct {
	f      *folder
	runner service
}

func (h *folderHandle) Filesystem() fs.Filesystem {
	return h.f.mtimefs
}

func (h *folderHandle) ShortID() protocol.ShortID {
	return h.f.shortID
}

func (h *folderHandle) ModTimeWindow() time.Duration {
	return h.f.modTimeWindow
}

func (h *folderHandle) ScanConfig() scanner.Config {
	return h.f.scanConfig(nil)
}

func (h *folderHandle) CurrentFile(name string) (protocol.FileInfo, bool, error) {
	return h.f.db.GetDeviceFile(h.f.folderID, protocol.LocalDeviceID, name)
}

func (h *folderHandle) GlobalFile(name string) (protocol.FileInfo, bool, error) {
	return h.f.db.GetGlobalFile(h.f.folderID, name)
}

func (h *folderHandle) IsIgnored(name string) bool {
	return h.f.ignores.Match(name).IsIgnored()
}

func (h *folderHandle) HasVersioner() bool {
	return h.f.versioner != nil
}

func (h *folderHandle) Archive(name string) error {
	archiver, ok := h.runner.(interface{ archiveVersion(string) error })
	if !ok || h.f.versioner == nil {
		return errNoVersioner
	}
	return archiver.archiveVersion(name)
}

func (h *folderHandle) DeletionOf(cur protocol.FileInfo) protocol.FileInfo {
	nf := cur
	nf.Version = cur.Version.Copy()
	nf.SetDeleted(h.f.shortID)
	nf.LocalFlags = h.f.localFlags
	if cur.ShouldConflict() {
		// As in scanSubdirsDeletedAndIgnored: this deletion is not based on
		// the versions others announced.
		nf.Version = protocol.Vector{}
	}
	return nf
}

func (h *folderHandle) CommitLocal(files []protocol.FileInfo) error {
	return h.f.commitLocal(files)
}

func (h *folderHandle) Scan(ctx context.Context, subs []string) error {
	return h.f.scanSubdirs(ctx, subs)
}

// RunInFolderLoop runs fn in the folder's serve loop, where it cannot overlap
// a scan or a pull of the folder, and returns its error. It waits for the
// initial scan and for any scan or pull in progress.
//
// fn must not call methods that wait for the folder's serve loop, such as
// ScanFolderSubdirs, and should return when its context is cancelled: the
// folder cannot stop before it does.
func (m *model) RunInFolderLoop(folderID string, fn func(context.Context, FolderHandle) error) error {
	m.mut.RLock()
	err := m.checkFolderRunningRLocked(folderID)
	runner, ok := m.folderRunners.Get(folderID)
	m.mut.RUnlock()
	if err != nil {
		return err
	}
	if !ok {
		return ErrFolderMissing
	}
	based, ok := runner.(interface{ baseFolder() *folder })
	if !ok {
		return errNotCoordinated
	}
	f := based.baseFolder()
	h := &folderHandle{f: f, runner: runner}
	return f.runCoordinated(func(ctx context.Context) error {
		return fn(ctx, h)
	})
}

// LocalIndexID returns the ID of this device's index of the folder, which
// changes when the index is reset.
func (m *model) LocalIndexID(folder string) (protocol.IndexID, error) {
	return m.sdb.GetIndexID(folder, protocol.LocalDeviceID)
}

// LocalSequence returns the sequence of this device's latest change to the
// folder's index.
func (m *model) LocalSequence(folder string) (int64, error) {
	return m.sdb.GetDeviceSequence(folder, protocol.LocalDeviceID)
}
