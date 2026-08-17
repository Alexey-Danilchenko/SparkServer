package test

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"sparkserver/internal/httpapi"
)

func TestHTTPServerStartsWithTLS(t *testing.T) {
	certificatePath, privateKeyPath := writeTestCertificate(t)

	var logs bytes.Buffer
	server := httpapi.New(
		":0",
		httpapi.Dependencies{},
		slog.New(slog.NewTextHandler(&logs, nil)).With("server", "http"),
		httpapi.WithTLS(httpapi.TLSConfig{
			Enabled:         true,
			CertificateFile: certificatePath,
			PrivateKeyFile:  privateKeyPath,
		}),
	)
	if err := server.Start(); err != nil {
		t.Fatalf("start HTTPS server: %v", err)
	}
	t.Cleanup(func() {
		if err := server.Shutdown(context.Background()); err != nil {
			t.Errorf("shutdown HTTPS server: %v", err)
		}
	})

	_, port, err := net.SplitHostPort(server.ListenerAddress())
	if err != nil {
		t.Fatalf("split listener address: %v", err)
	}

	client := &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}}
	response, err := client.Get("https://127.0.0.1:" + port + "/")
	if err != nil {
		t.Fatalf("GET HTTPS root: %v", err)
	}
	defer response.Body.Close()

	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read HTTPS response: %v", err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("HTTPS status = %d", response.StatusCode)
	}
	if !strings.Contains(string(body), `"name":"spark-server-go"`) {
		t.Fatalf("HTTPS body = %s", body)
	}
	if !strings.Contains(logs.String(), `msg="https listener started" server=http address=`) {
		t.Fatalf("HTTPS startup log missing:\n%s", logs.String())
	}
}

func TestHTTPServerRejectsMissingTLSFiles(t *testing.T) {
	server := httpapi.New(
		":0",
		httpapi.Dependencies{},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		httpapi.WithTLS(httpapi.TLSConfig{Enabled: true}),
	)

	err := server.Start()
	if err == nil || !strings.Contains(err.Error(), "SSL_CERTIFICATE_FILEPATH") {
		t.Fatalf("start error = %v", err)
	}
}

func writeTestCertificate(t *testing.T) (string, string) {
	t.Helper()

	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate private key: %v", err)
	}

	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1)},
	}
	certificateDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &privateKey.PublicKey, privateKey)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	privateKeyDER, err := x509.MarshalECPrivateKey(privateKey)
	if err != nil {
		t.Fatalf("marshal private key: %v", err)
	}

	directory := t.TempDir()
	certificatePath := filepath.Join(directory, "certificate.pem")
	privateKeyPath := filepath.Join(directory, "private-key.pem")
	if err := os.WriteFile(certificatePath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificateDER}), 0o600); err != nil {
		t.Fatalf("write certificate: %v", err)
	}
	if err := os.WriteFile(privateKeyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: privateKeyDER}), 0o600); err != nil {
		t.Fatalf("write private key: %v", err)
	}

	return certificatePath, privateKeyPath
}
