package oauthas

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

// ClientMetadata is the public-client subset used by client ID metadata
// documents. Registration alone never conveys consent or resource access.
type ClientMetadata struct {
	ClientID                string   `json:"client_id"`
	ClientName              string   `json:"client_name"`
	RedirectURIs            []string `json:"redirect_uris"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
}

func PublicAddress(ip net.IP) bool {
	a, ok := netip.AddrFromSlice(ip)
	if !ok {
		return false
	}
	a = a.Unmap()
	if !a.IsGlobalUnicast() || a.IsPrivate() || a.IsLoopback() || a.IsLinkLocalUnicast() || a.IsLinkLocalMulticast() {
		return false
	}
	for _, s := range []string{"100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "2001:db8::/32", "2001::/23", "64:ff9b::/96"} {
		if netip.MustParsePrefix(s).Contains(a) {
			return false
		}
	}
	return true
}
func ValidRedirect(s string) bool {
	u, e := url.Parse(s)
	if e != nil || u.Host == "" || u.User != nil || u.Fragment != "" || len(s) > 4096 || strings.ContainsAny(u.Host, "; \t\r\n\"'*") {
		return false
	}
	if u.Scheme == "https" {
		return true
	}
	return u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" || u.Hostname() == "::1")
}

// FetchClientMetadata pins each connection to a checked DNS answer, rejects
// redirects, caps bytes/deadline and disables environment proxies. It must never
// use an unchecked second DNS lookup after validating a hostname.
func FetchClientMetadata(ctx context.Context, id string) (ClientMetadata, error) {
	var out ClientMetadata
	u, e := url.Parse(id)
	if e != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" || len(id) > 2048 {
		return out, fmt.Errorf("invalid HTTPS client metadata URL")
	}
	tr := &http.Transport{TLSHandshakeTimeout: 2 * time.Second, ResponseHeaderTimeout: 2 * time.Second, DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, e := net.SplitHostPort(addr)
		if e != nil {
			return nil, e
		}
		ips, e := net.DefaultResolver.LookupIPAddr(ctx, host)
		if e != nil || len(ips) == 0 {
			return nil, fmt.Errorf("metadata DNS unavailable")
		}
		for _, ip := range ips {
			if !PublicAddress(ip.IP) {
				return nil, fmt.Errorf("metadata address is not public")
			}
		}
		var last error
		for _, ip := range ips {
			c, e := (&net.Dialer{Timeout: 2 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ip.IP.String(), port))
			if e == nil {
				return c, nil
			}
			last = e
		}
		return nil, last
	}}
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr, Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return fmt.Errorf("metadata redirects are unsupported") }}
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, id, nil)
	if e != nil {
		return out, e
	}
	req.Header.Set("Accept", "application/json")
	resp, e := client.Do(req)
	if e != nil {
		return out, fmt.Errorf("metadata retrieval failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return out, fmt.Errorf("metadata retrieval failed")
	}
	b, e := io.ReadAll(io.LimitReader(resp.Body, 65537))
	if e != nil || len(b) > 65536 {
		return out, fmt.Errorf("metadata exceeds limit")
	}
	if json.Unmarshal(b, &out) != nil || out.ClientID != id || len(out.ClientName) > 200 || len(out.RedirectURIs) == 0 || len(out.RedirectURIs) > 10 || (out.TokenEndpointAuthMethod != "" && out.TokenEndpointAuthMethod != "none") {
		return out, fmt.Errorf("invalid public client metadata")
	}
	for _, r := range out.RedirectURIs {
		if !ValidRedirect(r) {
			return out, fmt.Errorf("invalid redirect")
		}
	}
	return out, nil
}
