package rtsp

import (
	"net"
	"sync"
	"sync/atomic"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/bluenviron/gortsplib/v5"
	"github.com/bluenviron/gortsplib/v5/pkg/base"
	"github.com/bluenviron/gortsplib/v5/pkg/description"
	"github.com/bluenviron/gortsplib/v5/pkg/format"
	"github.com/bluenviron/gortsplib/v5/pkg/format/rtph264"
	"github.com/bluenviron/gortsplib/v5/pkg/liberrors"
	"github.com/pion/rtp"

	"github.com/ohcnetwork/mock-ptz-camera/auth"
	"github.com/ohcnetwork/mock-ptz-camera/media"
)

type Server struct {
	lib         *gortsplib.Server
	stream      *gortsplib.ServerStream
	Format      *format.H264
	creds       auth.Credentials
	subscribeFn media.SubscribeFunc
	rtpEncoder  *rtph264.Encoder
}

type serverHandler struct {
	s          *Server
	dropCount  atomic.Int64
	lastDropLog atomic.Int64 // unix nanos

	mu        sync.Mutex
	playing   map[*gortsplib.ServerSession]struct{}
	activeSub media.Subscription
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

	handler := &serverHandler{
		s:       s,
		playing: make(map[*gortsplib.ServerSession]struct{}),
	}

	s.lib = &gortsplib.Server{
		Handler:        handler,
		RTSPAddress:    address,
		WriteQueueSize: 256,
	}

	return s, nil
}

// SetListener overrides the TCP listener used by the RTSP server.
// Must be called before Start(). This is used to provide a
// TLS-muxing listener that transparently handles both RTSP and RTSPS.
func (s *Server) SetListener(ln net.Listener) {
	s.lib.Listen = func(network, address string) (net.Listener, error) {
		return ln, nil
	}
}

// SetSubscriber configures the function used to create AU subscriptions
// and the RTP encoder for packetising H.264 frames.
func (s *Server) SetSubscriber(fn media.SubscribeFunc, rtpEnc *rtph264.Encoder) {
	s.subscribeFn = fn
	s.rtpEncoder = rtpEnc
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
	h.mu.Lock()
	_, wasPlaying := h.playing[ctx.Session]
	if wasPlaying {
		delete(h.playing, ctx.Session)
		if len(h.playing) == 0 && h.activeSub != nil {
			sub := h.activeSub
			h.activeSub = nil
			h.mu.Unlock()
			sub.Unsubscribe() // closes channel → StreamLoop exits
			log.Info("last RTSP viewer left, unsubscribed from pipeline")
			return
		}
	}
	h.mu.Unlock()
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
	h.mu.Lock()
	h.playing[ctx.Session] = struct{}{}
	if len(h.playing) == 1 && h.s.subscribeFn != nil {
		sub := h.s.subscribeFn(8)
		h.activeSub = sub
		go StreamLoop(sub, h.s.rtpEncoder, h.s)
		log.Info("first RTSP viewer, subscribed to pipeline")
	}
	h.mu.Unlock()
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
