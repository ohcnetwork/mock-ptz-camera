package onvif

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"net/http"

	"github.com/ohcnetwork/mock-ptz-camera/ptz"
)

func (s *Server) continuousMove(w http.ResponseWriter, env *SOAPEnvelope) {
	var req struct {
		XMLName  xml.Name `xml:"ContinuousMove"`
		Velocity struct {
			PanTilt *struct {
				X float64 `xml:"x,attr"`
				Y float64 `xml:"y,attr"`
			} `xml:"PanTilt"`
			Zoom *struct {
				X float64 `xml:"x,attr"`
			} `xml:"Zoom"`
		} `xml:"Velocity"`
	}
	if err := xml.NewDecoder(bytes.NewReader(env.Body.Content)).Decode(&req); err != nil {
		writeSOAPFault(w, "s:Sender", "Invalid ContinuousMove request")
		return
	}
	var panSpeed, tiltSpeed, zoomSpeed float64
	if req.Velocity.PanTilt != nil {
		panSpeed = req.Velocity.PanTilt.X
		tiltSpeed = req.Velocity.PanTilt.Y
	}
	if req.Velocity.Zoom != nil {
		zoomSpeed = req.Velocity.Zoom.X
	}
	s.ptzState.ContinuousMove(panSpeed, tiltSpeed, zoomSpeed)
	writeSOAPResponse(w, `<tptz:ContinuousMoveResponse/>`)
}

func (s *Server) absoluteMove(w http.ResponseWriter, env *SOAPEnvelope) {
	var req struct {
		XMLName  xml.Name `xml:"AbsoluteMove"`
		Position struct {
			PanTilt *struct {
				X float64 `xml:"x,attr"`
				Y float64 `xml:"y,attr"`
			} `xml:"PanTilt"`
			Zoom *struct {
				X float64 `xml:"x,attr"`
			} `xml:"Zoom"`
		} `xml:"Position"`
	}
	if err := xml.NewDecoder(bytes.NewReader(env.Body.Content)).Decode(&req); err != nil {
		writeSOAPFault(w, "s:Sender", "Invalid AbsoluteMove request")
		return
	}
	var pan, tilt, zoom float64
	if req.Position.PanTilt != nil {
		pan = req.Position.PanTilt.X
		tilt = req.Position.PanTilt.Y
	}
	if req.Position.Zoom != nil {
		zoom = req.Position.Zoom.X
	}
	s.ptzState.AbsoluteMove(pan, tilt, zoom)
	writeSOAPResponse(w, `<tptz:AbsoluteMoveResponse/>`)
}

func (s *Server) relativeMove(w http.ResponseWriter, env *SOAPEnvelope) {
	var req struct {
		XMLName     xml.Name `xml:"RelativeMove"`
		Translation struct {
			PanTilt *struct {
				X float64 `xml:"x,attr"`
				Y float64 `xml:"y,attr"`
			} `xml:"PanTilt"`
			Zoom *struct {
				X float64 `xml:"x,attr"`
			} `xml:"Zoom"`
		} `xml:"Translation"`
	}
	if err := xml.NewDecoder(bytes.NewReader(env.Body.Content)).Decode(&req); err != nil {
		writeSOAPFault(w, "s:Sender", "Invalid RelativeMove request")
		return
	}
	var dpan, dtilt, dzoom float64
	if req.Translation.PanTilt != nil {
		dpan = req.Translation.PanTilt.X
		dtilt = req.Translation.PanTilt.Y
	}
	if req.Translation.Zoom != nil {
		dzoom = req.Translation.Zoom.X
	}
	s.ptzState.RelativeMove(dpan, dtilt, dzoom)
	writeSOAPResponse(w, `<tptz:RelativeMoveResponse/>`)
}

func (s *Server) stopMove(w http.ResponseWriter, env *SOAPEnvelope) {
	s.ptzState.StopMove(true, true)
	writeSOAPResponse(w, `<tptz:StopResponse/>`)
}

func (s *Server) getStatus(w http.ResponseWriter) {
	pos := s.ptzState.GetPosition()
	status := s.ptzState.GetStatus()
	ptStatus := "IDLE"
	zStatus := "IDLE"
	if status.MoveStatus == ptz.MoveStatusMoving {
		ptStatus = "MOVING"
		zStatus = "MOVING"
	}
	body := renderTemplate("getStatus", ptzStatusData{
		Pan: pos.Pan, Tilt: pos.Tilt, Zoom: pos.Zoom,
		PanTiltStatus: ptStatus, ZoomStatus: zStatus,
	})
	writeSOAPResponse(w, body)
}

func (s *Server) getPresets(w http.ResponseWriter) {
	presets := s.ptzState.GetPresets()
	var presetsXML string
	for _, p := range presets {
		presetsXML += renderTemplate("ptzPreset", presetData{
			Token: p.Token, Name: p.Name,
			Pan: p.Position.Pan, Tilt: p.Position.Tilt, Zoom: p.Position.Zoom,
		})
	}
	writeSOAPResponse(w, fmt.Sprintf(`<tptz:GetPresetsResponse>%s</tptz:GetPresetsResponse>`, presetsXML))
}

func (s *Server) setPreset(w http.ResponseWriter, env *SOAPEnvelope) {
	var req struct {
		XMLName     xml.Name `xml:"SetPreset"`
		PresetName  string   `xml:"PresetName"`
		PresetToken string   `xml:"PresetToken"`
	}
	if err := xml.NewDecoder(bytes.NewReader(env.Body.Content)).Decode(&req); err != nil {
		writeSOAPFault(w, "s:Sender", "Invalid SetPreset request")
		return
	}
	token := s.ptzState.SetPreset(req.PresetToken, req.PresetName)
	writeSOAPResponse(w, renderTemplate("setPresetResponse", presetTokenData{Token: token}))
}

func (s *Server) gotoPreset(w http.ResponseWriter, env *SOAPEnvelope) {
	var req struct {
		XMLName     xml.Name `xml:"GotoPreset"`
		PresetToken string   `xml:"PresetToken"`
	}
	if err := xml.NewDecoder(bytes.NewReader(env.Body.Content)).Decode(&req); err != nil {
		writeSOAPFault(w, "s:Sender", "Invalid GotoPreset request")
		return
	}
	if !s.ptzState.GotoPreset(req.PresetToken) {
		writeSOAPFault(w, "s:Sender", "Preset not found: "+req.PresetToken)
		return
	}
	writeSOAPResponse(w, `<tptz:GotoPresetResponse/>`)
}

func (s *Server) removePreset(w http.ResponseWriter, env *SOAPEnvelope) {
	var req struct {
		XMLName     xml.Name `xml:"RemovePreset"`
		PresetToken string   `xml:"PresetToken"`
	}
	if err := xml.NewDecoder(bytes.NewReader(env.Body.Content)).Decode(&req); err != nil {
		writeSOAPFault(w, "s:Sender", "Invalid RemovePreset request")
		return
	}
	s.ptzState.RemovePreset(req.PresetToken)
	writeSOAPResponse(w, `<tptz:RemovePresetResponse/>`)
}

func (s *Server) getNodes(w http.ResponseWriter) {
	writeSOAPResponse(w, renderTemplate("getNodes", nil))
}

func (s *Server) getConfigurations(w http.ResponseWriter) {
	writeSOAPResponse(w, renderTemplate("getConfigurations", nil))
}
