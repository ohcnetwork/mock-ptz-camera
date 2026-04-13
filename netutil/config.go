package netutil

import (
	"crypto/tls"
)

// NewTLSConfig builds a *tls.Config with modern cipher suites and protocols
// supported by all major browsers and curl.
// TLS 1.3 cipher suites are always enabled in Go and cannot be configured.
func NewTLSConfig(cert tls.Certificate) *tls.Config {
	return &tls.Config{
		MinVersion:       tls.VersionTLS12,
		CurvePreferences: []tls.CurveID{tls.X25519, tls.CurveP256},
		CipherSuites: []uint16{
			// TLS 1.2 ECDHE suites (AES-GCM + ChaCha20-Poly1305)
			tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256,
			tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256,
		},
		Certificates: []tls.Certificate{cert},
	}
}
