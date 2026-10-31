package oauthas

import (
	"context"
	"net"
	"testing"
)

func TestMetadataAddressBoundary(t *testing.T) {
	for _, s := range []string{"127.0.0.1", "::1", "10.0.0.1", "172.16.0.1", "192.168.1.1", "169.254.169.254", "100.100.100.100", "::ffff:127.0.0.1", "2001:db8::1", "64:ff9b::7f00:1", "0.0.0.0"} {
		if PublicAddress(net.ParseIP(s)) {
			t.Fatal("private/reserved address accepted", s)
		}
	}
	for _, s := range []string{"1.1.1.1", "2606:4700:4700::1111"} {
		if !PublicAddress(net.ParseIP(s)) {
			t.Fatal("public address rejected", s)
		}
	}
}
func TestClientMetadataRejectsUnsafeURLs(t *testing.T) {
	for _, s := range []string{"file:///etc/passwd", "http://example.com/metadata", "https://user:pass@example.com/client#fragment", "https://127.0.0.1:1/client"} {
		if _, e := FetchClientMetadata(context.Background(), s); e == nil {
			t.Fatal("unsafe URL accepted", s)
		}
	}
}
func TestRedirectPolicy(t *testing.T) {
	for _, s := range []string{"https://client.example/callback", "http://127.0.0.1:1234/callback", "http://[::1]:8080/callback"} {
		if !ValidRedirect(s) {
			t.Fatal(s)
		}
	}
	for _, s := range []string{"http://evil.example/callback", "javascript:alert(1)", "https://client.example/callback#fragment"} {
		if ValidRedirect(s) {
			t.Fatal(s)
		}
	}
}
