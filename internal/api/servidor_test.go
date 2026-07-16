package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/marcos14/praxis-autonomous/internal/db"
)

func TestHealthzSemBanco(t *testing.T) {
	srv := Novo(Opcoes{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, quero 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != tipoJSON {
		t.Fatalf("Content-Type = %q, quero %q", ct, tipoJSON)
	}
	var resp respHealth
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decodificar corpo: %v (corpo=%q)", err, rec.Body.String())
	}
	if resp.Status != "ok" {
		t.Fatalf("status = %q, quero ok", resp.Status)
	}
	if resp.Banco != "" {
		t.Fatalf("banco = %q, quero vazio (sem banco configurado)", resp.Banco)
	}
}

func TestHealthzComBancoOK(t *testing.T) {
	banco := abrirBancoTemp(t)
	srv := Novo(Opcoes{Banco: banco})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, quero 200", rec.Code)
	}
	var resp respHealth
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decodificar corpo: %v", err)
	}
	if resp.Status != "ok" || resp.Banco != "ok" {
		t.Fatalf("resp = %+v, quero status/banco ok", resp)
	}
}

func TestHealthzComBancoIndisponivel(t *testing.T) {
	banco := abrirBancoTemp(t)
	// Fecha o banco antes do request: o ping do /healthz deve falhar e devolver
	// 503 (readiness negativa), sem derrubar o servidor.
	if err := banco.Fechar(); err != nil {
		t.Fatalf("fechar banco: %v", err)
	}
	srv := Novo(Opcoes{Banco: banco})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, quero 503", rec.Code)
	}
	var resp respHealth
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decodificar corpo: %v", err)
	}
	if resp.Status != "degradado" {
		t.Fatalf("status = %q, quero degradado", resp.Status)
	}
	if resp.Banco == "" || resp.Banco == "ok" {
		t.Fatalf("banco = %q, quero mensagem de erro", resp.Banco)
	}
}

func TestHealthzMetodoNaoPermitido(t *testing.T) {
	srv := Novo(Opcoes{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/healthz", nil)
	srv.Handler().ServeHTTP(rec, req)

	// O roteador registra apenas GET /healthz; POST cai no 405 do net/http.
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, quero 405", rec.Code)
	}
}

func TestRotaInexistente(t *testing.T) {
	srv := Novo(Opcoes{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/nao-existe", nil)
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, quero 404", rec.Code)
	}
}

// abrirBancoTemp abre um banco em arquivo temporário e agenda o fechamento
// (idempotente: Fechar chamado duas vezes é tolerado pelos testes que fecham
// antes).
func abrirBancoTemp(t *testing.T) *db.DB {
	t.Helper()
	caminho := filepath.Join(t.TempDir(), "praxis.db")
	d, err := db.Abrir(caminho)
	if err != nil {
		t.Fatalf("abrir banco: %v", err)
	}
	t.Cleanup(func() { _ = d.Fechar() })
	return d
}
