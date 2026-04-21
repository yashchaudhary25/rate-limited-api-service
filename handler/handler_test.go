package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

func newTestHandler() *Handler {
	return New()
}

func post(t *testing.T, h *Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/request", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.HandleRequest(rr, req)
	return rr
}

func TestHandleRequestSuccess(t *testing.T) {
	h := newTestHandler()
	defer h.Stop()

	rr := post(t, h, `{"user_id":"alice","payload":"hello"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", rr.Code, rr.Body)
	}

	var resp successResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if !resp.Success {
		t.Error("expected success=true")
	}
	if resp.RequestsInWindow != 1 {
		t.Errorf("want RequestsInWindow=1, got %d", resp.RequestsInWindow)
	}
}

func TestHandleRequestMissingUserID(t *testing.T) {
	h := newTestHandler()
	defer h.Stop()

	rr := post(t, h, `{"payload":"hello"}`)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", rr.Code)
	}
}

func TestHandleRequestInvalidJSON(t *testing.T) {
	h := newTestHandler()
	defer h.Stop()

	rr := post(t, h, `not-json`)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", rr.Code)
	}
}

func TestHandleRequestRateLimit(t *testing.T) {
	h := newTestHandler()
	defer h.Stop()

	body := `{"user_id":"bob","payload":"x"}`

	for i := 0; i < 5; i++ {
		rr := post(t, h, body)
		if rr.Code != http.StatusOK {
			t.Fatalf("request %d: want 200, got %d", i+1, rr.Code)
		}
	}

	rr := post(t, h, body)
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("6th request: want 429, got %d", rr.Code)
	}
	if rr.Header().Get("Retry-After") == "" {
		t.Error("Retry-After header missing on 429")
	}
}

func TestHandleRequestRateLimitHeaders(t *testing.T) {
	h := newTestHandler()
	defer h.Stop()

	rr := post(t, h, `{"user_id":"carol","payload":"x"}`)
	if rr.Header().Get("X-RateLimit-Limit") != "5" {
		t.Error("X-RateLimit-Limit header missing or wrong")
	}
	if rr.Header().Get("X-RateLimit-Remaining") == "" {
		t.Error("X-RateLimit-Remaining header missing")
	}
}

func TestHandleStatsConcurrent(t *testing.T) {
	h := newTestHandler()
	defer h.Stop()

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			post(t, h, `{"user_id":"concurrent","payload":"x"}`)
		}()
	}
	wg.Wait()

	req := httptest.NewRequest(http.MethodGet, "/stats", nil)
	rr := httptest.NewRecorder()
	h.HandleStats(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("stats: want 200, got %d", rr.Code)
	}

	var stats map[string]statEntry
	if err := json.NewDecoder(rr.Body).Decode(&stats); err != nil {
		t.Fatal(err)
	}
	entry, ok := stats["concurrent"]
	if !ok {
		t.Fatal("concurrent user missing from stats")
	}
	// stats should never show more than the allowed window size
	if entry.RequestsInWindow > 5 {
		t.Errorf("requests_in_window=%d exceeds limit of 5", entry.RequestsInWindow)
	}
}

// local test type used only for decoding /stats responses
type statEntry struct {
	RequestsInWindow  int `json:"requests_in_window"`
	RemainingRequests int `json:"remaining_requests"`
	TotalRequests     int `json:"total_requests"`
}
