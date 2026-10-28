// Copyright (C) 2026 The Syncthing Authors.
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this file,
// You can obtain one at https://mozilla.org/MPL/2.0/.

package versioner

import (
	"compress/gzip"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/syncthing/syncthing/lib/fs"
	"github.com/syncthing/syncthing/lib/osutil"
	"github.com/syncthing/syncthing/lib/protocol"
)

// PlatformDataVersioner preserves foreign platform metadata that a relay cannot
// represent on its native filesystem. The index is authoritative when archiving.
// A pending restore is acknowledged only after the restored metadata is indexed.
type PlatformDataVersioner interface {
	ArchiveWithPlatformData(string, protocol.PlatformData, bool) error
	RestoreWithPlatformData(string, time.Time, protocol.PlatformData) error
	RestoredPlatformData(string) (protocol.PlatformData, bool, error)
	CommitRestoredPlatformData(string, protocol.PlatformData) error
}

func WithPlatformData(v Versioner) (PlatformDataVersioner, bool) {
	if wrapped, ok := v.(*versionerWithErrorContext); ok {
		v = wrapped.Versioner
	}
	s, ok := v.(*staggered)
	if !ok || !s.preservePlatformData {
		return nil, false
	}
	return s, true
}

const platformRefName = "user.syncthing.platform-history"

var platformRoot = filepath.Join(DefaultPath, "platform")

type platformRef struct {
	ID      string `json:"id"`
	Restore bool   `json:"restore,omitempty"`
}
type platformRefFilter struct{}

func (platformRefFilter) Permit(name string) bool    { return name == platformRefName }
func (platformRefFilter) GetMaxSingleEntrySize() int { return 0 }
func (platformRefFilter) GetMaxTotalSize() int       { return 0 }

func readPlatformRef(fsys fs.Filesystem, name string) (platformRef, bool, error) {
	attrs, err := fsys.GetXattr(name, platformRefFilter{})
	if fs.IsNotExist(err) {
		return platformRef{}, false, nil
	}
	if err != nil {
		return platformRef{}, false, err
	}
	if len(attrs) == 0 {
		return platformRef{}, false, nil
	}
	var ref platformRef
	if err := json.Unmarshal(attrs[0].Value, &ref); err != nil {
		return ref, false, err
	}
	id, err := hex.DecodeString(ref.ID)
	if err != nil || len(id) != 16 {
		return ref, false, errors.New("invalid platform history reference")
	}
	return ref, true, nil
}
func writePlatformRef(fsys fs.Filesystem, name string, ref platformRef) error {
	data, err := json.Marshal(ref)
	if err != nil {
		return err
	}
	// Versions keep their file's permissions, which may be read-only
	if err := fs.WithOwnerWrite(fsys, name, func() error {
		return fsys.SetXattr(name, []protocol.Xattr{{Name: platformRefName, Value: data}}, platformRefFilter{})
	}); err != nil {
		return err
	}
	file, err := fsys.Open(name)
	if err != nil {
		return err
	}
	return errors.Join(file.Sync(), file.Close())
}
func platformBlob(ref platformRef) string { return filepath.Join(platformRoot, ref.ID+".json.gz") }

func (v *staggered) savePlatformData(pd protocol.PlatformData) (platformRef, error) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return platformRef{}, err
	}
	ref := platformRef{ID: hex.EncodeToString(id[:])}
	if err := v.versionsFs.MkdirAll(platformRoot, 0o700); err != nil {
		return ref, err
	}
	name := platformBlob(ref)
	out, err := v.versionsFs.OpenFile(name, fs.OptWriteOnly|fs.OptCreate|fs.OptExclusive, 0o600)
	if err != nil {
		return ref, err
	}
	zw := gzip.NewWriter(out)
	err = json.NewEncoder(zw).Encode(pd)
	err = errors.Join(err, zw.Close(), out.Sync(), out.Close())
	if err == nil {
		err = syncPlatformDir(v.versionsFs, platformRoot)
	}
	if err != nil {
		_ = v.versionsFs.Remove(name)
	}
	return ref, err
}
func (v *staggered) loadPlatformData(ref platformRef) (protocol.PlatformData, error) {
	var pd protocol.PlatformData
	in, err := v.versionsFs.Open(platformBlob(ref))
	if err != nil {
		return pd, err
	}
	defer in.Close()
	zr, err := gzip.NewReader(in)
	if err != nil {
		return pd, err
	}
	defer zr.Close()
	// Base64 JSON can be larger than the protocol's 500 MB message limit.
	// Bound corrupt archives while retaining every representable platform value.
	const limit = 1_000_000_000
	data, err := io.ReadAll(io.LimitReader(zr, limit+1))
	if err != nil {
		return pd, err
	}
	if len(data) > limit {
		return pd, errors.New("platform history exceeds message size")
	}
	err = json.Unmarshal(data, &pd)
	return pd, err
}

// archivePlatformData requires the default versions directory on the same
// filesystem. Rename carries the reference atomically with the data fork. Never
// fall back to a cross-filesystem copy, which could silently drop the reference.
func (v *staggered) archivePlatformData(path string, pd protocol.PlatformData, copyOnly bool, prune bool) (string, error) {
	path = osutil.NativeFilename(path)
	info, err := v.folderFs.Lstat(path)
	if fs.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if !info.IsRegular() {
		return "", errors.New("platform history requires a regular file")
	}
	if err = v.versionsFs.MkdirAll(".", 0o700); err != nil {
		return "", err
	}
	if err = dupDirTree(v.folderFs, v.versionsFs, filepath.Dir(path)); err != nil {
		return "", err
	}
	ref, err := v.savePlatformData(pd)
	if err != nil {
		return "", err
	}
	published := false
	source := path
	defer func() {
		if !published {
			_ = v.versionsFs.Remove(platformBlob(ref))
		}
	}()
	if copyOnly {
		source = filepath.Join(DefaultPath, platformRoot, ref.ID+".tmp")
		defer v.folderFs.Remove(source)
		in, err := v.folderFs.Open(path)
		if err != nil {
			return "", err
		}
		out, err := v.folderFs.OpenFile(source, fs.OptWriteOnly|fs.OptCreate|fs.OptExclusive, 0o600)
		if err != nil {
			_ = in.Close()
			return "", err
		}
		err = fs.CopyRange(fs.CopyRangeMethodAllWithFallback, in, out, 0, 0, info.Size())
		err = errors.Join(err, out.Sync(), out.Close(), in.Close())
		if err != nil {
			return "", err
		}
		if err = v.folderFs.Chmod(source, info.Mode()&fs.ModePerm); err != nil {
			return "", err
		}
		if err = v.folderFs.Chtimes(source, info.ModTime(), info.ModTime()); err != nil {
			return "", err
		}
	}
	if err = writePlatformRef(v.folderFs, source, ref); err != nil {
		return "", err
	}
	// Native version timestamps have one-second precision. Do not overwrite a
	// version when two changes arrive in one second (including during restore).
	stamp := time.Now().Truncate(time.Second)
	var dest string
	for {
		dest = TagFilename(path, stamp.Format(TimeFormat))
		if _, err = v.versionsFs.Lstat(dest); fs.IsNotExist(err) {
			break
		}
		if err != nil {
			return "", err
		}
		stamp = stamp.Add(time.Second)
	}
	if err = v.folderFs.Rename(source, filepath.Join(DefaultPath, dest)); err != nil {
		_ = fs.WithOwnerWrite(v.folderFs, source, func() error { return v.folderFs.SetXattr(source, nil, platformRefFilter{}) })
		return "", err
	}
	published = true
	if err = errors.Join(syncPlatformDir(v.folderFs, filepath.Dir(source)), syncPlatformDir(v.versionsFs, filepath.Dir(dest))); err != nil {
		return dest, err
	}
	if prune {
		cleanVersions(v.versionsFs, findAllVersions(v.versionsFs, path), v.toRemove, true)
	}
	return dest, nil
}

func (v *staggered) ArchiveWithPlatformData(path string, pd protocol.PlatformData, copyOnly bool) error {
	v.platformMut.Lock()
	defer v.platformMut.Unlock()
	_, err := v.archivePlatformData(path, pd, copyOnly, true)
	return err
}
func (v *staggered) RestoreWithPlatformData(path string, stamp time.Time, current protocol.PlatformData) error {
	v.platformMut.Lock()
	defer v.platformMut.Unlock()
	path = osutil.NativeFilename(path)
	source := TagFilename(path, stamp.In(time.Local).Truncate(time.Second).Format(TimeFormat))
	info, err := v.versionsFs.Lstat(source)
	if err != nil {
		return err
	}
	if !info.IsRegular() {
		return errNotFound
	}
	ref, ok, err := readPlatformRef(v.versionsFs, source)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("version has no saved platform metadata; refusing incomplete restore")
	}
	if _, err = v.loadPlatformData(ref); err != nil {
		return fmt.Errorf("reading version metadata: %w", err)
	}
	// Copy the live version first, then atomically replace it. A crash or
	// failed rename before publication always leaves the live filename intact.
	if err = v.folderFs.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	// A persistent pending record lets cleanup protect restored metadata without
	// scanning millions of live files. It is written before publication.
	pending := platformPending(ref)
	out, err := v.versionsFs.OpenFile(pending+".tmp", fs.OptWriteOnly|fs.OptCreate|fs.OptTruncate, 0o600)
	if err != nil {
		return err
	}
	err = json.NewEncoder(out).Encode(path)
	err = errors.Join(err, out.Sync(), out.Close())
	if err != nil {
		return err
	}
	if err = v.versionsFs.Rename(pending+".tmp", pending); err != nil {
		return err
	}
	if err = syncPlatformDir(v.versionsFs, platformRoot); err != nil {
		return err
	}
	originalRef := ref
	ref.Restore = true
	if err = writePlatformRef(v.versionsFs, source, ref); err != nil {
		return err
	}
	restored := false
	defer func() {
		if !restored {
			_ = writePlatformRef(v.versionsFs, source, originalRef)
		}
	}()
	if live, err := v.folderFs.Lstat(path); err == nil {
		if !live.IsRegular() {
			return ErrDirectory
		}
		if _, err = v.archivePlatformData(path, current, true, false); err != nil {
			return err
		}
	} else if !fs.IsNotExist(err) {
		return err
	}
	if err = v.folderFs.Rename(filepath.Join(DefaultPath, source), path); err != nil {
		return err
	}
	restored = true
	return errors.Join(syncPlatformDir(v.folderFs, filepath.Dir(path)), syncPlatformDir(v.versionsFs, filepath.Dir(source)))
}

func (v *staggered) RestoredPlatformData(path string) (protocol.PlatformData, bool, error) {
	v.platformMut.Lock()
	defer v.platformMut.Unlock()
	ref, ok, err := readPlatformRef(v.folderFs, path)
	if err != nil || !ok || !ref.Restore {
		return protocol.PlatformData{}, false, err
	}
	pd, err := v.loadPlatformData(ref)
	return pd, true, err
}
func (v *staggered) CommitRestoredPlatformData(path string, pd protocol.PlatformData) error {
	v.platformMut.Lock()
	defer v.platformMut.Unlock()
	ref, ok, err := readPlatformRef(v.folderFs, path)
	if err != nil || !ok || !ref.Restore {
		return err
	}
	saved, err := v.loadPlatformData(ref)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(saved, pd) {
		return errors.New("restored platform metadata not indexed")
	}
	if err = fs.WithOwnerWrite(v.folderFs, path, func() error { return v.folderFs.SetXattr(path, nil, platformRefFilter{}) }); err != nil {
		return err
	}
	return errors.Join(v.versionsFs.Remove(platformBlob(ref)), v.versionsFs.Remove(platformPending(ref)))
}

func removePlatformVersion(fsys fs.Filesystem, path string) error {
	ref, ok, err := readPlatformRef(fsys, path)
	if err != nil {
		return err
	}
	if err := fsys.Remove(path); err != nil {
		return err
	}
	if ok {
		return fsys.Remove(platformBlob(ref))
	}
	return nil
}
func skipPlatformDir(path string, enabled []bool) bool {
	return len(enabled) > 0 && enabled[0] && (path == DefaultPath || strings.HasPrefix(path, DefaultPath+string(filepath.Separator)))
}

func platformPending(ref platformRef) string {
	return filepath.Join(platformRoot, ref.ID+".pending.json")
}

// Reconcile interrupted writes/deletions once a day. Pending restore records
// avoid a full walk of the live folder; retention already walks the archive.
// A grace period protects incomplete operations from an earlier process.
func (v *staggered) reconcilePlatformData(ctx context.Context) error {
	protected := make(map[string]bool)
	err := v.versionsFs.Walk(".", func(path string, info fs.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if path == DefaultPath {
			return fs.SkipDir
		}
		if !info.IsRegular() {
			return nil
		}
		ref, ok, err := readPlatformRef(v.versionsFs, path)
		if err != nil {
			return err
		}
		if ok {
			protected[ref.ID] = true
		}
		return nil
	})
	if fs.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	entries, err := v.versionsFs.DirNames(platformRoot)
	if fs.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, name := range entries {
		if !strings.HasSuffix(name, ".pending.json") {
			continue
		}
		file, err := v.versionsFs.Open(filepath.Join(platformRoot, name))
		if err != nil {
			return err
		}
		var path string
		err = json.NewDecoder(io.LimitReader(file, 1<<20)).Decode(&path)
		err = errors.Join(err, file.Close())
		if err != nil {
			return err
		}
		ref, ok, err := readPlatformRef(v.folderFs, path)
		if err != nil {
			return err
		}
		if ok && ref.Restore && name == ref.ID+".pending.json" {
			protected[ref.ID] = true
		}
	}
	cutoff := time.Now().Add(-24 * time.Hour)
	for _, name := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		id, _, _ := strings.Cut(name, ".")
		decoded, err := hex.DecodeString(id)
		if err != nil || len(decoded) != 16 || protected[id] {
			continue
		}
		path := filepath.Join(platformRoot, name)
		info, err := v.versionsFs.Lstat(path)
		if err != nil {
			return err
		}
		if !info.IsRegular() || !info.ModTime().Before(cutoff) {
			continue
		}
		if err := v.versionsFs.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func syncPlatformDir(fsys fs.Filesystem, path string) error {
	file, err := fsys.Open(path)
	if err != nil {
		return err
	}
	return errors.Join(file.Sync(), file.Close())
}
