package rtsp

import (
	log "github.com/sirupsen/logrus"

	"github.com/bluenviron/gortsplib/v5/pkg/format/rtph264"
)

// NALUSource provides a channel of H.264 access units (NALUs).
type NALUSource interface {
	AccessUnits() <-chan [][]byte
}

// StreamLoop reads encoded access units from src and sends them as RTP
// packets through the RTSP server. With single-threaded zero-latency
// encoding, FFmpeg outputs one AU per input frame so this naturally runs
// at the same rate as the render loop.
func StreamLoop(src NALUSource, rtpEncoder *rtph264.Encoder, srv *Server, fps int) {
	clockRate := uint32(90000)
	timestampIncrement := clockRate / uint32(fps)
	var rtpTimestamp uint32

	for au := range src.AccessUnits() {
		pkts, err := rtpEncoder.Encode(au)
		if err != nil {
			log.WithError(err).Error("RTP encode error")
			continue
		}
		for _, pkt := range pkts {
			pkt.Timestamp = rtpTimestamp
			srv.WritePacketRTP(pkt)
		}
		rtpTimestamp += timestampIncrement
	}
}
