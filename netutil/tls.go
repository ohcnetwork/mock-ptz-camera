package netutil

import (
	"crypto/tls"
	"path/filepath"

	log "github.com/sirupsen/logrus"
)

// TLSOptions holds the configuration needed to set up TLS.
type TLSOptions struct {
	CertFile string
	KeyFile  string
	CertDir  string
	HostIP   string
}

// SetupTLS loads or generates a TLS certificate and returns a *tls.Config.
// If CertFile/KeyFile are empty, it falls back to CertDir/server.{crt,key}.
func SetupTLS(opts TLSOptions) (*tls.Config, error) {
	certFile := opts.CertFile
	keyFile := opts.KeyFile
	if certFile == "" || keyFile == "" {
		certFile = filepath.Join(opts.CertDir, "server.crt")
		keyFile = filepath.Join(opts.CertDir, "server.key")
	}
	cert, err := LoadOrGenerateCert(certFile, keyFile, []string{opts.HostIP})
	if err != nil {
		return nil, err
	}
	log.WithFields(log.Fields{
		"cert": certFile, "key": keyFile,
	}).Info("TLS enabled")
	return NewTLSConfig(cert), nil
}
