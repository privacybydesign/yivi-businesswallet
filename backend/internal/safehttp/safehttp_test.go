package safehttp

import (
	"net"
	"testing"
)

func TestIsPublic(t *testing.T) {
	private := []string{"127.0.0.1", "10.1.2.3", "172.16.0.1", "192.168.1.1", "169.254.169.254", "100.64.0.1", "0.0.0.0", "::1", "fe80::1", "fc00::1"}
	for _, ip := range private {
		if IsPublic(net.ParseIP(ip)) {
			t.Errorf("%s classified public", ip)
		}
	}
	for _, ip := range []string{"93.184.216.34", "2606:2800:220:1:248:1893:25c8:1946"} {
		if !IsPublic(net.ParseIP(ip)) {
			t.Errorf("%s classified non-public", ip)
		}
	}
}
