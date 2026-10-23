// Package logsink ships request records to ClickHouse in batches. Enqueue
// never blocks: when the queue is full the record is dropped and counted, so
// an analytics outage can never slow down or fail a request.
package logsink

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/shadatr/switchyard/internal/metrics"
)

// Record is one row of the requests table.
type Record struct {
	TS                time.Time
	RequestID         string
	ProjectID         string
	KeyID             string
	ModelAlias        string
	Target            string
	Attempts          uint8
	Status            uint16
	ErrorCode         string
	Stream            bool
	Cache             string
	PromptTokens      uint32
	CompletionTokens  uint32
	TokensEstimated   bool
	CostUSD           float64
	LatencyMs         float32
	TTFTMs            float32
	UpstreamMs        float32
	GatewayOverheadUs uint32
}

// Writer persists a batch. Implementations must be safe to call from one
// goroutine at a time.
type Writer interface {
	Write(ctx context.Context, batch []Record) error
}

// Options tune batching.
type Options struct {
	QueueSize     int
	BatchSize     int
	FlushInterval time.Duration
	Retries       int           // attempts per batch before dropping it
	RetryBackoff  time.Duration // doubled after each failed attempt
	DrainTimeout  time.Duration // max time spent flushing on shutdown
}

// Sink is the async batcher.
type Sink struct {
	opts    Options
	w       Writer
	ch      chan Record
	metrics *metrics.Metrics
	log     *slog.Logger

	dropped atomic.Int64
	written atomic.Int64
	lastLog atomic.Int64 // unix nanos of the last warning (rate-limits logging)
}

// New creates a Sink; call Run to start flushing.
func New(w Writer, opts Options, m *metrics.Metrics, log *slog.Logger) *Sink {
	if opts.Retries <= 0 {
		opts.Retries = 3
	}
	if opts.RetryBackoff <= 0 {
		opts.RetryBackoff = 100 * time.Millisecond
	}
	if opts.DrainTimeout <= 0 {
		opts.DrainTimeout = 5 * time.Second
	}
	if log == nil {
		log = slog.Default()
	}
	return &Sink{opts: opts, w: w, ch: make(chan Record, opts.QueueSize), metrics: m, log: log}
}

// Enqueue adds r without blocking; it reports whether r was accepted.
func (s *Sink) Enqueue(r Record) bool {
	select {
	case s.ch <- r:
		return true
	default:
		s.drop(1)
		return false
	}
}

// Stats returns records written and dropped so far.
func (s *Sink) Stats() (written, dropped int64) { return s.written.Load(), s.dropped.Load() }

// Run batches and writes records until ctx is done, then drains what is
// queued (bounded by DrainTimeout).
func (s *Sink) Run(ctx context.Context) {
	batch := make([]Record, 0, s.opts.BatchSize)
	t := time.NewTicker(s.opts.FlushInterval)
	defer t.Stop()
	for {
		select {
		case r := <-s.ch:
			batch = append(batch, r)
			if len(batch) >= s.opts.BatchSize {
				batch = s.flush(ctx, batch)
			}
		case <-t.C:
			if len(batch) > 0 {
				batch = s.flush(ctx, batch)
			}
		case <-ctx.Done():
			s.drain(batch)
			return
		}
	}
}

func (s *Sink) drain(batch []Record) {
	ctx, cancel := context.WithTimeout(context.Background(), s.opts.DrainTimeout)
	defer cancel()
	for {
		select {
		case r := <-s.ch:
			batch = append(batch, r)
			if len(batch) >= s.opts.BatchSize {
				batch = s.flush(ctx, batch)
			}
		default:
			if len(batch) > 0 {
				s.flush(ctx, batch)
			}
			return
		}
	}
}

// flush writes batch with retries and returns an empty slice to reuse.
func (s *Sink) flush(ctx context.Context, batch []Record) []Record {
	backoff := s.opts.RetryBackoff
	var err error
	for attempt := 1; attempt <= s.opts.Retries; attempt++ {
		wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		err = s.w.Write(wctx, batch)
		cancel()
		if err == nil {
			s.written.Add(int64(len(batch)))
			if s.metrics != nil {
				s.metrics.LogsWritten.Add(float64(len(batch)))
			}
			return batch[:0]
		}
		if attempt < s.opts.Retries {
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
			}
			backoff *= 2
		}
	}
	if now := time.Now().UnixNano(); now-s.lastLog.Load() > int64(10*time.Second) {
		s.lastLog.Store(now)
		s.log.Warn("dropping request log batches; is ClickHouse down?", "rows", len(batch), "err", err)
	}
	if s.metrics != nil {
		s.metrics.DependencyErrors.WithLabelValues("clickhouse").Inc()
	}
	s.drop(len(batch))
	return batch[:0]
}

func (s *Sink) drop(n int) {
	s.dropped.Add(int64(n))
	if s.metrics != nil {
		s.metrics.LogsDropped.Add(float64(n))
	}
}
