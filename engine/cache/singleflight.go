package cache

import (
	"context"
	"fmt"
	"sync"
)

type call struct {
	done    chan struct{}
	val     interface{}
	err     error
	dups    int
	waiters int
}

const singleFlightShardCount = 16

type singleFlightShard struct {
	mu sync.Mutex
	m  map[string]*call
}

type SingleFlight struct {
	shards [singleFlightShardCount]singleFlightShard
}

func NewSingleFlight() *SingleFlight {
	g := &SingleFlight{}
	for i := range g.shards {
		g.shards[i].m = make(map[string]*call)
	}
	return g
}

func (g *SingleFlight) shard(key string) *singleFlightShard {
	return &g.shards[fnv32(key)&(singleFlightShardCount-1)]
}

// Do executes and returns the results of the given function, making
// sure that only one execution is in-flight for a given key at a
// time. If a duplicate comes in, the duplicate caller waits for the
// original to complete and receives the same results.
func (g *SingleFlight) Do(key string, fn func() (interface{}, error)) (interface{}, error) {
	return g.DoContext(context.Background(), key, fn)
}

// DoContext executes fn with context cancellation awareness for duplicate callers.
// If another call for the same key is in-flight, the duplicate caller waits for
// the completion channel or until ctx is cancelled.
func (g *SingleFlight) DoContext(ctx context.Context, key string, fn func() (interface{}, error)) (interface{}, error) {
	if ctx != nil && ctx.Err() != nil {
		return nil, ctx.Err()
	}

	shard := g.shard(key)
	shard.mu.Lock()
	if c, ok := shard.m[key]; ok {
		c.dups++
		c.waiters++
		shard.mu.Unlock()
		if ctx == nil {
			<-c.done
			return c.val, c.err
		}
		select {
		case <-c.done:
			return c.val, c.err
		case <-ctx.Done():
			shard.mu.Lock()
			c.waiters--
			shard.mu.Unlock()
			return nil, ctx.Err()
		}
	}

	c := &call{
		done: make(chan struct{}),
	}
	shard.m[key] = c
	shard.mu.Unlock()

	var (
		normalReturn bool
		recovered    interface{}
	)

	defer func() {
		if !normalReturn && recovered == nil {
			recovered = recover()
			if recovered != nil {
				c.err = fmt.Errorf("singleflight panic: %v", recovered)
			}
		}
		shard.mu.Lock()
		delete(shard.m, key)
		close(c.done)
		shard.mu.Unlock()

		if recovered != nil {
			panic(recovered)
		}
	}()

	c.val, c.err = fn()
	normalReturn = true
	return c.val, c.err
}

// IsInFlight reports whether a call for key is currently in-flight.
func (g *SingleFlight) IsInFlight(key string) bool {
	shard := g.shard(key)
	shard.mu.Lock()
	defer shard.mu.Unlock()
	_, ok := shard.m[key]
	return ok
}

// Waiters reports the number of currently active, uncancelled followers waiting for key (excluding the leader).
// Returns -1 if key is not in-flight.
func (g *SingleFlight) Waiters(key string) int {
	shard := g.shard(key)
	shard.mu.Lock()
	defer shard.mu.Unlock()
	if c, ok := shard.m[key]; ok {
		return c.waiters
	}
	return -1
}

// Forget tells singleflight to forget about a key. Future calls
// with this key will call the function rather than waiting on an earlier call.
func (g *SingleFlight) Forget(key string) {
	shard := g.shard(key)
	shard.mu.Lock()
	defer shard.mu.Unlock()
	delete(shard.m, key)
}
