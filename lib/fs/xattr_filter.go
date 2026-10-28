// Copyright (C) 2026 The Syncthing Authors.
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this file,
// You can obtain one at https://mozilla.org/MPL/2.0/.

package fs

import (
	"cmp"
	"slices"

	"github.com/syncthing/syncthing/lib/protocol"
)

// FilterXattrs returns a sorted, independent attribute list without changing the
// caller's index data. Limits count only attributes that are actually accepted.
func FilterXattrs(attrs []protocol.Xattr, filter XattrFilter) []protocol.Xattr {
	attrs = slices.Clone(attrs)
	slices.SortFunc(attrs, func(a, b protocol.Xattr) int { return cmp.Compare(a.Name, b.Name) })
	result := attrs[:0]
	total := 0
	for _, attr := range attrs {
		if !filter.Permit(attr.Name) {
			continue
		}
		size := len(attr.Name) + len(attr.Value)
		if max := filter.GetMaxSingleEntrySize(); max > 0 && size > max {
			continue
		}
		if max := filter.GetMaxTotalSize(); max > 0 && size > max-total {
			continue
		}
		total += size
		result = append(result, attr)
	}
	clear(attrs[len(result):]) // Release rejected payloads in the unused backing array.
	return result
}
