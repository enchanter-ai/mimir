package ratelimit

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestTokenBucketAllowsBurst(t *testing.T) {
	l := NewWithRate(1.0, 5) // 1 rps, burst of 5

	// First 5 requests should pass immediately (the burst).
	for i := 0; i < 5; i++ {
		ok, _ := l.Allow("1.2.3.4")
		if !ok {
			t.Fatalf("request %d: expected allow, got deny", i+1)
		}
	}

	// 6th in the same instant must be denied.
	ok, retry := l.Allow("1.2.3.4")
	if ok {
		t.Fatal("6th request: expected deny, got allow")
	}
	if retry <= 0 || retry > 2*time.Second {
		t.Errorf("retry-after should be ~1s, got %v", retry)
	}
}

func TestTokenBucketIsolatesIPs(t *testing.T) {
	l := NewWithRate(1.0, 1) // 1 rps, burst 1
	if ok, _ := l.Allow("10.0.0.1"); !ok {
		t.Fatal("first IP first request denied")
	}
	if ok, _ := l.Allow("10.0.0.2"); !ok {
		t.Fatal("second IP first request denied — bucket bled across IPs")
	}
	// Both IPs have depleted their burst.
	if ok, _ := l.Allow("10.0.0.1"); ok {
		t.Fatal("first IP second request should have been denied")
	}
}

func TestTokenBucketRefillsOverTime(t *testing.T) {
	l := NewWithRate(10.0, 1) // 10 rps, burst 1
	// Fake clock to make the test deterministic.
	t0 := time.Now()
	current := t0
	l.now = func() time.Time { return current }

	if ok, _ := l.Allow("ip"); !ok {
		t.Fatal("first request denied")
	}
	if ok, _ := l.Allow("ip"); ok {
		t.Fatal("immediate second request should have been denied")
	}

	// Advance the clock by 150ms (~1.5 tokens at 10 rps).
	current = current.Add(150 * time.Millisecond)
	if ok, _ := l.Allow("ip"); !ok {
		t.Fatal("after refill window, request was still denied")
	}
}

func TestMiddlewareSkipsHealthz(t *testing.T) {
	l := NewWithRate(1.0, 1)
	// Drain the bucket so any non-skipped request would be denied.
	_, _ = l.Allow("192.0.2.10")

	called := 0
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called++
		w.WriteHeader(http.StatusOK)
	})
	mw := l.Middleware(map[string]bool{"/v1/healthz": true})

	// Same IP, healthz path — must pass through despite empty bucket.
	req := httptest.NewRequest(http.MethodGet, "/v1/healthz", nil)
	req.RemoteAddr = "192.0.2.10:1234"
	rec := httptest.NewRecorder()
	mw(next).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || called != 1 {
		t.Errorf("healthz: expected 200 + 1 next-call, got code=%d called=%d", rec.Code, called)
	}

	// Same IP, attest path — must be 429.
	req2 := httptest.NewRequest(http.MethodPost, "/v1/attest", nil)
	req2.RemoteAddr = "192.0.2.10:1234"
	rec2 := httptest.NewRecorder()
	mw(next).ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusTooManyRequests {
		t.Errorf("attest: expected 429, got %d", rec2.Code)
	}
	if rec2.Header().Get("Retry-After") == "" {
		t.Error("429 response missing Retry-After header")
	}
}

func TestMiddlewareHonoursXForwardedFor(t *testing.T) {
	l := NewWithRate(1.0, 1)
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mw := l.Middleware(nil)

	// Two requests from the same RemoteAddr but different XFF — must NOT collide.
	mkReq := func(xff string) *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/v1/attest", nil)
		r.RemoteAddr = "10.0.0.1:1234" // load balancer IP
		r.Header.Set("X-Forwarded-For", xff)
		return r
	}

	for _, xff := range []string{"203.0.113.5", "198.51.100.10, 10.0.0.1"} {
		rec := httptest.NewRecorder()
		mw(next).ServeHTTP(rec, mkReq(xff))
		if rec.Code != http.StatusOK {
			t.Errorf("XFF %q: expected 200, got %d", xff, rec.Code)
		}
	}

	// Same XFF twice — second must be 429.
	rec1 := httptest.NewRecorder()
	mw(next).ServeHTTP(rec1, mkReq("203.0.113.99"))
	rec2 := httptest.NewRecorder()
	mw(next).ServeHTTP(rec2, mkReq("203.0.113.99"))
	if rec2.Code != http.StatusTooManyRequests {
		t.Errorf("same XFF twice: expected 429 on second, got %d", rec2.Code)
	}
}

func TestNewReturnsNilWhenDisabled(t *testing.T) {
	t.Setenv("ISSUER_RATELIMIT_RPS", "0")
	if l := New(); l != nil {
		t.Errorf("expected nil Limiter when RPS=0, got %+v", l)
	}
}

func TestNilLimiterMiddlewareIsPassthrough(t *testing.T) {
	var l *Limiter
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	mw := l.Middleware(nil)
	req := httptest.NewRequest(http.MethodPost, "/v1/attest", nil)
	rec := httptest.NewRecorder()
	mw(next).ServeHTTP(rec, req)
	if !called || rec.Code != http.StatusOK {
		t.Errorf("nil limiter: expected passthrough, got called=%v code=%d", called, rec.Code)
	}
}

func TestRateLimitResponseBody(t *testing.T) {
	l := NewWithRate(1.0, 1)
	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	mw := l.Middleware(nil)

	mkReq := func() *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/v1/attest", nil)
		r.RemoteAddr = "192.0.2.99:1"
		return r
	}
	mw(next).ServeHTTP(httptest.NewRecorder(), mkReq()) // drain
	rec := httptest.NewRecorder()
	mw(next).ServeHTTP(rec, mkReq())
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "rate_limit_exceeded") {
		t.Errorf("body should mention rate_limit_exceeded: %q", rec.Body.String())
	}
}
