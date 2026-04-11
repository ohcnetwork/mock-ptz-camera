// renderloop.go implements the main frame production loop that drives the
// entire rendering pipeline.
//
// The loop runs at the configured FPS using a time.Ticker, and on each tick:
//  1. Reads the current PTZ position from the shared state.
//  2. Calls the Renderer to produce an RGB24 frame.
//  3. Writes the frame to the H.264 Encoder (which feeds the RTSP/WebSocket streams).
//
// FPS is measured over 1-second windows. Stats are logged every 5 seconds.
package renderer

import (
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/ohcnetwork/mock-ptz-camera/ptz"
)

// Renderer is the interface for frame producers. Both PanoRenderer and
// TestRenderer implement this interface. The render loop calls Render()
// once per tick to obtain the next RGB24 frame.
type Renderer interface {
	Render(pos ptz.Position, fps float64) []byte
}

// RenderLoop is the main production loop that ties the renderer and encoder
// together. It runs on its own goroutine and never returns.
//
// On each tick (at the configured FPS):
//  1. Fetches the current PTZ position from ptzState.
//  2. Calls r.Render() to produce an RGB24 frame.
//  3. Writes the frame to encoder.WriteFrame() → FFmpeg → H.264 AUs.
//
// FPS is measured over 1-second windows. Stats are logged every 5 seconds.
func RenderLoop(r Renderer, encoder *Encoder, ptzState *ptz.State, fps int) {
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
