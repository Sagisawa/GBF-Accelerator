package cache

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSingleFlightCoalescing(t *testing.T) {
	sf := NewSingleFlight()
	var executed int32

	var wg sync.WaitGroup
	results := make([]string, 10)

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			val, err := sf.Do("test_key", func() (interface{}, error) {
				atomic.AddInt32(&executed, 1)
				time.Sleep(50 * time.Millisecond)
				return "coalesced_result", nil
			})
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			results[idx] = val.(string)
		}(i)
	}

	wg.Wait()

	if atomic.LoadInt32(&executed) != 1 {
		t.Errorf("expected 1 execution, got %d", executed)
	}
	for i, res := range results {
		if res != "coalesced_result" {
			t.Errorf("result %d: expected coalesced_result, got %s", i, res)
		}
	}
}

func TestSingleFlightFailureRecovery(t *testing.T) {
	sf := NewSingleFlight()
	testErr := errors.New("upstream failed")

	// Call 1: fails
	_, err := sf.Do("fail_key", func() (interface{}, error) {
		return nil, testErr
	})
	if err != testErr {
		t.Fatalf("expected testErr, got %v", err)
	}

	// Call 2: immediately subsequent call must not be poisoned by previous failure
	val, err2 := sf.Do("fail_key", func() (interface{}, error) {
		return "recovered", nil
	})
	if err2 != nil {
		t.Fatalf("expected nil error on second call, got %v", err2)
	}
	if val.(string) != "recovered" {
		t.Fatalf("expected 'recovered', got %v", val)
	}
}

func TestSingleFlightPanicRecovery(t *testing.T) {
	sf := NewSingleFlight()

	// Ensure that even if fn panics, subsequent calls don't deadlock
	func() {
		defer func() {
			_ = recover()
		}()
		_, _ = sf.Do("panic_key", func() (interface{}, error) {
			panic("something went wrong inside fn")
		})
	}()

	// Subsequent call must execute without deadlocking
	val, err := sf.Do("panic_key", func() (interface{}, error) {
		return "ok_after_panic", nil
	})
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if val.(string) != "ok_after_panic" {
		t.Fatalf("expected 'ok_after_panic', got %v", val)
	}
}

func TestSingleFlightDoContextCancellation(t *testing.T) {
	sf := NewSingleFlight()
	leaderStarted := make(chan struct{})
	leaderCanFinish := make(chan struct{})

	// Goroutine 1: Leader runs long-running fn
	go func() {
		_, _ = sf.Do("key_ctx", func() (interface{}, error) {
			close(leaderStarted)
			<-leaderCanFinish
			return "leader_done", nil
		})
	}()

	<-leaderStarted

	// Goroutine 2: Follower with cancellable context
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	start := time.Now()
	val, err := sf.DoContext(ctx, "key_ctx", func() (interface{}, error) {
		return "should_not_run", nil
	})
	elapsed := time.Since(start)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context.DeadlineExceeded, got: %v", err)
	}
	if val != nil {
		t.Fatalf("expected nil val on cancellation, got: %v", val)
	}
	if elapsed > 100*time.Millisecond {
		t.Fatalf("follower blocked too long, took %v (expected ~20ms)", elapsed)
	}

	// Release leader
	close(leaderCanFinish)
}

func TestSingleFlightDoContextSuccess(t *testing.T) {
	sf := NewSingleFlight()
	leaderStarted := make(chan struct{})

	go func() {
		_, _ = sf.Do("key_success", func() (interface{}, error) {
			close(leaderStarted)
			time.Sleep(30 * time.Millisecond)
			return "shared_value", nil
		})
	}()

	<-leaderStarted

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	val, err := sf.DoContext(ctx, "key_success", func() (interface{}, error) {
		return "should_not_run", nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if val.(string) != "shared_value" {
		t.Fatalf("expected shared_value, got %v", val)
	}
}

func TestSingleFlightDoContextPreCancelled(t *testing.T) {
	sf := NewSingleFlight()
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	called := false
	val, err := sf.DoContext(ctx, "pre_cancelled", func() (interface{}, error) {
		called = true
		return "unexpected", nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got: %v", err)
	}
	if called {
		t.Fatal("fn should not be executed when context is already cancelled")
	}
	if val != nil {
		t.Fatalf("expected nil val, got: %v", val)
	}
}

func TestSingleFlightPanicPropagationToFollowers(t *testing.T) {
	sf := NewSingleFlight()
	leaderStarted := make(chan struct{})
	leaderCanPanic := make(chan struct{})

	// Goroutine 1: Leader waits for follower to join, then panics
	go func() {
		defer func() {
			_ = recover()
		}()
		_, _ = sf.Do("panic_propagate", func() (interface{}, error) {
			close(leaderStarted)
			<-leaderCanPanic
			panic("catastrophic failure in leader")
		})
	}()

	<-leaderStarted

	// Goroutine 2: Follower joins in-flight call
	followerDone := make(chan struct{})
	var val interface{}
	var err error
	go func() {
		val, err = sf.Do("panic_propagate", func() (interface{}, error) {
			return "follower_result", nil
		})
		close(followerDone)
	}()

	// Allow follower goroutine to enter sf.Do and block on <-c.done
	time.Sleep(10 * time.Millisecond)
	close(leaderCanPanic)

	<-followerDone
	if err == nil {
		t.Fatal("expected follower to receive an error when leader panics, got nil err")
	}
	if !strings.Contains(err.Error(), "singleflight panic") {
		t.Fatalf("expected error message to contain 'singleflight panic', got: %v", err)
	}
	if val != nil {
		t.Fatalf("expected follower val to be nil on leader panic, got: %v", val)
	}
}

