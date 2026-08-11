// Package netutil provides shared network address helpers.
package netutil

import "net"

const routeProbeAddress = "192.0.2.1:80"

// AdvertisedAddress replaces a wildcard listener host with a reachable local IP.
func AdvertisedAddress(address net.Addr) string {
	host, port, err := net.SplitHostPort(address.String())
	if err != nil {
		return address.String()
	}

	listenerIP := net.ParseIP(host)
	if listenerIP == nil || !listenerIP.IsUnspecified() {
		return address.String()
	}

	return net.JoinHostPort(localIPv4().String(), port)
}

func localIPv4() net.IP {
	connection, err := net.Dial("udp4", routeProbeAddress)
	if err == nil {
		defer connection.Close()

		if address, ok := connection.LocalAddr().(*net.UDPAddr); ok && address.IP.IsGlobalUnicast() {
			return address.IP
		}
	}

	addresses, err := net.InterfaceAddrs()
	if err == nil {
		for _, address := range addresses {
			ip, _, parseErr := net.ParseCIDR(address.String())
			if parseErr == nil && ip.To4() != nil && ip.IsGlobalUnicast() {
				return ip
			}
		}
	}

	return net.IPv4(127, 0, 0, 1)
}
