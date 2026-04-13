package netutil

import (
	"crypto/tls"
	"net"
	"net/http"

	log "github.com/sirupsen/logrus"
)

// HTTPServeOptions configures how the HTTP server is started.
type HTTPServeOptions struct {
	Handler    http.Handler
	WebAddr    string
	TLSAddr    string // only used when TLSPort != 0
	TLSConfig  *tls.Config
	TLSEnabled bool
	TLSPort    int // 0 = mux HTTP+HTTPS on same port; >0 = separate HTTPS port
}

// ServeHTTP starts the HTTP (and optionally HTTPS) server based on the
// TLS configuration. It handles three modes:
//   - Plain HTTP only
//   - TLS mux: HTTP + HTTPS on the same port
//   - Separate ports: HTTP on WebAddr, HTTPS on TLSAddr
func ServeHTTP(opts HTTPServeOptions) {
	httpServer := &http.Server{Handler: opts.Handler}

	switch {
	case !opts.TLSEnabled:
		httpServer.Addr = opts.WebAddr
		ServeAsync("HTTP", opts.WebAddr, httpServer.ListenAndServe)

	case opts.TLSPort == 0:
		webLn, err := net.Listen("tcp", opts.WebAddr)
		if err != nil {
			log.WithError(err).Fatal("failed to listen on web address")
		}
		split := NewSplitListener(webLn)
		go split.Serve()

		httpsServer := &http.Server{Handler: opts.Handler, TLSConfig: opts.TLSConfig}
		ServeAsync("HTTP+HTTPS mux", opts.WebAddr, func() error {
			return httpsServer.ServeTLS(split.TLS(), "", "")
		})
		ServeAsync("", "", func() error {
			return httpServer.Serve(split.Plain())
		})

	default:
		httpServer.Addr = opts.WebAddr
		ServeAsync("HTTP", opts.WebAddr, httpServer.ListenAndServe)

		httpsServer := &http.Server{Addr: opts.TLSAddr, Handler: opts.Handler, TLSConfig: opts.TLSConfig}
		ServeAsync("HTTPS", opts.TLSAddr, func() error {
			return httpsServer.ListenAndServeTLS("", "")
		})
	}
}
