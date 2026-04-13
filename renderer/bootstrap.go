package renderer

import (
	log "github.com/sirupsen/logrus"
)

// BootstrapSPSPPS spins up a temporary encoder, feeds it a blank frame to
// extract the H.264 SPS and PPS parameter sets, then tears it down.
// These are needed before any real encoding begins (for RTSP SDP, etc.).
func BootstrapSPSPPS(width, height, fps int, bitrate string) (sps, pps []byte, err error) {
	encoder, err := NewEncoder(width, height, fps, bitrate)
	if err != nil {
		return nil, nil, err
	}

	blankFrame := make([]byte, width*height*3)
	if err := encoder.WriteFrame(blankFrame); err != nil {
		encoder.Stop()
		return nil, nil, err
	}
	encoder.WaitSPSPPS()
	sps, pps = encoder.SPS(), encoder.PPS()
	log.WithFields(log.Fields{
		"sps_bytes": len(sps), "pps_bytes": len(pps),
	}).Debug("got SPS/PPS from encoder")

	// Drain stale AU from the blank frame, then stop the bootstrap encoder.
	select {
	case <-encoder.AccessUnits():
	default:
	}
	encoder.Stop()

	return sps, pps, nil
}
