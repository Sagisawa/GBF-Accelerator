//go:build android

package proxy

import (
	"context"
	"net"
	"time"
)

var defaultAndroidDNSServers = []string{
	"223.5.5.5:53",
	"1.1.1.1:53",
	"8.8.8.8:53",
	"119.29.29.29:53",
}

func init() {
	net.DefaultResolver = &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			d := net.Dialer{Timeout: 3 * time.Second}
			var lastErr error
			for _, dnsServer := range defaultAndroidDNSServers {
				conn, err := d.DialContext(ctx, "udp", dnsServer)
				if err == nil {
					return conn, nil
				}
				lastErr = err
			}
			return nil, lastErr
		},
	}
}
