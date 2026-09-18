package cache

import (
	"errors"
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
