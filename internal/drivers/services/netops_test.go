package services

import (
	"net"
	"testing"
)

func TestDNSBLName(t *testing.T) {
	if name, ok := dnsblName(net.ParseIP("93.184.216.34"), "zen.spamhaus.org"); !ok || name != "34.216.184.93.zen.spamhaus.org" {
		t.Fatalf("name: %q", name)
	}
	if _, ok := dnsblName(net.ParseIP("::1"), "zen.spamhaus.org"); ok {
		t.Fatal("IPv6 accepted")
	}
}
