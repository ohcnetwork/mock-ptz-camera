package onvif

import (
	"net/http"
)

func (s *Server) getProfiles(w http.ResponseWriter) {
	writeSOAPResponse(w, renderTemplate("getProfiles", s.mediaConfig()))
}

func (s *Server) getStreamUri(w http.ResponseWriter, rtspBase string) {
	writeSOAPResponse(w, renderTemplate("getStreamUri", streamURIData{URI: rtspBase + "/stream"}))
}

func (s *Server) getVideoSources(w http.ResponseWriter) {
	writeSOAPResponse(w, renderTemplate("getVideoSources", s.mediaConfig()))
}

func (s *Server) getVideoSourceConfigurations(w http.ResponseWriter) {
	writeSOAPResponse(w, renderTemplate("getVideoSourceConfigurations", s.mediaConfig()))
}

func (s *Server) getVideoEncoderConfigurations(w http.ResponseWriter) {
	writeSOAPResponse(w, renderTemplate("getVideoEncoderConfigurations", s.mediaConfig()))
}
