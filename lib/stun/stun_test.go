// Copyright (C) 2026 The Syncthing Authors.
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this file,
// You can obtain one at https://mozilla.org/MPL/2.0/.

package stun

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/syncthing/syncthing/lib/config"
	"github.com/syncthing/syncthing/lib/dialer"
	"github.com/syncthing/syncthing/lib/events"
	"github.com/syncthing/syncthing/lib/protocol"
)

// countingConn counts what would have left, and never receives anything
type countingConn struct {
	net.PacketConn

	writes atomic.Int32
	wrote  chan struct{} // gets a value for the first write
	closed chan struct{}
}

func (c *countingConn) WriteTo(p []byte, _ net.Addr) (int, error) {
	if c.writes.Add(1) == 1 {
		c.wrote <- struct{}{}
	}
	return len(p), nil
}

func (c *countingConn) ReadFrom([]byte) (int, net.Addr, error) {
	<-c.closed
	return 0, nil, net.ErrClosed
}

func (c *countingConn) LocalAddr() net.Addr {
	return &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1}
}

func (c *countingConn) SetDeadline(time.Time) error      { return nil }
func (c *countingConn) SetReadDeadline(time.Time) error  { return nil }
func (c *countingConn) SetWriteDeadline(time.Time) error { return nil }
func (c *countingConn) Close() error                     { return nil }

type ignoringSubscriber struct{}

func (ignoringSubscriber) OnNATTypeChanged(NATType)               {}
func (ignoringSubscriber) OnExternalAddressChanged(*Host, string) {}

// stunWrites is how much a service sends in a moment
func stunWrites(t *testing.T) int32 {
	t.Helper()
	cfg := config.New(protocol.LocalDeviceID)
	cfg.Options.RawStunServers = []string{"127.0.0.1:3478"}
	conn := &countingConn{wrote: make(chan struct{}, 1), closed: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	wrapper := config.Wrap("", cfg, protocol.LocalDeviceID, events.NoopLogger)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = New(wrapper, ignoringSubscriber{}, conn).Serve(ctx)
	}()
	select {
	case <-conn.wrote:
	case <-time.After(300 * time.Millisecond):
	}
	// Ends a discovery under way before the service stops, which doesn't wait for one
	close(conn.closed)
	time.Sleep(100 * time.Millisecond)
	cancel()
	<-done
	return conn.writes.Load()
}

func TestSTUNStopsForAProxy(t *testing.T) {
	if stunWrites(t) == 0 {
		t.Fatal("nothing is sent without a proxy, so this proves nothing")
	}

	proxy, err := dialer.NewProxy("socks5://127.0.0.1:1080", []string{"127.0.0.0/8"})
	if err != nil {
		t.Fatal(err)
	}
	dialer.SetProxy(proxy)
	t.Cleanup(func() { dialer.SetProxy(nil) })
	if writes := stunWrites(t); writes != 0 {
		t.Fatalf("%d packets with a proxy set", writes)
	}
}
