package ratelimiter

import (
	"sync"
	"testing"
	"time"
)

func TestAllowUnderLimit(t *testing.T) {
	l := New()
	defer l.Stop()

	for i := 0; i < MaxRequests; i++ {
		r := l.Allow("user1")
		if !r.Allowed {
			t.Fatalf("request %d should be allowed", i+1)
		}
		if r.RequestsInWindow != i+1 {
			t.Errorf("want RequestsInWindow=%d, got %d", i+1, r.RequestsInWindow)
		}
		if r.RemainingRequests != MaxRequests-(i+1) {
			t.Errorf("want RemainingRequests=%d, got %d", MaxRequests-(i+1), r.RemainingRequests)
		}
	}
}

func TestAllowExceedsLimit(t *testing.T) {
	l := New()
	defer l.Stop()

	for i := 0; i < MaxRequests; i++ {
		l.Allow("user1")
	}

	r := l.Allow("user1")
	if r.Allowed {
		t.Fatal("6th request should be denied")
	}
	if r.RemainingRequests != 0 {
		t.Errorf("remaining should be 0, got %d", r.RemainingRequests)
	}
	if r.RetryAfter <= 0 {
		t.Errorf("RetryAfter should be positive, got %v", r.RetryAfter)
	}
}

func TestUserIsolation(t *testing.T) {
	l := New()
	defer l.Stop()

	for i := 0; i < MaxRequests; i++ {
		l.Allow("userA")
	}

	// rate limit should be tracked separately for each user
	r := l.Allow("userB")
	if !r.Allowed {
		t.Fatal("userB should not be affected by userA's limit")
	}
}

func TestConcurrentSafety(t *testing.T) {
	l := New()
	defer l.Stop()

	const goroutines = 50
	var wg sync.WaitGroup
	allowed := make(chan bool, goroutines)

	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			r := l.Allow("shared-user")
			allowed <- r.Allowed
		}()
	}
	wg.Wait()
	close(allowed)

	count := 0
	for a := range allowed {
		if a {
			count++
		}
	}

	// even under parallel calls, only MaxRequests should pass
	if count != MaxRequests {
		t.Errorf("expected exactly %d allowed requests, got %d", MaxRequests, count)
	}
}

func TestStats(t *testing.T) {
	l := New()
	defer l.Stop()

	l.Allow("alice")
	l.Allow("alice")
	l.Allow("bob")

	all := l.Stats("")
	if len(all) != 2 {
		t.Errorf("expected 2 users in stats, got %d", len(all))
	}

	alice := all["alice"]
	if alice.RequestsInWindow != 2 || alice.TotalRequests != 2 {
		t.Errorf("unexpected alice stats: %+v", alice)
	}

	filtered := l.Stats("bob")
	if len(filtered) != 1 {
		t.Errorf("expected 1 user when filtering, got %d", len(filtered))
	}
	if _, ok := filtered["bob"]; !ok {
		t.Error("bob missing from filtered stats")
	}
}

func TestRetryAfterAccuracy(t *testing.T) {
	l := New()
	defer l.Stop()

	before := time.Now()
	for i := 0; i < MaxRequests; i++ {
		l.Allow("user1")
	}

	r := l.Allow("user1")
	if r.Allowed {
		t.Fatal("should be denied")
	}

	// retry value should stay close to the remaining window duration
	maxExpected := Window - time.Since(before)
	if r.RetryAfter > maxExpected+50*time.Millisecond {
		t.Errorf("RetryAfter %v too large, expected ≤ %v", r.RetryAfter, maxExpected)
	}
}
