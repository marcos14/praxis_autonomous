package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/marcos14/praxis-autonomous/internal/db"
)

// reqChat é o corpo de POST /demands/{id}/chat: uma fala do usuário no chat da
// demanda (complemento ao PRD). O papel é sempre "user" — falas do analista/
// planejador/sistema são geradas pelo backend (intake das Fases 3b/3c).
type reqChat struct {
	Conteudo string `json:"conteudo"`
}

// handleChatDemanda acrescenta uma mensagem do usuário ao chat da demanda e
// devolve 201 com a mensagem persistida. Conteúdo vazio → 400. A conversa é a
// forma da demanda até o plano ser aprovado (Fase 3a).
func (s *Servidor) handleChatDemanda(w http.ResponseWriter, r *http.Request) {
	dem, ok := s.obterDemandaOu404(w, r)
	if !ok {
		return
	}
	var req reqChat
	if !decodificarCorpo(w, r, &req) {
		return
	}
	conteudo := strings.TrimSpace(req.Conteudo)
	if conteudo == "" {
		responderErro(w, http.StatusBadRequest, "invalido", "conteudo é obrigatório")
		return
	}

	msg, err := s.banco.CriarMensagemChat(r.Context(), db.MensagemChat{
		DemandID: dem.ID,
		Papel:    db.PapelUser,
		Conteudo: conteudo,
	})
	if err != nil {
		s.responderErroChat(w, err)
		return
	}
	responderJSON(w, http.StatusCreated, msg)
}

// handleListarChat devolve as mensagens do chat da demanda em ordem cronológica
// (aba Chat/PRD do card).
func (s *Servidor) handleListarChat(w http.ResponseWriter, r *http.Request) {
	dem, ok := s.obterDemandaOu404(w, r)
	if !ok {
		return
	}
	msgs, err := s.banco.ListarMensagensChat(r.Context(), dem.ID)
	if err != nil {
		s.responderErroChat(w, err)
		return
	}
	responderJSON(w, http.StatusOK, msgs)
}

// responderErroChat traduz os erros do store de chat para respostas HTTP.
func (s *Servidor) responderErroChat(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, db.ErrPapelInvalido):
		responderErro(w, http.StatusBadRequest, "invalido", "papel de mensagem inválido")
	case errors.Is(err, db.ErrNaoEncontrado):
		responderErro(w, http.StatusNotFound, "nao_encontrado", "demanda não encontrada")
	default:
		s.log.Error("erro no store de chat", "erro", err)
		responderErro(w, http.StatusInternalServerError, "erro_interno", "erro interno do servidor")
	}
}
