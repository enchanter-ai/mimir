// Command bench runs a load + concurrency benchmark against the MCP Provenance Issuer.
//
// Usage:
//
//	go run . [--addr http://localhost:8080] [--out results.json]
//
// The issuer must be running before invoking this command.
// Start it with: cd ../issuer && go run . &
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// RateLevel describes a single RPS rate point to test.
type RateLevel struct {
	TargetRPS int
	Duration  time.Duration
}

// Result holds aggregate metrics for one rate level.
type Result struct {
	TargetRPS   int     `json:"target_rps"`
	ActualRPS   float64 `json:"actual_rps"`
	TotalReqs   int64   `json:"total_requests"`
	Successes   int64   `json:"successes"`
	Failures    int64   `json:"failures"`
	SuccessRate float64 `json:"success_rate_pct"`
	P50Ms       float64 `json:"p50_ms"`
	P95Ms       float64 `json:"p95_ms"`
	P99Ms       float64 `json:"p99_ms"`
	MaxMs       float64 `json:"max_ms"`
	DurationSec float64 `json:"duration_sec"`
}

// BenchReport is the top-level output written to results.json.
type BenchReport struct {
	GeneratedAt  string   `json:"generated_at"`
	IssuerAddr   string   `json:"issuer_addr"`
	Results      []Result `json:"results"`
	Bottleneck   string   `json:"bottleneck_note"`
	Headroom     string   `json:"headroom_note"`
}

var (
	addr    = flag.String("addr", "http://localhost:8080", "issuer base URL")
	outFile = flag.String("out", "results.json", "path to write JSON results")
	report  = flag.String("report", "REPORT.md", "path to write Markdown report")
)

func main() {
	flag.Parse()

	log.Printf("bench: waiting for issuer at %s/v1/healthz ...", *addr)
	waitForHealthz(*addr, 30*time.Second)

	log.Printf("bench: issuer is healthy — generating payloads")
	payloads := GeneratePayloads(1000)
	log.Printf("bench: generated %d payloads", len(payloads))

	rateLevels := []RateLevel{
		{TargetRPS: 10, Duration: 8 * time.Second},
		{TargetRPS: 100, Duration: 10 * time.Second},
		{TargetRPS: 500, Duration: 10 * time.Second},
		{TargetRPS: 1000, Duration: 10 * time.Second},
		{TargetRPS: 5000, Duration: 10 * time.Second},
	}

	client := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:        2000,
			MaxIdleConnsPerHost: 2000,
			IdleConnTimeout:     90 * time.Second,
		},
	}

	var results []Result
	for _, level := range rateLevels {
		log.Printf("bench: running rate level %d RPS for %s ...", level.TargetRPS, level.Duration)
		r := runLevel(client, *addr, payloads, level)
		results = append(results, r)
		log.Printf("bench:   → actual=%.1f RPS  p50=%.1fms  p95=%.1fms  p99=%.1fms  success=%.1f%%",
			r.ActualRPS, r.P50Ms, r.P95Ms, r.P99Ms, r.SuccessRate)
		// Cool-down between levels
		time.Sleep(2 * time.Second)
	}

	br := BenchReport{
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		IssuerAddr:  *addr,
		Results:     results,
		Bottleneck:  inferBottleneck(results),
		Headroom:    inferHeadroom(results),
	}

	writeJSON(*outFile, br)
	writeMarkdown(*report, br)

	log.Printf("bench: results written to %s and %s", *outFile, *report)
}

// runLevel sends requests at the target RPS for the given duration and collects metrics.
func runLevel(client *http.Client, addr string, payloads []Payload, level RateLevel) Result {
	endpoint := addr + "/v1/attest"
	ticker := time.NewTicker(time.Second / time.Duration(level.TargetRPS))
	defer ticker.Stop()

	deadline := time.Now().Add(level.Duration)

	var (
		mu        sync.Mutex
		latencies []float64
		successes int64
		failures  int64
		total     int64
	)

	var wg sync.WaitGroup
	pIdx := int64(0)

	for time.Now().Before(deadline) {
		<-ticker.C
		if time.Now().After(deadline) {
			break
		}

		wg.Add(1)
		idx := int(atomic.AddInt64(&pIdx, 1)-1) % len(payloads)
		payload := payloads[idx]

		go func(p Payload) {
			defer wg.Done()
			start := time.Now()
			ok := doRequest(client, endpoint, p.Body)
			elapsed := float64(time.Since(start).Microseconds()) / 1000.0 // ms

			atomic.AddInt64(&total, 1)
			mu.Lock()
			latencies = append(latencies, elapsed)
			mu.Unlock()

			if ok {
				atomic.AddInt64(&successes, 1)
			} else {
				atomic.AddInt64(&failures, 1)
			}
		}(payload)
	}

	wg.Wait()

	p50, p95, p99, maxMs := percentiles(latencies)
	tot := atomic.LoadInt64(&total)
	suc := atomic.LoadInt64(&successes)
	fail := atomic.LoadInt64(&failures)

	successRate := 0.0
	if tot > 0 {
		successRate = float64(suc) / float64(tot) * 100.0
	}

	actualRPS := float64(tot) / level.Duration.Seconds()

	return Result{
		TargetRPS:   level.TargetRPS,
		ActualRPS:   round2(actualRPS),
		TotalReqs:   tot,
		Successes:   suc,
		Failures:    fail,
		SuccessRate: round2(successRate),
		P50Ms:       round2(p50),
		P95Ms:       round2(p95),
		P99Ms:       round2(p99),
		MaxMs:       round2(maxMs),
		DurationSec: level.Duration.Seconds(),
	}
}

// doRequest sends a single POST and returns true on HTTP 200.
func doRequest(client *http.Client, endpoint string, body []byte) bool {
	resp, err := client.Post(endpoint, "application/json", bytes.NewReader(body))
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	return resp.StatusCode == http.StatusOK
}

// waitForHealthz polls /v1/healthz until it returns 200 or timeout.
func waitForHealthz(addr string, timeout time.Duration) {
	client := &http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := client.Get(addr + "/v1/healthz")
		if err == nil && resp.StatusCode == 200 {
			resp.Body.Close()
			return
		}
		if resp != nil {
			resp.Body.Close()
		}
		time.Sleep(500 * time.Millisecond)
	}
	log.Fatalf("bench: issuer did not become healthy within %s", timeout)
}

// percentiles computes p50/p95/p99/max from a slice of latency values (ms).
func percentiles(latencies []float64) (p50, p95, p99, maxMs float64) {
	if len(latencies) == 0 {
		return 0, 0, 0, 0
	}
	sorted := make([]float64, len(latencies))
	copy(sorted, latencies)
	sort.Float64s(sorted)

	p50 = sorted[int(math.Floor(float64(len(sorted))*0.50))]
	p95 = sorted[int(math.Floor(float64(len(sorted))*0.95))]
	p99 = sorted[int(math.Floor(float64(len(sorted))*0.99))]
	maxMs = sorted[len(sorted)-1]
	return
}

func round2(f float64) float64 {
	return math.Round(f*100) / 100
}

// inferBottleneck produces a brief note based on latency growth pattern.
func inferBottleneck(results []Result) string {
	if len(results) < 2 {
		return "insufficient data"
	}
	// Compare p95 growth rate vs RPS growth rate
	first := results[0]
	last := results[len(results)-1]
	rpsGrowth := float64(last.TargetRPS) / float64(first.TargetRPS)
	latGrowth := last.P95Ms / (first.P95Ms + 0.001)
	if latGrowth > rpsGrowth*1.5 {
		return "CPU-bound: p95 latency grows super-linearly — signing/canonicalization dominates; consider goroutine pool or worker-limit"
	}
	if last.SuccessRate < 99.0 {
		return "overload: success rate drops at high RPS — issuer queue saturated; goroutine explosion likely"
	}
	return "well-scaled: latency grows sub-linearly relative to RPS — no obvious single bottleneck at tested rates"
}

// inferHeadroom produces a one-sentence production headroom statement.
func inferHeadroom(results []Result) string {
	// Find highest RPS level where success rate >= 99% and p99 < 500ms
	sustainable := 0
	for _, r := range results {
		if r.SuccessRate >= 99.0 && r.P99Ms < 500.0 {
			sustainable = r.TargetRPS
		}
	}
	if sustainable == 0 {
		return "issuer could not sustain even the lowest tested rate with <500ms p99 and >99% success"
	}
	return fmt.Sprintf("on this machine, a single issuer instance safely sustains ~%d RPS "+
		"(≥99%% success, p99 <500ms); above that, latency spikes or error rate climbs", sustainable)
}

// writeJSON serialises v to path.
func writeJSON(path string, v interface{}) {
	f, err := os.Create(path)
	if err != nil {
		log.Fatalf("bench: create %s: %v", path, err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		log.Fatalf("bench: encode %s: %v", path, err)
	}
}

// writeMarkdown writes a human-readable summary to path.
func writeMarkdown(path string, br BenchReport) {
	f, err := os.Create(path)
	if err != nil {
		log.Fatalf("bench: create %s: %v", path, err)
	}
	defer f.Close()

	fmt.Fprintf(f, "# Mimir Issuer — Load Benchmark Report\n\n")
	fmt.Fprintf(f, "**Generated:** %s  \n", br.GeneratedAt)
	fmt.Fprintf(f, "**Issuer:** %s  \n\n", br.IssuerAddr)

	fmt.Fprintf(f, "## Throughput by Rate Level\n\n")
	fmt.Fprintf(f, "| Target RPS | Actual RPS | Total Reqs | Success%% | p50 ms | p95 ms | p99 ms | Max ms |\n")
	fmt.Fprintf(f, "|------------|-----------|-----------|---------|--------|--------|--------|--------|\n")
	for _, r := range br.Results {
		fmt.Fprintf(f, "| %10d | %9.1f | %9d | %7.1f | %6.1f | %6.1f | %6.1f | %6.1f |\n",
			r.TargetRPS, r.ActualRPS, r.TotalReqs, r.SuccessRate,
			r.P50Ms, r.P95Ms, r.P99Ms, r.MaxMs)
	}

	fmt.Fprintf(f, "\n## Analysis\n\n")
	fmt.Fprintf(f, "**Bottleneck:** %s\n\n", br.Bottleneck)
	fmt.Fprintf(f, "**Production headroom:** %s\n\n", br.Headroom)

	fmt.Fprintf(f, "## Notes\n\n")
	fmt.Fprintf(f, "- Each request: JSON parse → 2× SHA-256 → RFC 8785 canonicalize → Ed25519 sign → JSON encode.\n")
	fmt.Fprintf(f, "- Payloads: 1000 distinct bodies (40%% small <1KB, 40%% medium 1-10KB, 20%% large 10-100KB).\n")
	fmt.Fprintf(f, "- For micro-benchmarks on signing path, run: `go test -bench=BenchmarkSignEnvelope -benchtime=5s ./...`\n")
	fmt.Fprintf(f, "- For concurrency safety, run: `go test -race -run TestConcurrent ./...`\n")
	fmt.Fprintf(f, "- CPU profile: start issuer with `GODEBUG=pprof=1` and use `go tool pprof http://localhost:8080/debug/pprof/profile`\n")
}
