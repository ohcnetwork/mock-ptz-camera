package rtsp

import (
	"sync/atomic"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/bluenviron/gortsplib/v5"
	"github.com/bluenviron/gortsplib/v5/pkg/base"
	"github.com/bluenviron/gortsplib/v5/pkg/description"
	"github.com/bluenviron/gortsplib/v5/pkg/format"
	"github.com/bluenviron/gortsplib/v5/pkg/liberrors"
	"github.com/pion/rtp"

	"github.com/ohcnetwork/mock-ptz-camera/auth"
)

type Server struct {
	lib    *gortsplib.Server
	stream *gortsplib.ServerStream
	Format *format.H264
	creds  auth.Credentials
}

type serverHandler struct {
	s          *Server
	dropCount  atomic.Int64
	lastDropLog atomic.Int64 // unix nanos
}

func NewServer(address string, creds auth.Credentials, sps, pps []byte) (*Server, error) {
	h264Format := &format.H264{
		PayloadTyp:        96,
		PacketizationMode: 1,
		SPS:               sps,
		PPS:               pps,
	}

	s := &Server{
		Format: h264Format,
		creds:  creds,
	}

	s.lib = &gortsplib.Server{
		Handler:        &serverHandler{s: s},
		RTSPAddress:    address,
		WriteQueueSize: 1024,
	}

	return s, nil
}

func (s *Server) Start() error {
	log.WithField("addr", s.lib.RTSPAddress).Info("starting RTSP server")
	if err := s.lib.Start(); err != nil {
		return err
	}

	media := &description.Media{
		Type:    description.MediaTypeVideo,
		Formats: []format.Format{s.Format},
	}
	desc := &description.Session{
		Medias: []*description.Media{media},
	}

	stream := &gortsplib.ServerStream{
		Server: s.lib,
		Desc:   desc,
	}
	if err := stream.Initialize(); err != nil {
		s.lib.Close()
		return err
	}
	s.stream = stream

	return nil
}

func (s *Server) Close() {
	s.stream.Close()
	s.lib.Close()
}

func (s *Server) WritePacketRTP(pkt *rtp.Packet) {
	s.stream.WritePacketRTP(s.stream.Desc.Medias[0], pkt) //nolint:errcheck // errors are per-session, handled via OnStreamWriteError
}

// authenticate checks RTSP digest auth and returns an unauthorized response if it fails.
func (h *serverHandler) authenticate(conn *gortsplib.ServerConn, req *base.Request) (*base.Response, error) {
	if !conn.VerifyCredentials(req, h.s.creds.Username, h.s.creds.Password) {
		return &base.Response{StatusCode: base.StatusUnauthorized}, liberrors.ErrServerAuth{}
	}
	return nil, nil
}

func (h *serverHandler) OnConnOpen(ctx *gortsplib.ServerHandlerOnConnOpenCtx) {
	log.WithField("remote", ctx.Conn.NetConn().RemoteAddr()).Info("RTSP connection opened")
}

func (h *serverHandler) OnConnClose(ctx *gortsplib.ServerHandlerOnConnCloseCtx) {
	log.WithField("remote", ctx.Conn.NetConn().RemoteAddr()).Info("RTSP connection closed")
}

func (h *serverHandler) OnSessionOpen(ctx *gortsplib.ServerHandlerOnSessionOpenCtx) {
	log.Debug("RTSP session opened")
}

func (h *serverHandler) OnSessionClose(ctx *gortsplib.ServerHandlerOnSessionCloseCtx) {
	log.Debug("RTSP session closed")
}

func (h *serverHandler) OnDescribe(ctx *gortsplib.ServerHandlerOnDescribeCtx) (*base.Response, *gortsplib.ServerStream, error) {
	if resp, err := h.authenticate(ctx.Conn, ctx.Request); resp != nil {
		return resp, nil, err
	}
	return &base.Response{StatusCode: base.StatusOK}, h.s.stream, nil
}

func (h *serverHandler) OnSetup(ctx *gortsplib.ServerHandlerOnSetupCtx) (*base.Response, *gortsplib.ServerStream, error) {
	if resp, err := h.authenticate(ctx.Conn, ctx.Request); resp != nil {
		return resp, nil, err
	}
	return &base.Response{StatusCode: base.StatusOK}, h.s.stream, nil
}

func (h *serverHandler) OnPlay(ctx *gortsplib.ServerHandlerOnPlayCtx) (*base.Response, error) {
	log.Info("RTSP client started playing")
	return &base.Response{StatusCode: base.StatusOK}, nil
}

// OnStreamWriteError is called by gortsplib when a per-session write queue
// overflows. This replaces the default log.Println with rate-limited warnings.
// Each session has its own ring buffer — drops for one slow client do NOT
// affect other clients. We intentionally do NOT influence StreamLoop here
// because skipping frames globally would corrupt video for healthy clients.
func (h *serverHandler) OnStreamWriteError(ctx *gortsplib.ServerHandlerOnStreamWriteErrorCtx) {
	count := h.dropCount.Add(1)
	last := h.lastDropLog.Load()
	now := time.Now().UnixNano()
	if now-last >= int64(5*time.Second) {
		if h.lastDropLog.CompareAndSwap(last, now) {
			log.WithField("dropped_packets", count).Warn("slow RTSP client: packets dropped from session queue")
			h.dropCount.Store(0)
		}
	}
}

func (h *serverHandler) OnAnnounce(ctx *gortsplib.ServerHandlerOnAnnounceCtx) (*base.Response, error) {
	return &base.Response{StatusCode: base.StatusMethodNotAllowed}, nil
}
