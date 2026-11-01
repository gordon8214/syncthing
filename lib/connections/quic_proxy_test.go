// Copyright (C) 2026 The Syncthing Authors.
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this file,
// You can obtain one at https://mozilla.org/MPL/2.0/.

//go:build go1.15 && !noquic

package connections

import (
	"context"
	"crypto/tls"
	"errors"
	"net/url"
	"testing"

	"github.com/syncthing/syncthing/lib/connections/registry"
	"github.com/syncthing/syncthing/lib/dialer"
	"github.com/syncthing/syncthing/lib/protocol"
)

func TestQUICLeavesProxiedHostsAlone(t *testing.T) {
	proxy, err := dialer.NewProxy("socks5://127.0.0.1:1080", []string{"192.0.2.0/24"})
	if err != nil {
		t.Fatal(err)
	}
	dialer.SetProxy(proxy)
	t.Cleanup(func() { dialer.SetProxy(nil) })

	quic := &quicDialer{commonDialer: commonDialer{tlsCfg: &tls.Config{}}, registry: registry.New()}
	// Nothing gets sent: this fails right after it got past the proxy
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	dial := func(address string) error {
		uri, err := url.Parse(address)
		if err != nil {
			t.Fatal(err)
		}
		_, err = quic.Dial(canceled, protocol.EmptyDeviceID, uri)
		return err
	}

	if err := dial("quic://198.51.100.1:22000"); !errors.Is(err, errQUICProxied) {
		t.Errorf("a host behind the proxy: %v", err)
	}
	if err := dial("quic://name.invalid:22000"); !errors.Is(err, errQUICProxied) {
		t.Errorf("a name behind the proxy: %v", err)
	}
	if err := dial("quic://192.0.2.1:22000"); err == nil || errors.Is(err, errQUICProxied) {
		t.Errorf("a host on the bypass list: %v", err)
	}
}
