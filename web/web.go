// Package web embute os assets estáticos do frontend (shell da UI) e os expõe
// como um sistema de arquivos somente-leitura para o servidor HTTP servir.
//
// Não há passo de build de frontend (sem npm/bundler): são HTML + CSS + ES
// modules vanilla, embutidos no binário via //go:embed. O servidor
// (internal/api) monta um http.FileServer sobre este FS.
package web

import (
	"embed"
	"io/fs"
)

// arquivos embute os assets do frontend. O prefixo raiz do FS embutido inclui
// os nomes literais (index.html, app.css, js/…); use Assets para obter um FS já
// com esses arquivos na raiz.
//
//go:embed index.html app.css js
var arquivos embed.FS

// Assets é o sistema de arquivos com os assets do frontend na raiz
// (index.html, app.css, js/*.js). É somente-leitura e seguro para uso
// concorrente. O servidor HTTP o serve com http.FileServerFS.
var Assets fs.FS = arquivos
