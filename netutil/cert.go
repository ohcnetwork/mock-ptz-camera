package netutil

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

// LoadOrGenerateCert loads a TLS certificate from certFile and keyFile.
// If neither file exists, it generates a self-signed certificate using
// ECDSA P-256 and writes it to those paths for reuse across restarts.
// hosts is a list of hostnames/IPs to include as SANs.
func LoadOrGenerateCert(certFile, keyFile string, hosts []string) (tls.Certificate, error) {
	if fileExists(certFile) && fileExists(keyFile) {
		return tls.LoadX509KeyPair(certFile, keyFile)
	}

	cert, err := generateSelfSigned(hosts)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("generate self-signed cert: %w", err)
	}

	if err := writePEM(certFile, keyFile, cert); err != nil {
		return cert, fmt.Errorf("persist cert: %w", err)
	}

	return cert, nil
}

func generateSelfSigned(hosts []string) (tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return tls.Certificate{}, err
	}

	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "mock-ptz-camera"},
		NotBefore:    now,
		NotAfter:     now.Add(365 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}

	// Always include localhost SANs.
	tmpl.DNSNames = append(tmpl.DNSNames, "localhost")
	tmpl.IPAddresses = append(tmpl.IPAddresses, net.IPv4(127, 0, 0, 1), net.IPv6loopback)

	for _, h := range hosts {
		if ip := net.ParseIP(h); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		} else if h != "" {
			tmpl.DNSNames = append(tmpl.DNSNames, h)
		}
	}

	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, err
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return tls.Certificate{}, err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	return tls.X509KeyPair(certPEM, keyPEM)
}

func writePEM(certFile, keyFile string, cert tls.Certificate) error {
	for _, dir := range []string{filepath.Dir(certFile), filepath.Dir(keyFile)} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return err
		}
	}

	leaf := cert.Certificate[0]
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leaf})
	if err := os.WriteFile(certFile, certPEM, 0600); err != nil {
		return err
	}

	keyDER, err := x509.MarshalECPrivateKey(cert.PrivateKey.(*ecdsa.PrivateKey))
	if err != nil {
		return err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return os.WriteFile(keyFile, keyPEM, 0600)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
