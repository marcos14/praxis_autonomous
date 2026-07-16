package api

import (
	"net/http"

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
// dev, recarregar resolve).
func (s *Servidor) registrarRotasWeb(mux *http.ServeMux) {
	fileServer := http.FileServerFS(web.Assets)
	mux.Handle("GET /", fileServer)
}
