// renderloop.go implements the main frame production loop that drives the
// entire rendering pipeline.
//
// The loop runs at the configured FPS using a time.Ticker, and on each tick:
//  1. Reads the current PTZ position from the shared state.
//  2. Calls the Renderer to produce an RGB24 frame.
//  3. Writes the frame to the H.264 Encoder (which feeds the RTSP stream).
//  4. Optionally encodes a JPEG snapshot for the MJPEG web preview stream
//     at a capped rate (~15fps) to avoid burning CPU on JPEG at high FPS.
//
// The JPEG encoding is fully asynchronous — a separate goroutine handles
// the encode while the render loop continues. If the previous JPEG encode
// hasn't finished, the frame is skipped (non-blocking drop).
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

// FrameSink receives JPEG snapshots for the web MJPEG preview stream.
// The web server's FrameStore implements this interface.
type FrameSink interface {
	SetFrame(jpeg []byte)
}

// RenderLoop is the main production loop that ties the renderer, encoder,
// and MJPEG sink together. It runs on its own goroutine and never returns.
//
// On each tick (at the configured FPS):
//  1. Fetches the current PTZ position from ptzState.
//  2. Calls r.Render() to produce an RGB24 frame.
//  3. Writes the frame to encoder.WriteFrame() → FFmpeg → RTSP.
//  4. Every ⌊fps/15⌋ ticks, asynchronously JPEG-encodes the frame
//     and delivers it to the FrameSink for the MJPEG web stream.
//
// FPS is measured over 1-second windows. Stats are logged every 5 seconds.
func RenderLoop(r Renderer, encoder *Encoder, ptzState *ptz.State, fps int, width, height int, sink FrameSink) {
	frameDuration := time.Duration(float64(time.Second) / float64(fps))
	ticker := time.NewTicker(frameDuration)
	defer ticker.Stop()

	jpegEnc := NewJPEGEncoder(width, height)
	jpegBuf := make([]byte, width*height*3)
	jpegDone := make(chan struct{}, 1)
	jpegDone <- struct{}{} // mark as initially idle

	// Cap MJPEG to ~15fps regardless of render FPS. This gives steady
	// pacing for the browser and avoids burning CPU on JPEG at 60fps.
	const mjpegTargetFPS = 15
	jpegEvery := fps / mjpegTargetFPS
	if jpegEvery < 1 {
		jpegEvery = 1
	}
	var jpegCounter int

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

		// JPEG encode at capped rate for the MJPEG preview stream.
		jpegCounter++
		if jpegCounter >= jpegEvery {
			jpegCounter = 0
			select {
			case <-jpegDone:
				copy(jpegBuf, frame)
				go func() {
					if jpegData, err := jpegEnc.Encode(jpegBuf); err == nil {
						sink.SetFrame(jpegData)
					}
					jpegDone <- struct{}{}
				}()
			default:
				// Previous JPEG still encoding — skip.
			}
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
