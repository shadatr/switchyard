package live

import (
	"testing"
	"time"
)

func TestFanOut(t *testing.T) {
	h := NewHub(nil)
	a, _, cancelA := h.Subscribe()
	b, _, cancelB := h.Subscribe()
	defer cancelA()
	h.Publish(Event{RequestID: "1"})
	if (<-a).RequestID != "1" || (<-b).RequestID != "1" {
		t.Fatal("both subscribers must receive the event")
	}
	cancelB()
	cancelB() // idempotent
	h.Publish(Event{RequestID: "2"})
	if (<-a).RequestID != "2" {
		t.Fatal("remaining subscriber must still receive events")
	}
	select {
	case e := <-b:
		t.Fatalf("unsubscribed channel received %v", e)
	default:
	}
}

func TestSlowSubscriberNeverBlocksPublisher(t *testing.T) {
	h := NewHub(nil)
	_, dropped, cancel := h.Subscribe() // never reads
	defer cancel()
	start := time.Now()
	for range 100_000 {
		h.Publish(Event{})
	}
	if el := time.Since(start); el > 500*time.Millisecond {
		t.Fatalf("publishing took %s with a stuck subscriber", el)
	}
	if got := dropped(); got != 100_000-BufferSize {
		t.Fatalf("dropped = %d, want %d", got, 100_000-BufferSize)
	}
}

func BenchmarkPublishNoSubscribers(b *testing.B) {
	h := NewHub(nil)
	e := Event{RequestID: "x"}
	for b.Loop() {
		h.Publish(e)
	}
}
