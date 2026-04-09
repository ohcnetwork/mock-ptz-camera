package web

import (
	"crypto/subtle"
	"embed"
	"net/http"

	"github.com/gorilla/websocket"

	"github.com/ohcnetwork/mock-ptz-camera/auth"
	"github.com/ohcnetwork/mock-ptz-camera/ptz"
)

//go:embed static/index.html
var staticFS embed.FS

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

type Server struct {
	ptzState   *ptz.State
	creds      auth.Credentials
	frameStore *FrameStore
}

func NewServer(ptzState *ptz.State, creds auth.Credentials, frameStore *FrameStore) *Server {
	return &Server{
		ptzState:   ptzState,
		creds:      creds,
		frameStore: frameStore,
	}
}

func (h *Server) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/", h.basicAuth(h.handleIndex))
	mux.HandleFunc("/api/stream", h.basicAuth(h.handleMJPEGStream))
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

func (h *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	data, err := staticFS.ReadFile("static/index.html")
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(data)
}
