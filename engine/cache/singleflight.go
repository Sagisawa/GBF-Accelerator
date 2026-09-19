package cache

import (
	"context"
	"fmt"
	"sync"
)

type call struct {
	done chan struct{}
	val  interface{}
	err  error
}

type SingleFlight struct {
	mu sync.Mutex
	m  map[string]*call
}

func NewSingleFlight() *SingleFlight {
	return &SingleFlight{
		m: make(map[string]*call),
	}
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

	g.mu.Lock()
	if c, ok := g.m[key]; ok {
		g.mu.Unlock()
		if ctx == nil {
			<-c.done
			return c.val, c.err
		}
		select {
		case <-c.done:
			return c.val, c.err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	c := &call{
		done: make(chan struct{}),
	}
	g.m[key] = c
	g.mu.Unlock()

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
		g.mu.Lock()
		delete(g.m, key)
		close(c.done)
		g.mu.Unlock()

		if recovered != nil {
			panic(recovered)
		}
	}()

	c.val, c.err = fn()
	normalReturn = true
	return c.val, c.err
}

// Forget tells singleflight to forget about a key. Future calls
// with this key will call the function rather than waiting on an earlier call.
func (g *SingleFlight) Forget(key string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.m, key)
}
