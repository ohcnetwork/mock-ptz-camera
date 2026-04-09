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
	panMin = -1.0
	panMax = 1.0

	// Tilt: -30° to +90° mapped to -0.33 to 1.0 in normalized space
	tiltMin = -0.33
	tiltMax = 1.0

	// Zoom: 1x to 20x
	zoomMin = 0.0
	zoomMax = 1.0
)

type Position struct {
	Pan  float64 // -1.0 to +1.0
	Tilt float64 // -1.0 to +1.0
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
		position: Position{Pan: 0, Tilt: 0, Zoom: 0},
		presets:  make(map[string]Preset),
		onChange: onChange,
		stopCh:   make(chan struct{}),
	}
	// Set a default "Home" preset at the initial position
	s.presets["preset_home"] = Preset{Token: "preset_home", Name: "Home", Position: s.position}
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
		Pan:  clamp(pan, panMin, panMax),
		Tilt: clamp(tilt, tiltMin, tiltMax),
		Zoom: clamp(zoom, zoomMin, zoomMax),
	}
	s.target = &t
	s.moving = true
	s.mu.Unlock()
}

func (s *State) RelativeMove(dPan, dTilt, dZoom float64) {
	s.mu.Lock()
	s.velocity = Velocity{}
	t := Position{
		Pan:  clamp(s.position.Pan+dPan, panMin, panMax),
		Tilt: clamp(s.position.Tilt+dTilt, tiltMin, tiltMax),
		Zoom: clamp(s.position.Zoom+dZoom, zoomMin, zoomMax),
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

		s.position.Pan += (s.target.Pan - s.position.Pan) * alpha
		s.position.Tilt += (s.target.Tilt - s.position.Tilt) * alpha
		s.position.Zoom += (s.target.Zoom - s.position.Zoom) * alpha

		// Snap when close enough
		const eps = 0.001
		if math.Abs(s.target.Pan-s.position.Pan) < eps &&
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
	s.position.Zoom = clamp(s.position.Zoom+s.velocity.ZoomSpeed*zoomMoveSpeed*dt, zoomMin, zoomMax)

	// Autoflip: when tilt exceeds max, flip pan 180° and mirror tilt back
	if s.position.Tilt > tiltMax {
		s.position.Tilt = 2*tiltMax - s.position.Tilt
		s.position.Pan += 1.0 // 180° pan flip
		s.velocity.TiltSpeed = -s.velocity.TiltSpeed
	} else if s.position.Tilt < tiltMin {
		s.position.Tilt = clamp(s.position.Tilt, tiltMin, tiltMax)
	}

	// Pan wraps around continuously (360° rotation)
	if s.position.Pan > panMax {
		s.position.Pan -= (panMax - panMin)
	} else if s.position.Pan < panMin {
		s.position.Pan += (panMax - panMin)
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
