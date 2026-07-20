package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBaixarCert(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PRAXIS_HOME", home)
	srv := Novo(Opcoes{})

	// Sem certificado gerado (serve sem -tls) → 404.
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/cert", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("sem cert: status = %d, quero 404", rec.Code)
	}

	// Com o cert.pem no lugar → download público com o conteúdo exato.
	if err := os.MkdirAll(filepath.Join(home, "tls"), 0o700); err != nil {
		t.Fatal(err)
	}
	pem := "-----BEGIN CERTIFICATE-----\nfake\n-----END CERTIFICATE-----\n"
	if err := os.WriteFile(filepath.Join(home, "tls", "cert.pem"), []byte(pem), 0o600); err != nil {
		t.Fatal(err)
	}
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/cert", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("com cert: status = %d, quero 200", rec.Code)
	}
	if rec.Body.String() != pem {
		t.Fatalf("corpo = %q, quero o PEM do certificado", rec.Body.String())
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, "praxis.crt") {
		t.Fatalf("Content-Disposition = %q, quero anexo praxis.crt", cd)
	}
}
