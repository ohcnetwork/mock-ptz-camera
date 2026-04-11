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
// packets through the RTSP server. It returns when the source channel closes.
func StreamLoop(src NALUSource, rtpEncoder *rtph264.Encoder, srv *Server) {
	const clockRate = 90000
	start := time.Now()

	for au := range src.AccessUnits() {
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
	log.Debug("RTSP stream loop exited")
}
