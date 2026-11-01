// Copyright (C) 2026 The Syncthing Authors.
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this file,
// You can obtain one at https://mozilla.org/MPL/2.0/.

package connections

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/syncthing/syncthing/lib/config"
	"github.com/syncthing/syncthing/lib/dialer"
	"github.com/syncthing/syncthing/lib/events"
	"github.com/syncthing/syncthing/lib/protocol"
	relayproto "github.com/syncthing/syncthing/lib/relay/protocol"
	"github.com/syncthing/syncthing/lib/tlsutil"
)

func useProxy(t *testing.T, proxyURL string, bypass ...string) {
	t.Helper()
	proxy, err := dialer.NewProxy(proxyURL, bypass)
	if err != nil {
		t.Fatal(err)
	}
	dialer.SetProxy(proxy)
	t.Cleanup(func() { dialer.SetProxy(nil) })
}

func serveConnections(t *testing.T, listener net.Listener, serve func(net.Conn)) {
	t.Helper()
	t.Cleanup(func() { listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				serve(conn)
			}()
		}
	}()
}

func localListener(t *testing.T) net.Listener {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	return listener
}

func forwardTo(client io.ReadWriter, target string) {
	backend, err := net.Dial("tcp", target)
	if err != nil {
		return
	}
	defer backend.Close()
	go func() { _, _ = io.Copy(backend, client) }()
	_, _ = io.Copy(client, backend)
}

func serveSOCKS5(conn net.Conn) {
	greeting := make([]byte, 2)
	if _, err := io.ReadFull(conn, greeting); err != nil {
		return
	}
	if _, err := io.CopyN(io.Discard, conn, int64(greeting[1])); err != nil {
		return
	}
	_, _ = conn.Write([]byte{5, 0})
	// An IPv4 address: version, command, reserved, its type, then the address and port
	request := make([]byte, 10)
	if _, err := io.ReadFull(conn, request); err != nil || request[3] != 1 {
		return
	}
	_, _ = conn.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0})
	forwardTo(conn, net.JoinHostPort(net.IP(request[4:8]).String(), fmt.Sprint(binary.BigEndian.Uint16(request[8:]))))
}

func serveHTTPConnect(conn net.Conn) {
	reader := bufio.NewReader(conn)
	request, err := http.ReadRequest(reader)
	if err != nil {
		return
	}
	_, _ = io.WriteString(conn, "HTTP/1.1 200 OK\r\n\r\n")
	forwardTo(struct {
		io.Reader
		io.Writer
	}{reader, conn}, request.Host)
}

// A relay's two ports: the one that hands out an invitation, and the one where a session is joined, whose other end
// then takes a TLS handshake, as the device on the other side of a session would
func standInRelay(t *testing.T, cert tls.Certificate) *url.URL {
	t.Helper()
	serverTLS := &tls.Config{Certificates: []tls.Certificate{cert}}

	session := localListener(t)
	serveConnections(t, session, func(conn net.Conn) {
		if _, err := relayproto.ReadMessage(conn); err != nil {
			return
		}
		if relayproto.WriteMessage(conn, relayproto.ResponseSuccess) != nil {
			return
		}
		_ = tls.Server(conn, serverTLS).Handshake()
	})

	relay := tls.NewListener(localListener(t), &tls.Config{
		Certificates: []tls.Certificate{cert},
		NextProtos:   []string{relayproto.ProtocolName},
		ClientAuth:   tls.RequestClientCert,
	})
	serveConnections(t, relay, func(conn net.Conn) {
		if _, err := relayproto.ReadMessage(conn); err != nil {
			return
		}
		_ = relayproto.WriteMessage(conn, relayproto.SessionInvitation{
			From:    make([]byte, 32),
			Key:     make([]byte, 32),
			Address: net.IPv4(127, 0, 0, 1).To4(),
			Port:    uint16(session.Addr().(*net.TCPAddr).Port),
		})
	})

	uri, err := url.Parse("relay://" + relay.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	return uri
}

func TestRelayDialGoesThroughAProxy(t *testing.T) {
	cert, err := tlsutil.NewCertificateInMemory("relay", 1)
	if err != nil {
		t.Fatal(err)
	}
	relay := standInRelay(t, cert)
	relayDialer := &relayDialer{commonDialer{tlsCfg: &tls.Config{Certificates: []tls.Certificate{cert}, InsecureSkipVerify: true}}}

	for scheme, serve := range map[string]func(net.Conn){"socks5": serveSOCKS5, "http": serveHTTPConnect} {
		t.Run(scheme, func(t *testing.T) {
			proxy := localListener(t)
			var connections atomic.Int32
			serveConnections(t, proxy, func(conn net.Conn) {
				connections.Add(1)
				serve(conn)
			})
			useProxy(t, scheme+"://"+proxy.Addr().String())

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			conn, err := relayDialer.Dial(ctx, protocol.NewDeviceID(cert.Certificate[0]), relay)
			if err != nil {
				t.Fatal(err)
			}
			conn.Close()
			if count := connections.Load(); count != 2 {
				t.Fatalf("%d connections through the proxy, not one for the invitation and one for the session", count)
			}
		})
	}
}

// addressListener has fixed addresses
type addressListener struct {
	genericListener

	lan, wan []*url.URL
}

func (l addressListener) LANAddresses() []*url.URL { return l.lan }
func (l addressListener) WANAddresses() []*url.URL { return l.wan }

func TestDiscoveryOnlyHearsOfRelaysBehindAProxy(t *testing.T) {
	address := func(text string) *url.URL {
		uri, err := url.Parse(text)
		if err != nil {
			t.Fatal(err)
		}
		return uri
	}
	cfg := config.New(protocol.LocalDeviceID)
	cfg.Options.AnnounceLANAddresses = true
	relay := "relay://203.0.113.2:22067/?id=ABC"
	s := &service{
		cfg: config.Wrap("", cfg, protocol.LocalDeviceID, events.NoopLogger),
		listeners: map[string]genericListener{
			"tcp": addressListener{
				lan: []*url.URL{address("tcp://192.168.1.2:22000")},
				wan: []*url.URL{address("tcp://0.0.0.0:22000"), address("tcp://198.51.100.7:22000")},
			},
			"quic":  addressListener{wan: []*url.URL{address("quic://198.51.100.7:22000")}},
			"relay": addressListener{wan: []*url.URL{address(relay)}},
		},
	}

	if addresses := s.ExternalAddresses(); len(addresses) != 5 {
		t.Fatalf("without a proxy: %v", addresses)
	}
	useProxy(t, "socks5://127.0.0.1:1080", "192.168.0.0/16")
	if addresses := s.ExternalAddresses(); !slices.Equal(addresses, []string{relay}) {
		t.Fatalf("with a proxy: %v", addresses)
	}
	if addresses := s.AllAddresses(); len(addresses) != 5 {
		t.Fatalf("local discovery goes on with all of them: %v", addresses)
	}
}

func TestProxiedNamesAreNotLookedUp(t *testing.T) {
	var lookups atomic.Int32
	previous := net.DefaultResolver
	net.DefaultResolver = &net.Resolver{PreferGo: true, Dial: func(context.Context, string, string) (net.Conn, error) {
		lookups.Add(1)
		return nil, errors.New("no lookups in this test")
	}}
	t.Cleanup(func() { net.DefaultResolver = previous })
	checker := &lanChecker{}

	useProxy(t, "socks5://127.0.0.1:1080")
	for _, host := range []string{"peer.invalid:22000", "peer.invalid", "192.168.1.2:22000"} {
		if checker.isLANHost(host) {
			t.Errorf("%s is local", host)
		}
	}
	if count := lookups.Load(); count != 0 {
		t.Fatalf("%d lookups", count)
	}

	dialer.SetProxy(nil)
	checker.isLANHost("peer.invalid:22000")
	if lookups.Load() == 0 {
		t.Fatal("no lookup without a proxy, so this proves nothing")
	}
}
