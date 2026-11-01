// Copyright (C) 2026 The Syncthing Authors.
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this file,
// You can obtain one at https://mozilla.org/MPL/2.0/.

package dialer

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/syncthing/syncthing/lib/connections/registry"
)

// testProxy is a proxy on a local port that takes every connection to backend, and records where it was asked to go
type testProxy struct {
	listener net.Listener
	backend  string
	mutex    sync.Mutex
	targets  []string
	// The user and password it was given, and an HTTP request's Proxy-Authorization
	credentials string
}

func newTestProxy(t *testing.T, backend string, serve func(*testProxy, net.Conn)) *testProxy {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	p := &testProxy{listener: listener, backend: backend}
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				serve(p, conn)
			}()
		}
	}()
	return p
}

func (p *testProxy) record(target string) {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	p.targets = append(p.targets, target)
}

func (p *testProxy) recordCredentials(credentials string) {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	p.credentials = credentials
}

func (p *testProxy) seen() []string {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	return append([]string(nil), p.targets...)
}

func (p *testProxy) pipe(client io.ReadWriter) {
	backend, err := net.Dial("tcp", p.backend)
	if err != nil {
		return
	}
	defer backend.Close()
	go func() { _, _ = io.Copy(backend, client) }()
	_, _ = io.Copy(client, backend)
}

func serveSOCKS5(p *testProxy, conn net.Conn) {
	if readSOCKSGreeting(conn) != nil {
		return
	}
	if _, err := conn.Write([]byte{5, 0}); err != nil {
		return
	}
	serveSOCKSRequest(p, conn)
}

// serveSOCKS5Password wants a user and password, and records them
func serveSOCKS5Password(p *testProxy, conn net.Conn) {
	if readSOCKSGreeting(conn) != nil {
		return
	}
	if _, err := conn.Write([]byte{5, 2}); err != nil {
		return
	}
	read := func() string {
		length := make([]byte, 1)
		if _, err := io.ReadFull(conn, length); err != nil {
			return ""
		}
		value := make([]byte, length[0])
		_, _ = io.ReadFull(conn, value)
		return string(value)
	}
	version := make([]byte, 1)
	if _, err := io.ReadFull(conn, version); err != nil {
		return
	}
	user, password := read(), read()
	p.recordCredentials(user + ":" + password)
	if _, err := conn.Write([]byte{1, 0}); err != nil {
		return
	}
	serveSOCKSRequest(p, conn)
}

func readSOCKSGreeting(conn net.Conn) error {
	greeting := make([]byte, 2)
	if _, err := io.ReadFull(conn, greeting); err != nil {
		return err
	}
	_, err := io.CopyN(io.Discard, conn, int64(greeting[1]))
	return err
}

func serveSOCKSRequest(p *testProxy, conn net.Conn) {
	header := make([]byte, 4)
	if _, err := io.ReadFull(conn, header); err != nil {
		return
	}
	var host string
	switch header[3] {
	case 1:
		address := make([]byte, 4)
		if _, err := io.ReadFull(conn, address); err != nil {
			return
		}
		host = net.IP(address).String()
	case 3:
		length := make([]byte, 1)
		if _, err := io.ReadFull(conn, length); err != nil {
			return
		}
		name := make([]byte, length[0])
		if _, err := io.ReadFull(conn, name); err != nil {
			return
		}
		host = string(name)
	default:
		return
	}
	port := make([]byte, 2)
	if _, err := io.ReadFull(conn, port); err != nil {
		return
	}
	p.record(net.JoinHostPort(host, fmt.Sprint(binary.BigEndian.Uint16(port))))
	if _, err := conn.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0}); err != nil {
		return
	}
	p.pipe(conn)
}

func serveHTTPConnect(p *testProxy, conn net.Conn) {
	reader := bufio.NewReader(conn)
	request, err := http.ReadRequest(reader)
	if err != nil {
		return
	}
	p.record(request.Host)
	p.recordCredentials(request.Header.Get("Proxy-Authorization"))
	if _, err := io.WriteString(conn, "HTTP/1.1 200 OK\r\n\r\n"); err != nil {
		return
	}
	p.pipe(struct {
		io.Reader
		io.Writer
	}{reader, conn})
}

// echoServer answers a connection with what it receives
func echoServer(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				_, _ = io.Copy(conn, conn)
			}()
		}
	}()
	return listener.Addr().String()
}

func useProxy(t *testing.T, proxyURL string, bypass ...string) {
	t.Helper()
	p, err := NewProxy(proxyURL, bypass)
	if err != nil {
		t.Fatal(err)
	}
	SetProxy(p)
	t.Cleanup(func() { SetProxy(nil) })
}

func expectEcho(t *testing.T, conn net.Conn) {
	t.Helper()
	defer conn.Close()
	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	answer := make([]byte, 4)
	if _, err := io.ReadFull(conn, answer); err != nil {
		t.Fatal(err)
	}
	if string(answer) != "ping" {
		t.Fatalf("answer %q", answer)
	}
}

func testContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func TestProxyCarriesConnections(t *testing.T) {
	cases := []struct {
		scheme string
		serve  func(*testProxy, net.Conn)
	}{
		{"socks5", serveSOCKS5},
		{"socks5h", serveSOCKS5},
		{"socks", serveSOCKS5},
		{"http", serveHTTPConnect},
	}
	for _, tc := range cases {
		t.Run(tc.scheme, func(t *testing.T) {
			p := newTestProxy(t, echoServer(t), tc.serve)
			useProxy(t, tc.scheme+"://"+p.listener.Addr().String())

			// The name goes to the proxy unresolved: it is not one that resolves here
			conn, err := DialContext(testContext(t), "tcp", "peer.invalid:22000")
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := conn.(dialerConn); !ok {
				t.Errorf("connection of type %T", conn)
			}
			if conn.RemoteAddr().String() != "peer.invalid:22000" {
				t.Errorf("remote address %v", conn.RemoteAddr())
			}
			expectEcho(t, conn)
			if seen := p.seen(); len(seen) != 1 || seen[0] != "peer.invalid:22000" {
				t.Errorf("proxy saw %v", seen)
			}
		})
	}
}

func TestProxyBypass(t *testing.T) {
	backend := echoServer(t)
	p := newTestProxy(t, backend, serveSOCKS5)
	useProxy(t, "socks5://"+p.listener.Addr().String(), "127.0.0.0/8")

	conn, err := DialContext(testContext(t), "tcp", backend)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := conn.(*net.TCPConn); !ok {
		t.Errorf("a direct connection has its real address, but is a %T", conn)
	}
	expectEcho(t, conn)
	if seen := p.seen(); len(seen) != 0 {
		t.Errorf("proxy saw %v", seen)
	}
}

func TestProxyHasNoFallback(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closed := listener.Addr().String()
	listener.Close()
	backend := echoServer(t)
	useProxy(t, "socks5://"+closed)

	if conn, err := DialContext(testContext(t), "tcp", backend); err == nil {
		conn.Close()
		t.Fatal("reached the host without the proxy")
	}
}

func TestProxyLeavesUDPAlone(t *testing.T) {
	useProxy(t, "socks5://127.0.0.1:1")

	conn, err := DialContext(testContext(t), "udp4", "127.0.0.1:1900")
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()
}

// reusableRegistry is a registry with a listener whose port dials can reuse
func reusableRegistry(t *testing.T) (*registry.Registry, int) {
	t.Helper()
	config := net.ListenConfig{Control: ReusePortControl}
	listener, err := config.Listen(context.Background(), "tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	reg := registry.New()
	reg.Register("tcp", listener.Addr())
	return reg, listener.Addr().(*net.TCPAddr).Port
}

func TestProxyReplacesPortReuse(t *testing.T) {
	reg, _ := reusableRegistry(t)
	p := newTestProxy(t, echoServer(t), serveSOCKS5)
	useProxy(t, "socks5://"+p.listener.Addr().String())

	conn, err := DialContextReusePortFunc(reg)(testContext(t), "tcp", "peer.invalid:22000")
	if err != nil {
		t.Fatal(err)
	}
	expectEcho(t, conn)
	if seen := p.seen(); len(seen) != 1 {
		t.Errorf("proxy saw %v", seen)
	}
}

func TestPortReuseGoesOnForTheBypassList(t *testing.T) {
	reg, port := reusableRegistry(t)
	backend := echoServer(t)
	p := newTestProxy(t, backend, serveSOCKS5)
	useProxy(t, "socks5://"+p.listener.Addr().String(), "127.0.0.0/8")

	conn, err := DialContextReusePortFunc(reg)(testContext(t), "tcp", backend)
	if err != nil {
		t.Fatal(err)
	}
	if local := conn.LocalAddr().(*net.TCPAddr).Port; local != port {
		t.Errorf("dialed from port %d, not %d", local, port)
	}
	expectEcho(t, conn)
	if seen := p.seen(); len(seen) != 0 {
		t.Errorf("proxy saw %v", seen)
	}
}

func TestProxyTakesCredentials(t *testing.T) {
	t.Run("socks5", func(t *testing.T) {
		p := newTestProxy(t, echoServer(t), serveSOCKS5Password)
		useProxy(t, "socks5://alice:p%40ss@"+p.listener.Addr().String())
		conn, err := DialContext(testContext(t), "tcp", "peer.invalid:22000")
		if err != nil {
			t.Fatal(err)
		}
		expectEcho(t, conn)
		if p.credentials != "alice:p@ss" {
			t.Errorf("proxy got %q", p.credentials)
		}
	})
	t.Run("http", func(t *testing.T) {
		p := newTestProxy(t, echoServer(t), serveHTTPConnect)
		useProxy(t, "http://alice:p%40ss@"+p.listener.Addr().String())
		conn, err := DialContext(testContext(t), "tcp", "peer.invalid:22000")
		if err != nil {
			t.Fatal(err)
		}
		expectEcho(t, conn)
		if expected := "Basic " + base64.StdEncoding.EncodeToString([]byte("alice:p@ss")); p.credentials != expected {
			t.Errorf("proxy got %q", p.credentials)
		}
	})
}

func TestProxyAddressLeavesOutCredentials(t *testing.T) {
	p, err := NewProxy("socks5://token:secret@127.0.0.1:1080/", nil)
	if err != nil {
		t.Fatal(err)
	}
	if p.address != "socks5://127.0.0.1:1080" {
		t.Fatalf("address %q", p.address)
	}
}

func TestHTTPProxyConnectionTakesTCPOptions(t *testing.T) {
	p := newTestProxy(t, echoServer(t), serveHTTPConnect)
	useProxy(t, "http://"+p.listener.Addr().String())
	conn, err := DialContext(testContext(t), "tcp", "peer.invalid:22000")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := SetTCPOptions(conn); err != nil {
		t.Error(err)
	}
	// An IPv4 socket refuses the IPv6 half of this, which is no matter here
	if err := SetTrafficClass(conn, 0); err != nil && strings.Contains(err.Error(), "unknown connection type") {
		t.Error(err)
	}
}

func TestDialStopsWaitingForASilentHTTPProxy(t *testing.T) {
	silent := newTestProxy(t, "", func(_ *testProxy, conn net.Conn) {
		_, _ = io.Copy(io.Discard, conn)
	})
	useProxy(t, "http://"+silent.listener.Addr().String())

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(100*time.Millisecond, cancel)
	done := make(chan error, 1)
	go func() {
		_, err := DialContext(ctx, "tcp", "peer.invalid:22000")
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("connected through a proxy that never answered")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("still waiting for the proxy after the dial was canceled")
	}
}

// The environment is read once per process, so this runs in a process of its own
func TestEnvironmentProxyStaysAsItWas(t *testing.T) {
	const target = "peer.invalid:22000"
	if os.Getenv("DIALER_TEST_ENVIRONMENT") != "" {
		if !fallbackAllowed() || ProxySet() || Proxied("example.com") {
			t.Fatalf("fallback %v, set %v, proxied %v", fallbackAllowed(), ProxySet(), Proxied("example.com"))
		}
		conn, err := DialContext(testContext(t), "tcp", target)
		if err != nil {
			t.Fatal(err)
		}
		expectEcho(t, conn)
		return
	}

	p := newTestProxy(t, echoServer(t), serveSOCKS5)
	test := exec.Command(os.Args[0], "-test.run=^TestEnvironmentProxyStaysAsItWas$")
	test.Env = append(os.Environ(), "DIALER_TEST_ENVIRONMENT=1", "ALL_PROXY=socks5://"+p.listener.Addr().String())
	if output, err := test.CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, output)
	}
	if seen := p.seen(); len(seen) != 1 || seen[0] != target {
		t.Errorf("proxy saw %v", seen)
	}
}

func TestProxyGoesBackToTheEnvironment(t *testing.T) {
	backend := echoServer(t)
	p := newTestProxy(t, backend, serveSOCKS5)
	useProxy(t, "socks5://"+p.listener.Addr().String())
	if !Proxied("example.com") {
		t.Fatal("not proxied")
	}

	SetProxy(nil)
	if Proxied("example.com") {
		t.Fatal("still proxied")
	}
	conn, err := DialContext(testContext(t), "tcp", backend)
	if err != nil {
		t.Fatal(err)
	}
	expectEcho(t, conn)
	if seen := p.seen(); len(seen) != 0 {
		t.Errorf("proxy saw %v", seen)
	}
}

func TestNewProxyRefusesWhatItCantUse(t *testing.T) {
	for _, proxyURL := range []string{
		"", "proxy", "127.0.0.1:1080", "localhost:1080", "socks5://", "socks5://127.0.0.1", "ftp://example.com:21",
		"socks5://user:secret@", "http://user:secret@proxy", "socks5://user:secret@host:port", "\x7f://user:secret@host:1",
		"socks5://127.0.0.1:99999", "http://proxy:0", "socks5://host:1/path", "socks5://host:1?token=secret",
		"socks5://alice:12#secret@host:1080", "socks5://alice:12/secret@host:1080", "socks5://alice:12?secret@host:1080",
	} {
		_, err := NewProxy(proxyURL, nil)
		if err == nil {
			t.Errorf("%q was accepted", proxyURL)
		} else if !errors.Is(err, errInvalidProxyURL) || strings.Contains(err.Error(), "secret") {
			t.Errorf("%q: %v", proxyURL, err)
		}
	}
	for _, proxyURL := range []string{
		"socks5://127.0.0.1:1080", "socks5h://user:secret@proxy.example.com:1080", "socks://[::1]:1080",
		"http://proxy.example.com:8080", "https://user:secret@proxy.example.com:8443", "socks5://127.0.0.1:1080/",
	} {
		if _, err := NewProxy(proxyURL, nil); err != nil {
			t.Errorf("%q: %v", proxyURL, err)
		}
	}
}

func TestNewProxyRefusesAnEntryItCantUse(t *testing.T) {
	for _, entry := range []string{
		"10.0.0.0/33", "not/network", "two words", "*", ".", "*.", "..", "a..b", "*example.com", "exa*mple.com", "host:22000",
		"fe80::%en0/64", "localhost,127.0.0.1", "localhost;127.0.0.1",
	} {
		if _, err := NewProxy("socks5://127.0.0.1:1080", []string{entry}); err == nil {
			t.Errorf("%q was accepted", entry)
		}
	}
}

func TestBypassListMatches(t *testing.T) {
	list, err := newBypassList([]string{
		"", " localhost ", "Example.ORG", "*.example.com", ".example.net", "trailing.example.", "192.0.2.1", "2001:db8::1",
		"10.1.2.3/8", "fe80::/10", "::ffff:172.16.0.0/108",
	})
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]bool{
		"localhost":           true,
		"LOCALHOST":           true,
		"localhost.":          true,
		"example.org":         true,
		"www.example.org":     false,
		"example.com":         true,
		"www.example.com":     true,
		"a.b.example.com":     true,
		"notexample.com":      false,
		"example.net":         true,
		"www.example.net":     true,
		"trailing.example":    true,
		"192.0.2.1":           true,
		"192.0.2.2":           false,
		"::ffff:192.0.2.1":    true,
		"2001:db8::1":         true,
		"10.200.0.1":          true,
		"11.0.0.1":            false,
		"fe80::1":             true,
		"fe80::1%en0":         true,
		"fec0::1":             false,
		"172.16.1.1":          true,
		"172.32.1.1":          false,
		"":                    false,
		"proxy.example.world": false,
	}
	for host, expected := range cases {
		if list.contains(host) != expected {
			t.Errorf("%q: %v", host, !expected)
		}
	}
}
