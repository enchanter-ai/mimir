// Package ratelimit provides a per-IP token-bucket HTTP middleware.
//
// Each remote IP gets an independent token bucket with `rate` tokens/sec
// regenerating, capped at `burst`. Requests beyond the bucket return 429
// with Retry-After indicating when the next token will be available.
//
// Idle buckets are reaped from the map after `idleTimeout` so memory stays
// bounded under churn — production deploys should pair this with a CDN /
// WAF rate-limiter; this middleware exists as a defence-in-depth floor that
// keeps a misconfigured public deploy from immediately becoming an
// uncapped Claude-API budget bomb.
//
// Rate + burst are configured via env:
//
//	ISSUER_RATELIMIT_RPS    requests per second per IP   default 10
//	ISSUER_RATELIMIT_BURST  max burst per IP             default 20
//
// Set ISSUER_RATELIMIT_RPS=0 to disable rate limiting entirely (NOT
// recommended in production).
package ratelimit

import (
	"net"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"
)

// Default tuning. The numbers were chosen so a single integrator running
// the demo locally does not hit the limit during normal exploration, while
// a misbehaving caller (or a public-facing leak) is bounded at single-digit
// dollars per minute of Claude spend.
const (
	defaultRPS         = 10.0
	defaultBurst       = 20
	defaultIdleTimeout = 5 * time.Minute
)

// Limiter is the middleware state. Safe for concurrent use.
type Limiter struct {
	mu          sync.Mutex
	buckets     map[string]*bucket
	rps         float64
	burst       int
	idleTimeout time.Duration
	now         func() time.Time // injectable for tests
}

type bucket struct {
	tokens    float64
	updatedAt time.Time
	lastSeen  time.Time
}

// New constructs a Limiter using ISSUER_RATELIMIT_* env vars (or the
// defaults). Returns nil if rate limiting is disabled (RPS == 0).
func New() *Limiter {
	rps := defaultRPS
	burst := defaultBurst

	if v := os.Getenv("ISSUER_RATELIMIT_RPS"); v != "" {
		if parsed, err := strconv.ParseFloat(v, 64); err == nil {
			rps = parsed
		}
	}
	if v := os.Getenv("ISSUER_RATELIMIT_BURST"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 {
			burst = parsed
		}
	}
	if rps <= 0 {
		return nil // disabled
	}
	return NewWithRate(rps, burst)
}

// NewWithRate constructs a Limiter with explicit parameters.
func NewWithRate(rps float64, burst int) *Limiter {
	return &Limiter{
		buckets:     make(map[string]*bucket),
		rps:         rps,
		burst:       burst,
		idleTimeout: defaultIdleTimeout,
		now:         time.Now,
	}
}

// Allow returns (true, 0) if the IP is allowed to proceed; (false, retryAfter)
// otherwise. RetryAfter is the duration until the next token will be available.
func (l *Limiter) Allow(ip string) (bool, time.Duration) {
	if l == nil {
		return true, 0
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	l.reapLocked(now)

	b, ok := l.buckets[ip]
	if !ok {
		b = &bucket{
			tokens:    float64(l.burst),
			updatedAt: now,
			lastSeen:  now,
		}
		l.buckets[ip] = b
	}

	elapsed := now.Sub(b.updatedAt).Seconds()
	b.tokens = minFloat(float64(l.burst), b.tokens+elapsed*l.rps)
	b.updatedAt = now
	b.lastSeen = now

	if b.tokens >= 1.0 {
		b.tokens -= 1.0
		return true, 0
	}
	deficit := 1.0 - b.tokens
	return false, time.Duration(deficit/l.rps*float64(time.Second)) + time.Millisecond
}

// reapLocked removes buckets that have not been seen for idleTimeout. Caller
// must hold l.mu.
func (l *Limiter) reapLocked(now time.Time) {
	if len(l.buckets) < 1024 {
		return // cheap memory; only sweep once the map is non-trivial
	}
	for ip, b := range l.buckets {
		if now.Sub(b.lastSeen) > l.idleTimeout {
			delete(l.buckets, ip)
		}
	}
}

// Middleware returns an http.Handler middleware. Disabled Limiter (nil receiver)
// passes through. Paths in skip (exact match) bypass the limit entirely — used
// for healthz so liveness probes don't get throttled.
func (l *Limiter) Middleware(skip map[string]bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if l == nil {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if skip[r.URL.Path] {
				next.ServeHTTP(w, r)
				return
			}
			ip := clientIP(r)
			ok, retryAfter := l.Allow(ip)
			if !ok {
				retrySecs := int(retryAfter.Seconds())
				if retrySecs < 1 {
					retrySecs = 1
				}
				w.Header().Set("Retry-After", strconv.Itoa(retrySecs))
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = w.Write([]byte(`{"error":"rate_limit_exceeded","retry_after_seconds":` + strconv.Itoa(retrySecs) + `}`))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// clientIP extracts the caller's IP, honouring X-Forwarded-For first
// (operators behind a CDN / load-balancer must trust their LB chain).
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// Take the first IP in the comma-separated list (real client).
		for i := 0; i < len(xff); i++ {
			if xff[i] == ',' {
				return trim(xff[:i])
			}
		}
		return trim(xff)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func trim(s string) string {
	start, end := 0, len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t') {
		end--
	}
	return s[start:end]
}

func minFloat(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}
