# Rate-Limited API Service

This is a simple HTTP API built in Go that implements per-user rate limiting (5 requests per minute) using in-memory storage.

The goal of this project was to build something that:
- works correctly under concurrency
- is easy to understand
- reflects real-world backend design decisions (with some trade-offs)

---

## Endpoints

| Method | Path       | Description                          |
|--------|------------|--------------------------------------|
| POST   | /request   | Submit a request for a user          |
| GET    | /stats     | Get request stats (per user or all)  |
| GET    | /health    | Health check                         |

---
## Project Structure

```text
.
├── main.go                  # server bootstrap & routing
├── handler/
│   ├── handler.go
│   └── handler_test.go
├── ratelimiter/
│   ├── limiter.go
│   └── limiter_test.go
├── go.mod
└── README.md
---

### POST /request

**Request body**
```json
{
  "user_id": "alice",
  "payload": "anything"
}
```

**Success (200)**
```json
{
  "success": true,
  "message": "request accepted",
  "requests_in_window": 3,
  "remaining_requests": 2,
  "total_requests": 10
}
```

**Rate limited (429)**
```json
{
  "error": "rate limit exceeded: max 5 requests per minute",
  "retry_after_seconds": 47
}
```
Response headers also include:

- X-RateLimit-Limit
- X-RateLimit-Remaining
- Retry-After

### GET /stats

Returns request statistics
```json
{
  "alice": { "requests_in_window": 3, "remaining_requests": 2, "total_requests": 10 },
  "bob":   { "requests_in_window": 5, "remaining_requests": 0, "total_requests": 22 }
}
```

Single user: `GET /stats?user_id=alice`

---

## How to run

**Prerequisites:** Go 1.22+ — download from https://go.dev/dl/ (Windows installer available)

### macOS / Linux

```bash
cd goLang

# Run (default port 8080)
go run .

# Custom port
PORT=9000 go run .
```

**Tests**
```bash
go test ./... -race -v
```

**Smoke test**
```bash
# Send 5 requests (all should succeed)
for i in $(seq 1 5); do
  curl -s -X POST http://localhost:8080/request \
    -H 'Content-Type: application/json' \
    -d '{"user_id":"alice","payload":"hello"}'
  echo
done

# 6th request — should return 429
curl -s -X POST http://localhost:8080/request \
  -H 'Content-Type: application/json' \
  -d '{"user_id":"alice","payload":"hello"}'

# Stats
curl -s http://localhost:8080/stats
curl -s "http://localhost:8080/stats?user_id=alice"
```

---

### Windows (PowerShell)

```powershell
cd goLang

# Run (default port 8080)
go run .

# Custom port
$env:PORT = "9000"; go run .
```

**Tests**
```powershell
go test ./... -race -v
```

**Smoke test (PowerShell)**
```powershell
$body = '{"user_id":"alice","payload":"hello"}'
$headers = @{ "Content-Type" = "application/json" }

# Send 5 requests (all should succeed)
1..5 | ForEach-Object {
    Invoke-RestMethod -Method POST -Uri http://localhost:8080/request `
        -Headers $headers -Body $body | ConvertTo-Json
}

# 6th request — should return 429
try {
    Invoke-RestMethod -Method POST -Uri http://localhost:8080/request `
        -Headers $headers -Body $body
} catch {
    $_.Exception.Response.StatusCode.value__   # expect 429
    ($_.ErrorDetails.Message | ConvertFrom-Json).error
}

# Stats
Invoke-RestMethod http://localhost:8080/stats | ConvertTo-Json
Invoke-RestMethod "http://localhost:8080/stats?user_id=alice" | ConvertTo-Json
```

**Stop the server:** press `Ctrl+C` in the terminal — graceful shutdown drains in-flight requests within 10 s.

> **Note:** `curl` on Windows PowerShell is an alias for `Invoke-WebRequest`, not the real curl.
> Either use `Invoke-RestMethod` (shown above) or install real curl from https://curl.se/windows/.

---

## Design decisions

### Sliding window log (not fixed window)

A fixed window resets on the clock minute, which allows a burst of 10 requests in 2 seconds straddling a boundary. The **sliding window log** approach stores each request's timestamp and counts only those within the last 60 s, giving precise enforcement regardless of when in the minute the requests arrive.

### Single mutex over the entire map

All user records share one `sync.Mutex`. This is the simplest correct solution and performs well up to tens of thousands of requests per second on a single node. The lock is held only for the duration of an O(n) timestamp prune (where n ≤ 5 for any user), so contention is low in practice.

### Accurate `Retry-After`

Rather than always returning 60 s, the limiter finds the oldest timestamp inside the current window and computes `oldest + 60s - now`. This tells callers exactly how long to wait, improving client experience and reducing unnecessary retries.

### Background cleanup goroutine

A ticker fires every 60 s and prunes stale timestamps from every user record. Without this, a user who made 5 requests and then disappeared would forever hold a 5-element slice in memory. The number of timestamps per user is bounded (max 5), but user records are retained to preserve stats, so memory can grow with the number of unique users over time.

### Graceful shutdown (cross-platform)

`main` listens for `os.Interrupt` (Ctrl+C on all platforms) and `syscall.SIGTERM` (Linux/macOS process managers — Docker stop, systemd, Kubernetes). Windows only delivers `os.Interrupt`; `SIGTERM` is defined in Go's syscall package on Windows but never sent by the OS, so it is harmless to include. The server gets 10 s to drain in-flight requests before exiting. The rate-limiter's background goroutine is stopped via a `stopCh` channel.

### Input validation & body size limit

The handler rejects missing `user_id`, invalid JSON, and bodies larger than 1 MB (`http.MaxBytesReader`), preventing trivial abuse.

### Standard HTTP timeout configuration

`ReadTimeout`, `WriteTimeout`, and `IdleTimeout` are set on the server to prevent slowloris and idle connection accumulation.

---

## Limitations of the in-memory approach

| Limitation | Impact |
|---|---|
| **Not distributed** | Each process has its own counter; running 2 instances means each user gets 5 req/min *per instance*, not globally. |
| **Lost on restart** | All state (timestamps, totals) is discarded when the process exits. |
| **Single-node throughput** | The global mutex is a bottleneck at very high concurrency (> ~50 k req/s). |

---

## What I would improve with more time

1. Use Redis for distributed rate limiting (atomic operations using Lua scripts)
2. Add TTL-based cleanup for inactive users
3. Replace global mutex with sharded locks
4. Add authentication-based rate limiting
5. Add metrics (Prometheus)
6. Containerize and deploy (Docker + cloud)
7. Make rate limits configurable
