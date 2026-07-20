package main

import (
	"crypto/tls"
	"net"
	"net/http"
	"testing"
)

func TestListenerMistoRedirecionaHTTPEServeTLS(t *testing.T) {
	cert, err := certificadoAutoassinado(t.TempDir(), func(string, ...any) {})
	if err != nil {
		t.Fatalf("certificado: %v", err)
	}
	cfg := &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})}
	go srv.Serve(novoListenerMisto(ln, cfg))
	t.Cleanup(func() { _ = srv.Close() })
	addr := ln.Addr().String()

	// HTTP puro na porta TLS → 307 apontando para https:// (mesmo host+caminho).
	semSeguir := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := semSeguir.Get("http://" + addr + "/demandas?x=1")
	if err != nil {
		t.Fatalf("GET http puro: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusTemporaryRedirect {
		t.Fatalf("status = %d, quero 307", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "https://"+addr+"/demandas?x=1" {
		t.Fatalf("Location = %q, quero https://%s/demandas?x=1", loc, addr)
	}

	// TLS de verdade → chega ao handler (204).
	clienteTLS := &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}}
	resp2, err := clienteTLS.Get("https://" + addr + "/ok")
	if err != nil {
		t.Fatalf("GET https: %v", err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusNoContent {
		t.Fatalf("status TLS = %d, quero 204", resp2.StatusCode)
	}
}
