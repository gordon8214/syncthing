// Copyright (C) 2015 The Syncthing Authors.
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this file,
// You can obtain one at https://mozilla.org/MPL/2.0/.

package versioner

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/syncthing/syncthing/lib/config"
	"github.com/syncthing/syncthing/lib/fs"
	"github.com/syncthing/syncthing/lib/osutil"
)

func init() {
	// Register the constructor for this type of versioner
	factories["trashcan"] = newTrashcan
}

type trashcan struct {
	folderFs        fs.Filesystem
	versionsFs      fs.Filesystem
	cleanoutDays    int
	copyRangeMethod fs.CopyRangeMethod
}

func newTrashcan(cfg config.FolderConfiguration) Versioner {
	cleanoutDays := parseCleanoutDays(cfg.Versioning.Params["cleanoutDays"])

	s := &trashcan{
		folderFs:        cfg.Filesystem(),
		versionsFs:      versionerFsFromFolderCfg(cfg),
		cleanoutDays:    cleanoutDays,
		copyRangeMethod: cfg.CopyRangeMethod.ToFS(),
	}

	l.Debugf("instantiated %#v", s)
	return s
}

// Archive moves the named file away to a version archive. If this function
// returns nil, the named file does not exist any more (has been archived).
func (t *trashcan) Archive(filePath string) error {
	return archiveFile(t.copyRangeMethod, t.folderFs, t.versionsFs, filePath, func(name, tag string) string {
		return name
	})
}

func (t *trashcan) String() string {
	return fmt.Sprintf("trashcan@%p", t)
}

func (t *trashcan) Clean(ctx context.Context) error {
	if t.cleanoutDays <= 0 {
		return nil
	}

	if _, err := t.versionsFs.Lstat("."); fs.IsNotExist(err) {
		return nil
	}

	cutoff := time.Now().Add(time.Duration(-24*t.cleanoutDays) * time.Hour)
	dirTracker := make(emptyDirTracker)

	walkFn := func(path string, info fs.FileInfo, err error) error {
		if err != nil {
			return err
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if info.IsDir() && !info.IsSymlink() {
			dirTracker.addDir(path)
			return nil
		}

		if archivedAt(path, info).Before(cutoff) {
			// The file is too old; remove it.
			err = t.versionsFs.Remove(path)
		} else {
			// Keep this file, and remember it so we don't unnecessarily try
			// to remove this directory.
			dirTracker.addFile(path)
		}
		return err
	}

	if err := t.versionsFs.Walk(".", walkFn); err != nil {
		return err
	}

	dirTracker.deleteEmptyDirs(t.versionsFs)

	return nil
}

func (t *trashcan) GetVersions() (map[string][]FileVersion, error) {
	return retrieveVersions(t.versionsFs)
}

func (t *trashcan) Restore(name string, versionTime time.Time) error {
	// If we have an untagged file A and want to restore it on top of existing file A, we can't first archive the
	// existing A as we'd overwrite the old A version, therefore when we archive existing file, we archive it with a
	// tag but when the restoration is finished, we rename it (untag it). This is only important if when restoring A,
	// there already exists a file at the same location

	taggedName := ""
	tagger := func(name, tag string) string {
		// We also abuse the fact that tagger gets called twice, once for tagging the restoration version, which
		// should just return the plain name, and second time by archive which archives existing file in the folder.
		// We can't use TagFilename here, as restoreFile would discover that as a valid version and restore that instead.
		if taggedName != "" {
			// Archiving goes by the name in its directory
			return filepath.Base(taggedName)
		}

		taggedName = fs.TempName(name)

		// A folder that used another type of versioner before has tagged versions, which are listed along with the rest
		if tagged := TagFilename(name, tag); tagged != name {
			if _, err := t.versionsFs.Lstat(tagged); err == nil {
				return tagged
			}
		}
		return name
	}

	restoreErr := restoreFile(t.copyRangeMethod, t.versionsFs, t.folderFs, name, versionTime, tagger)

	// Nothing was in the way, or it could not be moved
	if _, err := t.versionsFs.Lstat(taggedName); err != nil {
		return restoreErr
	}

	// The version is still where it was, so the file goes back to where it was too
	if restoreErr != nil {
		return errors.Join(restoreErr, osutil.RenameOrCopy(t.copyRangeMethod, t.versionsFs, t.folderFs, taggedName, name))
	}

	if err := t.versionsFs.Rename(taggedName, name); err != nil {
		return err
	}

	// Cleaning up goes by this time. This is when the file became a version, as in Archive.
	now := time.Now()
	return t.versionsFs.Chtimes(name, now, now)
}

// archivedAt is when an entry of the trash can was archived. That is its
// modification time, as Archive sets it. Tagged versions come from a folder
// that used another versioner before, and have the time of the file instead.
// The later of the two counts, as a name can look tagged without being so.
func archivedAt(path string, info fs.FileInfo) time.Time {
	tagged, err := time.ParseInLocation(TimeFormat, extractTag(path), time.Local)
	if err == nil && tagged.After(info.ModTime()) {
		return tagged
	}
	return info.ModTime()
}
