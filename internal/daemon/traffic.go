package daemon

import (
	"sync"
	"time"
)

// Sample is one reading of the traffic counters.
type Sample struct {
	// At is a Unix timestamp in seconds; the UI plots against it directly.
	At           int64 `json:"at"`
	Uplink       int64 `json:"uplink"`
	Downlink     int64 `json:"downlink"`
	UplinkRate   int64 `json:"uplink_rate"`
	DownlinkRate int64 `json:"downlink_rate"`
}

// History is a fixed-size ring of traffic samples.
//
// It lives in memory and is lost on restart, which is the right trade here:
// the question it answers is "what is happening now", and writing a sample
// every two seconds to flash would wear the device out to answer a question
// nobody asks about last week.
type History struct {
	mu      sync.RWMutex
	samples []Sample
	next    int
	full    bool
}

// NewHistory returns a ring holding size samples.
func NewHistory(size int) *History {
	if size <= 0 {
		size = 180
	}
	return &History{samples: make([]Sample, size)}
}

// Add records a sample.
func (h *History) Add(s Sample) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.samples[h.next] = s
	h.next = (h.next + 1) % len(h.samples)
	if h.next == 0 {
		h.full = true
	}
}

// Samples returns the recorded samples, oldest first.
func (h *History) Samples() []Sample {
	h.mu.RLock()
	defer h.mu.RUnlock()

	// An empty ring returns an empty slice rather than nil, so the JSON always
	// carries an array and a consumer never has to special-case null.
	out := make([]Sample, 0, len(h.samples))
	if h.full {
		out = append(out, h.samples[h.next:]...)
		out = append(out, h.samples[:h.next]...)
	} else {
		out = append(out, h.samples[:h.next]...)
	}
	return out
}

// Reset clears the ring. A disconnect makes the previous session's samples
// misleading, because the counters restart from zero with the new core.
func (h *History) Reset() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.next = 0
	h.full = false
	for i := range h.samples {
		h.samples[i] = Sample{}
	}
}

// Peak returns the highest rates seen, which is what a chart needs to scale
// its axis without jumping on every redraw.
func (h *History) Peak() (up, down int64) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, s := range h.samples {
		if s.UplinkRate > up {
			up = s.UplinkRate
		}
		if s.DownlinkRate > down {
			down = s.DownlinkRate
		}
	}
	return up, down
}

// TrafficReport is what the API returns for the live view.
type TrafficReport struct {
	Connected bool     `json:"connected"`
	Samples   []Sample `json:"samples"`
	PeakUp    int64    `json:"peak_up"`
	PeakDown  int64    `json:"peak_down"`
	// IntervalSeconds tells the UI how far apart samples are, so it can label
	// the axis without guessing from the timestamps.
	IntervalSeconds int       `json:"interval_seconds"`
	TakenAt         time.Time `json:"taken_at"`
}
