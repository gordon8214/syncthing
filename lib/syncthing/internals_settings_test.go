// Copyright (C) 2026 The Syncthing Authors.
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this file,
// You can obtain one at https://mozilla.org/MPL/2.0/.

package syncthing

import (
	"errors"
	"testing"
	"time"

	"github.com/syncthing/syncthing/internal/db/sqlite"
	"github.com/syncthing/syncthing/lib/config"
	"github.com/syncthing/syncthing/lib/events"
	"github.com/syncthing/syncthing/lib/model"
	"github.com/syncthing/syncthing/lib/protocol"
)

// A folder that doesn't exist gets the model's errors. Applications rely on
// those to tell why nothing happened.
func TestInternalsSettingsOnMissingFolder(t *testing.T) {
	sdb, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sdb.Close() })
	cfg := config.Wrap("", config.Configuration{}, protocol.LocalDeviceID, events.NoopLogger)
	internals := newInternals(model.NewModel(cfg, protocol.LocalDeviceID, sdb, nil, events.NoopLogger, protocol.NewKeyGenerator()))

	if _, err := internals.FolderErrors("missing"); !errors.Is(err, model.ErrFolderMissing) {
		t.Errorf("FolderErrors: got %v", err)
	}
	if _, err := internals.FolderVersions("missing"); !errors.Is(err, model.ErrFolderMissing) {
		t.Errorf("FolderVersions: got %v", err)
	}
	versions := map[string]time.Time{"file": time.Now()}
	if _, err := internals.RestoreFolderVersions("missing", versions); !errors.Is(err, model.ErrFolderMissing) {
		t.Errorf("RestoreFolderVersions: got %v", err)
	}
	if counts, err := internals.ReceiveOnlySize("missing"); err != nil || counts != (Counts{}) {
		t.Errorf("ReceiveOnlySize: got %v, %v", counts, err)
	}
	if err := internals.Override("missing"); !errors.Is(err, model.ErrFolderMissing) {
		t.Errorf("Override: got %v", err)
	}
	if err := internals.Revert("missing"); !errors.Is(err, model.ErrFolderMissing) {
		t.Errorf("Revert: got %v", err)
	}
}

func TestInternalsPendingDevices(t *testing.T) {
	sdb, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sdb.Close() })
	cfg := config.Wrap("", config.Configuration{}, protocol.LocalDeviceID, events.NoopLogger)
	internals := newInternals(model.NewModel(cfg, protocol.LocalDeviceID, sdb, nil, events.NoopLogger, protocol.NewKeyGenerator()))

	pending, err := internals.PendingDevices()
	if err != nil || len(pending) != 0 {
		t.Fatalf("got %v, %v", pending, err)
	}
	if err := internals.DismissPendingDevice(protocol.DeviceID{1}); err != nil {
		t.Fatal(err)
	}
}
