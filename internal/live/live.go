// Package live fans request events out to live-tail subscribers. Publish
// never blocks: a slow subscriber loses events instead of slowing requests.
package live

import (
	"sync"
	"sync/atomic"

	"github.com/shadatr/switchyard/internal/metrics"
)

// Event summarizes one finished request.
type Event struct {
	TS               int64   `json:"ts"` // unix millis
	RequestID        string  `json:"request_id"`
	ProjectID        string  `json:"project_id"`
	KeyID            string  `json:"key_id"`
	Alias            string  `json:"model"`
	Target           string  `json:"target"`
	Status           int     `json:"status"`
	ErrorCode        string  `json:"error_code,omitempty"`
	Stream           bool    `json:"stream"`
	Cache            string  `json:"cache"`
	Attempts         int     `json:"attempts"`
	LatencyMs        float64 `json:"latency_ms"`
	TTFTMs           float64 `json:"ttft_ms"`
	PromptTokens     int     `json:"prompt_tokens"`
	CompletionTokens int     `json:"completion_tokens"`
	CostUSD          float64 `json:"cost_usd"`
}

// BufferSize is the per-subscriber queue length.
const BufferSize = 256

type sub struct {
	ch      chan Event
	dropped atomic.Int64
}

// Hub is safe for concurrent use.
type Hub struct {
	mu      sync.RWMutex
	subs    map[*sub]struct{}
	n       atomic.Int32
	metrics *metrics.Metrics
}

// NewHub returns an empty hub.
func NewHub(m *metrics.Metrics) *Hub { return &Hub{subs: make(map[*sub]struct{}), metrics: m} }

// Subscribe registers a subscriber. Call cancel to unsubscribe; dropped
// reports how many events this subscriber missed.
func (h *Hub) Subscribe() (events <-chan Event, dropped func() int64, cancel func()) {
	s := &sub{ch: make(chan Event, BufferSize)}
	h.mu.Lock()
	h.subs[s] = struct{}{}
	h.n.Add(1)
	h.mu.Unlock()
	if h.metrics != nil {
		h.metrics.LiveSubscribers.Inc()
	}
	var once sync.Once
	return s.ch, s.dropped.Load, func() {
		once.Do(func() {
			h.mu.Lock()
			delete(h.subs, s)
			h.n.Add(-1)
			h.mu.Unlock()
			if h.metrics != nil {
				h.metrics.LiveSubscribers.Dec()
			}
		})
	}
}

// Publish delivers e to every subscriber that has room.
func (h *Hub) Publish(e Event) {
	if h.n.Load() == 0 {
		return // fast path: nobody is watching
	}
	h.mu.RLock()
	for s := range h.subs {
		select {
		case s.ch <- e:
		default:
			s.dropped.Add(1)
		}
	}
	h.mu.RUnlock()
}
