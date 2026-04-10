package renderer

import (
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/ohcnetwork/mock-ptz-camera/ptz"
)

// Renderer produces raw RGB24 frames for the render loop.
type Renderer interface {
	Render(pos ptz.Position, fps float64) []byte
}

// FrameSink receives JPEG frames produced by the render loop.
type FrameSink interface {
	SetFrame(jpeg []byte)
}

// RenderLoop feeds rendered frames to the encoder at the target FPS.
// JPEG snapshots are also sent to sink for the web MJPEG stream.
func RenderLoop(r Renderer, encoder *Encoder, ptzState *ptz.State, fps int, width, height int, sink FrameSink) {
	frameDuration := time.Duration(float64(time.Second) / float64(fps))
	ticker := time.NewTicker(frameDuration)
	defer ticker.Stop()

	var measuredFPS float64
	var frameCount int
	var logFrames int
	lastFPSTime := time.Now()
	lastLogTime := time.Now()

	for range ticker.C {
		pos := ptzState.GetPosition()
		frame := r.Render(pos, measuredFPS)

		if err := encoder.WriteFrame(frame); err != nil {
			log.WithError(err).Error("encoder write error")
			continue
		}

		if jpegData, err := EncodeJPEG(frame, width, height); err == nil {
			sink.SetFrame(jpegData)
		}

		frameCount++
		logFrames++

		if elapsed := time.Since(lastFPSTime); elapsed >= time.Second {
			measuredFPS = float64(frameCount) / elapsed.Seconds()
			frameCount = 0
			lastFPSTime = time.Now()
		}

		if time.Since(lastLogTime) >= 5*time.Second && logFrames > 0 {
			log.WithFields(log.Fields{"fps": measuredFPS, "frames": logFrames}).Debug("render stats")
			logFrames = 0
			lastLogTime = time.Now()
		}
	}
}
