// encoder.go implements the H.264 video encoder pipeline.
//
// It manages an FFmpeg subprocess that accepts raw YUV420P frames on stdin,
// encodes them to H.264 using libx264 (ultrafast/zerolatency), and emits
// Annex B NAL units on stdout. These NAL units are parsed into access units
// (one per frame) and delivered via a channel for the RTSP stream loop.
//
// Key design decisions:
//   - Single FFmpeg process with automatic restart on unexpected exit.
//   - RGB24→I420 colour-space conversion is done in-process (rgb24ToI420)
//     to feed FFmpeg's rawvideo input in the format it expects.
//   - SPS/PPS parameter sets are captured from the encoder's first output
//     and exposed via SPS()/PPS() for RTSP SDP negotiation.
//   - H.264 level is auto-selected based on resolution and FPS to avoid
//     the libx264 "MB size exceeds level limit" warning.
//   - Access-unit channel has a capacity of 4 to absorb short encode
//     latency spikes without blocking the render loop.
package renderer

import (
	"bytes"
	"fmt"
	"io"
	"os/exec"
	"sync"

	log "github.com/sirupsen/logrus"
)

// Encoder wraps an FFmpeg H.264 encoding subprocess.
// It accepts raw RGB24 frames via WriteFrame, converts them to I420, pipes
// them to FFmpeg, and parses the output Annex B stream into access units
// available on the AccessUnits() channel.
type Encoder struct {
	width, height, fps int        // output dimensions and frame rate
	bitrate            string     // target bitrate string (e.g. "2M")
	cmd                *exec.Cmd  // running FFmpeg process
	stdin              io.WriteCloser // pipe to FFmpeg's stdin (raw YUV frames)
	aus                chan [][]byte  // access units channel: each element is a slice of NALUs forming one frame
	done               chan struct{}  // closed on Stop() to signal all goroutines
	stopOnce           sync.Once     // ensures Stop() is idempotent
	mu                 sync.Mutex    // protects cmd, stdin, running, sps, pps
	running            bool          // true while FFmpeg process is alive
	sps                []byte        // cached H.264 Sequence Parameter Set
	pps                []byte        // cached H.264 Picture Parameter Set
	spsPPSReady        chan struct{} // closed once SPS+PPS have been captured
	spsPPSOnce         sync.Once     // ensures spsPPSReady is closed exactly once
}

// NewEncoder creates a new H.264 encoder with the given output parameters.
// It immediately starts the FFmpeg subprocess. The bitrate string is passed
// directly to FFmpeg's -b:v flag (e.g. "2M", "4000k").
func NewEncoder(width, height, fps int, bitrate string) (*Encoder, error) {
	e := &Encoder{
		width: width, height: height, fps: fps,
		bitrate:     bitrate,
		aus:         make(chan [][]byte, 2),
		done:        make(chan struct{}),
		spsPPSReady: make(chan struct{}),
	}
	if err := e.startProcess(); err != nil {
		return nil, err
	}
	return e, nil
}

// SPS returns a thread-safe copy of the H.264 Sequence Parameter Set.
func (e *Encoder) SPS() []byte { return copyBytes(e, e.sps) }

// PPS returns a thread-safe copy of the H.264 Picture Parameter Set.
func (e *Encoder) PPS() []byte { return copyBytes(e, e.pps) }

// copyBytes returns a mutex-protected copy of a byte slice.
func copyBytes(e *Encoder, src []byte) []byte {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]byte, len(src))
	copy(out, src)
	return out
}

// WaitSPSPPS blocks until the encoder has extracted SPS and PPS from its first output.
func (e *Encoder) WaitSPSPPS() {
	<-e.spsPPSReady
}

// h264Level returns the minimum H.264 level string for the given resolution and FPS.
//
// H.264 levels define constraints on macroblock counts (resolution) and
// macroblock throughput (resolution × framerate). Each 16×16 pixel block
// is one macroblock. For example, 1920×1080 = 120×68 = 8160 macroblocks,
// which requires at least level 4.2 at 60fps.
//
// This function walks the level table from lowest to highest and returns
// the first level that can accommodate both the frame size and frame rate.
func h264Level(width, height, fps int) string {
	mbW := (width + 15) / 16   // macroblock columns (rounded up)
	mbH := (height + 15) / 16  // macroblock rows (rounded up)
	mbPerFrame := mbW * mbH    // total macroblocks per frame
	mbPerSec := mbPerFrame * fps // macroblock throughput

	// Table: level → (max macroblocks per frame, max macroblocks per second)
	levels := []struct {
		name    string
		maxMBs  int
		maxMBps int
	}{
		{"3.0", 1620, 40500},
		{"3.1", 3600, 108000},
		{"3.2", 5120, 216000},
		{"4.0", 8192, 245760},
		{"4.1", 8192, 245760},
		{"4.2", 8704, 522240},
		{"5.0", 22080, 589824},
		{"5.1", 36864, 983040},
		{"5.2", 36864, 2073600},
	}
	for _, l := range levels {
		if mbPerFrame <= l.maxMBs && mbPerSec <= l.maxMBps {
			return l.name
		}
	}
	return "5.2"
}

// buildStream constructs the FFmpeg command pipeline using ffmpeg-go.
//
// Encoding settings:
//   - Input: rawvideo YUV420P on stdin at the configured resolution/FPS.
//   - Codec: libx264 with "ultrafast" preset and "zerolatency" tune for
//     minimal encoding latency and CPU usage.
//   - Profile: baseline (no B-frames, simplest decode).
//   - Level: auto-selected via h264Level() to match resolution/FPS.
//   - Rate control: CBR using -b:v, -maxrate, -bufsize all set to bitrate.
//   - GOP: one IDR frame per second (keyint = FPS).
//   - Scene detection disabled (sc_threshold=0) for predictable IDR spacing.
//   - Single thread to avoid contention with the Go render workers.
//   - Output: raw H.264 Annex B on stdout.
func (e *Encoder) buildCmd() *exec.Cmd {
	gop := e.fps // 1 IDR per second (fewer expensive I-frames)
	if gop < 1 {
		gop = 1
	}
	level := h264Level(e.width, e.height, e.fps)
	log.WithField("level", level).Info("auto-selected H.264 level")

	gopStr := fmt.Sprintf("%d", gop)
	return exec.Command("ffmpeg",
		"-v", "warning",
		"-f", "rawvideo",
		"-pix_fmt", "rgb24",
		"-s", fmt.Sprintf("%dx%d", e.width, e.height),
		"-r", fmt.Sprintf("%d", e.fps),
		"-i", "pipe:0",
		"-c:v", "libx264",
		"-preset", "ultrafast",
		"-tune", "zerolatency",
		"-profile:v", "baseline",
		"-level", level,
		"-pix_fmt", "yuv420p",
		"-b:v", e.bitrate,
		"-maxrate", e.bitrate,
		"-bufsize", e.bitrate,
		"-g", gopStr,
		"-keyint_min", gopStr,
		"-sc_threshold", "0",
		"-f", "h264",
		"-an",
		"-flush_packets", "1",
		"-threads", "1",
		"pipe:1",
	)
}

// startProcess launches the FFmpeg subprocess and wires up stdin/stdout/stderr.
// It spawns three goroutines: one to log stderr, one to parse NALUs from stdout,
// and one to monitor the process and auto-restart on unexpected exit.
func (e *Encoder) startProcess() error {
	cmd := e.buildCmd()
	log.WithField("args", cmd.Args).Debug("starting ffmpeg encoder")

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("stderr pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start ffmpeg encoder: %w", err)
	}

	e.mu.Lock()
	e.cmd = cmd
	e.stdin = stdin
	e.running = true
	e.mu.Unlock()

	go e.logStderr(stderr)
	go e.readNALUs(stdout)
	go e.waitAndRestart()

	log.WithField("pid", cmd.Process.Pid).Info("ffmpeg encoder started")
	return nil
}

// logStderr continuously reads FFmpeg's stderr and logs any output as warnings.
func (e *Encoder) logStderr(r io.Reader) {
	buf := make([]byte, 4096)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			log.WithField("output", string(buf[:n])).Warn("ffmpeg stderr")
		}
		if err != nil {
			return
		}
	}
}

// waitAndRestart blocks until FFmpeg exits, then restarts it unless Stop() was called.
// This provides resilience against FFmpeg crashes or unexpected termination.
func (e *Encoder) waitAndRestart() {
	err := e.cmd.Wait()
	e.mu.Lock()
	e.running = false
	e.mu.Unlock()

	select {
	case <-e.done:
		return
	default:
	}

	// Allocate a fresh AU channel for the new readNALUs goroutine.
	e.aus = make(chan [][]byte, 2)

	log.WithError(err).Warn("ffmpeg exited, restarting...")
	if restartErr := e.startProcess(); restartErr != nil {
		log.WithError(restartErr).Error("failed to restart ffmpeg")
	}
}

// WriteFrame writes a raw RGB24 frame directly to the FFmpeg subprocess's
// stdin pipe. FFmpeg handles the RGB24→YUV420P conversion internally using
// SIMD-optimised libswscale, which is faster than a pure Go conversion.
func (e *Encoder) WriteFrame(frame []byte) error {
	expected := e.width * e.height * 3
	if len(frame) != expected {
		return fmt.Errorf("frame size mismatch: got %d, want %d", len(frame), expected)
	}
	e.mu.Lock()
	stdin := e.stdin
	running := e.running
	e.mu.Unlock()
	if !running || stdin == nil {
		return nil
	}
	_, err := stdin.Write(frame)
	return err
}


// AccessUnits returns a read-only channel that delivers H.264 access units.
// Each access unit is a slice of NAL units comprising exactly one encoded frame.
// The RTSP stream loop reads from this channel to packetise frames into RTP.
func (e *Encoder) AccessUnits() <-chan [][]byte { return e.aus }

// Stop gracefully shuts down the encoder: closes stdin to signal FFmpeg,
// kills the process. The access-unit channel is closed by readNALUs when
// FFmpeg's stdout reaches EOF. Safe to call multiple times.
func (e *Encoder) Stop() {
	e.stopOnce.Do(func() {
		close(e.done)
		e.mu.Lock()
		if e.stdin != nil {
			e.stdin.Close()
		}
		if e.cmd != nil && e.cmd.Process != nil {
			e.cmd.Process.Kill()
		}
		e.running = false
		e.mu.Unlock()
	})
}

// startCode3 is the 3-byte Annex B start code used to delimit NAL units.
var startCode3 = []byte{0x00, 0x00, 0x01}

// readNALUs continuously reads FFmpeg's stdout, splits the Annex B byte stream
// into individual NAL units, captures SPS/PPS parameter sets, and groups
// NALUs into access units (one per frame) sent on the aus channel.
//
// An access unit is considered complete when a VCL NALU (type 1=non-IDR slice
// or type 5=IDR slice) is encountered — at that point all accumulated NALUs
// are flushed as a single access unit.
func (e *Encoder) readNALUs(r io.Reader) {
	defer close(e.aus)

	buf := make([]byte, 1024*1024)
	var acc []byte
	var pendingAU [][]byte

	for {
		select {
		case <-e.done:
			return
		default:
		}
		n, err := r.Read(buf)
		if n > 0 {
			acc = append(acc, buf[:n]...)

			// Extract complete NALUs from accumulated data.
			// We search for 00 00 01 (or 00 00 00 01) boundaries.
			for {
				nalu, rest, ok := extractOneNALU(acc)
				if !ok {
					break
				}
				acc = rest

				if len(nalu) == 0 {
					continue
				}
				naluType := nalu[0] & 0x1F

				// Capture SPS/PPS
				if naluType == 7 {
					e.mu.Lock()
					e.sps = make([]byte, len(nalu))
					copy(e.sps, nalu)
					e.mu.Unlock()
				} else if naluType == 8 {
					e.mu.Lock()
					e.pps = make([]byte, len(nalu))
					copy(e.pps, nalu)
					e.mu.Unlock()
					e.spsPPSOnce.Do(func() { close(e.spsPPSReady) })
				}

				pendingAU = append(pendingAU, nalu)

				// VCL NALU (slice) completes the access unit.
				// With -slices 1, there's exactly one VCL NALU per frame.
				if naluType == 1 || naluType == 5 {
					select {
					case e.aus <- pendingAU:
					case <-e.done:
						return
					}
					pendingAU = nil
				}
			}
		}
		if err != nil {
			if len(pendingAU) > 0 {
				select {
				case e.aus <- pendingAU:
				case <-e.done:
				}
			}
			return
		}
	}
}

// extractOneNALU extracts one complete NALU from an Annex B byte stream.
//
// The Annex B format delimits NALUs with start codes (00 00 01 or 00 00 00 01).
// This function finds the first start code, then scans for the next one to
// determine the NALU boundary. Trailing zero bytes that belong to the next
// start code are excluded from the NALU data.
//
// Returns:
//   - nalu: the raw NALU bytes (without start code prefix)
//   - rest: remaining unparsed data (positioned at the next start code)
//   - ok:   false if no complete NALU could be extracted (need more data)
func extractOneNALU(data []byte) (nalu []byte, rest []byte, ok bool) {
	// Find first start code
	start := bytes.Index(data, startCode3)
	if start < 0 {
		return nil, data, false
	}

	// Determine start code length (3 or 4 bytes)
	scLen := 3
	if start > 0 && data[start-1] == 0x00 {
		start--
		scLen = 4
	}

	naluStart := start + scLen

	// Find the next start code after this NALU
	nextSC := bytes.Index(data[naluStart:], startCode3)
	if nextSC < 0 {
		// No next start code yet — NALU is incomplete
		return nil, data, false
	}
	nextSC += naluStart

	// Determine NALU end: back up past any trailing zero byte that's
	// part of the next start code (handles 00 00 00 01 correctly)
	naluEnd := nextSC
	if naluEnd > naluStart && data[naluEnd-1] == 0x00 {
		naluEnd--
	}

	// Copy NALU data
	naluData := make([]byte, naluEnd-naluStart)
	copy(naluData, data[naluStart:naluEnd])

	// Remaining data starts at the next start code
	// Back up to include the leading zero if it's a 4-byte code
	restStart := nextSC
	if restStart > 0 && data[restStart-1] == 0x00 {
		restStart--
	}

	return naluData, data[restStart:], true
}
