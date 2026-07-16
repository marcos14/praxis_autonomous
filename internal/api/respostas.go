package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
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
