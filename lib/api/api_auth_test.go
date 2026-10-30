// Copyright (C) 2014 The Syncthing Authors.
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this file,
// You can obtain one at https://mozilla.org/MPL/2.0/.

package api

import (
	"fmt"
	"math"
	"net/http"
	"testing"
	"time"

	"github.com/syncthing/syncthing/internal/db"
	"github.com/syncthing/syncthing/internal/db/sqlite"
	"github.com/syncthing/syncthing/lib/config"
	"github.com/syncthing/syncthing/lib/protocol"
)

var guiCfg config.GUIConfiguration

func init() {
	guiCfg.User = "user"
	guiCfg.SetPassword("pass")
}

func TestStaticAuthOK(t *testing.T) {
	t.Parallel()

	ok := authStatic("user", "pass", guiCfg)
	if !ok {
		t.Fatalf("should pass auth")
	}
}

func TestSimpleAuthUsernameFail(t *testing.T) {
	t.Parallel()

	ok := authStatic("userWRONG", "pass", guiCfg)
	if ok {
		t.Fatalf("should fail auth")
	}
}

func TestStaticAuthPasswordFail(t *testing.T) {
	t.Parallel()

	ok := authStatic("user", "passWRONG", guiCfg)
	if ok {
		t.Fatalf("should fail auth")
	}
}

func TestFormatOptionalPercentS(t *testing.T) {
	t.Parallel()

	cases := []struct {
		template string
		username string
		expected string
	}{
		{"cn=%s,dc=some,dc=example,dc=com", "username", "cn=username,dc=some,dc=example,dc=com"},
		{"cn=fixedusername,dc=some,dc=example,dc=com", "username", "cn=fixedusername,dc=some,dc=example,dc=com"},
		{"cn=%%s,dc=%s,dc=example,dc=com", "username", "cn=%s,dc=username,dc=example,dc=com"},
		{"cn=%%s,dc=%%s,dc=example,dc=com", "username", "cn=%s,dc=%s,dc=example,dc=com"},
		{"cn=%s,dc=%s,dc=example,dc=com", "username", "cn=username,dc=username,dc=example,dc=com"},
	}

	for _, c := range cases {
		templatedDn := formatOptionalPercentS(c.template, c.username)
		if c.expected != templatedDn {
			t.Fatalf("result should be %s != %s", c.expected, templatedDn)
		}
	}
}

type mockClock struct {
	now time.Time
}

func (c *mockClock) Now() time.Time {
	c.now = c.now.Add(1) // time always ticks by at least 1 ns
	return c.now
}

func (c *mockClock) wind(t time.Duration) {
	c.now = c.now.Add(t)
}

func TestTokenManager(t *testing.T) {
	t.Parallel()

	mdb, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		mdb.Close()
	})
	kdb := db.NewMiscDB(mdb)
	clock := &mockClock{now: time.Now()}

	// Token manager keeps up to three tokens with a validity time of 24 hours.
	tm := newTokenManager("testTokens", kdb, 24*time.Hour, 3)
	tm.timeNow = clock.Now

	// Create three tokens
	t0 := tm.New()
	t1 := tm.New()
	t2 := tm.New()

	// Check that the tokens are valid
	if !tm.Check(t0) {
		t.Errorf("token %q should be valid", t0)
	}
	if !tm.Check(t1) {
		t.Errorf("token %q should be valid", t1)
	}
	if !tm.Check(t2) {
		t.Errorf("token %q should be valid", t2)
	}

	// Create a fourth token
	t3 := tm.New()
	// It should be valid
	if !tm.Check(t3) {
		t.Errorf("token %q should be valid", t3)
	}
	// But the first token should have been removed
	if tm.Check(t0) {
		t.Errorf("token %q should be invalid", t0)
	}

	// Wind the clock by 12 hours
	clock.wind(12 * time.Hour)
	// The second token should still be valid (and checking it will give it more life)
	if !tm.Check(t1) {
		t.Errorf("token %q should be valid", t1)
	}

	// Wind the clock by 12 hours
	clock.wind(12 * time.Hour)
	// The second token should still be valid
	if !tm.Check(t1) {
		t.Errorf("token %q should be valid", t1)
	}
	// But the third and fourth tokens should have expired
	if tm.Check(t2) {
		t.Errorf("token %q should be invalid", t2)
	}
	if tm.Check(t3) {
		t.Errorf("token %q should be invalid", t3)
	}
}

func TestTokenManagerNoExpiry(t *testing.T) {
	t.Parallel()

	mdb, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		mdb.Close()
	})
	kdb := db.NewMiscDB(mdb)
	clock := &mockClock{now: time.Now()}

	tm := newTokenManager("testTokensNoExpiry", kdb, -1, 3)
	tm.timeNow = clock.Now

	token := tm.New()
	if expiry, ok := tm.tokens.Tokens[token]; !ok || expiry != 0 {
		t.Fatalf("token should have no expiry, got %d", expiry)
	}

	clock.wind(365 * 24 * time.Hour)
	if !tm.Check(token) {
		t.Fatal("token should still be valid after long time when no-expiry is enabled")
	}
}

func TestTokenManagerClear(t *testing.T) {
	t.Parallel()

	mdb, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		mdb.Close()
	})
	kdb := db.NewMiscDB(mdb)

	tm := newTokenManager("testTokensClear", kdb, -1, 3)
	stale := tm.New() // this leaves a delayed save pending

	if err := tm.Clear(); err != nil {
		t.Fatal(err)
	}
	if tm.Check(stale) {
		t.Error("token should be gone")
	}
	if tm.saveTimer != nil {
		t.Error("a delayed save should no longer be pending")
	}

	// It's gone from the database as well, right away.
	if reloaded := newTokenManager("testTokensClear", kdb, -1, 3); reloaded.Check(stale) {
		t.Error("token should be gone after reloading")
	}

	// New tokens work as usual.
	if token := tm.New(); !tm.Check(token) {
		t.Error("new token should be valid")
	}
}

func TestTokenManagerBindCredentials(t *testing.T) {
	t.Parallel()

	mdb, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		mdb.Close()
	})
	kdb := db.NewMiscDB(mdb)

	// Each step is a run of the service, which starts from what is stored.
	run := func(fingerprint string) *tokenManager {
		t.Helper()
		tm := newTokenManager("testTokensBind", kdb, -1, 3)
		if err := tm.BindCredentials(fingerprint); err != nil {
			t.Fatal(err)
		}
		return tm
	}

	// Sessions from before we kept track of credentials could belong to any,
	// so they go.
	tm := newTokenManager("testTokensBind", kdb, -1, 3)
	untracked := tm.New()
	tm.scheduledSave()
	if run("one").Check(untracked) {
		t.Error("token of unknown credentials should be gone")
	}

	tm = run("one")
	token := tm.New()
	tm.scheduledSave()

	// With the same credentials, the token stays.
	if tm = run("one"); !tm.Check(token) {
		t.Error("token should survive a restart")
	}

	// A change we only find out about now, say to the config file while the
	// engine was stopped, ends it. Going back doesn't bring it back.
	if run("two").Check(token) {
		t.Error("token should be gone with the credentials changed")
	}
	if run("one").Check(token) {
		t.Error("token should not come back with the old credentials")
	}
}

func TestCredentialsFingerprint(t *testing.T) {
	t.Parallel()

	static := config.GUIConfiguration{User: "user", Password: "hash"}
	ldapMode := config.GUIConfiguration{User: "user", Password: "hash", AuthMode: config.AuthModeLDAP}
	ldapCfg := config.LDAPConfiguration{Address: "ldap.example.com:389", BindDN: "cn=%s"}

	changed := func(gui config.GUIConfiguration, fn func(gui *config.GUIConfiguration, ldap *config.LDAPConfiguration)) bool {
		newGUI, newLDAP := gui, ldapCfg
		fn(&newGUI, &newLDAP)
		return credentialsFingerprint(gui, ldapCfg) != credentialsFingerprint(newGUI, newLDAP)
	}

	cases := []struct {
		name     string
		gui      config.GUIConfiguration
		fn       func(gui *config.GUIConfiguration, ldap *config.LDAPConfiguration)
		expected bool
	}{
		{"static user", static, func(gui *config.GUIConfiguration, _ *config.LDAPConfiguration) { gui.User = "new" }, true},
		{"static password", static, func(gui *config.GUIConfiguration, _ *config.LDAPConfiguration) { gui.Password = "new" }, true},
		{"to LDAP", static, func(gui *config.GUIConfiguration, _ *config.LDAPConfiguration) { gui.AuthMode = config.AuthModeLDAP }, true},
		{"to static", ldapMode, func(gui *config.GUIConfiguration, _ *config.LDAPConfiguration) { gui.AuthMode = config.AuthModeStatic }, true},
		{"LDAP address", ldapMode, func(_ *config.GUIConfiguration, ldap *config.LDAPConfiguration) { ldap.Address = "new" }, true},
		{"LDAP bind DN", ldapMode, func(_ *config.GUIConfiguration, ldap *config.LDAPConfiguration) { ldap.BindDN = "new" }, true},
		{"LDAP transport", ldapMode, func(_ *config.GUIConfiguration, ldap *config.LDAPConfiguration) {
			ldap.Transport = config.LDAPTransportTLS
		}, true},
		{"LDAP verification", ldapMode, func(_ *config.GUIConfiguration, ldap *config.LDAPConfiguration) { ldap.InsecureSkipVerify = true }, true},
		{"LDAP search base", ldapMode, func(_ *config.GUIConfiguration, ldap *config.LDAPConfiguration) { ldap.SearchBaseDN = "new" }, true},
		{"LDAP search filter", ldapMode, func(_ *config.GUIConfiguration, ldap *config.LDAPConfiguration) { ldap.SearchFilter = "new" }, true},

		// These don't decide who can log in in the mode at hand.
		{"LDAP settings in static mode", static, func(_ *config.GUIConfiguration, ldap *config.LDAPConfiguration) { ldap.Address = "new" }, false},
		{"static user in LDAP mode", ldapMode, func(gui *config.GUIConfiguration, _ *config.LDAPConfiguration) { gui.User = "new" }, false},
		{"unrelated", static, func(gui *config.GUIConfiguration, _ *config.LDAPConfiguration) { gui.Theme = "new" }, false},
	}
	for _, tc := range cases {
		if got := changed(tc.gui, tc.fn); got != tc.expected {
			t.Errorf("%s: fingerprint changed: %v, expected %v", tc.name, got, tc.expected)
		}
	}
}

func TestSessionsAfterConfigChange(t *testing.T) {
	t.Parallel()

	type change = func(cfg *config.Configuration)
	authOff := func(cfg *config.Configuration) { cfg.GUI.User, cfg.GUI.Password = "", "" }
	authOn := func(cfg *config.Configuration) { cfg.GUI.User, cfg.GUI.Password = guiCfg.User, guiCfg.Password }

	cases := []struct {
		name    string
		changes []change
		valid   bool
	}{
		{"password", []change{func(cfg *config.Configuration) { _ = cfg.GUI.SetPassword("new") }}, false},
		{"user", []change{func(cfg *config.Configuration) { cfg.GUI.User = "new" }}, false},
		{"auth mode", []change{func(cfg *config.Configuration) { cfg.GUI.AuthMode = config.AuthModeLDAP }}, false},
		{"auth off and back on", []change{authOff, authOn}, false},
		{"theme", []change{func(cfg *config.Configuration) { cfg.GUI.Theme = "new" }}, true},
		{"session lifetime", []change{func(cfg *config.Configuration) { cfg.GUI.SessionCookieDurationS = 7200 }}, true},
	}

	for _, tc := range cases {
		for _, lifetimeS := range []int{3600, -1} { // the latter never expires
			t.Run(fmt.Sprintf("%s lifetime %d", tc.name, lifetimeS), func(t *testing.T) {
				t.Parallel()

				cfg := config.New(protocol.LocalDeviceID)
				cfg.GUI.RawAddress = "127.0.0.1:0"
				cfg.GUI.SessionCookieDurationS = lifetimeS
				authOn(&cfg)
				w := startConfig(t, cfg)
				svc, baseURL := startService(t, w, 0)

				cookies := logIn(t, baseURL, "user", "pass")
				if status := sessionStatus(t, baseURL, cookies); status != http.StatusOK {
					t.Fatalf("unexpected status %d with a new session", status)
				}

				for _, change := range tc.changes {
					changeConfig(t, w, change)
					baseURL = waitForRestart(t, svc)
				}

				if status := sessionStatus(t, baseURL, cookies); tc.valid != (status == http.StatusOK) {
					t.Errorf("unexpected status %d", status)
				}
			})
		}
	}
}

// logIn logs in as the GUI does, and returns the cookies that make the session.
func logIn(t *testing.T, baseURL, username, password string) []*http.Cookie {
	t.Helper()
	resp := httpPost(baseURL+"/rest/noauth/auth/password", map[string]string{"username": username, "password": password}, nil, t)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("unexpected status %d logging in", resp.StatusCode)
	}
	return resp.Cookies()
}

// sessionStatus returns the status of a request that requires a login.
func sessionStatus(t *testing.T, baseURL string, cookies []*http.Cookie) int {
	t.Helper()
	resp := httpGet(baseURL+"/meta.js", "", "", "", "", cookies, t)
	resp.Body.Close()
	return resp.StatusCode
}

func TestSessionCookieMaxAgeNoExpiry(t *testing.T) {
	t.Parallel()

	m := tokenCookieManager{
		guiCfg: config.GUIConfiguration{
			SessionCookieDurationS: -1,
		},
	}
	if got := m.sessionCookieMaxAge(); got != math.MaxInt32 {
		t.Fatalf("unexpected max age %d", got)
	}
}
