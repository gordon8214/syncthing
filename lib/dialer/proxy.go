// Copyright (C) 2026 The Syncthing Authors.
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this file,
// You can obtain one at https://mozilla.org/MPL/2.0/.

package dialer

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"unicode"

	"golang.org/x/net/proxy"
)

var errInvalidProxyURL = errors.New("not a proxy URL with a scheme, host and port, such as socks5://127.0.0.1:1080")

// override is what SetProxy set. Without one the proxy comes from the
// environment.
var override atomic.Pointer[Proxy]

// Proxy is a proxy for SetProxy, and the hosts that don't go through it.
type Proxy struct {
	dialer proxy.Dialer
	// Only where it is, as a user name can be a secret as well
	address string
	bypass  bypassList
}

// NewProxy checks a proxy URL (socks5, socks5h, socks, http or https) and a
// bypass list, whose entries are IP addresses, CIDR networks, host names, and
// zones such as .example.com or *.example.com, which also take in the zone's
// own name. Entries are compared with what is being dialed, so a host name
// doesn't match a network that it resolves to. Errors don't contain the URL,
// which can hold a password.
func NewProxy(proxyURL string, bypass []string) (*Proxy, error) {
	u, err := url.Parse(proxyURL)
	if err != nil || u.Hostname() == "" || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return nil, errInvalidProxyURL
	}
	if port, err := strconv.Atoi(u.Port()); err != nil || port < 1 || port > 65535 {
		return nil, errInvalidProxyURL
	}
	switch u.Scheme {
	case "socks", "socks5", "socks5h", "http", "https":
	default:
		return nil, errInvalidProxyURL
	}
	dialer, err := proxy.FromURL(u, proxy.Direct)
	if err != nil {
		return nil, errInvalidProxyURL
	}
	if _, ok := dialer.(proxy.ContextDialer); !ok {
		return nil, errUnexpectedInterfaceType
	}
	list, err := newBypassList(bypass)
	if err != nil {
		return nil, err
	}
	return &Proxy{dialer: dialer, address: u.Scheme + "://" + u.Host, bypass: list}, nil
}

// SetProxy replaces the proxy from the environment, for an application that
// embeds Syncthing: the environment is read once, when this package loads,
// so setting variables later does nothing. A proxy set here has no fallback
// to direct connections, whatever ALL_PROXY_NO_FALLBACK says, as TCP to a
// host outside the bypass list goes through the proxy or nowhere. Nil goes
// back to the environment. Connections that exist stay as they are, so call
// this while Syncthing is stopped. HTTP_PROXY and HTTPS_PROXY aren't replaced:
// HTTP clients still go through those, on top of what is set here.
func SetProxy(p *Proxy) {
	override.Store(p)
	if p != nil {
		slog.Info("Using a proxy", "proxy", p.address)
	}
}

// ProxySet reports whether SetProxy set a proxy.
func ProxySet() bool {
	return override.Load() != nil
}

// Proxied reports whether a proxy from SetProxy is what reaches host. UDP
// can't go through one, so QUIC has to leave such a host alone. A proxy from
// the environment doesn't count, as ALL_PROXY never covered that.
func Proxied(host string) bool {
	p := override.Load()
	return p != nil && !p.bypass.contains(host)
}

// proxyFor is the dialer that reaches addr, which is proxy.Direct unless a
// proxy has to be used.
func proxyFor(network, addr string) proxy.Dialer {
	p := override.Load()
	if p == nil {
		return proxy.FromEnvironment()
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	// UPnP uses UDP to see which of our addresses reaches the router
	if !strings.HasPrefix(network, "tcp") || p.bypass.contains(host) {
		return proxy.Direct
	}
	return p.dialer
}

// fallbackAllowed reports whether a failed dial through a proxy may be tried
// directly.
func fallbackAllowed() bool {
	return !noFallback && override.Load() == nil
}

// bypassList is what connects directly when a proxy is set.
type bypassList struct {
	networks []netip.Prefix
	hosts    []string
	zones    []string // with their leading dot
}

func newBypassList(entries []string) (bypassList, error) {
	var list bypassList
	for _, entry := range entries {
		entry = strings.ToLower(strings.TrimSpace(entry))
		if entry == "" {
			continue
		}
		// Before addresses, which would take what follows a zone (fe80::%en0/64) as part of it
		if strings.Contains(entry, "/") {
			network, err := netip.ParsePrefix(entry)
			if err != nil {
				return bypassList{}, fmt.Errorf("%q is not a network such as 192.168.0.0/16", entry)
			}
			if network.Addr().Is4In6() && network.Bits() >= 96 {
				network = netip.PrefixFrom(network.Addr().Unmap(), network.Bits()-96)
			}
			list.networks = append(list.networks, network.Masked())
			continue
		}
		if address, err := netip.ParseAddr(entry); err == nil {
			address = address.WithZone("").Unmap()
			list.networks = append(list.networks, netip.PrefixFrom(address, address.BitLen()))
			continue
		}
		name := strings.TrimSuffix(strings.TrimPrefix(entry, "*"), ".")
		if strings.HasPrefix(entry, "*") && !strings.HasPrefix(name, ".") {
			name = "*"
		}
		zone := strings.HasPrefix(name, ".")
		name = strings.TrimPrefix(name, ".")
		if !isHostName(name) {
			return bypassList{}, fmt.Errorf("%q is not an address, network or host name, with one entry per line", entry)
		}
		if zone {
			list.zones = append(list.zones, "."+name)
		} else {
			list.hosts = append(list.hosts, name)
		}
	}
	return list, nil
}

func isHostName(name string) bool {
	return name != "" && !strings.Contains(name, "..") && !strings.ContainsFunc(name, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '-' && r != '_' && r != '.'
	})
}

func (l bypassList) contains(host string) bool {
	if address, err := netip.ParseAddr(host); err == nil {
		address = address.WithZone("").Unmap()
		return slices.ContainsFunc(l.networks, func(network netip.Prefix) bool {
			return network.Contains(address)
		})
	}
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	return slices.Contains(l.hosts, host) || slices.ContainsFunc(l.zones, func(zone string) bool {
		return strings.HasSuffix(host, zone) || host == zone[1:]
	})
}
