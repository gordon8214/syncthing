// Copyright (C) 2026 The Syncthing Authors.
// SPDX-License-Identifier: MPL-2.0
//go:build !windows

package versioner

import (
	"errors"
	"github.com/syncthing/syncthing/lib/fs"
	"github.com/syncthing/syncthing/lib/protocol"
)

type platformRefFilter struct{}

func (platformRefFilter) Permit(name string) bool    { return name == platformRefName }
func (platformRefFilter) GetMaxSingleEntrySize() int { return 0 }
func (platformRefFilter) GetMaxTotalSize() int       { return 0 }
func readPlatformRefData(fsys fs.Filesystem, name string) ([]byte, error) {
	attrs, err := fsys.GetXattr(name, platformRefFilter{})
	if err != nil || len(attrs) == 0 {
		return nil, err
	}
	return attrs[0].Value, nil
}
func writePlatformRefData(fsys fs.Filesystem, name string, data []byte) error {
	if err := fsys.SetXattr(name, []protocol.Xattr{{Name: platformRefName, Value: data}}, platformRefFilter{}); err != nil {
		return err
	}
	return nil
}
func syncPlatformFile(fsys fs.Filesystem, name string) error {
	file, err := fsys.Open(name)
	if err != nil {
		return err
	}
	return errors.Join(file.Sync(), file.Close())
}
func removePlatformRefData(fsys fs.Filesystem, name string) error {
	return fsys.SetXattr(name, nil, platformRefFilter{})
}
func syncPlatformDir(fsys fs.Filesystem, path string) error {
	file, err := fsys.Open(path)
	if err != nil {
		return err
	}
	return errors.Join(file.Sync(), file.Close())
}

func platformRestoreRename(fsys fs.Filesystem, source, target string) error {
	return fsys.Rename(source, target)
}
