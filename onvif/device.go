package onvif

import (
	"net/http"
	"time"
)

func (s *Server) getSystemDateAndTime(w http.ResponseWriter) {
	now := time.Now().UTC()
	body := renderTemplate("getSystemDateAndTime", dateTimeData{
		Hour: now.Hour(), Minute: now.Minute(), Second: now.Second(),
		Year: now.Year(), Month: int(now.Month()), Day: now.Day(),
	})
	writeSOAPResponse(w, body)
}

func (s *Server) getDeviceInformation(w http.ResponseWriter) {
	writeSOAPResponse(w, renderTemplate("getDeviceInformation", nil))
}

func (s *Server) getServices(w http.ResponseWriter, base string) {
	writeSOAPResponse(w, renderTemplate("getServices", serviceURLs(base)))
}

func (s *Server) getCapabilities(w http.ResponseWriter, base string) {
	writeSOAPResponse(w, renderTemplate("getCapabilities", serviceURLs(base)))
}

func (s *Server) getScopes(w http.ResponseWriter) {
	writeSOAPResponse(w, renderTemplate("getScopes", nil))
}
