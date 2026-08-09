package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/marcos14/praxis-autonomous/internal/db"
)

// registrarRotasTokens registra as rotas de gestão de tokens de API (Fase 5a).
// Exigem a permissão usuarios.gerir (garantida pelo middleware comAuth via
// prefixo /tokens — gestão de acessos).
func (s *Servidor) registrarRotasTokens(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/tokens", s.handleCriarToken)
	mux.HandleFunc("GET /api/v1/tokens", s.handleListarTokens)
	mux.HandleFunc("DELETE /api/v1/tokens/{id}", s.handleRevogarToken)
}

// reqToken é o corpo de POST /tokens.
type reqToken struct {
	Nome  string `json:"nome"`
	Papel string `json:"papel"`
}

// handleCriarToken cria um token e devolve 201 com o valor em CLARO (mostrado uma
// única vez — o banco só guarda o hash). Papel default: operador.
func (s *Servidor) handleCriarToken(w http.ResponseWriter, r *http.Request) {
	var req reqToken
	if !decodificarCorpo(w, r, &req) {
		return
	}
	papel := strings.TrimSpace(req.Papel)
	if papel == "" {
		papel = db.PapelOperador
	}
	if !db.PapelTokenValido(papel) {
		erroT(w, r, http.StatusBadRequest, "invalido", "erro.token_papel_invalido")
		return
	}
	if strings.TrimSpace(req.Nome) == "" {
		erroT(w, r, http.StatusBadRequest, "invalido", "erro.token_nome_obrigatorio")
		return
	}
	tok, err := s.banco.CriarToken(r.Context(), req.Nome, papel)
	if err != nil {
		s.log.Error("criar token", "erro", err)
		erroT(w, r, http.StatusInternalServerError, "erro_interno", "erro.interno")
		return
	}
	responderJSON(w, http.StatusCreated, tok)
}

// handleListarTokens devolve os tokens (sem hash nem valor em claro).
func (s *Servidor) handleListarTokens(w http.ResponseWriter, r *http.Request) {
	tokens, err := s.banco.ListarTokens(r.Context())
	if err != nil {
		s.log.Error("listar tokens", "erro", err)
		erroT(w, r, http.StatusInternalServerError, "erro_interno", "erro.interno")
		return
	}
	responderJSON(w, http.StatusOK, tokens)
}

// handleRevogarToken revoga o token de {id}. Idempotente (204). Inexistente → 404.
func (s *Servidor) handleRevogarToken(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		erroT(w, r, http.StatusBadRequest, "invalido", "erro.id_invalido")
		return
	}
	if err := s.banco.RevogarToken(r.Context(), id); err != nil {
		if errors.Is(err, db.ErrNaoEncontrado) {
			erroT(w, r, http.StatusNotFound, "nao_encontrado", "erro.token_nao_encontrado")
			return
		}
		s.log.Error("revogar token", "erro", err)
		erroT(w, r, http.StatusInternalServerError, "erro_interno", "erro.interno")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
