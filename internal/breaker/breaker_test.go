package breaker

import (
	"math/rand/v2"
	"sync"
	"testing"
	"time"
)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) Add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// newTest: trip at ≥50% failures over ≥10 requests in a 10s window.
func newTest() (*Breaker, *clock, *[]State) {
	c := &clock{t: time.Unix(1_000_000, 0)}
	var changes []State
	b := New(Options{FailureRate: 0.5, MinRequests: 10, Window: 10 * time.Second, OpenDuration: 10 * time.Second,
		HalfOpenMaxRequests: 1, Now: c.Now, OnChange: func(s State) { changes = append(changes, s) }})
	return b, c, &changes
}

// fail and succeed model one request through the breaker.
func fail(b *Breaker) {
	if b.Allow() {
		b.Failure()
	}
}

func succeed(b *Breaker) {
	if b.Allow() {
		b.Success()
	}
}

func trip(b *Breaker) {
	for range 10 {
		fail(b)
	}
}

func TestNeedsMinRequests(t *testing.T) {
	b, _, _ := newTest()
	for range 9 {
		fail(b)
	}
	if b.State() != Closed {
		t.Fatal("9 failures < MinRequests must not trip")
	}
	fail(b)
	if b.State() != Open || b.Allow() {
		t.Fatal("10 failures must trip")
	}
}

func TestFailureRateThreshold(t *testing.T) {
	b, _, _ := newTest()
	// 40% failures over 50 requests (S S S F F …): the running ratio never
	// reaches 50%, so the breaker stays closed.
	for i := range 50 {
		if i%5 >= 3 {
			fail(b)
		} else {
			succeed(b)
		}
	}
	if b.State() != Closed {
		t.Fatal("40% failure rate must not trip a 50% breaker")
	}
	// Push the rate over 50%.
	for range 15 {
		fail(b)
	}
	if b.State() != Open {
		t.Fatal("rate above 50% must trip")
	}
}

func TestOldFailuresExpire(t *testing.T) {
	b, c, _ := newTest()
	for range 9 {
		fail(b)
	}
	c.Add(11 * time.Second) // all of them slide out of the window
	for range 9 {
		fail(b)
	}
	if b.State() != Closed {
		t.Fatal("failures outside the window must not count")
	}
}

func TestFastFailuresDoNotTripMostlyHealthyTarget(t *testing.T) {
	// The scenario that motivated counting started requests: 20 requests
	// are admitted, the 6 that fail return immediately, the 14 successes are
	// still in flight. Counting completions this would be 6/6 = 100% failed.
	b, _, _ := newTest()
	for range 20 {
		if !b.Allow() {
			t.Fatal("unexpected rejection")
		}
	}
	for range 6 {
		b.Failure()
	}
	if b.State() != Closed {
		t.Fatal("6 failures out of 20 started requests (30%) must not trip")
	}
	for range 14 {
		b.Success()
	}
}

func TestHalfOpenProbeCloses(t *testing.T) {
	b, c, changes := newTest()
	trip(b)
	c.Add(9 * time.Second)
	if b.Allow() {
		t.Fatal("still cooling down")
	}
	c.Add(time.Second)
	if !b.Allow() {
		t.Fatal("first probe after cool-down must be allowed")
	}
	if b.State() != HalfOpen || b.Allow() {
		t.Fatal("only one probe allowed while half-open")
	}
	b.Success()
	if b.State() != Closed || !b.Allow() {
		t.Fatal("successful probe must close the breaker")
	}
	want := []State{Open, HalfOpen, Closed}
	if len(*changes) != len(want) {
		t.Fatalf("changes = %v", *changes)
	}
	for i := range want {
		if (*changes)[i] != want[i] {
			t.Fatalf("changes = %v, want %v", *changes, want)
		}
	}
	// The window was reset on close: a few failures don't immediately re-trip.
	for range 5 {
		fail(b)
	}
	for range 10 {
		succeed(b)
	}
	if b.State() != Closed {
		t.Fatal("window must be reset after closing")
	}
}

func TestHalfOpenProbeFailureReopens(t *testing.T) {
	b, c, _ := newTest()
	trip(b)
	c.Add(10 * time.Second)
	b.Allow()
	b.Failure()
	if b.State() != Open || b.Allow() {
		t.Fatal("failed probe must re-open the breaker")
	}
	c.Add(10 * time.Second)
	if !b.Allow() {
		t.Fatal("must half-open again after another cool-down")
	}
}

func TestCancelReleasesProbe(t *testing.T) {
	b, c, _ := newTest()
	trip(b)
	c.Add(10 * time.Second)
	if !b.Allow() || b.Allow() {
		t.Fatal("expected exactly one probe")
	}
	b.Cancel()
	if !b.Allow() {
		t.Fatal("canceled probe must free the slot")
	}
}

func TestConcurrentUse(t *testing.T) {
	b := New(Options{FailureRate: 0.5, MinRequests: 5, Window: 50 * time.Millisecond, OpenDuration: time.Millisecond})
	var wg sync.WaitGroup
	for range 50 {
		wg.Go(func() {
			for range 1000 {
				if b.Allow() {
					if rand.Float64() < 0.5 {
						fail(b)
					} else {
						succeed(b)
					}
				}
			}
		})
	}
	wg.Wait()
	_ = b.State()
}
