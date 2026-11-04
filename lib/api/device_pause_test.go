// Copyright (C) 2026 The Syncthing Authors.
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this file,
// You can obtain one at https://mozilla.org/MPL/2.0/.

package api

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/syncthing/syncthing/lib/config"
	"github.com/syncthing/syncthing/lib/events"
	"github.com/syncthing/syncthing/lib/protocol"
)

type pauseIntentConfig struct {
	config.Wrapper
	devices []protocol.DeviceID
	paused  bool
	calls   int
	err     error
}

func (c *pauseIntentConfig) SetDevicePause(devices []protocol.DeviceID, paused bool) error {
	c.devices = slices.Clone(devices)
	c.paused = paused
	c.calls++
	return c.err
}

func TestDevicePauseController(t *testing.T) {
	self := protocol.LocalDeviceID
	peer := protocol.NewDeviceID([]byte("pause intent peer"))
	other := protocol.NewDeviceID([]byte("another pause intent peer"))
	unknown := protocol.NewDeviceID([]byte("unconfigured pause intent peer"))
	for _, tc := range []struct {
		name, device string
		paused       bool
		hookError    error
		status       int
		want         []protocol.DeviceID
	}{
		{"already paused", peer.String(), true, nil, http.StatusOK, []protocol.DeviceID{peer}},
		{"resume", peer.String(), false, nil, http.StatusOK, []protocol.DeviceID{peer}},
		{"pause all", "", true, nil, http.StatusOK, []protocol.DeviceID{peer, other}},
		{"resume all", "", false, nil, http.StatusOK, []protocol.DeviceID{peer, other}},
		{"self", self.String(), true, nil, http.StatusOK, nil},
		{"invalid", "invalid", true, nil, http.StatusInternalServerError, nil},
		{"unknown", unknown.String(), true, nil, http.StatusNotFound, nil},
		{"hook failure", peer.String(), true, errors.New("could not persist intent"), http.StatusInternalServerError, []protocol.DeviceID{peer}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.New(self)
			for _, id := range []protocol.DeviceID{peer, other} {
				cfg.SetDevice(config.DeviceConfiguration{DeviceID: id, Paused: true})
			}
			wrapped := &pauseIntentConfig{Wrapper: config.Wrap("", cfg, self, events.NoopLogger), err: tc.hookError}
			srv := &service{id: self, cfg: wrapped}
			req := httptest.NewRequest(http.MethodPost, "/rest/system/pause?device="+tc.device, nil)
			response := httptest.NewRecorder()
			srv.makeDevicePauseHandler(tc.paused)(response, req)
			if response.Code != tc.status {
				t.Fatalf("status %d: %s", response.Code, response.Body.String())
			}
			if len(wrapped.devices) != len(tc.want) {
				t.Fatalf("intent targets %v, want %v", wrapped.devices, tc.want)
			}
			for _, id := range tc.want {
				if !slices.Contains(wrapped.devices, id) {
					t.Fatalf("missing explicit intent for %v", id)
				}
			}
			if len(tc.want) != 0 && (wrapped.calls != 1 || wrapped.paused != tc.paused) {
				t.Fatalf("lost explicit intent: calls=%d paused=%v", wrapped.calls, wrapped.paused)
			}
			if len(tc.want) == 0 && wrapped.calls != 0 {
				t.Fatal("invalid or local device reached the remote pause controller")
			}
		})
	}
}

func TestDevicePauseWithoutController(t *testing.T) {
	peer := protocol.NewDeviceID([]byte("ordinary pause peer"))
	cfg := config.New(protocol.LocalDeviceID)
	cfg.SetDevice(config.DeviceConfiguration{DeviceID: peer})
	wrapper := startConfig(t, cfg)
	srv := &service{id: protocol.LocalDeviceID, cfg: wrapper}
	for _, paused := range []bool{true, false} {
		response := httptest.NewRecorder()
		srv.makeDevicePauseHandler(paused)(response, httptest.NewRequest(http.MethodPost, "/rest/system/pause?device="+peer.String(), nil))
		if response.Code != http.StatusOK {
			t.Fatal(response.Code, response.Body.String())
		}
		device, ok := wrapper.Device(peer)
		if !ok || device.Paused != paused {
			t.Fatalf("ordinary configuration pause=%v, want %v", device.Paused, paused)
		}
	}
}
