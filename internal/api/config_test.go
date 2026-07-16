package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/marcos14/praxis-autonomous/internal/db"
)

// criarProjetoStore cria um projeto direto no store (sem passar pela validação de
// repo git do handler) e devolve seu id. Usado pelos testes de config.
func criarProjetoStore(t *testing.T, banco *db.DB, slug string) int64 {
	t.Helper()
	p, err := banco.CriarProjeto(context.Background(), db.Projeto{
		Nome:            "Projeto " + slug,
		Slug:            slug,
		Pasta:           `C:\repo`,
		BranchPrincipal: "main",
		ModoIntegracao:  "merge_request",
	})
	if err != nil {
		t.Fatalf("criar projeto %s: %v", slug, err)
	}
	return p.ID
}

func decodConfig(t *testing.T, rec *httptest.ResponseRecorder) map[string]json.RawMessage {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("decodificar config: %v (corpo=%q)", err, rec.Body.String())
	}
	return m
}

func decodEfetiva(t *testing.T, rec *httptest.ResponseRecorder) map[string]db.ValorEfetivo {
	t.Helper()
	var m map[string]db.ValorEfetivo
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("decodificar efetiva: %v (corpo=%q)", err, rec.Body.String())
	}
	return m
}

func TestConfigGlobalVaziaRetornaObjeto(t *testing.T) {
	srv := Novo(Opcoes{Banco: abrirBancoTemp(t)})
	rec := fazerReq(t, srv, http.MethodGet, "/api/v1/config", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (corpo=%s)", rec.Code, rec.Body.String())
	}
	if cfg := decodConfig(t, rec); len(cfg) != 0 {
		t.Fatalf("config global deveria estar vazia, veio %v", cfg)
	}
}

func TestPutGetConfigGlobal(t *testing.T) {
	srv := Novo(Opcoes{Banco: abrirBancoTemp(t)})
	rec := fazerReq(t, srv, http.MethodPut, "/api/v1/config", map[string]any{
		"execucoes_simultaneas": 2,
		"gates":                 []string{"go build", "go vet"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT status = %d (corpo=%s)", rec.Code, rec.Body.String())
	}
	cfg := decodConfig(t, rec)
	if string(cfg["execucoes_simultaneas"]) != "2" {
		t.Fatalf("execucoes_simultaneas = %s", cfg["execucoes_simultaneas"])
	}
	// GET reflete o que foi gravado.
	rec = fazerReq(t, srv, http.MethodGet, "/api/v1/config", nil)
	cfg = decodConfig(t, rec)
	if len(cfg) != 2 || string(cfg["gates"]) != `["go build","go vet"]` {
		t.Fatalf("GET config = %v", cfg)
	}
}

func TestPutConfigGlobalCorpoInvalido(t *testing.T) {
	srv := Novo(Opcoes{Banco: abrirBancoTemp(t)})
	// Um array não é um mapa chave→valor.
	rec := fazerReq(t, srv, http.MethodPut, "/api/v1/config", []any{1, 2})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, quero 400 (corpo=%s)", rec.Code, rec.Body.String())
	}
}

func TestPutConfigGlobalChaveVazia(t *testing.T) {
	srv := Novo(Opcoes{Banco: abrirBancoTemp(t)})
	rec := fazerReq(t, srv, http.MethodPut, "/api/v1/config", map[string]any{"  ": 1})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, quero 400 (corpo=%s)", rec.Code, rec.Body.String())
	}
	if e := decodErro(t, rec); e.Erro.Codigo != "invalido" {
		t.Fatalf("codigo = %q, quero invalido", e.Erro.Codigo)
	}
}

func TestPutConfigGlobalFullReplace(t *testing.T) {
	srv := Novo(Opcoes{Banco: abrirBancoTemp(t)})
	fazerReq(t, srv, http.MethodPut, "/api/v1/config", map[string]any{"a": 1, "b": 2})
	rec := fazerReq(t, srv, http.MethodPut, "/api/v1/config", map[string]any{"b": 3})
	cfg := decodConfig(t, rec)
	if len(cfg) != 1 || string(cfg["b"]) != "3" {
		t.Fatalf("full replace falhou: %v", cfg)
	}
}

func TestConfigProjetoProjetoInexistente(t *testing.T) {
	srv := Novo(Opcoes{Banco: abrirBancoTemp(t)})
	for _, metodo := range []string{http.MethodGet, http.MethodPut} {
		var corpo any
		if metodo == http.MethodPut {
			corpo = map[string]any{"a": 1}
		}
		rec := fazerReq(t, srv, metodo, "/api/v1/projects/999/config", corpo)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s status = %d, quero 404 (corpo=%s)", metodo, rec.Code, rec.Body.String())
		}
	}
}

func TestConfigProjetoIDInvalido(t *testing.T) {
	srv := Novo(Opcoes{Banco: abrirBancoTemp(t)})
	rec := fazerReq(t, srv, http.MethodGet, "/api/v1/projects/abc/config", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, quero 400", rec.Code)
	}
}

func TestPutGetConfigProjeto(t *testing.T) {
	banco := abrirBancoTemp(t)
	srv := Novo(Opcoes{Banco: banco})
	pid := criarProjetoStore(t, banco, "p1")

	rec := fazerReq(t, srv, http.MethodPut, "/api/v1/projects/"+itoa(pid)+"/config",
		map[string]any{"budget_usd": 99})
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT status = %d (corpo=%s)", rec.Code, rec.Body.String())
	}
	cfg := decodConfig(t, rec)
	if len(cfg) != 1 || string(cfg["budget_usd"]) != "99" {
		t.Fatalf("override = %v", cfg)
	}
}

func TestConfigEfetivaViaAPI(t *testing.T) {
	banco := abrirBancoTemp(t)
	srv := Novo(Opcoes{Banco: banco})
	pid := criarProjetoStore(t, banco, "p1")

	// Global define duas chaves; projeto sobrepõe uma.
	if rec := fazerReq(t, srv, http.MethodPut, "/api/v1/config",
		map[string]any{"execucoes_simultaneas": 2, "budget_usd": 10}); rec.Code != http.StatusOK {
		t.Fatalf("PUT global: %d (%s)", rec.Code, rec.Body.String())
	}
	if rec := fazerReq(t, srv, http.MethodPut, "/api/v1/projects/"+itoa(pid)+"/config",
		map[string]any{"budget_usd": 99}); rec.Code != http.StatusOK {
		t.Fatalf("PUT projeto: %d (%s)", rec.Code, rec.Body.String())
	}

	rec := fazerReq(t, srv, http.MethodGet, "/api/v1/projects/"+itoa(pid)+"/config/efetiva", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET efetiva = %d (%s)", rec.Code, rec.Body.String())
	}
	ef := decodEfetiva(t, rec)
	if len(ef) != 2 {
		t.Fatalf("efetiva = %v, quero 2 chaves", ef)
	}
	if v := ef["budget_usd"]; string(v.Valor) != "99" || v.Origem != db.OrigemProjeto {
		t.Fatalf("budget_usd = %+v, quero 99/project", v)
	}
	if v := ef["execucoes_simultaneas"]; string(v.Valor) != "2" || v.Origem != db.OrigemGlobal {
		t.Fatalf("execucoes_simultaneas = %+v, quero 2/global", v)
	}
}

func TestConfigEfetivaProjetoInexistente(t *testing.T) {
	srv := Novo(Opcoes{Banco: abrirBancoTemp(t)})
	rec := fazerReq(t, srv, http.MethodGet, "/api/v1/projects/999/config/efetiva", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, quero 404", rec.Code)
	}
}
