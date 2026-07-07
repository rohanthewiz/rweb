package rweb_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/rohanthewiz/assert"
	"github.com/rohanthewiz/rweb"
)

// genSelfSignedCert creates an in-memory self-signed cert whose CommonName
// lets a test tell which certificate a handshake actually served.
func genSelfSignedCert(t *testing.T, commonName string) tls.Certificate {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	assert.Nil(t, err)

	tmpl := x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: commonName},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		DNSNames:     []string{"localhost"},
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	assert.Nil(t, err)

	leaf, err := x509.ParseCertificate(der)
	assert.Nil(t, err)

	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}
}

// TestTLSConfigDynamicCert verifies that TLSCfg.Config's GetCertificate is
// consulted per handshake, i.e. a certificate swapped at runtime (autocert
// renewal, certbot file replacement) is served without a server restart.
func TestTLSConfigDynamicCert(t *testing.T) {
	certA := genSelfSignedCert(t, "cert-a")
	certB := genSelfSignedCert(t, "cert-b")

	var current atomic.Pointer[tls.Certificate]
	current.Store(&certA)

	readyChan := make(chan struct{}, 1)
	s := rweb.NewServer(rweb.ServerOptions{
		ReadyChan: readyChan,
		TLS: rweb.TLSCfg{
			UseTLS:  true,
			TLSAddr: "localhost:", // high port
			Config: &tls.Config{
				GetCertificate: func(_ *tls.ClientHelloInfo) (*tls.Certificate, error) {
					return current.Load(), nil
				},
			},
		},
	})

	s.Get("/", func(ctx rweb.Context) error {
		return ctx.WriteString("hello over TLS")
	})

	// servedCN performs one full handshake and reports the peer cert's CN
	servedCN := func() string {
		conn, err := tls.Dial("tcp", fmt.Sprintf("127.0.0.1:%s", s.GetListenPort()),
			&tls.Config{InsecureSkipVerify: true})
		assert.Nil(t, err)
		defer func() { _ = conn.Close() }()
		return conn.ConnectionState().PeerCertificates[0].Subject.CommonName
	}

	go func() {
		defer syscall.Kill(syscall.Getpid(), syscall.SIGTERM)

		<-readyChan // wait for server

		assert.Equal(t, servedCN(), "cert-a")

		// Simulate a renewal: swap the cert, then handshake again — the new
		// cert must be served by the still-running listener
		current.Store(&certB)
		assert.Equal(t, servedCN(), "cert-b")

		// The listener must also actually serve requests over TLS
		client := &http.Client{Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		}}
		resp, err := client.Get(fmt.Sprintf("https://127.0.0.1:%s/", s.GetListenPort()))
		assert.Nil(t, err)
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		assert.Equal(t, string(body), "hello over TLS")

		// MinVersion should have been defaulted to TLS 1.2 on the caller's
		// config (which deliberately left it zero)
		_, err = tls.Dial("tcp", fmt.Sprintf("127.0.0.1:%s", s.GetListenPort()),
			&tls.Config{InsecureSkipVerify: true, MaxVersion: tls.VersionTLS11})
		assert.NotNil(t, err)
	}()

	_ = s.Run()
}
