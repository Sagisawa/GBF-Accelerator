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
	defaultDNSCacheHardTTL    = 5 * time.Minute
	defaultDNSCacheMaxEntries = 512
	defaultIPFailPenaltyTTL   = 30 * time.Second
	happyEyeballsFallbackWait = 250 * time.Millisecond
)

type dnsCacheEntry struct {
	ips           []net.IP
	softExpiresAt time.Time
	hardExpiresAt time.Time
	refreshing    bool
}

type dnsInFlight struct {
	done chan struct{}
	ips  []net.IP
	err  error
}

// dnsCache caches domain->IP resolutions for direct upstream dialing with
// SingleFlight deduplication, Stale-While-Revalidate (SWR) background refresh,
// and short-lived failed-IP demotion. Security policy checks (loopback/self-port
// protection) intentionally bypass this cache and resolve fresh via resolveHostIPs.
type dnsCache struct {
	mu        sync.RWMutex
	softTTL   time.Duration
	hardTTL   time.Duration
	maxSize   int
	entries   map[string]dnsCacheEntry
	inFlight  map[string]*dnsInFlight
	failedIPs map[string]time.Time
}

func newDNSCache(ttl time.Duration, maxSize int) *dnsCache {
	return newDNSCacheWithSWR(ttl, ttl, maxSize)
}

func newDNSCacheWithSWR(softTTL, hardTTL time.Duration, maxSize int) *dnsCache {
	if softTTL <= 0 {
		softTTL = defaultDNSCacheTTL
	}
	if hardTTL < softTTL {
		hardTTL = softTTL
	}
	if maxSize <= 0 {
		maxSize = defaultDNSCacheMaxEntries
	}
	return &dnsCache{
		softTTL:   softTTL,
		hardTTL:   hardTTL,
		maxSize:   maxSize,
		entries:   make(map[string]dnsCacheEntry, 64),
		inFlight:  make(map[string]*dnsInFlight, 16),
		failedIPs: make(map[string]time.Time, 16),
	}
}

func (c *dnsCache) storeLocked(key string, ips []net.IP, now time.Time) []net.IP {
	copied := make([]net.IP, len(ips))
	copy(copied, ips)

	if len(c.entries) >= c.maxSize {
		for k, v := range c.entries {
			if !now.Before(v.hardExpiresAt) {
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
		ips:           copied,
		softExpiresAt: now.Add(c.softTTL),
		hardExpiresAt: now.Add(c.hardTTL),
		refreshing:    false,
	}
	return copied
}

func (c *dnsCache) recordDialResult(ip net.IP, err error) {
	if c == nil || ip == nil {
		return
	}
	ipStr := ip.String()
	if err == nil {
		c.mu.RLock()
		_, hasFailed := c.failedIPs[ipStr]
		c.mu.RUnlock()
		if !hasFailed {
			return
		}
		c.mu.Lock()
		delete(c.failedIPs, ipStr)
		c.mu.Unlock()
		return
	}

	now := time.Now()
	c.mu.Lock()
	if len(c.failedIPs) >= c.maxSize {
		for k, exp := range c.failedIPs {
			if !now.Before(exp) {
				delete(c.failedIPs, k)
			}
		}
		if len(c.failedIPs) >= c.maxSize {
			for k := range c.failedIPs {
				delete(c.failedIPs, k)
				break
			}
		}
	}
	c.failedIPs[ipStr] = now.Add(defaultIPFailPenaltyTTL)
	c.mu.Unlock()
}

// rankIPs demotes recently failed IPs to the tail while preserving original order
// among healthy candidates. Fast-paths with zero allocations when no IP is penalized.
func (c *dnsCache) rankIPs(ips []net.IP, now time.Time) []net.IP {
	if c == nil || len(ips) <= 1 {
		return ips
	}
	c.mu.RLock()
	if len(c.failedIPs) == 0 {
		c.mu.RUnlock()
		return ips
	}
	anyPenalized := false
	for _, ip := range ips {
		if exp, ok := c.failedIPs[ip.String()]; ok && now.Before(exp) {
			anyPenalized = true
			break
		}
	}
	if !anyPenalized {
		c.mu.RUnlock()
		return ips
	}
	healthy := make([]net.IP, 0, len(ips))
	penalized := make([]net.IP, 0, len(ips))
	for _, ip := range ips {
		if exp, ok := c.failedIPs[ip.String()]; ok && now.Before(exp) {
			penalized = append(penalized, ip)
		} else {
			healthy = append(healthy, ip)
		}
	}
	c.mu.RUnlock()
	if len(healthy) == 0 {
		return ips
	}
	return append(healthy, penalized...)
}

func (c *dnsCache) triggerBackgroundRefresh(key string, resolveFn func(context.Context, string) ([]net.IP, error)) {
	c.mu.Lock()
	ent, ok := c.entries[key]
	if !ok || ent.refreshing {
		c.mu.Unlock()
		return
	}
	if _, active := c.inFlight[key]; active {
		c.mu.Unlock()
		return
	}
	ent.refreshing = true
	c.entries[key] = ent
	call := &dnsInFlight{done: make(chan struct{})}
	c.inFlight[key] = call
	c.mu.Unlock()

	go func() {
		bgCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		ips, err := resolveFn(bgCtx, key)
		cancel()

		now := time.Now()
		c.mu.Lock()
		if err == nil && len(ips) > 0 {
			call.ips = c.storeLocked(key, ips, now)
		} else {
			call.err = err
			if cur, exists := c.entries[key]; exists {
				cur.refreshing = false
				c.entries[key] = cur
			}
		}
		delete(c.inFlight, key)
		close(call.done)
		c.mu.Unlock()
	}()
}

func (c *dnsCache) lookup(ctx context.Context, host string, resolveFn func(context.Context, string) ([]net.IP, error)) ([]net.IP, error) {
	if c == nil {
		return resolveFn(ctx, host)
	}
	key := strings.ToLower(strings.TrimRight(strings.Trim(host, "[]"), "."))
	now := time.Now()

	c.mu.RLock()
	if ent, ok := c.entries[key]; ok && len(ent.ips) > 0 {
		if now.Before(ent.softExpiresAt) {
			ips := ent.ips
			c.mu.RUnlock()
			return c.rankIPs(ips, now), nil
		}
		if now.Before(ent.hardExpiresAt) {
			ips := ent.ips
			needRefresh := !ent.refreshing
			c.mu.RUnlock()
			if needRefresh {
				c.triggerBackgroundRefresh(key, resolveFn)
			}
			return c.rankIPs(ips, now), nil
		}
	}
	c.mu.RUnlock()

	// Hard miss or hard expiration: coalesce concurrent callers via SingleFlight.
	c.mu.Lock()
	if ent, ok := c.entries[key]; ok && len(ent.ips) > 0 && now.Before(ent.hardExpiresAt) {
		ips := ent.ips
		needRefresh := !now.Before(ent.softExpiresAt) && !ent.refreshing
		c.mu.Unlock()
		if needRefresh {
			c.triggerBackgroundRefresh(key, resolveFn)
		}
		return c.rankIPs(ips, now), nil
	}
	if existing, ok := c.inFlight[key]; ok {
		c.mu.Unlock()
		if ctx == nil {
			<-existing.done
			if existing.err != nil {
				return nil, existing.err
			}
			return c.rankIPs(existing.ips, time.Now()), nil
		}
		select {
		case <-existing.done:
			if existing.err != nil {
				return nil, existing.err
			}
			return c.rankIPs(existing.ips, time.Now()), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	call := &dnsInFlight{done: make(chan struct{})}
	c.inFlight[key] = call
	c.mu.Unlock()

	ips, err := resolveFn(ctx, key)
	resolveNow := time.Now()

	c.mu.Lock()
	if err == nil && len(ips) > 0 {
		call.ips = c.storeLocked(key, ips, resolveNow)
	} else {
		call.err = err
	}
	delete(c.inFlight, key)
	close(call.done)
	c.mu.Unlock()

	if err != nil || len(ips) == 0 {
		return ips, err
	}
	return c.rankIPs(call.ips, resolveNow), nil
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

func dialSequentialIPs(ctx context.Context, d *net.Dialer, network, port string, ips []net.IP, onResult func(net.IP, error)) (net.Conn, error) {
	var lastErr error
	for _, ip := range ips {
		if err := ctx.Err(); err != nil {
			if lastErr != nil {
				return nil, lastErr
			}
			return nil, err
		}
		conn, err := d.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if onResult != nil && (err == nil || ctx.Err() == nil) {
			onResult(ip, err)
		}
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
func dialHappyEyeballs(ctx context.Context, d *net.Dialer, network, port string, ips []net.IP, onResult func(net.IP, error)) (net.Conn, error) {
	primaries, fallbacks := splitIPFamilies(ips)
	if len(fallbacks) == 0 {
		return dialSequentialIPs(ctx, d, network, port, primaries, onResult)
	}

	raceCtx, cancelRace := context.WithCancel(ctx)
	defer cancelRace()

	type dialResult struct {
		conn net.Conn
		err  error
	}
	resCh := make(chan dialResult, 2)
	pending := 1
	drainRemaining := func(count int) {
		if count <= 0 {
			return
		}
		go func() {
			for k := 0; k < count; k++ {
				r := <-resCh
				if r.err == nil && r.conn != nil {
					_ = r.conn.Close()
				}
			}
		}()
	}

	go func() {
		conn, err := dialSequentialIPs(raceCtx, d, network, port, primaries, onResult)
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
		pending++
		go func() {
			conn, err := dialSequentialIPs(raceCtx, d, network, port, fallbacks, onResult)
			resCh <- dialResult{conn: conn, err: err}
		}()
	}

	select {
	case r := <-resCh:
		pending--
		if r.err == nil {
			return r.conn, nil
		}
		primaryDone = true
		primaryErr = r.err
		startFallback()
	case <-timer.C:
		startFallback()
	case <-ctx.Done():
		cancelRace()
		drainRemaining(pending)
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
			pending--
			if r.err == nil {
				cancelRace()
				drainRemaining(pending)
				return r.conn, nil
			}
			lastErr = r.err
		case <-ctx.Done():
			cancelRace()
			drainRemaining(pending)
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
	var onDialResult func(net.IP, error)
	if cache != nil {
		onDialResult = cache.recordDialResult
	}
	return dialHappyEyeballs(ctx, d, network, port, ips, onDialResult)
}
