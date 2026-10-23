// Package breaker implements a per-target circuit breaker based on the
// failure rate over a sliding time window:
//
//	closed → (failures ≥ rate × requests started, over ≥ MinRequests in Window) → open
//	open   → (after OpenDuration) → half-open
//	half-open → (probe succeeds) closed / (probe fails) open
//
// Under concurrency, fast failures complete before slow successes. Counting
// "N consecutive failures", or even failures over *completed* requests, then
// trips a target that is mostly healthy (e.g. right after startup, the first
// completions are all failures). The denominator is therefore the number of
// requests *started* (admitted by Allow) in the window, which does not depend
// on completion order.
package breaker

import (
	"sync"
	"sync/atomic"
	"time"
)

// State of a breaker.
type State int32

const (
	Closed State = iota
	HalfOpen
	Open
)

func (s State) String() string {
	switch s {
	case Closed:
		return "closed"
	case HalfOpen:
		return "half_open"
	default:
		return "open"
	}
}

// Options configure a Breaker.
type Options struct {
	FailureRate         float64       // trip when failures/total ≥ this (0 < r ≤ 1)
	MinRequests         int           // …and at least this many outcomes are in the window
	Window              time.Duration // sliding window length (split into 10 buckets)
	OpenDuration        time.Duration
	HalfOpenMaxRequests int
	Now                 func() time.Time // for tests; defaults to time.Now
	OnChange            func(State)      // called after every state change, outside the lock
}

const numBuckets = 10

type bucket struct {
	start    int64 // bucket index (time / bucket duration)
	started  int
	failures int
}

// Breaker is safe for concurrent use.
type Breaker struct {
	opts      Options
	bucketDur time.Duration
	state     atomic.Int32

	mu       sync.Mutex
	buckets  [numBuckets]bucket
	openedAt time.Time
	probes   int
}

// New returns a closed breaker.
func New(opts Options) *Breaker {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.FailureRate <= 0 || opts.FailureRate > 1 {
		opts.FailureRate = 0.5
	}
	if opts.MinRequests <= 0 {
		opts.MinRequests = 20
	}
	if opts.Window <= 0 {
		opts.Window = 10 * time.Second
	}
	if opts.OpenDuration <= 0 {
		opts.OpenDuration = 30 * time.Second
	}
	if opts.HalfOpenMaxRequests <= 0 {
		opts.HalfOpenMaxRequests = 1
	}
	bd := opts.Window / numBuckets
	if bd <= 0 {
		bd = time.Millisecond
	}
	return &Breaker{opts: opts, bucketDur: bd}
}

// State returns the current state without locking. An open breaker whose
// cool-down has elapsed still reports Open until the next Allow call.
func (b *Breaker) State() State { return State(b.state.Load()) }

// Allow reports whether a request may be sent to the target now.
func (b *Breaker) Allow() bool {
	b.mu.Lock()
	changed, allowed := false, false
	switch State(b.state.Load()) {
	case Closed:
		b.current().started++
		allowed = true
	case Open:
		if b.opts.Now().Sub(b.openedAt) >= b.opts.OpenDuration {
			b.state.Store(int32(HalfOpen))
			b.probes = 1
			changed, allowed = true, true
		}
	case HalfOpen:
		if b.probes < b.opts.HalfOpenMaxRequests {
			b.probes++
			allowed = true
		}
	}
	b.mu.Unlock()
	if changed {
		b.notify()
	}
	return allowed
}

// Success records a successful request.
func (b *Breaker) Success() { b.record(true) }

// Failure records a failed request.
func (b *Breaker) Failure() { b.record(false) }

func (b *Breaker) record(ok bool) {
	b.mu.Lock()
	changed := false
	switch State(b.state.Load()) {
	case Closed:
		if !ok {
			b.current().failures++
			if b.shouldTrip() {
				b.trip()
				changed = true
			}
		}
	case HalfOpen:
		if ok {
			b.state.Store(int32(Closed))
			b.resetWindow()
		} else {
			b.trip()
		}
		b.probes = 0
		changed = true
	case Open:
		// Late outcome from a request admitted before the breaker opened.
	}
	b.mu.Unlock()
	if changed {
		b.notify()
	}
}

// Cancel releases a half-open probe slot for a request that ended without a
// verdict (e.g. the client went away), so the breaker cannot get stuck.
func (b *Breaker) Cancel() {
	if State(b.state.Load()) != HalfOpen {
		return
	}
	b.mu.Lock()
	if State(b.state.Load()) == HalfOpen && b.probes > 0 {
		b.probes--
	}
	b.mu.Unlock()
}

// current returns the bucket for now, recycling a stale one. Caller holds mu.
func (b *Breaker) current() *bucket {
	n := b.opts.Now().UnixNano() / int64(b.bucketDur)
	bk := &b.buckets[n%numBuckets]
	if bk.start != n {
		*bk = bucket{start: n}
	}
	return bk
}

// shouldTrip evaluates the window. Caller holds mu.
func (b *Breaker) shouldTrip() bool {
	oldest := b.opts.Now().UnixNano()/int64(b.bucketDur) - numBuckets + 1
	total, failures := 0, 0
	for i := range b.buckets {
		if bk := &b.buckets[i]; bk.start >= oldest {
			total += bk.started
			failures += bk.failures
		}
	}
	return total >= b.opts.MinRequests && float64(failures) >= b.opts.FailureRate*float64(total)
}

func (b *Breaker) resetWindow() { b.buckets = [numBuckets]bucket{} }

func (b *Breaker) trip() {
	b.state.Store(int32(Open))
	b.openedAt = b.opts.Now()
	b.probes = 0
	b.resetWindow()
}

func (b *Breaker) notify() {
	if b.opts.OnChange != nil {
		b.opts.OnChange(b.State())
	}
}
