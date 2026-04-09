package web

import (
	"fmt"
	"net/http"
)

func (h *Server) handleMJPEGStream(w http.ResponseWriter, r *http.Request) {
	const boundary = "mjpegboundary"
	w.Header().Set("Content-Type", fmt.Sprintf("multipart/x-mixed-replace; boundary=%s", boundary))
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming not supported", http.StatusInternalServerError)
		return
	}

	var lastVersion uint64
	for {
		frame, ver := h.frameStore.WaitFrame(lastVersion)
		lastVersion = ver

		header := fmt.Sprintf("\r\n--%s\r\nContent-Type: image/jpeg\r\nContent-Length: %d\r\n\r\n", boundary, len(frame))
		if _, err := w.Write([]byte(header)); err != nil {
			return
		}
		if _, err := w.Write(frame); err != nil {
			return
		}
		flusher.Flush()
	}
}
