package cluster

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func testIdentity(t *testing.T, names ...string) (tls.Certificate, string) {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: "logdir-node-test"},
		NotBefore:    time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage:    x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:    names,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &k.PublicKey, k)
	if err != nil {
		t.Fatal(err)
	}
	c, _ := x509.ParseCertificate(der)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: k}, certFingerprint(c)
}

func pinServer(t *testing.T, c tls.Certificate) string {
	t.Helper()
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "ok") }))
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{c}}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
	return "127.0.0.1:" + port
}

func pinGet(addr, fp string) error {
	resp, err := pinnedClient(addr, fp, 5*time.Second).Get(PeerURL("/x"))
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

func TestPinnedClientChecksFingerprint(t *testing.T) {
	good, fp := testIdentity(t, peerServerName)
	addr := pinServer(t, good)
	if err := pinGet(addr, fp); err != nil {
		t.Fatalf("pinned peer refused: %v", err)
	}
	if err := pinGet(addr, fp); err != nil { // second call uses the remembered certificate
		t.Fatalf("second call: %v", err)
	}
	// Right address, wrong pin.
	_, other := testIdentity(t, peerServerName)
	if err := pinGet(addr, other); err == nil {
		t.Fatal("a certificate with another fingerprint was accepted")
	}
	// An impostor on the address: its own certificate does not carry the pin.
	imp, _ := testIdentity(t, peerServerName)
	if err := pinGet(pinServer(t, imp), fp); err == nil {
		t.Fatal("an impostor was accepted")
	}
	// The pin matches but the certificate is for another name.
	wrong, wfp := testIdentity(t, "something-else")
	if err := pinGet(pinServer(t, wrong), wfp); err == nil {
		t.Fatal("a certificate for another name was accepted")
	}
	// A bad address never dials.
	if _, err := dialPinned(context.Background(), "evil@127.0.0.1:1", fp); err == nil {
		t.Fatal("invalid address dialled")
	}
}

// A second call to the same peer goes over the connection the first one opened; another fingerprint, or another own
// identity, never shares it.
func TestPinnedClientKeepsConnection(t *testing.T) {
	good, fp := testIdentity(t, peerServerName)
	var conns atomic.Int32
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"ok":true}`)
	}))
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{good}, ClientAuth: tls.RequestClientCert}
	srv.Config.ConnState = func(_ net.Conn, st http.ConnState) {
		if st == http.StateNew {
			conns.Add(1)
		}
	}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	t.Cleanup(ClosePeerConns)
	_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
	addr := "127.0.0.1:" + port
	get := func(me *tls.Certificate) {
		t.Helper()
		resp, err := PinnedClientAs(addr, fp, 5*time.Second, me).Get(PeerURL("/x"))
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
	me, _ := testIdentity(t, peerServerName)
	for range 5 {
		get(&me)
	}
	// one connection to learn the certificate, one for the calls
	if n := conns.Load(); n != 2 {
		t.Fatalf("5 calls opened %d connections, want 2 (1 to learn the certificate)", n)
	}
	other, _ := testIdentity(t, peerServerName)
	get(&other)
	if n := conns.Load(); n != 3 {
		t.Fatalf("another own identity reused a connection: %d connections", n)
	}
	_, otherFp := testIdentity(t, peerServerName)
	if _, err := PinnedClientAs(addr, otherFp, 5*time.Second, &me).Get(PeerURL("/x")); err == nil {
		t.Fatal("another fingerprint was served over the kept connection")
	}
}
