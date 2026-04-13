package netutil

import (
	"net/http"

	log "github.com/sirupsen/logrus"
)

// ServeAsync starts fn in a background goroutine. If label is non-empty
// it logs the address before serving. Fatal-logs on non-ErrServerClosed errors.
func ServeAsync(label, addr string, fn func() error) {
	go func() {
		if label != "" {
			log.WithField("addr", addr).Infof("web server listening (%s)", label)
		}
		if err := fn(); err != nil && err != http.ErrServerClosed {
			log.WithError(err).Fatalf("%s server error", label)
		}
	}()
}
