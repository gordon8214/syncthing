// Copyright (C) 2026 The Syncthing Authors.
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this file,
// You can obtain one at https://mozilla.org/MPL/2.0/.

//go:build !darwin && !windows && !dragonfly && !illumos && !solaris && !openbsd

package fs

import "golang.org/x/sys/unix"

func setXattr(path, name string, value []byte) error { return unix.Lsetxattr(path, name, value, 0) }
