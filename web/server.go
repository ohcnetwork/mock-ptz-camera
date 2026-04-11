package web

import (
	"crypto/subtle"
	"embed"
	"io/fs"
	"net/http"
	"strings"

	"github.com/gorilla/websocket"

	"github.com/ohcnetwork/mock-ptz-camera/auth"
	"github.com/ohcnetwork/mock-ptz-camera/ptz"
)

//go:embed static/*
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
	mux.HandleFunc("/test", h.basicAuth(h.handleTest))
	staticSub, _ := fs.Sub(staticFS, "static")
	mux.Handle("/static/", h.basicAuthHandler(http.StripPrefix("/static/", http.FileServer(http.FS(staticSub)))))
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
