package main

import (
	"sync"

	log "github.com/sirupsen/logrus"

	"github.com/ohcnetwork/mock-ptz-camera/ptz"
	"github.com/ohcnetwork/mock-ptz-camera/renderer"
	"github.com/ohcnetwork/mock-ptz-camera/web"
)

// Pipeline manages the on-demand lifecycle of the rendering and encoding
// pipeline. It starts the FFmpeg encoder and render loop when the first
// consumer subscribes to the AUHub, and stops them when the last consumer
// unsubscribes. This avoids burning CPU when nobody is watching.
type Pipeline struct {
	mu       sync.Mutex
	running  bool
	r        renderer.Renderer
	ptzState *ptz.State
	auHub    *web.AUHub
	width    int
	height   int
	fps      int
	bitrate  string
	encoder  *renderer.Encoder
	done     chan struct{}
}

// NewPipeline creates a new on-demand pipeline and registers itself as the
// AUHub's active/idle callbacks.
func NewPipeline(r renderer.Renderer, ptzState *ptz.State, auHub *web.AUHub, width, height, fps int, bitrate string) *Pipeline {
	p := &Pipeline{
		r:        r,
		ptzState: ptzState,
		auHub:    auHub,
		width:    width,
		height:   height,
		fps:      fps,
		bitrate:  bitrate,
	}
	auHub.SetCallbacks(p.Start, p.Stop)
	return p
}

// Start launches the encoder and render loop. Safe to call if already running.
func (p *Pipeline) Start() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.running {
		return
	}

	encoder, err := renderer.NewEncoder(p.width, p.height, p.fps, p.bitrate)
	if err != nil {
		log.WithError(err).Error("failed to start encoder for on-demand pipeline")
		return
	}

	p.encoder = encoder
	p.done = make(chan struct{})
	p.running = true

	log.Info("pipeline started (on-demand)")

	done := p.done

	// Broadcast loop: read AUs from encoder and fan out via AUHub.
	go func() {
		for au := range encoder.AccessUnits() {
			p.auHub.Broadcast(au)
		}
	}()

	// Render loop: produce frames and feed to encoder.
	go renderer.RenderLoop(p.r, encoder, p.ptzState, p.fps, done)
}

// Stop shuts down the encoder and render loop. Safe to call if already stopped.
func (p *Pipeline) Stop() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.running {
		return
	}

	close(p.done)      // signals render loop to exit
	p.encoder.Stop()   // kills FFmpeg, closes AccessUnits channel → broadcast loop exits
	p.running = false

	log.Info("pipeline stopped (no consumers)")
}
