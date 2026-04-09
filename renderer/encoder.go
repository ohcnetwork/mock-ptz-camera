package renderer

import (
	"bytes"
	"fmt"
	"io"
	"os/exec"
	"sync"

	log "github.com/sirupsen/logrus"
)

type Encoder struct {
	width, height, fps int
	cmd                *exec.Cmd
	stdin              io.WriteCloser
	aus                chan [][]byte // access units: each element is a slice of NALUs forming one frame
	done               chan struct{}
	stopOnce           sync.Once
	mu                 sync.Mutex
	running            bool
	sps                []byte
	pps                []byte
	spsPPSReady        chan struct{}
	spsPPSOnce         sync.Once
}

func NewEncoder(width, height, fps int) (*Encoder, error) {
	e := &Encoder{
		width: width, height: height, fps: fps,
		aus:         make(chan [][]byte, 256),
		done:        make(chan struct{}),
		spsPPSReady: make(chan struct{}),
	}
	if err := e.startProcess(); err != nil {
		return nil, err
	}
	return e, nil
}

func (e *Encoder) SPS() []byte { return copyBytes(e, e.sps) }
func (e *Encoder) PPS() []byte { return copyBytes(e, e.pps) }

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

func (e *Encoder) buildArgs() []string {
	return []string{
		"-f", "rawvideo",
		"-pix_fmt", "rgb24",
		"-s", fmt.Sprintf("%dx%d", e.width, e.height),
		"-r", fmt.Sprintf("%d", e.fps),
		"-i", "pipe:0",
		"-pix_fmt", "yuv420p",
		"-c:v", "libx264",
		"-preset", "ultrafast",
		"-tune", "zerolatency",
		"-profile:v", "baseline",
		"-level", "3.1",
		"-crf", "23",
		"-g", fmt.Sprintf("%d", e.fps),
		"-slices", "1",
		"-threads", "1",
		"-f", "h264",
		"-an",
		"-flush_packets", "1",
		"-v", "warning",
		"pipe:1",
	}
}

func (e *Encoder) startProcess() error {
	args := e.buildArgs()
	log.WithField("args", args).Debug("starting ffmpeg encoder")
	cmd := exec.Command("ffmpeg", args...)

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

	log.WithError(err).Warn("ffmpeg exited, restarting...")
	if restartErr := e.startProcess(); restartErr != nil {
		log.WithError(restartErr).Error("failed to restart ffmpeg")
	}
}

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

func (e *Encoder) AccessUnits() <-chan [][]byte { return e.aus }

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
		close(e.aus)
	})
}

var startCode3 = []byte{0x00, 0x00, 0x01}

func (e *Encoder) readNALUs(r io.Reader) {
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
// Returns the NALU data (without start code), remaining data, and success flag.
// Uses the same trailing-zero handling as the official h264.AnnexB parser.
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
