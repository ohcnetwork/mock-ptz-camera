package onvif

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"

	log "github.com/sirupsen/logrus"

	"github.com/ohcnetwork/mock-ptz-camera/auth"
	"github.com/ohcnetwork/mock-ptz-camera/config"
	"github.com/ohcnetwork/mock-ptz-camera/ptz"
)

type Server struct {
	cfg      *config.Config
	creds    auth.Credentials
	ptzState *ptz.State
	events   *EventsService
	hostIP   string
}

func NewServer(cfg *config.Config, creds auth.Credentials, ptzState *ptz.State, events *EventsService, hostIP string) *Server {
	return &Server{
		cfg:      cfg,
		creds:    creds,
		ptzState: ptzState,
		events:   events,
		hostIP:   hostIP,
	}
}

func (s *Server) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/onvif/device_service", s.handleDevice)
	mux.HandleFunc("/onvif/media_service", s.handleMedia)
	mux.HandleFunc("/onvif/ptz_service", s.handlePTZ)
	mux.HandleFunc("/onvif/event_service", s.handleEvents)
	mux.HandleFunc("/onvif/subscription", s.handleSubscription)
}

// requestHost extracts the hostname from the incoming HTTP request,
// falling back to the configured hostIP when the Host header is absent.
func (s *Server) requestHost(r *http.Request) string {
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	if host == "" {
		return s.hostIP
	}
	return host
}

// httpBaseURL returns the scheme://host:port base for HTTP(S) URLs,
// derived from the incoming request.
func (s *Server) httpBaseURL(r *http.Request) string {
	host := s.requestHost(r)
	scheme := "http"
	port := s.cfg.WebPort
	if r.TLS != nil {
		scheme = "https"
		if s.cfg.TLSPort != 0 {
			port = s.cfg.TLSPort
		}
	}
	return fmt.Sprintf("%s://%s:%d", scheme, host, port)
}

// rtspBaseURL returns the scheme://host:port base for RTSP(S) URLs,
// derived from the incoming request.
func (s *Server) rtspBaseURL(r *http.Request) string {
	host := s.requestHost(r)
	scheme := "rtsp"
	if r.TLS != nil {
		scheme = "rtsps"
	}
	return fmt.Sprintf("%s://%s:%d", scheme, host, s.cfg.RTSPPort)
}

// serviceURLs returns all ONVIF service endpoint URLs.
func serviceURLs(base string) serviceURLsData {
	return serviceURLsData{
		DeviceURL: base + "/onvif/device_service",
		MediaURL:  base + "/onvif/media_service",
		PTZURL:    base + "/onvif/ptz_service",
		EventsURL: base + "/onvif/event_service",
	}
}

// mediaConfig returns the video resolution and frame rate.
func (s *Server) mediaConfig() mediaConfigData {
	return mediaConfigData{
		Width:  s.cfg.Width,
		Height: s.cfg.Height,
		FPS:    s.cfg.FPS,
	}
}

func (s *Server) parseAndAuth(w http.ResponseWriter, r *http.Request, requireAuth bool) (*SOAPEnvelope, string, bool) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeSOAPFault(w, "s:Receiver", "Could not read request body")
		return nil, "", false
	}

	var env SOAPEnvelope
	if err := xml.Unmarshal(body, &env); err != nil {
		writeSOAPFault(w, "s:Sender", "Malformed SOAP envelope")
		return nil, "", false
	}

	action := detectAction(env.Body.Content)
	if action == "" {
		writeSOAPFault(w, "s:Sender", "Could not determine SOAP action")
		return nil, "", false
	}

	if requireAuth && action != "GetSystemDateAndTime" {
		if !s.authenticate(&env) {
			w.WriteHeader(http.StatusUnauthorized)
			writeSOAPFault(w, "s:Sender", "Authentication failed")
			return nil, "", false
		}
	}

	return &env, action, true
}

func (s *Server) authenticate(env *SOAPEnvelope) bool {
	if env.Header == nil || env.Header.Security == nil || env.Header.Security.UsernameToken == nil {
		return false
	}
	ut := env.Header.Security.UsernameToken
	return auth.ValidateWSUsernameToken(
		s.creds,
		ut.Username,
		ut.Password.Value,
		ut.Nonce,
		ut.Created,
		ut.Password.Type,
	)
}

func detectAction(bodyXML []byte) string {
	decoder := xml.NewDecoder(bytes.NewReader(bodyXML))
	for {
		tok, err := decoder.Token()
		if err != nil {
			return ""
		}
		if se, ok := tok.(xml.StartElement); ok {
			return se.Name.Local
		}
	}
}

func writeSOAPResponse(w http.ResponseWriter, bodyInner string) {
	w.Header().Set("Content-Type", "application/soap+xml; charset=utf-8")
	resp := renderTemplate("soapEnvelope", envelopeData{Body: bodyInner})
	w.Write([]byte(resp))
}

func writeSOAPFault(w http.ResponseWriter, code, reason string) {
	w.Header().Set("Content-Type", "application/soap+xml; charset=utf-8")
	if w.Header().Get("Status") == "" {
		w.WriteHeader(http.StatusInternalServerError)
	}
	resp := renderTemplate("soapFault", faultData{Code: code, Reason: reason})
	w.Write([]byte(resp))
}

func (s *Server) handleDevice(w http.ResponseWriter, r *http.Request) {
	env, action, ok := s.parseAndAuth(w, r, true)
	if !ok {
		return
	}
	_ = env
	base := s.httpBaseURL(r)
	switch action {
	case "GetSystemDateAndTime":
		s.getSystemDateAndTime(w)
	case "GetDeviceInformation":
		s.getDeviceInformation(w)
	case "GetServices":
		s.getServices(w, base)
	case "GetCapabilities":
		s.getCapabilities(w, base)
	case "GetScopes":
		s.getScopes(w)
	default:
		log.WithField("action", action).Warn("unknown ONVIF device action")
		writeSOAPFault(w, "s:Sender", "Unknown action: "+action)
	}
}

func (s *Server) handleMedia(w http.ResponseWriter, r *http.Request) {
	env, action, ok := s.parseAndAuth(w, r, true)
	if !ok {
		return
	}
	_ = env
	switch action {
	case "GetProfiles", "GetProfile":
		s.getProfiles(w)
	case "GetStreamUri":
		s.getStreamUri(w, s.rtspBaseURL(r))
	case "GetVideoSources":
		s.getVideoSources(w)
	case "GetVideoSourceConfigurations":
		s.getVideoSourceConfigurations(w)
	case "GetVideoEncoderConfigurations", "GetVideoEncoderConfiguration":
		s.getVideoEncoderConfigurations(w)
	default:
		log.WithField("action", action).Warn("unknown ONVIF media action")
		writeSOAPFault(w, "s:Sender", "Unknown action: "+action)
	}
}

func (s *Server) handlePTZ(w http.ResponseWriter, r *http.Request) {
	env, action, ok := s.parseAndAuth(w, r, true)
	if !ok {
		return
	}
	switch action {
	case "ContinuousMove":
		s.continuousMove(w, env)
	case "AbsoluteMove":
		s.absoluteMove(w, env)
	case "RelativeMove":
		s.relativeMove(w, env)
	case "Stop":
		s.stopMove(w, env)
	case "GetStatus":
		s.getStatus(w)
	case "GetPresets":
		s.getPresets(w)
	case "SetPreset":
		s.setPreset(w, env)
	case "GotoPreset":
		s.gotoPreset(w, env)
	case "RemovePreset":
		s.removePreset(w, env)
	case "GetNodes", "GetNode":
		s.getNodes(w)
	case "GetConfigurations", "GetConfiguration":
		s.getConfigurations(w)
	default:
		log.WithField("action", action).Warn("unknown ONVIF PTZ action")
		writeSOAPFault(w, "s:Sender", "Unknown action: "+action)
	}
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	env, action, ok := s.parseAndAuth(w, r, true)
	if !ok {
		return
	}
	switch action {
	case "GetEventProperties":
		s.getEventProperties(w)
	case "CreatePullPointSubscription":
		s.createPullPointSubscription(w, env, s.httpBaseURL(r))
	default:
		log.WithField("action", action).Warn("unknown ONVIF events action")
		writeSOAPFault(w, "s:Sender", "Unknown action: "+action)
	}
}

func (s *Server) handleSubscription(w http.ResponseWriter, r *http.Request) {
	env, action, ok := s.parseAndAuth(w, r, true)
	if !ok {
		return
	}
	subID := r.URL.Query().Get("id")
	if subID == "" {
		subID = extractSubscriptionID(env.Body.Content)
	}
	switch action {
	case "PullMessages":
		s.pullMessages(w, env, subID)
	case "Renew":
		s.renewSubscription(w, subID)
	case "Unsubscribe":
		s.unsubscribe(w, subID)
	default:
		log.WithField("action", action).Warn("unknown ONVIF subscription action")
		writeSOAPFault(w, "s:Sender", "Unknown action: "+action)
	}
}

func extractSubscriptionID(body []byte) string {
	s := string(body)
	if idx := strings.Index(s, "id="); idx >= 0 {
		rest := s[idx+3:]
		end := strings.IndexAny(rest, `"'<& `)
		if end > 0 {
			return rest[:end]
		}
		return rest
	}
	return ""
}
