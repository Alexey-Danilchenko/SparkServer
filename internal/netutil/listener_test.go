package netutil

import (
	"net"
	"testing"
)

type testAddress string

func (address testAddress) Network() string { return "tcp" }
func (address testAddress) String() string  { return string(address) }

func TestAdvertisedAddressPreservesSpecificHost(t *testing.T) {
	address := AdvertisedAddress(testAddress("127.0.0.1:8080"))
	if address != "127.0.0.1:8080" {
		t.Fatalf("advertised address = %q", address)
	}
}

func TestAdvertisedAddressReplacesWildcardHost(t *testing.T) {
	address := AdvertisedAddress(testAddress("[::]:5683"))
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		t.Fatalf("split advertised address: %v", err)
	}
	if host == "" || net.ParseIP(host).IsUnspecified() {
		t.Fatalf("advertised host = %q", host)
	}
	if port != "5683" {
		t.Fatalf("advertised port = %q", port)
	}
}
