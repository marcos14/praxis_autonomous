package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/marcos14/praxis-autonomous/internal/db"
)

// reqGrupoUsuarios é o corpo aceito em POST/PUT de grupos de usuários. EngineID
// nulo/0 = motor padrão (ordem de fallback); Modelo vazio = modelo_consulta do
// motor. O vínculo usuário↔grupo é feito no cadastro do usuário (grupo_id).
type reqGrupoUsuarios struct {
	Nome      string `json:"nome"`
	Descricao string `json:"descricao"`
	EngineID  *int64 `json:"engine_id"`
	Modelo    string `json:"modelo"`
}

// registrarRotasGruposUsuarios registra as rotas de grupos de usuários (feature
// de consultas). Como users/roles, TODAS exigem usuarios.gerir (garantido pelo
// prefixo em requisitoRota).
func (s *Servidor) registrarRotasGruposUsuarios(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/user-groups", s.handleListarGruposUsuarios)
	mux.HandleFunc("POST /api/v1/user-groups", s.handleCriarGrupoUsuarios)
	mux.HandleFunc("PUT /api/v1/user-groups/{id}", s.handleAtualizarGrupoUsuarios)
	mux.HandleFunc("DELETE /api/v1/user-groups/{id}", s.handleExcluirGrupoUsuarios)
}

func (s *Servidor) handleListarGruposUsuarios(w http.ResponseWriter, r *http.Request) {
	grupos, err := s.banco.ListarGruposUsuarios(r.Context())
	if err != nil {
		s.responderErroGrupoUsuarios(w, err)
		return
	}
	responderJSON(w, http.StatusOK, grupos)
}

func (s *Servidor) handleCriarGrupoUsuarios(w http.ResponseWriter, r *http.Request) {
	var req reqGrupoUsuarios
	if !decodificarCorpo(w, r, &req) {
		return
	}
	g, msg := montarGrupoUsuarios(req)
	if msg != "" {
		responderErro(w, http.StatusBadRequest, "invalido", msg)
		return
	}
	criado, err := s.banco.CriarGrupoUsuarios(r.Context(), g)
	if err != nil {
		s.responderErroGrupoUsuarios(w, err)
		return
	}
	responderJSON(w, http.StatusCreated, criado)
}

func (s *Servidor) handleAtualizarGrupoUsuarios(w http.ResponseWriter, r *http.Request) {
	id, ok := idDaRota(w, r)
	if !ok {
		return
	}
	var req reqGrupoUsuarios
	if !decodificarCorpo(w, r, &req) {
		return
	}
	g, msg := montarGrupoUsuarios(req)
	if msg != "" {
		responderErro(w, http.StatusBadRequest, "invalido", msg)
		return
	}
	g.ID = id
	atualizado, err := s.banco.AtualizarGrupoUsuarios(r.Context(), g)
	if err != nil {
		s.responderErroGrupoUsuarios(w, err)
		return
	}
	responderJSON(w, http.StatusOK, atualizado)
}

func (s *Servidor) handleExcluirGrupoUsuarios(w http.ResponseWriter, r *http.Request) {
	id, ok := idDaRota(w, r)
	if !ok {
		return
	}
	if err := s.banco.ExcluirGrupoUsuarios(r.Context(), id); err != nil {
		s.responderErroGrupoUsuarios(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// montarGrupoUsuarios valida o corpo e devolve o grupo pronto para persistir.
func montarGrupoUsuarios(req reqGrupoUsuarios) (db.GrupoUsuarios, string) {
	g := db.GrupoUsuarios{
		Nome:      strings.TrimSpace(req.Nome),
		Descricao: strings.TrimSpace(req.Descricao),
		Modelo:    strings.TrimSpace(req.Modelo),
	}
	if g.Nome == "" {
		return db.GrupoUsuarios{}, "nome é obrigatório"
	}
	if req.EngineID != nil && *req.EngineID > 0 {
		g.EngineID = req.EngineID
	}
	return g, ""
}

// responderErroGrupoUsuarios traduz os erros do store para respostas HTTP.
func (s *Servidor) responderErroGrupoUsuarios(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, db.ErrNaoEncontrado):
		responderErro(w, http.StatusNotFound, "nao_encontrado", "grupo de usuários ou motor não encontrado")
	case errors.Is(err, db.ErrGrupoUsuariosDuplicado):
		responderErro(w, http.StatusConflict, "nome_duplicado", err.Error())
	default:
		s.log.Error("erro no store de grupos de usuários", "erro", err)
		responderErro(w, http.StatusInternalServerError, "erro_interno", "erro interno do servidor")
	}
}
