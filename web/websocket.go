package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/gorilla/websocket"

	"github.com/ohcnetwork/mock-ptz-camera/ptz"
)

// WebSocket message and response types

type wsMessage struct {
	Type      string  `json:"type"`
	Pan       float64 `json:"pan,omitempty"`
	Tilt      float64 `json:"tilt,omitempty"`
	Zoom      float64 `json:"zoom,omitempty"`
	PanSpeed  float64 `json:"pan_speed,omitempty"`
	TiltSpeed float64 `json:"tilt_speed,omitempty"`
	ZoomSpeed float64 `json:"zoom_speed,omitempty"`
	Name      string  `json:"name,omitempty"`
	Token     string  `json:"token,omitempty"`
}

type wsStatusResponse struct {
	Type     string       `json:"type"`
	Position positionJSON `json:"position"`
	Moving   bool         `json:"moving"`
}

type positionJSON struct {
	Pan  float64 `json:"pan"`
	Tilt float64 `json:"tilt"`
	Zoom float64 `json:"zoom"`
}

func newPositionJSON(p ptz.Position) positionJSON {
	return positionJSON{Pan: p.Pan, Tilt: p.Tilt, Zoom: p.Zoom}
}

func newStatusResponse(s ptz.Status) wsStatusResponse {
	return wsStatusResponse{
		Type:     "status",
		Position: newPositionJSON(s.Position),
		Moving:   s.MoveStatus == ptz.MoveStatusMoving,
	}
}

type wsPresetsResponse struct {
	Type    string       `json:"type"`
	Presets []presetJSON `json:"presets"`
}

type presetJSON struct {
	Token    string       `json:"token"`
	Name     string       `json:"name"`
	Position positionJSON `json:"position"`
}

type wsPresetSetResponse struct {
	Type  string `json:"type"`
	Token string `json:"token"`
}

type wsErrorResponse struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

func (h *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	if !h.checkBasicAuth(r) {
		w.Header().Set("WWW-Authenticate", `Basic realm="Mock PTZ Camera"`)
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.WithError(err).Error("websocket upgrade error")
		return
	}
	defer conn.Close()
	log.WithField("remote", r.RemoteAddr).Info("websocket connected")

	// Status push goroutine
	done := make(chan struct{})
	defer close(done)

	go func() {
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()
		var lastPan, lastTilt, lastZoom float64
		var lastMoving bool
		first := true
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				status := h.ptzState.GetStatus()
				moving := status.MoveStatus == ptz.MoveStatusMoving
				if !first &&
					status.Position.Pan == lastPan &&
					status.Position.Tilt == lastTilt &&
					status.Position.Zoom == lastZoom &&
					moving == lastMoving {
					continue
				}
				lastPan = status.Position.Pan
				lastTilt = status.Position.Tilt
				lastZoom = status.Position.Zoom
				lastMoving = moving
				first = false
				resp := newStatusResponse(status)
				if err := conn.WriteJSON(resp); err != nil {
					return
				}
			}
		}
	}()

	// Message read loop
	for {
		_, msgBytes, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				log.WithError(err).Warn("websocket read error")
			}
			return
		}

		var msg wsMessage
		if err := json.Unmarshal(msgBytes, &msg); err != nil {
			h.sendError(conn, "invalid JSON")
			continue
		}

		h.handleWSMessage(conn, &msg)
	}
}

func (h *Server) handleWSMessage(conn *websocket.Conn, msg *wsMessage) {
	switch msg.Type {
	case "absolute_move":
		h.ptzState.AbsoluteMove(msg.Pan, msg.Tilt, msg.Zoom)

	case "relative_move":
		h.ptzState.RelativeMove(msg.Pan, msg.Tilt, msg.Zoom)

	case "continuous_move":
		h.ptzState.ContinuousMove(msg.PanSpeed, msg.TiltSpeed, msg.ZoomSpeed)

	case "stop":
		h.ptzState.StopMove(true, true)

	case "set_preset":
		name := msg.Name
		if name == "" {
			name = "Untitled"
		}
		token := h.ptzState.SetPreset("", name)
		conn.WriteJSON(wsPresetSetResponse{Type: "preset_set", Token: token})

	case "goto_preset":
		if !h.ptzState.GotoPreset(msg.Token) {
			h.sendError(conn, "preset not found")
		}

	case "remove_preset":
		if !h.ptzState.RemovePreset(msg.Token) {
			h.sendError(conn, "preset not found")
		}
		h.sendPresets(conn)

	case "get_presets":
		h.sendPresets(conn)

	case "get_status":
		status := h.ptzState.GetStatus()
		conn.WriteJSON(newStatusResponse(status))

	default:
		h.sendError(conn, fmt.Sprintf("unknown message type: %s", msg.Type))
	}
}

func (h *Server) sendPresets(conn *websocket.Conn) {
	presets := h.ptzState.GetPresets()
	items := make([]presetJSON, len(presets))
	for i, p := range presets {
		items[i] = presetJSON{
			Token:    p.Token,
			Name:     p.Name,
			Position: newPositionJSON(p.Position),
		}
	}
	conn.WriteJSON(wsPresetsResponse{Type: "presets", Presets: items})
}

func (h *Server) sendError(conn *websocket.Conn, message string) {
	conn.WriteJSON(wsErrorResponse{Type: "error", Message: message})
}
