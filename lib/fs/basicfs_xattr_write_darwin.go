// Copyright (C) 2026 The Syncthing Authors.
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this file,
// You can obtain one at https://mozilla.org/MPL/2.0/.

//go:build darwin

package fs

import (
	"errors"
	"fmt"
	"golang.org/x/sys/unix"
)

func setXattr(path, name string, value []byte) error {
	if name == "com.apple.ResourceFork" {
		// Resource forks are written at offset zero without truncation. Query
		// the native size independently of filters, which can omit the old fork.
		size, err := unix.Lgetxattr(path, name, nil)
		if err != nil && !errors.Is(err, unix.ENOATTR) {
			return err
		}
		if err == nil && size > len(value) {
			if err := unix.Lremovexattr(path, name); err != nil {
				return fmt.Errorf("truncating resource fork: %w", err)
			}
		}
	}
	return unix.Lsetxattr(path, name, value, 0)
}
