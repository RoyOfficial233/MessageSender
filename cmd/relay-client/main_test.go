package main

import (
	"net/http"
	"net/url"
	"os"
	"testing"
	"time"
)

func TestResolveProxySpec(t *testing.T) {
	cases := []struct {
		name    string
		spec    string
		env     map[string]string
		want    string
		wantErr bool
	}{
		{"empty is direct", "", nil, "", false},
		{"none is direct", "none", nil, "", false},
		{"off is direct", "OFF", nil, "", false},
		{"env with nothing set", "env", nil, "", false},
		{"auto with nothing set", "auto", nil, "", false},
		{"env reads ALL_PROXY first", "env", map[string]string{"ALL_PROXY": "socks5://a:1", "HTTPS_PROXY": "http://b:2"}, "socks5://a:1", false},
		{"env falls back to HTTPS_PROXY", "env", map[string]string{"HTTPS_PROXY": "http://b:2"}, "http://b:2", false},
		{"env falls back to HTTP_PROXY", "env", map[string]string{"http_proxy": "http://c:3"}, "http://c:3", false},
		{"explicit url passes through", "socks5://x:1080", nil, "socks5://x:1080", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			getenv := func(k string) string { return c.env[k] }
			got, err := resolveProxySpec(c.spec, getenv)
			if c.wantErr {
				if err == nil {
					t.Fatalf("want error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("err: %v", err)
			}
			if got != c.want {
				t.Errorf("spec %q env %v: got %q want %q", c.spec, c.env, got, c.want)
			}
		})
	}
}

func TestBuildDialerDirect(t *testing.T) {
	d, err := buildDialer("none", time.Second, func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	if d.Proxy != nil {
		t.Error("direct dialer must not set Proxy")
	}
	if d.NetDialContext != nil {
		t.Error("direct dialer must not set NetDialContext")
	}
	if d.HandshakeTimeout != time.Second {
		t.Errorf("handshake timeout = %v", d.HandshakeTimeout)
	}
}

func TestBuildDialerHTTPProxy(t *testing.T) {
	d, err := buildDialer("http://127.0.0.1:7890", time.Second, func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	if d.Proxy == nil {
		t.Fatal("Proxy not set for http proxy")
	}
	u, err := d.Proxy(&http.Request{URL: &url.URL{Scheme: "wss", Host: "example.com"}})
	if err != nil {
		t.Fatal(err)
	}
	if u.String() != "http://127.0.0.1:7890" {
		t.Errorf("proxy url = %v", u)
	}
	if d.NetDialContext != nil {
		t.Error("http proxy should not set NetDialContext")
	}
}

func TestBuildDialerSocks5(t *testing.T) {
	d, err := buildDialer("socks5://127.0.0.1:7891", time.Second, func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	if d.NetDialContext == nil {
		t.Fatal("NetDialContext not set for socks5 proxy")
	}
	if d.Proxy != nil {
		t.Error("socks5 should not use http Proxy field")
	}
}

func TestBuildDialerSocks5hFromEnv(t *testing.T) {
	env := func(k string) string {
		if k == "ALL_PROXY" {
			return "socks5h://127.0.0.1:7892"
		}
		return ""
	}
	d, err := buildDialer("env", time.Second, env)
	if err != nil {
		t.Fatal(err)
	}
	if d.NetDialContext == nil {
		t.Fatal("NetDialContext not set for socks5h from env")
	}
}

func TestBuildDialerErrors(t *testing.T) {
	if _, err := buildDialer("ftp://x:1", time.Second, func(string) string { return "" }); err == nil {
		t.Error("ftp scheme should be rejected")
	}
	if _, err := buildDialer("://bad", time.Second, func(string) string { return "" }); err == nil {
		t.Error("malformed url should be rejected")
	}
}

func TestBuildDialerRealEnv(t *testing.T) {
	// Uses the actual process environment — must never error.
	d, err := buildDialer("env", time.Second, os.Getenv)
	if err != nil {
		t.Fatalf("env mode with real environment: %v", err)
	}
	_ = d
}
