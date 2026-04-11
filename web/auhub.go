package web

import (
	"sync"

	log "github.com/sirupsen/logrus"
)

// AUHub fans out H.264 access units to multiple subscribers.
// It supports lifecycle callbacks: OnActive is called when the first
// subscriber joins, and OnIdle when the last subscriber leaves. This
// enables on-demand pipeline start/stop.
type AUHub struct {
	mu       sync.Mutex
	subs     map[*AUSub]struct{}
	sps      []byte
	pps      []byte
	lastIDR  [][]byte
	onActive func() // called when subscriber count goes 0→1
	onIdle   func() // called when subscriber count goes N→0
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

// SetCallbacks registers functions called when the first subscriber arrives
// (onActive) and when the last subscriber leaves (onIdle).
func (h *AUHub) SetCallbacks(onActive, onIdle func()) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.onActive = onActive
	h.onIdle = onIdle
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
// If this is the first subscriber and onActive is set, it is called.
func (h *AUHub) Subscribe(bufSize int) *AUSub {
	s := &AUSub{
		C:   make(chan [][]byte, bufSize),
		hub: h,
	}
	h.mu.Lock()
	wasEmpty := len(h.subs) == 0
	h.subs[s] = struct{}{}
	onActive := h.onActive
	h.mu.Unlock()

	if wasEmpty && onActive != nil {
		log.Info("first subscriber joined, activating pipeline")
		onActive()
	}
	return s
}

// Unsubscribe removes this subscriber from the hub and closes its channel.
// If this was the last subscriber and onIdle is set, it is called.
func (s *AUSub) Unsubscribe() {
	s.hub.mu.Lock()
	_, exists := s.hub.subs[s]
	if exists {
		delete(s.hub.subs, s)
		close(s.C)
	}
	nowEmpty := len(s.hub.subs) == 0
	onIdle := s.hub.onIdle
	s.hub.mu.Unlock()

	if nowEmpty && onIdle != nil {
		log.Info("last subscriber left, deactivating pipeline")
		onIdle()
	}
}

// AccessUnits returns the subscriber's AU channel.
// This satisfies the rtsp.NALUSource interface.
func (s *AUSub) AccessUnits() <-chan [][]byte {
	return s.C
}

// Broadcast sends an access unit to all subscribers (non-blocking).
// It caches IDR frames for late-joining subscribers.
func (h *AUHub) Broadcast(au [][]byte) {
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
