// Copyright (C) 2026 The Syncthing Authors.
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this file,
// You can obtain one at https://mozilla.org/MPL/2.0/.

package client

import (
	"context"
	"errors"
	"testing"

	"github.com/syncthing/syncthing/lib/relay/protocol"
)

func TestJoinSessionNeedsAnAddress(t *testing.T) {
	if _, err := JoinSession(context.Background(), protocol.SessionInvitation{Port: 22067}); !errors.Is(err, errNoSessionAddress) {
		t.Fatalf("error %v", err)
	}
}
