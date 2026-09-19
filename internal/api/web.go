package api

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/marcos14/praxis-autonomous/web"
)

// registrarRotasWeb registra o servidor de assets estáticos do frontend na raiz.
// As rotas de API (/api/v1/…) e /healthz são padrões mais específicos no
// ServeMux e têm precedência sobre o "GET /" abaixo, então convivem sem
// conflito: só o que não casa com uma rota específica cai no file server.
//
// Serve index.html em "/", app.css e os ES modules em /js/*.js — tudo do FS
// embutido (web.Assets), sem depender de arquivos em disco. O Cache-Control é
// deixado no default do http.FileServerFS (assets versionam com o binário; em
// dev, recarregar resolve). Duas exceções do PWA (M3) têm handler próprio: o
// service worker (versão e lista do shell injetadas; nunca cacheado pelo
// navegador) e o manifest (tipo MIME correto).
func (s *Servidor) registrarRotasWeb(mux *http.ServeMux) {
	fileServer := http.FileServerFS(web.Assets)
	mux.HandleFunc("GET /sw.js", handleServiceWorker)
	mux.HandleFunc("GET /manifest.webmanifest", handleManifest)
	mux.Handle("GET /", fileServer)
}

// shellPWA lista, uma única vez (o embed é imutável), os caminhos públicos dos
// assets que o service worker pré-cacheia: tudo que está embutido, menos o
// próprio sw.js; index.html entra como "/".
var shellPWA = sync.OnceValue(func() []string {
	var lista []string
	_ = fs.WalkDir(web.Assets, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || p == "sw.js" {
			return nil
		}
		if p == "index.html" {
			lista = append(lista, "/")
			return nil
		}
		lista = append(lista, "/"+strings.ReplaceAll(p, "\\", "/"))
		return nil
	})
	sort.Strings(lista)
	return lista
})

// handleServiceWorker serve web/sw.js com a versão do binário e a lista do
// shell injetadas no topo. Cache-Control: no-cache — o navegador reconsulta o
// arquivo a cada visita e, quando o binário muda, o SW muda e instala um cache
// novo; sem isto um SW velho poderia sobreviver a uma atualização.
func handleServiceWorker(w http.ResponseWriter, r *http.Request) {
	corpo, err := fs.ReadFile(web.Assets, "sw.js")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	versao := Versao
	if versao == "" {
		versao = "dev"
	}
	shell, _ := json.Marshal(shellPWA())
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write([]byte("const VERSAO = " + string(mustJSON(versao)) + ";\nconst SHELL = " + string(shell) + ";\n"))
	_, _ = w.Write(corpo)
}

// handleManifest serve o manifest do PWA com o tipo MIME que o Chrome espera
// (o FileServer não conhece .webmanifest).
func handleManifest(w http.ResponseWriter, r *http.Request) {
	corpo, err := fs.ReadFile(web.Assets, "manifest.webmanifest")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/manifest+json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(corpo)
}

// mustJSON serializa um valor simples (string) como literal JSON.
func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}
