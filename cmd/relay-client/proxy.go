package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	xproxy "golang.org/x/net/proxy"
)

// resolveProxySpec interprets the -proxy flag:
//
//	""      -> "" (direct)
//	none|off-> "" (direct)
//	auto    -> read standard env vars (ALL_PROXY > HTTPS_PROXY > HTTP_PROXY)
//	env     -> same as auto
//	<url>   -> that proxy url (http://, https://, socks5://, socks5h://)
//
// Returns "" for direct connection.
func resolveProxySpec(spec string, getenv func(string) string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(spec)) {
	case "", "none", "off":
		return "", nil
	case "env", "auto":
		for _, k := range []string{"ALL_PROXY", "all_proxy", "HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy"} {
			if v := strings.TrimSpace(getenv(k)); v != "" {
				return v, nil
			}
		}
		return "", nil
	}
	u, err := url.Parse(strings.TrimSpace(spec))
	if err != nil {
		return "", fmt.Errorf("parse proxy url: %w", err)
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https", "socks5", "socks5h":
		return u.String(), nil
	}
	return "", fmt.Errorf("unsupported proxy scheme %q (use http/https/socks5/socks5h)", u.Scheme)
}

// buildDialer constructs a websocket.Dialer honoring the resolved proxy spec.
// Direct: plain dialer. http(s): HTTP CONNECT tunnel. socks5(h): SOCKS tunnel
// (socks5h resolves DNS through the proxy too).
func buildDialer(spec string, handshakeTimeout time.Duration, getenv func(string) string) (*websocket.Dialer, error) {
	resolved, err := resolveProxySpec(spec, getenv)
	if err != nil {
		return nil, err
	}
	d := &websocket.Dialer{HandshakeTimeout: handshakeTimeout}
	if resolved == "" {
		return d, nil
	}
	u, _ := url.Parse(resolved)
	switch strings.ToLower(u.Scheme) {
	case "socks5", "socks5h":
		var auth *xproxy.Auth
		if u.User != nil {
			pw, _ := u.User.Password()
			auth = &xproxy.Auth{User: u.User.Username(), Password: pw}
		}
		pd, err := xproxy.SOCKS5("tcp", u.Host, auth, &net.Dialer{Timeout: 10 * time.Second})
		if err != nil {
			return nil, fmt.Errorf("socks5: %w", err)
		}
		ctxd, ok := pd.(xproxy.ContextDialer)
		if !ok {
			return nil, fmt.Errorf("socks5 dialer lacks context support")
		}
		d.NetDialContext = ctxd.DialContext
	default: // http / https CONNECT
		d.Proxy = func(*http.Request) (*url.URL, error) { return u, nil }
	}
	return d, nil
}

var _ = context.Background // context kept for future keepalive tuning
