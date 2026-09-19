package api

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// TestAssetsEmbutidosServem verifica que os assets do frontend embutidos
// (index.html, CSS e ES modules) são servidos com 200 e conteúdo esperado. Não
// dependem de banco (o file server é registrado sempre).
func TestAssetsEmbutidosServem(t *testing.T) {
	srv := Novo(Opcoes{})
	casos := []struct {
		caminho string
		marca   string // trecho que deve aparecer no corpo servido
	}{
		{"/", "view-projetos"}, // raiz → index.html (o FileServer redireciona /index.html → /)
		{"/app.css", ":root"},
		{"/js/app.js", "irPara"},
		{"/js/demandas.js", "abrirCard"},
		{"/js/api.js", "/api/v1/projects"},
		{"/js/projetos.js", "config efetiva"},
		{"/js/motores.js", "reordenar"},
		{"/js/config.js", "definirConfigGlobal"},
		{"/js/config-fields.js", "CAMPOS"},
		{"/js/ui.js", "toast"},
	}
	for _, c := range casos {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, c.caminho, nil)
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s: status = %d, quero 200", c.caminho, rec.Code)
			continue
		}
		if body := rec.Body.String(); !strings.Contains(body, c.marca) {
			t.Errorf("GET %s: corpo não contém %q", c.caminho, c.marca)
		}
	}
}

// TestServiceWorkerEManifest cobre o PWA (M3): o SW sai com a versão e a lista
// do shell injetadas, sem cache do navegador e sem se listar; o manifest sai com
// o tipo MIME certo; os ícones são servidos.
func TestServiceWorkerEManifest(t *testing.T) {
	srv := Novo(Opcoes{})
	get := func(caminho string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, caminho, nil))
		return rec
	}

	rec := get("/sw.js")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /sw.js: status %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/javascript") {
		t.Errorf("Content-Type do SW = %q", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("Cache-Control do SW = %q, quero no-cache", cc)
	}
	corpo := rec.Body.String()
	for _, trecho := range []string{`const VERSAO = "dev";`, `const SHELL = [`, `"/"`, `"/app.css"`, `"/js/app.js"`, `"/manifest.webmanifest"`, `"/icons/icon-192.png"`, `addEventListener("fetch"`} {
		if !strings.Contains(corpo, trecho) {
			t.Errorf("SW sem %q", trecho)
		}
	}
	if strings.Contains(corpo, `"/sw.js"`) && strings.Index(corpo, `"/sw.js"`) < strings.Index(corpo, "addEventListener") {
		t.Error("o SW não deve se listar no shell")
	}

	rec = get("/manifest.webmanifest")
	if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/manifest+json") {
		t.Fatalf("manifest: status %d, Content-Type %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	if !strings.Contains(rec.Body.String(), `"short_name": "Praxis"`) {
		t.Error("manifest sem short_name")
	}

	for _, ic := range []string{"/icons/icon-192.png", "/icons/icon-512.png", "/icons/icon-maskable-512.png", "/icons/apple-touch-icon.png"} {
		rec = get(ic)
		if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Header().Get("Content-Type"), "image/png") {
			t.Errorf("%s: status %d, Content-Type %q", ic, rec.Code, rec.Header().Get("Content-Type"))
		}
	}
}

// TestAssetInexistente404 confirma que um asset inexistente cai em 404 (o file
// server não vaza para outras rotas).
func TestAssetInexistente404(t *testing.T) {
	srv := Novo(Opcoes{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/js/nao-existe.js", nil)
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, quero 404", rec.Code)
	}
}

// TestRotasDasTelasRespondem faz um smoke das rotas de API consumidas pelas
// telas Projetos/Motores/Config: todas devem responder 200 com um banco real.
func TestRotasDasTelasRespondem(t *testing.T) {
	banco := abrirBancoTemp(t)
	srv := Novo(Opcoes{Banco: banco})

	// Rotas de listagem/leitura globais usadas ao montar as telas.
	for _, caminho := range []string{"/api/v1/projects", "/api/v1/engines", "/api/v1/config"} {
		rec := fazerReq(t, srv, http.MethodGet, caminho, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s: status = %d, quero 200 (corpo=%q)", caminho, rec.Code, rec.Body.String())
		}
	}

	// Cria um projeto real (repo git temporário) e exercita as rotas de config
	// por projeto que a tela de Projetos usa.
	pasta := repoGitTemp(t)
	rec := fazerReq(t, srv, http.MethodPost, "/api/v1/projects", map[string]any{
		"nome":  "Smoke UI",
		"pasta": pasta,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("criar projeto: status = %d, quero 201 (corpo=%q)", rec.Code, rec.Body.String())
	}
	proj := decodProjeto(t, rec)

	for _, caminho := range []string{
		"/api/v1/projects/" + strconv.FormatInt(proj.ID, 10),
		"/api/v1/projects/" + strconv.FormatInt(proj.ID, 10) + "/config",
		"/api/v1/projects/" + strconv.FormatInt(proj.ID, 10) + "/config/efetiva",
	} {
		rec := fazerReq(t, srv, http.MethodGet, caminho, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s: status = %d, quero 200 (corpo=%q)", caminho, rec.Code, rec.Body.String())
		}
	}
}
