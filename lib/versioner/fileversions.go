// Copyright (C) 2026 The Syncthing Authors.
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this file,
// You can obtain one at https://mozilla.org/MPL/2.0/.

package versioner

import (
	"cmp"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/syncthing/syncthing/lib/fs"
	"github.com/syncthing/syncthing/lib/osutil"
)

// ErrVersionNotFound is returned when the requested version of a file does
// not exist.
var ErrVersionNotFound = errNotFound

// FileVersioner is implemented by versioners that keep versions in a
// directory tree mirroring the folder (simple, staggered and trashcan). Unlike
// GetVersions, it looks only at the versions of one file.
type FileVersioner interface {
	// FileVersions returns the versions of the named file, newest first.
	FileVersions(name string) ([]FileVersion, error)
	// OpenVersion opens the version of the named file archived at
	// versionTime (as returned by FileVersions), or returns
	// ErrVersionNotFound. The open file stays readable when the version is
	// removed meanwhile.
	OpenVersion(name string, versionTime time.Time) (fs.File, error)
}

// AsFileVersioner returns the versioner's FileVersioner, when it has one.
func AsFileVersioner(v Versioner) (FileVersioner, bool) {
	if wrapped, ok := v.(*versionerWithErrorContext); ok {
		v = wrapped.Versioner
	}
	fv, ok := v.(FileVersioner)
	return fv, ok
}

// How a version is named, in order of preference when two have the same
// version time
const (
	versionTagged = iota
	versionOldStyleTag
	versionUntagged
)

type fileVersion struct {
	FileVersion
	path  string // in the versions filesystem
	style int
}

// fileVersions lists the versions of the named file in the versions
// filesystem, newest first and one per version time: tagged ones
// (name~tag.ext, or name.ext~tag in the old style) and, with untagged (the
// trashcan), the one named as the file, whose version time is its
// modification time, as in retrieveVersions. Internal files have no versions,
// and versions are not looked for through symlinks.
func fileVersions(versionsFs fs.Filesystem, name string, untagged bool) ([]fileVersion, error) {
	name = osutil.NormalizedFilename(name)
	if fs.IsInternal(name) {
		return nil, nil
	}
	dir, base := filepath.Dir(name), filepath.Base(name)
	nativeDir := osutil.NativeFilename(dir)
	if err := traversesSymlink(versionsFs, nativeDir); err != nil {
		return nil, err
	}
	entries, err := versionsFs.DirNames(nativeDir)
	if fs.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	ext := filepath.Ext(base)
	stem := base[:len(base)-len(ext)]
	var versions []fileVersion
	for _, entry := range entries {
		normalized := osutil.NormalizedFilename(entry)
		var versionTime time.Time
		style := versionUntagged
		if untagged && normalized == base {
			// The version time is the modification time, below
		} else if tag, tagStyle, ok := versionTag(normalized, base, stem, ext); ok {
			versionTime, err = time.ParseInLocation(TimeFormat, tag, time.Local)
			if err != nil {
				continue
			}
			style = tagStyle
		} else {
			continue
		}

		path := filepath.Join(nativeDir, entry)
		info, err := versionsFs.Lstat(path)
		if err != nil || !info.IsRegular() {
			continue
		}
		modTime := info.ModTime().Truncate(time.Second)
		if versionTime.IsZero() {
			versionTime = modTime
		}
		versions = append(versions, fileVersion{
			FileVersion: FileVersion{VersionTime: versionTime, ModTime: modTime, Size: info.Size()},
			path:        path,
			style:       style,
		})
	}
	slices.SortFunc(versions, func(a, b fileVersion) int {
		return cmp.Or(b.VersionTime.Compare(a.VersionTime), cmp.Compare(a.style, b.style), strings.Compare(a.path, b.path))
	})
	return slices.CompactFunc(versions, func(a, b fileVersion) bool {
		return a.VersionTime.Equal(b.VersionTime)
	}), nil
}

// versionTag returns the tag of entry when it names a version of the file
// base (stem plus ext), as TagFilename names them, or in the old style
// (base~tag)
func versionTag(entry, base, stem, ext string) (string, int, bool) {
	if rest, ok := strings.CutPrefix(entry, stem+"~"); ok {
		if tag, ok := strings.CutSuffix(rest, ext); ok && len(tag) == len(TimeFormat) {
			return tag, versionTagged, true
		}
	}
	if tag, ok := strings.CutPrefix(entry, base+"~"); ok && len(tag) == len(TimeFormat) {
		return tag, versionOldStyleTag, true
	}
	return "", 0, false
}

// traversesSymlink checks a directory of the versions tree: it returns
// errNoVersionsHere when the directory is (or is reached through) a symlink
// or a file, nil when it is a directory or missing, and other errors when it
// cannot be checked.
func traversesSymlink(versionsFs fs.Filesystem, dir string) error {
	err := osutil.TraversesSymlink(versionsFs, dir)
	var symlink *osutil.TraversesSymlinkError
	var notDir *osutil.NotADirectoryError
	if errors.As(err, &symlink) || errors.As(err, &notDir) {
		return errNoVersionsHere
	}
	return err
}

// errNoVersionsHere marks a directory of the versions tree that holds no
// versions: one reached through a symlink, or a file
var errNoVersionsHere = errors.New("no versions in this directory")

func listFileVersions(versionsFs fs.Filesystem, name string, untagged bool) ([]FileVersion, error) {
	versions, err := fileVersions(versionsFs, name, untagged)
	if errors.Is(err, errNoVersionsHere) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	list := make([]FileVersion, len(versions))
	for i, version := range versions {
		list[i] = version.FileVersion
	}
	return list, nil
}

func openFileVersion(versionsFs fs.Filesystem, name string, versionTime time.Time, untagged bool) (fs.File, error) {
	versionTime = versionTime.Truncate(time.Second)
	path := ""
	// Versions are usually named as TagFilename names them: look for that
	// one, before listing the directory for the other forms
	normalized := osutil.NormalizedFilename(name)
	tagged := osutil.NativeFilename(TagFilename(normalized, versionTime.In(time.Local).Format(TimeFormat)))
	if !fs.IsInternal(normalized) {
		if err := traversesSymlink(versionsFs, filepath.Dir(tagged)); err == nil {
			if info, err := versionsFs.Lstat(tagged); err == nil && info.IsRegular() {
				path = tagged
			}
		}
	}
	if path == "" {
		versions, err := fileVersions(versionsFs, name, untagged)
		if errors.Is(err, errNoVersionsHere) {
			return nil, ErrVersionNotFound
		}
		if err != nil {
			return nil, err
		}
		for _, version := range versions {
			if version.VersionTime.Equal(versionTime) {
				path = version.path
				break
			}
		}
		if path == "" {
			return nil, ErrVersionNotFound
		}
	}

	fd, err := versionsFs.Open(path)
	if fs.IsNotExist(err) {
		return nil, ErrVersionNotFound
	}
	if err != nil {
		return nil, err
	}
	// Replaced by something else since it was looked up
	if info, err := fd.Stat(); err != nil || !info.IsRegular() {
		fd.Close()
		return nil, ErrVersionNotFound
	}
	return fd, nil
}

func (v simple) FileVersions(name string) ([]FileVersion, error) {
	return listFileVersions(v.versionsFs, name, false)
}

func (v simple) OpenVersion(name string, versionTime time.Time) (fs.File, error) {
	return openFileVersion(v.versionsFs, name, versionTime, false)
}

func (v *staggered) FileVersions(name string) ([]FileVersion, error) {
	return listFileVersions(v.versionsFs, name, false)
}

func (v *staggered) OpenVersion(name string, versionTime time.Time) (fs.File, error) {
	return openFileVersion(v.versionsFs, name, versionTime, false)
}

func (t *trashcan) FileVersions(name string) ([]FileVersion, error) {
	return listFileVersions(t.versionsFs, name, true)
}

func (t *trashcan) OpenVersion(name string, versionTime time.Time) (fs.File, error) {
	return openFileVersion(t.versionsFs, name, versionTime, true)
}
