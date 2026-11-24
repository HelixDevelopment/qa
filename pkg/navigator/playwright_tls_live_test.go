// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

package navigator

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
	"fmt"
	"image/png"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	osexec "os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// privateCAServer starts a TLS-only server whose leaf is signed by a throwaway
// private CA; it returns the URL and the path of the CA PEM.
func privateCAServer(t *testing.T) (string, string) {
	t.Helper()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	caTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "helixqa-test-ca"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	require.NoError(t, err)
	caCert, err := x509.ParseCertificate(caDER)
	require.NoError(t, err)
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	leafTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "localhost"},
		DNSNames: []string{"localhost"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTmpl, caCert, &leafKey.PublicKey, caKey)
	require.NoError(t, err)
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html><body style="background:#123456;color:#fff"><h1>Sign in</h1><button style="background:#e11;padding:40px">Go</button></body></html>`)
	}))
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{leafDER}, PrivateKey: leafKey}}}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	caPath := filepath.Join(t.TempDir(), "ca.pem")
	require.NoError(t, os.WriteFile(caPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}), 0o600))
	return srv.URL, caPath
}

// pageColourShare returns the fraction of sampled pixels equal to the test
// page's background (#123456). A TLS interstitial or error page never carries
// that colour, so this is a positive oracle for "the served page rendered".
func pageColourShare(t *testing.T, b []byte) float64 {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(b))
	require.NoError(t, err)
	bd := img.Bounds()
	hit, total := 0, 0
	for y := bd.Min.Y; y < bd.Max.Y; y += 7 {
		for x := bd.Min.X; x < bd.Max.X; x += 7 {
			r, g, bl, _ := img.At(x, y).RGBA()
			total++
			if r>>8 == 0x12 && g>>8 == 0x34 && bl>>8 == 0x56 {
				hit++
			}
		}
	}
	return float64(hit) / float64(total)
}

func requireBrowserTools(t *testing.T) {
	t.Helper()
	if _, err := osexec.LookPath("node"); err != nil {
		t.Skip("SKIP topology: node not on PATH")
	}
	if os.Getenv("HELIXQA_PLAYWRIGHT_NODE_PATH") == "" {
		t.Skip("SKIP topology: set HELIXQA_PLAYWRIGHT_NODE_PATH to a node_modules directory holding playwright")
	}
	found := false
	for _, b := range []string{"chromium", "chromium-browser", "google-chrome", "google-chrome-stable"} {
		if _, err := osexec.LookPath(b); err == nil {
			found = true
		}
	}
	if !found {
		t.Skip("SKIP topology: no chromium on PATH")
	}
}

func screenshotVia(t *testing.T, url string, env map[string]string) ([]byte, error) {
	t.Helper()
	for k, v := range env {
		t.Setenv(k, v)
	}
	t.Setenv("HELIX_OUTPUT_DIR", t.TempDir())
	wd, _ := os.Getwd()
	pe := NewPlaywrightExecutor(url, nil)
	pe.bridgePath = filepath.Join(wd, "..", "..", "scripts", "playwright-bridge.js")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	defer func() { _, _ = pe.runBridgeCmd(context.Background(), map[string]interface{}{"action": "close"}) }()
	return pe.Screenshot(ctx)
}

// RED on the broken bridge: against a TLS-only private-CA server the browser
// shows an interstitial, so the screenshot carries no page content.
func TestBridge_PrivateCA_NoTrust_IsNotTrusted(t *testing.T) {
	requireBrowserTools(t)
	url, _ := privateCAServer(t)
	shot, err := screenshotVia(t, url+"/login", nil)
	require.NoError(t, err)
	require.Less(t, pageColourShare(t, shot), 0.05,
		"without any trust option the private-CA page must NOT render (control for the CA test below)")
}

func TestBridge_PrivateCA_WithCAFile_RendersPage(t *testing.T) {
	requireBrowserTools(t)
	url, ca := privateCAServer(t)
	shot, err := screenshotVia(t, url+"/login", map[string]string{"HELIX_WEB_CA_FILE": ca})
	require.NoError(t, err)
	require.Greater(t, pageColourShare(t, shot), 0.4, "the page must render when the private CA is configured")
}

func TestBridge_InsecureTLS_RefusedForPublicHost(t *testing.T) {
	requireBrowserTools(t)
	_, err := screenshotVia(t, "https://example.com/", map[string]string{"HELIX_WEB_INSECURE_TLS": "1"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "HELIX_WEB_INSECURE_TLS")
}

func TestBridge_InsecureTLS_AllowedForLoopback(t *testing.T) {
	requireBrowserTools(t)
	url, _ := privateCAServer(t)
	shot, err := screenshotVia(t, url+"/login", map[string]string{"HELIX_WEB_INSECURE_TLS": "1"})
	require.NoError(t, err)
	require.Greater(t, pageColourShare(t, shot), 0.4)
}

// A leaf the configured CA did NOT sign must be refused (a CA file is trust in
// that CA only, not "ignore errors").
func TestBridge_PrivateCA_WrongCA_Refused(t *testing.T) {
	requireBrowserTools(t)
	url, _ := privateCAServer(t)
	_, otherCA := privateCAServer(t)
	_, err := screenshotVia(t, url+"/login", map[string]string{"HELIX_WEB_CA_FILE": otherCA})
	require.Error(t, err)
	require.Contains(t, err.Error(), "HELIX_WEB_CA_FILE")
}

func TestBridge_CAFile_Unreadable_Refused(t *testing.T) {
	requireBrowserTools(t)
	url, _ := privateCAServer(t)
	_, err := screenshotVia(t, url+"/login", map[string]string{"HELIX_WEB_CA_FILE": filepath.Join(t.TempDir(), "nope.pem")})
	require.Error(t, err)
	require.Contains(t, err.Error(), "HELIX_WEB_CA_FILE")
}
