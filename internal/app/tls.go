package app

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

// TLS exists for one reason: browsers only expose getUserMedia on a secure
// context. localhost counts, so check-in on the server Mac works over plain
// HTTP — but a check-in iPad hitting http://192.168.1.42 does not, and would
// silently have no camera.
//
// So we mint a self-signed certificate covering localhost, the machine's .local
// name, and every current LAN address. The operator trusts it once per device.

const certValidity = 3 * 365 * 24 * time.Hour

// EnsureCert loads the stored certificate, regenerating it if it is missing,
// expiring within 30 days, or no longer covers the machine's current addresses
// (which happens whenever the venue's DHCP hands out a different subnet).
func EnsureCert(paths Paths) (tls.Certificate, error) {
	certPath := filepath.Join(paths.Certs, "server.crt")
	keyPath := filepath.Join(paths.Certs, "server.key")

	if cert, err := tls.LoadX509KeyPair(certPath, keyPath); err == nil {
		if leaf, err := x509.ParseCertificate(cert.Certificate[0]); err == nil {
			if certStillGood(leaf) {
				cert.Leaf = leaf
				return cert, nil
			}
		}
	}

	certPEM, keyPEM, err := generateCert()
	if err != nil {
		return tls.Certificate{}, err
	}
	if err := os.WriteFile(certPath, certPEM, 0o644); err != nil {
		return tls.Certificate{}, fmt.Errorf("write cert: %w", err)
	}
	// The key is readable only by the user; it never leaves this machine.
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		return tls.Certificate{}, fmt.Errorf("write key: %w", err)
	}
	return tls.X509KeyPair(certPEM, keyPEM)
}

// certStillGood reports whether cert is valid for long enough and covers every
// address we currently answer on.
func certStillGood(cert *x509.Certificate) bool {
	if time.Now().Add(30 * 24 * time.Hour).After(cert.NotAfter) {
		return false
	}
	covered := map[string]bool{}
	for _, ip := range cert.IPAddresses {
		covered[ip.String()] = true
	}
	for _, ip := range LANAddrs() {
		if !covered[ip.String()] {
			return false
		}
	}
	return true
}

// generateCert mints a fresh self-signed certificate for this machine.
func generateCert() (certPEM, keyPEM []byte, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("generate key: %w", err)
	}

	serialMax := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, serialMax)
	if err != nil {
		return nil, nil, fmt.Errorf("generate serial: %w", err)
	}

	ips := []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")}
	ips = append(ips, LANAddrs()...)

	dns := []string{"localhost"}
	if h := LocalHostname(); h != "" {
		dns = append(dns, h)
	}

	tmpl := x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			Organization: []string{"MidSouth Derby and Ales"},
			CommonName:   AppName + " Race Server",
		},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(certValidity),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		DNSNames:              dns,
		IPAddresses:           ips,
	}

	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, fmt.Errorf("create certificate: %w", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal key: %w", err)
	}

	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return certPEM, keyPEM, nil
}
