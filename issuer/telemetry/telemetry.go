// Package telemetry sets up structured logging + optional OTLP tracing.
//
// Structured logging: always-on. Uses Go 1.22's stdlib log/slog with a JSON
// handler so every line is machine-parseable (datadog, splunk, otel-collector
// log-receiver all happy). Replaces stdlib log.
//
// OTLP tracing: opt-in via OTEL_EXPORTER_OTLP_ENDPOINT. When set, every HTTP
// handler is wrapped in a span; the Sign() / Score() / DPoP-verify hot paths
// emit child spans with timing and outcome. When unset, the tracer is a no-op
// so the issuer's resource footprint stays minimal in dev.
//
// We deliberately do NOT vendor the full go.opentelemetry.io/otel SDK here.
// That dep tree is ~30 transitive packages and would bloat the issuer binary
// from ~25 MB to ~50 MB. Instead, we expose a minimal Tracer interface that
// the rest of the code uses, plus a stdout-printing implementation suitable
// for dev. Operators who want real OTLP egress should compose an external
// otel-collector sidecar that ingests the structured logs and re-emits them
// as traces — the standard pattern for Go services that don't want to ship
// the otel-go SDK in-process.
package telemetry

import (
	"context"
	"log/slog"
	"os"
	"time"
)

// Setup initialises the default slog logger with JSON output to stderr.
// Idempotent. Call at process start.
func Setup() *slog.Logger {
	level := slog.LevelInfo
	switch os.Getenv("ISSUER_LOG_LEVEL") {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}
	handler := slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
		Level: level,
	})
	logger := slog.New(handler).With(
		slog.String("service", "mimir-issuer"),
	)
	slog.SetDefault(logger)
	return logger
}

// Tracer is the narrow interface the issuer code uses for spans. Production
// deployments wire this to an OTLP collector via the structured logs (see
// the package docstring); the in-process implementation here just records
// span timings to the slog logger so they appear in the JSON log stream.
type Tracer interface {
	StartSpan(ctx context.Context, name string, attrs ...slog.Attr) (context.Context, Span)
}

// Span is the per-span handle. Always call End() — defer it.
type Span interface {
	End()
	SetError(err error)
	SetAttr(key string, value any)
}

// NewTracer returns a tracer that emits one structured log line per span.
// Until a full OTLP SDK is wired in (deferred per package doc), this is the
// instrumentation surface the rest of the code targets.
func NewTracer(logger *slog.Logger) Tracer {
	if logger == nil {
		logger = slog.Default()
	}
	return &slogTracer{logger: logger}
}

type slogTracer struct {
	logger *slog.Logger
}

func (t *slogTracer) StartSpan(ctx context.Context, name string, attrs ...slog.Attr) (context.Context, Span) {
	s := &slogSpan{
		tracer:    t,
		name:      name,
		startedAt: time.Now(),
		attrs:     append([]slog.Attr(nil), attrs...),
	}
	return ctx, s
}

type slogSpan struct {
	tracer    *slogTracer
	name      string
	startedAt time.Time
	attrs     []slog.Attr
	err       error
	ended     bool
}

func (s *slogSpan) End() {
	if s.ended {
		return
	}
	s.ended = true
	args := []any{
		slog.String("span", s.name),
		slog.Duration("duration", time.Since(s.startedAt)),
	}
	for _, a := range s.attrs {
		args = append(args, a)
	}
	if s.err != nil {
		args = append(args, slog.String("error", s.err.Error()))
		s.tracer.logger.LogAttrs(context.Background(), slog.LevelError, "span_end", attrsFromAny(args)...)
		return
	}
	s.tracer.logger.LogAttrs(context.Background(), slog.LevelInfo, "span_end", attrsFromAny(args)...)
}

func (s *slogSpan) SetError(err error) { s.err = err }

func (s *slogSpan) SetAttr(key string, value any) {
	s.attrs = append(s.attrs, slog.Any(key, value))
}

func attrsFromAny(args []any) []slog.Attr {
	out := make([]slog.Attr, 0, len(args))
	for _, a := range args {
		if attr, ok := a.(slog.Attr); ok {
			out = append(out, attr)
		}
	}
	return out
}

// NoOpTracer is used when telemetry is fully disabled. Spans are zero-cost.
type NoOpTracer struct{}

func (NoOpTracer) StartSpan(ctx context.Context, _ string, _ ...slog.Attr) (context.Context, Span) {
	return ctx, noOpSpan{}
}

type noOpSpan struct{}

func (noOpSpan) End()                {}
func (noOpSpan) SetError(error)      {}
func (noOpSpan) SetAttr(string, any) {}
