package web

import "sync"

// AUHub fans out H.264 access units from a single source to multiple subscribers.
// It sits between the encoder's single output channel and multiple consumers
// (RTSP stream loop, WebSocket video clients). Subscribers that can't keep up
// have frames dropped via non-blocking sends. The hub caches the last keyframe
// (IDR) AU so new subscribers can start displaying video immediately.
type AUHub struct {
	mu      sync.Mutex
	subs    map[*AUSub]struct{}
	sps     []byte
	pps     []byte
	lastIDR [][]byte
}

// AUSub is a subscriber that receives access units on a buffered channel.
// It implements the rtsp.NALUSource interface via AccessUnits().
type AUSub struct {
	C   chan [][]byte
	hub *AUHub
}

// NewAUHub creates a new access-unit fan-out hub with cached SPS/PPS.
func NewAUHub(sps, pps []byte) *AUHub {
	return &AUHub{
		subs: make(map[*AUSub]struct{}),
		sps:  sps,
		pps:  pps,
	}
}

// SPS returns the cached Sequence Parameter Set.
func (h *AUHub) SPS() []byte { return h.sps }

// PPS returns the cached Picture Parameter Set.
func (h *AUHub) PPS() []byte { return h.pps }

// LastKeyframe returns the last cached IDR access unit, or nil if none yet.
func (h *AUHub) LastKeyframe() [][]byte {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.lastIDR
}

// Subscribe creates a new subscriber with the given channel buffer size.
func (h *AUHub) Subscribe(bufSize int) *AUSub {
	s := &AUSub{
		C:   make(chan [][]byte, bufSize),
		hub: h,
	}
	h.mu.Lock()
	h.subs[s] = struct{}{}
	h.mu.Unlock()
	return s
}

// Unsubscribe removes this subscriber from the hub.
func (s *AUSub) Unsubscribe() {
	s.hub.mu.Lock()
	delete(s.hub.subs, s)
	s.hub.mu.Unlock()
}

// AccessUnits returns the subscriber's AU channel.
// This satisfies the rtsp.NALUSource interface.
func (s *AUSub) AccessUnits() <-chan [][]byte {
	return s.C
}

// Run reads access units from source and broadcasts to all subscribers.
// It blocks until the source channel is closed.
func (h *AUHub) Run(source <-chan [][]byte) {
	for au := range source {
		isIDR := false
		for _, nalu := range au {
			if len(nalu) > 0 && (nalu[0]&0x1F) == 5 {
				isIDR = true
				break
			}
		}

		h.mu.Lock()
		if isIDR {
			h.lastIDR = au
		}
		for s := range h.subs {
			select {
			case s.C <- au:
			default:
			}
		}
		h.mu.Unlock()
	}

	h.mu.Lock()
	for s := range h.subs {
		close(s.C)
		delete(h.subs, s)
	}
	h.mu.Unlock()
}
