// Copyright (C) 2026 The Syncthing Authors.
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this file,
// You can obtain one at https://mozilla.org/MPL/2.0/.

package syncthing_test

import (
	"testing"

	"github.com/syncthing/syncthing/lib/syncthing"
)

// In a package of its own, so that registering the wrong frame's package
// (that of RegisterLogFacility) fails the test.
func TestRegisterLogFacility(t *testing.T) {
	descr := syncthing.LogFacilities()["syncthing"]
	syncthing.RegisterLogFacility("Test facility")
	facilities := syncthing.LogFacilities()
	if got := facilities["syncthing_test"]; got != "Test facility" {
		t.Errorf("description %q", got)
	}
	if got := facilities["syncthing"]; got != descr {
		t.Errorf("the engine's description became %q", got)
	}
}
