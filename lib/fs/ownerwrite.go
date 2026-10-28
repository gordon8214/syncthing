// Copyright (C) 2026 The Syncthing Authors.
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this file,
// You can obtain one at https://mozilla.org/MPL/2.0/.

package fs

import "errors"

// WithOwnerWrite runs fn, which changes the extended attributes of name.
// Where that needs write access to the item (as on macOS, and for user
// attributes on Linux) and its owner may not write it, such as a read-only
// git object or a file whose final permissions were already applied, fn runs
// again with owner write access added for the moment. The item's mode is then
// restored.
func WithOwnerWrite(fsys Filesystem, name string, fn func() error) error {
	err := fn()
	if err == nil || !IsPermission(err) {
		return err
	}
	info, statErr := fsys.Lstat(name)
	if statErr != nil || info.IsSymlink() || info.Mode()&0o200 != 0 {
		return err
	}
	mode := info.Mode() & (ModePerm | ModeSetuid | ModeSetgid | ModeSticky)
	if chmodErr := fsys.Chmod(name, mode|0o200); chmodErr != nil {
		return err
	}
	return errors.Join(fn(), fsys.Chmod(name, mode))
}
