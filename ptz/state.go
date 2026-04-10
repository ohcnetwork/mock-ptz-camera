package ptz

import (
	"fmt"
	"math"
	"sync"
	"time"
)

const (
	updateInterval = 50 * time.Millisecond
	panSpeed       = 1.5 // normalized units per second at max speed (~240°/s over 360° range)
	tiltSpeed      = 1.0 // normalized units per second at max speed
	zoomMoveSpeed  = 0.5 // normalized units per second

	// Pan wraps around continuously (360° rotation)
	PanMin = -1.0
	PanMax = 1.0

	// Tilt: 1.0 = ~10° above horizontal, 0.8 = horizontal, -1.0 = straight down (nadir)
	// Autoflip occurs when tilting past -1.0 (through nadir)
	TiltMin     = -1.0
	TiltMax     = 1.0
	TiltHorizon = 0.8 // tilt value corresponding to 0° (horizontal)

	// Zoom: 1x to 20x
	ZoomMin = 0.0
	ZoomMax = 1.0
)

type Position struct {
	Pan  float64 // -1.0 to +1.0
	Tilt float64 // TiltMin to TiltMax
	Zoom float64 // 0.0 to 1.0
}

type Velocity struct {
	PanSpeed  float64
	TiltSpeed float64
	ZoomSpeed float64
}

type MoveStatus int

const (
	MoveStatusIdle MoveStatus = iota
	MoveStatusMoving
)

type Status struct {
	Position   Position
	MoveStatus MoveStatus
}

type Preset struct {
	Token    string
	Name     string
	Position Position
}

type PositionChangedFunc func(Status)

type State struct {
	mu           sync.RWMutex
	position     Position
	velocity     Velocity
	target       *Position // non-nil when animating toward a target
	moving       bool
	presets      map[string]Preset
	nextPresetID int
	onChange     PositionChangedFunc
	stopCh       chan struct{}
	stopOnce     sync.Once
}

func NewState(onChange PositionChangedFunc) *State {
	s := &State{
		position: Position{Pan: 0, Tilt: TiltHorizon, Zoom: 0},
		presets:  make(map[string]Preset),
		onChange: onChange,
		stopCh:   make(chan struct{}),
	}
	// Set a default "Home" preset at the initial position
	s.presets["preset_home"] = Preset{Token: "preset_home", Name: "Home", Position: Position{Pan: 0, Tilt: TiltHorizon, Zoom: 0}}
	s.nextPresetID = 0
	go s.updateLoop()
	return s
}

func (s *State) Stop() {
	s.stopOnce.Do(func() { close(s.stopCh) })
}

func (s *State) GetStatus() Status {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ms := MoveStatusIdle
	if s.moving {
		ms = MoveStatusMoving
	}
	return Status{Position: s.position, MoveStatus: ms}
}

func (s *State) GetPosition() Position {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.position
}

func (s *State) ContinuousMove(ps, ts, zs float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.target = nil // cancel any target-seeking animation
	s.velocity = Velocity{
		PanSpeed:  clamp(ps, -1, 1),
		TiltSpeed: clamp(ts, -1, 1),
		ZoomSpeed: clamp(zs, -1, 1),
	}
	s.moving = s.velocity.PanSpeed != 0 || s.velocity.TiltSpeed != 0 || s.velocity.ZoomSpeed != 0
}

func (s *State) AbsoluteMove(pan, tilt, zoom float64) {
	s.mu.Lock()
	s.velocity = Velocity{}
	t := Position{
		Pan:  clamp(pan, PanMin, PanMax),
		Tilt: clamp(tilt, TiltMin, TiltMax),
		Zoom: clamp(zoom, ZoomMin, ZoomMax),
	}
	s.target = &t
	s.moving = true
	s.mu.Unlock()
}

func (s *State) RelativeMove(dPan, dTilt, dZoom float64) {
	s.mu.Lock()
	s.velocity = Velocity{}
	newTilt := s.position.Tilt + dTilt
	newPan := s.position.Pan + dPan
	// Autoflip: tilt past nadir (-1.0) → pan 180° and mirror tilt
	if newTilt < TiltMin {
		newTilt = 2*TiltMin - newTilt
		newPan += 1.0
	}
	newTilt = clamp(newTilt, TiltMin, TiltMax)
	// Wrap pan
	if newPan > PanMax {
		newPan -= (PanMax - PanMin)
	} else if newPan < PanMin {
		newPan += (PanMax - PanMin)
	}
	t := Position{
		Pan:  newPan,
		Tilt: newTilt,
		Zoom: clamp(s.position.Zoom+dZoom, ZoomMin, ZoomMax),
	}
	s.target = &t
	s.moving = true
	s.mu.Unlock()
}

func (s *State) StopMove(stopPanTilt, stopZoom bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.target = nil // cancel any target-seeking animation
	if stopPanTilt {
		s.velocity.PanSpeed = 0
		s.velocity.TiltSpeed = 0
	}
	if stopZoom {
		s.velocity.ZoomSpeed = 0
	}
	s.moving = s.velocity.PanSpeed != 0 || s.velocity.TiltSpeed != 0 || s.velocity.ZoomSpeed != 0
}

func (s *State) GetPresets() []Preset {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]Preset, 0, len(s.presets))
	for _, p := range s.presets {
		result = append(result, p)
	}
	return result
}

func (s *State) SetPreset(token, name string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if token == "" {
		s.nextPresetID++
		token = fmt.Sprintf("preset_%d", s.nextPresetID)
	}
	s.presets[token] = Preset{Token: token, Name: name, Position: s.position}
	return token
}

func (s *State) GotoPreset(token string) bool {
	s.mu.Lock()
	p, ok := s.presets[token]
	if !ok {
		s.mu.Unlock()
		return false
	}
	s.velocity = Velocity{}
	t := p.Position
	s.target = &t
	s.moving = true
	s.mu.Unlock()
	return true
}

func (s *State) RemovePreset(token string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.presets[token]; !ok {
		return false
	}
	delete(s.presets, token)
	return true
}

func (s *State) updateLoop() {
	ticker := time.NewTicker(updateInterval)
	defer ticker.Stop()
	for {
		select {
		case <-s.stopCh:
			return
		case <-ticker.C:
			s.tick()
		}
	}
}

func (s *State) tick() {
	s.mu.Lock()

	// Target-seeking animation (AbsoluteMove, RelativeMove, GotoPreset)
	if s.target != nil {
		const lerpRate = 5.0 // exponential approach factor per second
		dt := updateInterval.Seconds()
		alpha := 1.0 - math.Exp(-lerpRate*dt)

		// Shortest-path delta for pan (wraps around -1..+1)
		panDelta := s.target.Pan - s.position.Pan
		if panDelta > 1.0 {
			panDelta -= 2.0
		} else if panDelta < -1.0 {
			panDelta += 2.0
		}
		s.position.Pan += panDelta * alpha
		// Wrap pan
		if s.position.Pan > PanMax {
			s.position.Pan -= (PanMax - PanMin)
		} else if s.position.Pan < PanMin {
			s.position.Pan += (PanMax - PanMin)
		}
		s.position.Tilt += (s.target.Tilt - s.position.Tilt) * alpha
		s.position.Zoom += (s.target.Zoom - s.position.Zoom) * alpha
		// Snap when close enough
		const eps = 0.001
		panSnap := s.target.Pan - s.position.Pan
		if panSnap > 1.0 {
			panSnap -= 2.0
		} else if panSnap < -1.0 {
			panSnap += 2.0
		}
		if math.Abs(panSnap) < eps &&
			math.Abs(s.target.Tilt-s.position.Tilt) < eps &&
			math.Abs(s.target.Zoom-s.position.Zoom) < eps {
			s.position = *s.target
			s.target = nil
			s.moving = false
			status := Status{Position: s.position, MoveStatus: MoveStatusIdle}
			s.mu.Unlock()
			s.notifyChange(status)
			return
		}
		status := Status{Position: s.position, MoveStatus: MoveStatusMoving}
		s.mu.Unlock()
		s.notifyChange(status)
		return
	}

	// Velocity-based continuous movement
	if !s.moving {
		s.mu.Unlock()
		return
	}
	dt := updateInterval.Seconds()
	s.position.Pan += s.velocity.PanSpeed * panSpeed * dt
	s.position.Tilt += s.velocity.TiltSpeed * tiltSpeed * dt
	s.position.Zoom = clamp(s.position.Zoom+s.velocity.ZoomSpeed*zoomMoveSpeed*dt, ZoomMin, ZoomMax)

	// Autoflip: tilt past nadir (-1.0) → pan 180° and mirror tilt
	if s.position.Tilt < TiltMin {
		s.position.Tilt = 2*TiltMin - s.position.Tilt
		s.position.Pan += 1.0
		s.velocity.TiltSpeed = -s.velocity.TiltSpeed
	} else if s.position.Tilt > TiltMax {
		s.position.Tilt = TiltMax
	}

	// Pan wraps around continuously (360° rotation)
	if s.position.Pan > PanMax {
		s.position.Pan -= (PanMax - PanMin)
	} else if s.position.Pan < PanMin {
		s.position.Pan += (PanMax - PanMin)
	}
	status := Status{Position: s.position, MoveStatus: MoveStatusMoving}
	s.mu.Unlock()
	s.notifyChange(status)
}

func (s *State) notifyChange(status Status) {
	if s.onChange != nil {
		s.onChange(status)
	}
}

func clamp(v, min, max float64) float64 {
	return math.Min(math.Max(v, min), max)
}
