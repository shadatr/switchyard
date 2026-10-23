package logsink

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeWriter struct {
	mu      sync.Mutex
	batches [][]Record
	fail    atomic.Int32 // number of upcoming writes to fail
	block   chan struct{}
}

func (f *fakeWriter) Write(_ context.Context, b []Record) error {
	if f.block != nil {
		<-f.block
	}
	if f.fail.Load() > 0 {
		f.fail.Add(-1)
		return errors.New("clickhouse down")
	}
	f.mu.Lock()
	f.batches = append(f.batches, append([]Record(nil), b...))
	f.mu.Unlock()
	return nil
}

func (f *fakeWriter) rows() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, b := range f.batches {
		n += len(b)
	}
	return n
}

func opts() Options {
	return Options{QueueSize: 100, BatchSize: 10, FlushInterval: 20 * time.Millisecond, Retries: 3, RetryBackoff: time.Millisecond}
}

func TestBatchesBySizeAndInterval(t *testing.T) {
	w := &fakeWriter{}
	s := New(w, opts(), nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()

	for range 25 {
		s.Enqueue(Record{RequestID: "r"})
	}
	deadline := time.Now().Add(time.Second)
	for w.rows() < 25 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.batches) != 3 || len(w.batches[0]) != 10 || len(w.batches[2]) != 5 {
		sizes := []int{}
		for _, b := range w.batches {
			sizes = append(sizes, len(b))
		}
		t.Fatalf("batch sizes = %v, want [10 10 5]", sizes)
	}
}

func TestRetriesThenSucceeds(t *testing.T) {
	w := &fakeWriter{}
	w.fail.Store(2)
	s := New(w, opts(), nil, nil)
	if b := s.flush(context.Background(), []Record{{}, {}}); len(b) != 0 {
		t.Fatal("flush must return an empty batch")
	}
	if w.rows() != 2 {
		t.Fatalf("rows = %d, want 2 after retries", w.rows())
	}
}

func TestDropsAfterRetries(t *testing.T) {
	w := &fakeWriter{}
	w.fail.Store(100)
	s := New(w, opts(), nil, nil)
	s.flush(context.Background(), []Record{{}, {}, {}})
	if _, dropped := s.Stats(); dropped != 3 {
		t.Fatalf("dropped = %d, want 3", dropped)
	}
}

func TestEnqueueNeverBlocks(t *testing.T) {
	w := &fakeWriter{block: make(chan struct{})} // writer stuck, like a hung ClickHouse
	s := New(w, Options{QueueSize: 5, BatchSize: 1, FlushInterval: time.Millisecond}, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	go s.Run(ctx)

	start := time.Now()
	accepted := 0
	for range 1000 {
		if s.Enqueue(Record{}) {
			accepted++
		}
	}
	if el := time.Since(start); el > 50*time.Millisecond {
		t.Fatalf("enqueue took %s with a stuck writer", el)
	}
	if _, dropped := s.Stats(); dropped == 0 || accepted > 10 {
		t.Fatalf("accepted %d dropped %d", accepted, dropped)
	}
	close(w.block)
	cancel()
}

func TestDrainOnShutdown(t *testing.T) {
	w := &fakeWriter{}
	o := opts()
	o.FlushInterval = time.Hour // only the drain can flush
	s := New(w, o, nil, nil)
	for range 7 {
		s.Enqueue(Record{})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s.Run(ctx)
	if w.rows() != 7 {
		t.Fatalf("rows = %d, want 7 drained on shutdown", w.rows())
	}
}
