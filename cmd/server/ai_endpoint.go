package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const aiDefaultBaseURL = "https://api.openai.com/v1"
const aiEndpoint = aiDefaultBaseURL + "/responses"

// A gateway may reflect its Authorization header even in a syntactically valid
// success response. Do not expose the stored credential to ordinary AI users.
func aiOutputContainsKey(text, key string) bool {
	if key == "" {
		return false
	}
	var value any
	if json.Unmarshal([]byte(text), &value) != nil {
		return strings.Contains(text, key)
	}
	var contains func(any) bool
	contains = func(value any) bool {
		switch v := value.(type) {
		case string:
			return strings.Contains(v, key)
		case []any:
			for _, nested := range v {
				if contains(nested) {
					return true
				}
			}
		case map[string]any:
			for name, nested := range v {
				if strings.Contains(name, key) || contains(nested) {
					return true
				}
			}
		}
		return false
	}
	return contains(value)
}

func invalidAIBaseURL() error {
	return aiFailure(422, "ai_invalid_base_url", "API 地址必须是公开 HTTPS 地址，不能包含账号、查询参数或片段")
}

// Only the origin/root needs a /v1 suffix. A gateway's explicit API prefix is
// preserved; /v1 and /v1/ never become /v1/v1. No secrets belong in this URL.
func normalizeAIBaseURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > 2048 || strings.ContainsAny(raw, "\\%?#\t\r\n\x00") {
		return "", invalidAIBaseURL()
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Opaque != "" || u.ForceQuery || u.RawQuery != "" || u.Fragment != "" {
		return "", invalidAIBaseURL()
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if ip, err := netip.ParseAddr(host); err == nil {
		if !aiPublicIP(ip) {
			return "", invalidAIBaseURL()
		}
		host = ip.Unmap().String()
	} else {
		if len(host) > 253 || !strings.Contains(host, ".") || strings.Trim(host, "0123456789.") == "" {
			return "", invalidAIBaseURL()
		}
		for _, suffix := range []string{".localhost", ".local", ".internal", ".lan", ".home", ".test", ".invalid", ".example"} {
			if strings.HasSuffix(host, suffix) {
				return "", invalidAIBaseURL()
			}
		}
		for _, label := range strings.Split(host, ".") {
			if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
				return "", invalidAIBaseURL()
			}
			for _, c := range label {
				if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
					return "", invalidAIBaseURL()
				}
			}
		}
	}
	port := u.Port()
	if port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return "", invalidAIBaseURL()
		}
		port = strconv.Itoa(n)
		if port == "443" {
			port = ""
		}
	} else if strings.HasSuffix(u.Host, ":") {
		return "", invalidAIBaseURL()
	}
	u.Host = host
	if strings.Contains(host, ":") {
		u.Host = "[" + host + "]"
	}
	if port != "" {
		u.Host = net.JoinHostPort(host, port)
	}
	path := strings.TrimRight(u.Path, "/")
	if path == "" {
		path = "/v1"
	}
	for _, segment := range strings.Split(strings.TrimPrefix(path, "/"), "/") {
		if segment == "" || segment == "." || segment == ".." {
			return "", invalidAIBaseURL()
		}
		for _, c := range segment {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.') {
				return "", invalidAIBaseURL()
			}
		}
	}
	u.Path, u.RawPath = path, ""
	return u.String(), nil
}

var aiNonPublicNetworks = func() []netip.Prefix {
	result := []netip.Prefix{}
	for _, cidr := range []string{"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16", "172.16.0.0/12", "192.0.0.0/24", "192.0.2.0/24", "192.88.99.0/24", "192.168.0.0/16", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "224.0.0.0/4", "240.0.0.0/4", "2001::/23", "2001:db8::/32", "2002::/16", "3fff::/20"} {
		result = append(result, netip.MustParsePrefix(cidr))
	}
	return result
}()

func aiPublicIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsValid() || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.Zone() != "" {
		return false
	}
	// IPv6 translation/tunneling ranges can encode a private IPv4 target.
	if ip.Is6() && !netip.MustParsePrefix("2000::/3").Contains(ip) {
		return false
	}
	for _, prefix := range aiNonPublicNetworks {
		if prefix.Contains(ip) {
			return false
		}
	}
	return true
}

type aiLookupIP func(context.Context, string) ([]net.IPAddr, error)
type aiDial func(context.Context, string, string) (net.Conn, error)

// Validate every DNS answer, then dial a concrete approved IP, not the name.
// TLS still verifies the original URL hostname. No proxy or alternate resolver
// is allowed to resolve the hostname again after the SSRF check.
func aiPinnedDial(authority string, lookup aiLookupIP, dial aiDial) aiDial {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		if network != "tcp" && network != "tcp4" && network != "tcp6" || address != authority {
			return nil, errors.New("AI destination rejected")
		}
		host, port, err := net.SplitHostPort(authority)
		if err != nil {
			return nil, errors.New("AI destination rejected")
		}
		addresses := []net.IPAddr{}
		if ip, err := netip.ParseAddr(host); err == nil {
			addresses = append(addresses, net.IPAddr{IP: net.IP(ip.AsSlice())})
		} else {
			addresses, err = lookup(ctx, host)
			if err != nil || len(addresses) == 0 || len(addresses) > 64 {
				return nil, errors.New("AI destination unavailable")
			}
		}
		for _, candidate := range addresses {
			ip, ok := netip.AddrFromSlice(candidate.IP)
			if !ok || candidate.Zone != "" || !aiPublicIP(ip) {
				return nil, errors.New("AI destination rejected")
			}
		}
		for _, candidate := range addresses {
			conn, err := dial(ctx, network, net.JoinHostPort(candidate.IP.String(), port))
			if err == nil {
				return conn, nil
			}
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
		}
		return nil, errors.New("AI destination unavailable")
	}
}

func (a *App) aiRequestClient(baseURL string) (string, *http.Client, error) {
	base, err := normalizeAIBaseURL(baseURL)
	if err != nil {
		return "", nil, err
	}
	u, _ := url.Parse(base)
	port := u.Port()
	if port == "" {
		port = "443"
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	transport := &http.Transport{Proxy: nil, DialContext: aiPinnedDial(net.JoinHostPort(u.Hostname(), port), net.DefaultResolver.LookupIPAddr, dialer.DialContext), TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}, TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 40 * time.Second, DisableKeepAlives: true, MaxResponseHeaderBytes: 64 << 10}
	client := &http.Client{Timeout: 45 * time.Second, Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	if a.aiHTTP != nil && a.aiHTTP.Transport != nil {
		// In-process test seam only; no API, setting or environment variable can
		// replace the pinned production transport or its redirect policy.
		client.Transport = a.aiHTTP.Transport
	}
	return base + "/responses", client, nil
}
