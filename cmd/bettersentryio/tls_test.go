package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// selfSigned writes a throwaway certificate for 127.0.0.1 and returns its paths and pool.
func selfSigned(t *testing.T) (certFile, keyFile string, pool *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "bsio-test"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, _ := x509.MarshalECPrivateKey(key)
	dir := t.TempDir()
	certFile, keyFile = filepath.Join(dir, "tls.crt"), filepath.Join(dir, "tls.key")
	_ = os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600)
	_ = os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600)
	cert, _ := x509.ParseCertificate(der)
	pool = x509.NewCertPool()
	pool.AddCert(cert)
	return certFile, keyFile, pool
}

func startServe(t *testing.T, certFile, keyFile string) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := newHTTPServer(ln.Addr().String(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	go func() {
		if err := serve(srv, ln, certFile, keyFile); err != nil && !errors.Is(err, http.ErrServerClosed) {
			t.Errorf("serve: %v", err)
		}
	}()
	t.Cleanup(func() { _ = srv.Close() })
	return ln.Addr().String()
}

// VAPT SAST #50 (plaintext transport, main.go:254): with --tls-cert-file the engine
// serves HTTPS itself, at TLS 1.2 or better, and a plaintext request is refused.
func TestTLSListenerServesHTTPSOnly(t *testing.T) {
	certFile, keyFile, pool := selfSigned(t)
	addr := startServe(t, certFile, keyFile)

	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}}}
	resp, err := client.Get("https://" + addr + "/-/ready")
	if err != nil {
		t.Fatalf("https: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent || resp.TLS == nil || resp.TLS.Version < tls.VersionTLS12 {
		t.Fatalf("https: status %d, tls %+v", resp.StatusCode, resp.TLS)
	}

	old := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{
		RootCAs: pool, MinVersion: tls.VersionTLS10, MaxVersion: tls.VersionTLS11,
	}}}
	if _, err := old.Get("https://" + addr + "/-/ready"); err == nil {
		t.Fatal("a TLS 1.1 client completed a handshake")
	}

	plain, err := http.Get("http://" + addr + "/-/ready")
	if err != nil {
		t.Fatalf("plain http: %v", err)
	}
	plain.Body.Close()
	if plain.StatusCode != http.StatusBadRequest {
		t.Fatalf("plain http to the TLS port: status %d, want 400", plain.StatusCode)
	}
}

// Without a certificate the listener stays plain HTTP (TLS terminated at the edge).
func TestPlainListenerWithoutCertificate(t *testing.T) {
	addr := startServe(t, "", "")
	resp, err := http.Get("http://" + addr + "/-/ready")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status %d", resp.StatusCode)
	}
}
