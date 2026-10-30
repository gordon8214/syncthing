// Copyright (C) 2026 The Syncthing Authors.
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this file,
// You can obtain one at https://mozilla.org/MPL/2.0/.

package api

import (
	"crypto/tls"
	"encoding/asn1"
	"errors"
	"io"
	"net"
	"net/http"
	"testing"

	ldap "github.com/go-ldap/ldap/v3"

	"github.com/syncthing/syncthing/lib/config"
	"github.com/syncthing/syncthing/lib/protocol"
	"github.com/syncthing/syncthing/lib/tlsutil"
)

// fakeLDAPServer speaks just enough LDAP to take any simple bind, after
// StartTLS if the client asks for that.
type fakeLDAPServer struct {
	addr        string
	tlsCfg      *tls.Config
	serverNames chan string // from the client hello of each TLS handshake
}

func startFakeLDAPServer(t *testing.T) *fakeLDAPServer {
	t.Helper()

	cert, err := tlsutil.NewCertificateInMemory("ldap.example.com", 1)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })

	s := &fakeLDAPServer{
		addr:        listener.Addr().String(),
		serverNames: make(chan string, 10),
	}
	s.tlsCfg = &tls.Config{
		Certificates: []tls.Certificate{cert},
		GetConfigForClient: func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
			s.serverNames <- hello.ServerName
			return nil, nil
		},
	}

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go s.serve(conn)
		}
	}()
	return s
}

func (s *fakeLDAPServer) serve(conn net.Conn) {
	defer func() { conn.Close() }()
	for {
		id, op, err := readLDAPMessage(conn)
		if err != nil {
			return
		}
		switch op {
		case ldap.ApplicationBindRequest:
			if _, err := conn.Write(ldapSuccess(id, ldap.ApplicationBindResponse)); err != nil {
				return
			}
		case ldap.ApplicationExtendedRequest: // the only one a client sends us is StartTLS
			if _, err := conn.Write(ldapSuccess(id, ldap.ApplicationExtendedResponse)); err != nil {
				return
			}
			tlsConn := tls.Server(conn, s.tlsCfg)
			if err := tlsConn.Handshake(); err != nil {
				return
			}
			conn = tlsConn
		default:
			return
		}
	}
}

// readLDAPMessage reads one message and returns its ID and operation.
func readLDAPMessage(r io.Reader) (id int, op uint8, err error) {
	header := make([]byte, 2)
	if _, err := io.ReadFull(r, header); err != nil {
		return 0, 0, err
	}
	length := int(header[1])
	if length&0x80 != 0 {
		lengthBytes := make([]byte, length&0x7f)
		if _, err := io.ReadFull(r, lengthBytes); err != nil {
			return 0, 0, err
		}
		length = 0
		for _, b := range lengthBytes {
			length = length<<8 | int(b)
		}
	}
	message := make([]byte, length)
	if _, err := io.ReadFull(r, message); err != nil {
		return 0, 0, err
	}
	rest, err := asn1.Unmarshal(message, &id)
	if err != nil {
		return 0, 0, err
	}
	if len(rest) == 0 {
		return 0, 0, errors.New("LDAP message without an operation")
	}
	return id, rest[0] & 0x1f, nil
}

// ldapSuccess returns a successful response with the given ID and operation.
func ldapSuccess(id int, op uint8) []byte {
	result := []byte{
		0x0a, 1, ldap.LDAPResultSuccess, // the result code
		0x04, 0, // the matched DN
		0x04, 0, // the diagnostic message
	}
	message := append([]byte{0x02, 1, byte(id), 0x60 | op, byte(len(result))}, result...)
	return append([]byte{0x30, byte(len(message))}, message...)
}

func TestLDAPStartTLS(t *testing.T) {
	t.Parallel()

	server := startFakeLDAPServer(t)
	_, port, err := net.SplitHostPort(server.addr)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.LDAPConfiguration{
		Address:   net.JoinHostPort("localhost", port),
		BindDN:    "cn=%s",
		Transport: config.LDAPTransportStartTLS,
	}

	expectServerName := func(t *testing.T) {
		t.Helper()
		select {
		case name := <-server.serverNames:
			if name != "localhost" {
				t.Errorf("unexpected server name %q in the TLS handshake", name)
			}
		default:
			t.Error("the client never started the TLS handshake")
		}
	}

	t.Run("verifying the certificate", func(t *testing.T) {
		// We don't trust the server's certificate, and authLDAP has no way to
		// be told to, so this can't succeed. What matters is that we get as
		// far as asking the server for it, and then say no.
		if authLDAP("user", "pass", cfg) {
			t.Error("logged in despite an untrusted certificate")
		}
		expectServerName(t)
	})

	t.Run("skipping verification", func(t *testing.T) {
		cfg := cfg
		cfg.InsecureSkipVerify = true
		if !authLDAP("user", "pass", cfg) {
			t.Error("login failed")
		}
		expectServerName(t)
	})
}

func TestLDAPChangeReachesRunningService(t *testing.T) {
	t.Parallel()

	server := startFakeLDAPServer(t)

	// Nothing listens here anymore
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	unreachable := listener.Addr().String()
	listener.Close()

	cfg := config.New(protocol.LocalDeviceID)
	cfg.GUI.RawAddress = "127.0.0.1:0"
	cfg.GUI.AuthMode = config.AuthModeLDAP
	cfg.LDAP = config.LDAPConfiguration{Address: unreachable, BindDN: "cn=%s"}
	w := startConfig(t, cfg)
	svc, baseURL := startService(t, w, 0)

	resp := httpPost(baseURL+"/rest/noauth/auth/password", map[string]string{"username": "user", "password": "pass"}, nil, t)
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("unexpected status %d logging in without an LDAP server", resp.StatusCode)
	}

	// Only the LDAP settings change, and the next login should go by them.
	changeConfig(t, w, func(cfg *config.Configuration) {
		cfg.LDAP.Address = server.addr
	})
	baseURL = waitForRestart(t, svc)
	cookies := logIn(t, baseURL, "user", "pass")

	// In LDAP mode, these settings are also the credentials.
	changeConfig(t, w, func(cfg *config.Configuration) {
		cfg.LDAP.BindDN = "uid=%s"
	})
	baseURL = waitForRestart(t, svc)
	if status := sessionStatus(t, baseURL, cookies); status == http.StatusOK {
		t.Error("the session should have ended")
	}
}
