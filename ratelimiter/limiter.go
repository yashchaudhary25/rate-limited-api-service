package ratelimiter

import (
	"sync"
	"time"
)

const (
	MaxRequests = 5
	Window      = time.Minute
)

// AllowResult carries the outcome of an Allow call.
type AllowResult struct {
	Allowed           bool
	RequestsInWindow  int
	RemainingRequests int
	TotalRequests     int
	// set only when the request is blocked
	RetryAfter time.Duration
}

// StatEntry is the per-user snapshot returned by Stats.
type StatEntry struct {
	RequestsInWindow  int `json:"requests_in_window"`
	RemainingRequests int `json:"remaining_requests"`
	TotalRequests     int `json:"total_requests"`
}

type userRecord struct {
	timestamps []time.Time
	total      int
}

// one mutex protects the shared in-memory state
type Limiter struct {
	mu      sync.Mutex
	records map[string]*userRecord
	stopCh  chan struct{}
}

// New returns a started Limiter. Call Stop when done.
func New() *Limiter {
	l := &Limiter{
		records: make(map[string]*userRecord),
		stopCh:  make(chan struct{}),
	}
	go l.cleanup()
	return l
}

// Stop shuts down the background cleanup goroutine.
func (l *Limiter) Stop() {
	close(l.stopCh)
}

// Allow atomically checks the rate limit and records the request if allowed.
func (l *Limiter) Allow(userID string) AllowResult {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-Window)

	rec := l.records[userID]
	if rec == nil {
		rec = &userRecord{}
		l.records[userID] = rec
	}

	rec.timestamps = pruneOld(rec.timestamps, cutoff)
	inWindow := len(rec.timestamps)

	if inWindow >= MaxRequests {
		// oldest request in the active window tells us when the user can retry
		retryAfter := rec.timestamps[0].Add(Window).Sub(now)
		return AllowResult{
			Allowed:           false,
			RequestsInWindow:  inWindow,
			RemainingRequests: 0,
			TotalRequests:     rec.total,
			RetryAfter:        retryAfter,
		}
	}

	rec.timestamps = append(rec.timestamps, now)
	rec.total++
	inWindow++

	return AllowResult{
		Allowed:           true,
		RequestsInWindow:  inWindow,
		RemainingRequests: MaxRequests - inWindow,
		TotalRequests:     rec.total,
	}
}

// Stats returns a snapshot for all users. Pass a non-empty userID to filter.
func (l *Limiter) Stats(userID string) map[string]StatEntry {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-Window)
	out := make(map[string]StatEntry)

	snapshot := func(uid string, rec *userRecord) {
		inWindow := 0
		for _, t := range rec.timestamps {
			if t.After(cutoff) {
				inWindow++
			}
		}
		remaining := MaxRequests - inWindow
		if remaining < 0 {
			remaining = 0
		}
		out[uid] = StatEntry{
			RequestsInWindow:  inWindow,
			RemainingRequests: remaining,
			TotalRequests:     rec.total,
		}
	}

	if userID != "" {
		if rec, ok := l.records[userID]; ok {
			snapshot(userID, rec)
		}
		return out
	}

	for uid, rec := range l.records {
		snapshot(uid, rec)
	}
	return out
}

// cleanup only removes expired timestamps; user records are kept for stats.
func (l *Limiter) cleanup() {
	ticker := time.NewTicker(Window)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			l.mu.Lock()
			cutoff := time.Now().Add(-Window)
			for _, rec := range l.records {
				rec.timestamps = pruneOld(rec.timestamps, cutoff)
			}
			l.mu.Unlock()
		case <-l.stopCh:
			return
		}
	}
}

// pruneOld reuses the same slice and skips entries outside the current window.
func pruneOld(ts []time.Time, cutoff time.Time) []time.Time {
	i := 0
	for i < len(ts) && !ts[i].After(cutoff) {
		i++
	}
	return ts[i:]
}
