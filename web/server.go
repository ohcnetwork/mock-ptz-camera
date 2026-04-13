package web

import (
	"crypto/subtle"
	"embed"
	"fmt"
	"io/fs"
	"net/http"
	"strings"

	log "github.com/sirupsen/logrus"

	"github.com/gorilla/websocket"

	"github.com/ohcnetwork/mock-ptz-camera/auth"
	"github.com/ohcnetwork/mock-ptz-camera/media"
	"github.com/ohcnetwork/mock-ptz-camera/ptz"
)

//go:embed static/*
var staticFS embed.FS

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

type Server struct {
	ptzState *ptz.State
	creds    auth.Credentials
	auHub    *media.AUHub
	width    int
	height   int
}

func NewServer(ptzState *ptz.State, creds auth.Credentials, auHub *media.AUHub, width, height int) *Server {
	return &Server{
		ptzState: ptzState,
		creds:    creds,
		auHub:    auHub,
		width:    width,
		height:   height,
	}
}

func (h *Server) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/", h.basicAuth(h.handleIndex))
	mux.HandleFunc("/test", h.basicAuth(h.handleTest))
	staticSub, _ := fs.Sub(staticFS, "static")
	mux.Handle("/static/", h.basicAuthHandler(http.StripPrefix("/static/", http.FileServer(http.FS(staticSub)))))
	mux.HandleFunc("/ws/video", h.handleVideoWS)
	mux.HandleFunc("/ws", h.handleWebSocket)
}

func (h *Server) basicAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !h.checkBasicAuth(r) {
			w.Header().Set("WWW-Authenticate", `Basic realm="Mock PTZ Camera"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

func (h *Server) checkBasicAuth(r *http.Request) bool {
	user, pass, ok := r.BasicAuth()
	return ok &&
		subtle.ConstantTimeCompare([]byte(user), []byte(h.creds.Username)) == 1 &&
		subtle.ConstantTimeCompare([]byte(pass), []byte(h.creds.Password)) == 1
}

func (h *Server) basicAuthHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !h.checkBasicAuth(r) {
			w.Header().Set("WWW-Authenticate", `Basic realm="Mock PTZ Camera"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (h *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	h.serveStaticFile(w, r, "static/index.html")
}

func (h *Server) handleTest(w http.ResponseWriter, r *http.Request) {
	h.serveStaticFile(w, r, "static/test.html")
}

func (h *Server) serveStaticFile(w http.ResponseWriter, r *http.Request, name string) {
	data, err := staticFS.ReadFile(name)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	ct := "text/html; charset=utf-8"
	if strings.HasSuffix(name, ".css") {
		ct = "text/css; charset=utf-8"
	} else if strings.HasSuffix(name, ".js") {
		ct = "application/javascript; charset=utf-8"
	}
	w.Header().Set("Content-Type", ct)
	w.Write(data)
}

// annexBStartCode is the 4-byte Annex B start code prefix.
var annexBStartCode = []byte{0x00, 0x00, 0x00, 0x01}

// handleVideoWS streams H.264 access units over a WebSocket connection.
// The client uses the WebCodecs VideoDecoder API to decode and render frames.
func (h *Server) handleVideoWS(w http.ResponseWriter, r *http.Request) {
	if !h.checkBasicAuth(r) {
		w.Header().Set("WWW-Authenticate", `Basic realm="Mock PTZ Camera"`)
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.WithError(err).Error("video websocket upgrade error")
		return
	}
	defer conn.Close()
	log.WithField("remote", r.RemoteAddr).Info("video websocket connected")

	sub := h.auHub.Subscribe(8)
	defer sub.Unsubscribe()

	// Send init message with codec string and dimensions.
	sps := h.auHub.SPS()
	codec := "avc3.42001e" // fallback baseline level 3.0
	if len(sps) >= 4 {
		// sps[0] is NALU header (0x67); profile/constraint/level start at byte 1
		codec = fmt.Sprintf("avc3.%02x%02x%02x", sps[1], sps[2], sps[3])
	}
	initMsg := fmt.Sprintf(`{"type":"init","codec":"%s","width":%d,"height":%d}`, codec, h.width, h.height)
	if err := conn.WriteMessage(websocket.TextMessage, []byte(initMsg)); err != nil {
		return
	}

	// Send cached last keyframe for instant video start.
	if idr := h.auHub.LastKeyframe(); idr != nil {
		if msg := buildAUMessage(idr, true); msg != nil {
			conn.WriteMessage(websocket.BinaryMessage, msg)
		}
	}

	// Read pump: drain incoming messages to detect close.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	// Stream loop: forward access units as binary WebSocket messages.
	for {
		select {
		case au, ok := <-sub.C:
			if !ok {
				return
			}
			isKey := false
			for _, nalu := range au {
				if len(nalu) > 0 && (nalu[0]&0x1F) == 5 {
					isKey = true
					break
				}
			}
			msg := buildAUMessage(au, isKey)
			if msg == nil {
				continue
			}
			if err := conn.WriteMessage(websocket.BinaryMessage, msg); err != nil {
				return
			}
		case <-done:
			return
		}
	}
}

// buildAUMessage constructs a binary message: [1-byte key flag][Annex B NALUs].
func buildAUMessage(au [][]byte, isKey bool) []byte {
	size := 1 // key flag byte
	for _, nalu := range au {
		size += 4 + len(nalu) // 4-byte start code + NALU data
	}
	msg := make([]byte, 0, size)
	if isKey {
		msg = append(msg, 0x01)
	} else {
		msg = append(msg, 0x00)
	}
	for _, nalu := range au {
		msg = append(msg, annexBStartCode...)
		msg = append(msg, nalu...)
	}
	return msg
}
