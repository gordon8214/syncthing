// Copyright (C) 2026 The Syncthing Authors.
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this file,
// You can obtain one at https://mozilla.org/MPL/2.0/.

package syncthing

import (
	"context"
	"errors"
	"testing"

	"github.com/syncthing/syncthing/internal/db/sqlite"
	"github.com/syncthing/syncthing/lib/config"
	"github.com/syncthing/syncthing/lib/events"
	"github.com/syncthing/syncthing/lib/model"
	"github.com/syncthing/syncthing/lib/protocol"
)

// Internals reach the coordinated changes through an interface of their own,
// which the model must keep satisfying.
func TestInternalsReachCoordinatedChanges(t *testing.T) {
	sdb, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sdb.Close() })
	cfg := config.Wrap("", config.Configuration{}, protocol.LocalDeviceID, events.NoopLogger)
	internals := newInternals(model.NewModel(cfg, protocol.LocalDeviceID, sdb, nil, events.NoopLogger, protocol.NewKeyGenerator()))

	if _, err := internals.coordinated(); err != nil {
		t.Fatal(err)
	}
	err = internals.RunInFolderLoop(t.Context(), "missing", func(context.Context, FolderHandle) error { return nil })
	if err == nil || errors.Is(err, errNotCoordinated) {
		t.Fatalf("got %v, not the model's error for a missing folder", err)
	}
}
