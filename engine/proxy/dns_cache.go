package proxy

import (
	"context"
	"net"
	"strings"
	"sync"
	"time"
)

const (
	defaultDNSCacheTTL        = 30 * time.Second
	defaultDNSCacheMaxEntries = 512
	happyEyeballsFallbackWait = 250 * time.Millisecond
)

type dnsCacheEntry struct {
	ips       []net.IP
	expiresAt time.Time
}

// dnsCache caches domain->IP resolutions for direct upstream dialing with a
// bounded short TTL and capacity cap. Security policy checks (loopback/self-port
// protection) intentionally bypass this cache and resolve fresh via resolveHostIPs.
type dnsCache struct {
	mu      sync.RWMutex
	ttl     time.Duration
	maxSize int
	entries map[string]dnsCacheEntry
}

func newDNSCache(ttl time.Duration, maxSize int) *dnsCache {
	if ttl <= 0 {
		ttl = defaultDNSCacheTTL
	}
	if maxSize <= 0 {
		maxSize = defaultDNSCacheMaxEntries
	}
	return &dnsCache{
		ttl:     ttl,
		maxSize: maxSize,
		entries: make(map[string]dnsCacheEntry, 64),
	}
}

func (c *dnsCache) lookup(ctx context.Context, host string, resolveFn func(context.Context, string) ([]net.IP, error)) ([]net.IP, error) {
	if c == nil {
		return resolveFn(ctx, host)
	}
	key := strings.ToLower(strings.TrimRight(strings.Trim(host, "[]"), "."))
	now := time.Now()

	c.mu.RLock()
	if ent, ok := c.entries[key]; ok && now.Before(ent.expiresAt) && len(ent.ips) > 0 {
		ips := ent.ips
		c.mu.RUnlock()
		return ips, nil
	}
	c.mu.RUnlock()

	ips, err := resolveFn(ctx, key)
	if err != nil || len(ips) == 0 {
		return ips, err
	}

	copied := make([]net.IP, len(ips))
	copy(copied, ips)

	c.mu.Lock()
	if len(c.entries) >= c.maxSize {
		for k, v := range c.entries {
			if !now.Before(v.expiresAt) {
				delete(c.entries, k)
			}
		}
		if len(c.entries) >= c.maxSize {
			for k := range c.entries {
				delete(c.entries, k)
				break
			}
		}
	}
	c.entries[key] = dnsCacheEntry{
		ips:       copied,
		expiresAt: time.Now().Add(c.ttl),
	}
	c.mu.Unlock()
	return copied, nil
}

func splitIPFamilies(ips []net.IP) (primaries, fallbacks []net.IP) {
	if len(ips) == 0 {
		return nil, nil
	}
	firstIsV4 := ips[0].To4() != nil
	for _, ip := range ips {
		if (ip.To4() != nil) == firstIsV4 {
			primaries = append(primaries, ip)
		} else {
			fallbacks = append(fallbacks, ip)
		}
	}
	return primaries, fallbacks
}

func dialSequentialIPs(ctx context.Context, d *net.Dialer, network, port string, ips []net.IP) (net.Conn, error) {
	var lastErr error
	for _, ip := range ips {
		if err := ctx.Err(); err != nil {
			if lastErr != nil {
				return nil, lastErr
			}
			return nil, err
		}
		conn, err := d.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		return nil, &net.OpError{Op: "dial", Net: network, Err: net.ErrClosed}
	}
	return nil, lastErr
}

// dialHappyEyeballs dials a resolved set of IPs using RFC 8305-style staggered
// dual-stack racing when both IPv4 and IPv6 addresses are available.
func dialHappyEyeballs(ctx context.Context, d *net.Dialer, network, port string, ips []net.IP) (net.Conn, error) {
	primaries, fallbacks := splitIPFamilies(ips)
	if len(fallbacks) == 0 {
		return dialSequentialIPs(ctx, d, network, port, primaries)
	}

	raceCtx, cancelRace := context.WithCancel(ctx)
	defer cancelRace()

	type dialResult struct {
		conn net.Conn
		err  error
	}
	resCh := make(chan dialResult, 2)

	go func() {
		conn, err := dialSequentialIPs(raceCtx, d, network, port, primaries)
		resCh <- dialResult{conn: conn, err: err}
	}()

	timer := time.NewTimer(happyEyeballsFallbackWait)
	defer timer.Stop()

	var primaryErr error
	primaryDone := false
	fallbackStarted := false

	startFallback := func() {
		if fallbackStarted {
			return
		}
		fallbackStarted = true
		go func() {
			conn, err := dialSequentialIPs(raceCtx, d, network, port, fallbacks)
			resCh <- dialResult{conn: conn, err: err}
		}()
	}

	select {
	case r := <-resCh:
		if r.err == nil {
			return r.conn, nil
		}
		primaryDone = true
		primaryErr = r.err
		startFallback()
	case <-timer.C:
		startFallback()
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	remaining := 2
	if primaryDone {
		remaining = 1
	}
	var lastErr error = primaryErr
	for i := 0; i < remaining; i++ {
		select {
		case r := <-resCh:
			if r.err == nil {
				cancelRace()
				// Drain the other racer cleanly if it also succeeded concurrently.
				if i == 0 && remaining == 2 {
					go func() {
						r2 := <-resCh
						if r2.err == nil && r2.conn != nil {
							_ = r2.conn.Close()
						}
					}()
				}
				return r.conn, nil
			}
			lastErr = r.err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return nil, lastErr
}

func (s *ProxyServer) directDialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	if s != nil && s.dialContextFn != nil {
		return s.dialContextFn(ctx, network, addr)
	}
	d := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 15 * time.Second}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return d.DialContext(ctx, network, addr)
	}
	trimmed := strings.Trim(host, "[]")
	if net.ParseIP(trimmed) != nil {
		return d.DialContext(ctx, network, addr)
	}
	var cache *dnsCache
	var resolveFn func(context.Context, string) ([]net.IP, error)
	if s != nil {
		cache = s.dnsCache
		resolveFn = s.resolveHostIPs
	} else {
		resolveFn = func(rCtx context.Context, h string) ([]net.IP, error) {
			addrs, lErr := net.DefaultResolver.LookupIPAddr(rCtx, h)
			if lErr != nil {
				return nil, lErr
			}
			ips := make([]net.IP, len(addrs))
			for i, a := range addrs {
				ips[i] = a.IP
			}
			return ips, nil
		}
	}
	ips, lErr := cache.lookup(ctx, trimmed, resolveFn)
	if lErr != nil || len(ips) == 0 {
		return d.DialContext(ctx, network, addr)
	}
	return dialHappyEyeballs(ctx, d, network, port, ips)
}
