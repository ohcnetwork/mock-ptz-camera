package rtsp

import (
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/bluenviron/gortsplib/v5/pkg/format/rtph264"
)

// NALUSource provides a channel of H.264 access units (NALUs).
type NALUSource interface {
	AccessUnits() <-chan [][]byte
}

// StreamLoop reads encoded access units from src and sends them as RTP
// packets through the RTSP server.
//
// Every access unit from the encoder is sent — H.264 P-frames reference their
// predecessors, so skipping any AU corrupts every subsequent frame until the
// next IDR. Per-session packet dropping (when a client can't keep up) is
// handled inside gortsplib independently per session without affecting
// other clients.
//
// RTP timestamps are derived from wall-clock time rather than a fixed
// increment. This correctly handles the case where the encoder produces
// fewer frames than the configured FPS (e.g., 27fps at 1080p60) — the
// client sees accurate inter-frame timing regardless of actual throughput.
func StreamLoop(src NALUSource, rtpEncoder *rtph264.Encoder, srv *Server, fps int) {
	const clockRate = 90000
	start := time.Now()

	for au := range src.AccessUnits() {
		// Wall-clock timestamp: the number of 90kHz ticks since stream start.
		// This matches actual frame production timing regardless of whether
		// the encoder hits the target FPS.
		elapsed := time.Since(start)
		rtpTimestamp := uint32(elapsed.Seconds() * clockRate)

		pkts, err := rtpEncoder.Encode(au)
		if err != nil {
			log.WithError(err).Error("RTP encode error")
			continue
		}
		for _, pkt := range pkts {
			pkt.Timestamp = rtpTimestamp
			srv.WritePacketRTP(pkt)
		}
	}
}
