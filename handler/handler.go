package handler

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"

	"rate-limited-api/ratelimiter"
)

const maxBodyBytes = 1 << 20 // 1 MB

// Handler keeps shared dependencies for HTTP routes.
type Handler struct {
	limiter *ratelimiter.Limiter
}

// New returns an initialised Handler.
func New() *Handler {
	return &Handler{limiter: ratelimiter.New()}
}

// Stop cleans up background goroutines.
func (h *Handler) Stop() {
	h.limiter.Stop()
}

type requestBody struct {
	UserID  string `json:"user_id"`
	Payload any    `json:"payload"`
}

type successResponse struct {
	Success           bool   `json:"success"`
	Message           string `json:"message"`
	RequestsInWindow  int    `json:"requests_in_window"`
	RemainingRequests int    `json:"remaining_requests"`
	TotalRequests     int    `json:"total_requests"`
}

type errorResponse struct {
	Error             string `json:"error"`
	RetryAfterSeconds int    `json:"retry_after_seconds,omitempty"`
}

// HandleRequest handles POST /request.
func (h *Handler) HandleRequest(w http.ResponseWriter, r *http.Request) {
	// cap request size early to avoid oversized payloads
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)

	var body requestBody
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		if err == io.EOF {
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: "request body is required"})
			return
		}
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid JSON: " + err.Error()})
		return
	}

	if body.UserID == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "user_id is required"})
		return
	}

	result := h.limiter.Allow(body.UserID)

	w.Header().Set("X-RateLimit-Limit", fmt.Sprintf("%d", ratelimiter.MaxRequests))
	w.Header().Set("X-RateLimit-Remaining", fmt.Sprintf("%d", result.RemainingRequests))
	w.Header().Set("X-RateLimit-Window", "60")

	if !result.Allowed {
		// round up so clients don't retry slightly too early
		retrySecs := int(math.Ceil(result.RetryAfter.Seconds()))
		if retrySecs < 1 {
			retrySecs = 1
		}
		w.Header().Set("Retry-After", fmt.Sprintf("%d", retrySecs))
		writeJSON(w, http.StatusTooManyRequests, errorResponse{
			Error:             "rate limit exceeded: max 5 requests per minute",
			RetryAfterSeconds: retrySecs,
		})
		return
	}

	writeJSON(w, http.StatusOK, successResponse{
		Success:           true,
		Message:           "request accepted",
		RequestsInWindow:  result.RequestsInWindow,
		RemainingRequests: result.RemainingRequests,
		TotalRequests:     result.TotalRequests,
	})
}

// HandleStats handles GET /stats.
// Optional query param: ?user_id=<id> to filter to a single user.
func (h *Handler) HandleStats(w http.ResponseWriter, r *http.Request) {
	userID := r.URL.Query().Get("user_id")
	stats := h.limiter.Stats(userID)
	writeJSON(w, http.StatusOK, stats)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
