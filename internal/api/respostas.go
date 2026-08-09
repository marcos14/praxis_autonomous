package api

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/marcos14/praxis-autonomous/internal/i18n"
)

// tipoJSON é o Content-Type usado em todas as respostas JSON da API.
const tipoJSON = "application/json; charset=utf-8"

// ErroResp é o envelope padronizado de erro devolvido pela API. Todos os
// handlers que falham respondem com esta forma, para o frontend tratar erros de
// maneira uniforme.
type ErroResp struct {
	Erro ErroDetalhe `json:"erro"`
}

// ErroDetalhe descreve um erro: um código estável (legível por máquina) e uma
// mensagem legível por humano.
type ErroDetalhe struct {
	Codigo   string `json:"codigo"`
	Mensagem string `json:"mensagem"`
}

// responderJSON serializa v como JSON e escreve a resposta com o status dado.
// Se a codificação falhar depois do cabeçalho enviado, apenas registra — não há
// como corrigir o status já escrito.
func responderJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", tipoJSON)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if v == nil {
		return
	}
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("codificar resposta JSON", "erro", err)
	}
}

// responderErro escreve um erro padronizado (ErroResp) com o status HTTP dado.
func responderErro(w http.ResponseWriter, status int, codigo, mensagem string) {
	responderJSON(w, status, ErroResp{Erro: ErroDetalhe{Codigo: codigo, Mensagem: mensagem}})
}

// idiomaDaRequisicao resolve o idioma da resposta: o header X-Praxis-Idioma
// (a UI envia o idioma ativo em toda requisição) → Accept-Language do navegador
// → o padrão do i18n. Resolução por headers apenas — determinística e sem
// consulta ao banco no caminho de erro.
func idiomaDaRequisicao(r *http.Request) string {
	if l := i18n.Normalizar(r.Header.Get("X-Praxis-Idioma")); l != "" {
		return l
	}
	if l := i18n.DoAcceptLanguage(r.Header.Get("Accept-Language")); l != "" {
		return l
	}
	return i18n.Padrao
}

// erroT é o responderErro com a mensagem vinda do catálogo (internal/i18n) no
// idioma da requisição. `codigo` continua sendo o contrato estável da API;
// `chave` aponta a mensagem ("erro.<algo>"), com pares nome/valor interpolados.
func erroT(w http.ResponseWriter, r *http.Request, status int, codigo, chave string, args ...string) {
	responderErro(w, status, codigo, i18n.T(idiomaDaRequisicao(r), chave, args...))
}
