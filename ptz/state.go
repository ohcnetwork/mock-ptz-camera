package ptz

import (
	"fmt"
	"math"
	"sync"
	"time"
)

const (
	updateInterval = 50 * time.Millisecond
	panSpeed       = 1.5 // max pan velocity: ~240°/s across full 360° range
	tiltSpeed      = 1.0 // max tilt velocity in normalized units/s
	zoomMoveSpeed  = 0.5 // max zoom velocity in normalized units/s

	// Pan range: normalized -1..+1 mapping to 0°..360°, wraps continuously.
	PanMin = -1.0
	PanMax = 1.0

	// Tilt range: +1.0 ≈ 10° above horizontal, 0.8 = horizontal, -1.0 = nadir (straight down).
	// Tilting past nadir triggers an autoflip (180° pan reversal).
	TiltMin     = -1.0
	TiltMax     = 1.0
	TiltHorizon = 0.8 // normalized tilt at 0° elevation (horizontal)

	// Zoom range: 0.0 = 1× (wide), 1.0 = 20× (telephoto).
	ZoomMin = 0.0
	ZoomMax = 1.0

	autoflipPanRate = 2.0 // pan velocity during autoflip (normalized units/s)
)

type Position struct {
	Pan  float64 // -1.0..+1.0 (maps to 0°..360°)
	Tilt float64 // -1.0..+1.0 (nadir to above horizon)
	Zoom float64 //  0.0..1.0  (1× to 20×)
}

type Velocity struct {
	PanSpeed  float64
	TiltSpeed float64
	ZoomSpeed float64
}

type MoveStatus int

const (
	MoveStatusIdle   MoveStatus = iota
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

// State manages the simulated PTZ camera with an internal update loop.
type State struct {
	mu           sync.RWMutex
	position     Position
	velocity     Velocity
	target       *Position // non-nil while animating toward AbsoluteMove/GotoPreset target
	moving       bool
	presets      map[string]Preset
	nextPresetID int
	onChange     PositionChangedFunc
	stopCh       chan struct{}
	stopOnce     sync.Once

	// Autoflip: when tilt passes nadir, the camera pans 180° and reverses tilt.
	autoflipping         bool    // true during the 180° pan phase
	autoflipPanRemaining float64 // remaining pan distance (positive)
	autoflipPanDir       float64 // +1 or -1
	tiltReversed         bool    // tilt input is inverted after autoflip completes
}

func NewState(onChange PositionChangedFunc) *State {
	home := Position{Pan: 0, Tilt: TiltHorizon, Zoom: 0}
	s := &State{
		position: home,
		presets:  make(map[string]Preset),
		onChange: onChange,
		stopCh:   make(chan struct{}),
	}
	s.presets["preset_home"] = Preset{Token: "preset_home", Name: "Home", Position: home}
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

// ContinuousMove sets constant-velocity movement, cancelling any in-progress animation.
func (s *State) ContinuousMove(ps, ts, zs float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.target = nil
	s.autoflipping = false
	s.tiltReversed = false
	s.velocity = Velocity{
		PanSpeed:  Clamp(ps, -1, 1),
		TiltSpeed: Clamp(ts, -1, 1),
		ZoomSpeed: Clamp(zs, -1, 1),
	}
	s.moving = s.velocity.PanSpeed != 0 || s.velocity.TiltSpeed != 0 || s.velocity.ZoomSpeed != 0
}

func (s *State) AbsoluteMove(pan, tilt, zoom float64) {
	s.mu.Lock()
	s.velocity = Velocity{}
	s.autoflipping = false
	s.tiltReversed = false
	t := Position{
		Pan:  Clamp(pan, PanMin, PanMax),
		Tilt: Clamp(tilt, TiltMin, TiltMax),
		Zoom: Clamp(zoom, ZoomMin, ZoomMax),
	}
	s.target = &t
	s.moving = true
	s.mu.Unlock()
}

func (s *State) RelativeMove(dPan, dTilt, dZoom float64) {
	s.mu.Lock()
	s.velocity = Velocity{}

	newTilt := Clamp(s.position.Tilt+dTilt, TiltMin, TiltMax)
	newPan := s.position.Pan + dPan
	if newPan > PanMax {
		newPan -= (PanMax - PanMin)
	} else if newPan < PanMin {
		newPan += (PanMax - PanMin)
	}
	t := Position{
		Pan:  newPan,
		Tilt: newTilt,
		Zoom: Clamp(s.position.Zoom+dZoom, ZoomMin, ZoomMax),
	}
	s.target = &t
	s.moving = true
	s.mu.Unlock()
}

func (s *State) StopMove(stopPanTilt, stopZoom bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.target = nil
	s.autoflipping = false
	s.tiltReversed = false
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
	s.autoflipping = false
	s.tiltReversed = false
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

	// Exponential ease toward target (AbsoluteMove / RelativeMove / GotoPreset)
	if s.target != nil {
		const lerpRate = 5.0 // exponential decay rate (units/s)
		dt := updateInterval.Seconds()
		alpha := 1.0 - math.Exp(-lerpRate*dt)

		// Shortest-path pan delta (wraps at ±1)
		panDelta := s.target.Pan - s.position.Pan
		if panDelta > 1.0 {
			panDelta -= 2.0
		} else if panDelta < -1.0 {
			panDelta += 2.0
		}
		s.position.Pan += panDelta * alpha
		if s.position.Pan > PanMax {
			s.position.Pan -= (PanMax - PanMin)
		} else if s.position.Pan < PanMin {
			s.position.Pan += (PanMax - PanMin)
		}
		s.position.Tilt += (s.target.Tilt - s.position.Tilt) * alpha
		s.position.Zoom += (s.target.Zoom - s.position.Zoom) * alpha

		// Snap to target once within tolerance
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

	// Continuous velocity-based movement
	if !s.moving {
		s.mu.Unlock()
		return
	}
	dt := updateInterval.Seconds()

	if s.autoflipping {
		// Autoflip: rotate pan 180° at constant speed while holding tilt at nadir
		panStep := autoflipPanRate * dt
		if panStep >= s.autoflipPanRemaining {
			s.position.Pan += s.autoflipPanDir * s.autoflipPanRemaining
			s.autoflipPanRemaining = 0
			s.autoflipping = false
			s.tiltReversed = true
			s.velocity.TiltSpeed = -s.velocity.TiltSpeed // invert tilt direction post-flip
		} else {
			s.position.Pan += s.autoflipPanDir * panStep
			s.autoflipPanRemaining -= panStep
		}
		s.position.Tilt = TiltMin
		s.position.Zoom = Clamp(s.position.Zoom+s.velocity.ZoomSpeed*zoomMoveSpeed*dt, ZoomMin, ZoomMax)
	} else {
		s.position.Pan += s.velocity.PanSpeed * panSpeed * dt
		s.position.Tilt += s.velocity.TiltSpeed * tiltSpeed * dt
		s.position.Zoom = Clamp(s.position.Zoom+s.velocity.ZoomSpeed*zoomMoveSpeed*dt, ZoomMin, ZoomMax)

		// Trigger autoflip when tilt exceeds nadir
		if s.position.Tilt < TiltMin {
			s.position.Tilt = TiltMin
			s.autoflipping = true
			s.autoflipPanRemaining = 1.0 // 180° in normalized units
			s.autoflipPanDir = 1.0
		} else if s.position.Tilt > TiltMax {
			s.position.Tilt = TiltMax
		}
	}

	// Wrap pan to stay within -1..+1
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

// PanDeg converts normalized pan to degrees (0°..360°).
func (p Position) PanDeg() float64 {
	d := p.Pan * 180.0
	if d < 0 {
		d += 360.0
	}
	return d
}

// TiltDeg converts normalized tilt to degrees (0° = horizontal, 90° = nadir).
func (p Position) TiltDeg() float64 {
	return (TiltHorizon - p.Tilt) * 90.0 / (TiltHorizon - TiltMin)
}

// ZoomX returns the zoom as a linear multiplier (1× to 20×).
func (p Position) ZoomX() float64 {
	return 1.0 + p.Zoom*19.0
}

func Clamp(v, min, max float64) float64 {
	return math.Min(math.Max(v, min), max)
}
