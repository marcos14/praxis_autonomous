package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/marcos14/praxis-autonomous/internal/db"
)

// reqGrupo é o corpo aceito em POST/PUT de grupos de repositórios. ProjectIDs
// define os membros NA ORDEM: o primeiro é o repositório principal (cwd do
// harness nas consultas do grupo). Ativo é ponteiro para distinguir "não
// informado" de false explícito.
type reqGrupo struct {
	Nome       string  `json:"nome"`
	Slug       string  `json:"slug"`
	Descricao  string  `json:"descricao"`
	ProjectIDs []int64 `json:"project_ids"`
	Ativo      *bool   `json:"ativo"`
}

// registrarRotasGrupos registra as rotas de CRUD de grupos de repositórios.
// Leituras exigem autenticação; mutações exigem projetos.gerir (ver
// permissaoMutacao).
func (s *Servidor) registrarRotasGrupos(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/groups", s.handleCriarGrupo)
	mux.HandleFunc("GET /api/v1/groups", s.handleListarGrupos)
	mux.HandleFunc("GET /api/v1/groups/{id}", s.handleObterGrupo)
	mux.HandleFunc("PUT /api/v1/groups/{id}", s.handleAtualizarGrupo)
	mux.HandleFunc("DELETE /api/v1/groups/{id}", s.handleExcluirGrupo)
}

func (s *Servidor) handleCriarGrupo(w http.ResponseWriter, r *http.Request) {
	var req reqGrupo
	if !decodificarCorpo(w, r, &req) {
		return
	}
	g, ids, msg := montarGrupo(req, db.Grupo{}, true)
	if msg != "" {
		responderErro(w, http.StatusBadRequest, "invalido", msg)
		return
	}
	criado, err := s.banco.CriarGrupo(r.Context(), g, ids)
	if err != nil {
		s.responderErroGrupo(w, r, err)
		return
	}
	responderJSON(w, http.StatusCreated, criado)
}

func (s *Servidor) handleListarGrupos(w http.ResponseWriter, r *http.Request) {
	grupos, err := s.banco.ListarGrupos(r.Context())
	if err != nil {
		s.responderErroGrupo(w, r, err)
		return
	}
	// ACL de projetos: um grupo de repositórios só aparece para o usuário
	// restrito quando TODOS os projetos-membros são visíveis (fail-closed).
	if uid := visibilidadeDaRequisicao(r); uid != nil {
		visiveis := grupos[:0]
		for _, g := range grupos {
			ve, err := s.banco.UsuarioVeGrupoProjetos(r.Context(), *uid, g.ID)
			if err != nil {
				s.responderErroGrupo(w, r, err)
				return
			}
			if ve {
				visiveis = append(visiveis, g)
			}
		}
		grupos = visiveis
	}
	responderJSON(w, http.StatusOK, grupos)
}

func (s *Servidor) handleObterGrupo(w http.ResponseWriter, r *http.Request) {
	id, ok := lerIDGrupo(w, r)
	if !ok {
		return
	}
	g, err := s.banco.ObterGrupo(r.Context(), id)
	if err != nil {
		s.responderErroGrupo(w, r, err)
		return
	}
	responderJSON(w, http.StatusOK, g)
}

func (s *Servidor) handleAtualizarGrupo(w http.ResponseWriter, r *http.Request) {
	id, ok := lerIDGrupo(w, r)
	if !ok {
		return
	}
	atual, err := s.banco.ObterGrupo(r.Context(), id)
	if err != nil {
		s.responderErroGrupo(w, r, err)
		return
	}
	var req reqGrupo
	if !decodificarCorpo(w, r, &req) {
		return
	}
	g, ids, msg := montarGrupo(req, atual, false)
	if msg != "" {
		responderErro(w, http.StatusBadRequest, "invalido", msg)
		return
	}
	g.ID = id
	atualizado, err := s.banco.AtualizarGrupo(r.Context(), g, ids)
	if err != nil {
		s.responderErroGrupo(w, r, err)
		return
	}
	responderJSON(w, http.StatusOK, atualizado)
}

// handleExcluirGrupo remove o grupo. Atenção (documentado na UI): as consultas
// vinculadas ao grupo caem junto (ON DELETE CASCADE).
func (s *Servidor) handleExcluirGrupo(w http.ResponseWriter, r *http.Request) {
	id, ok := lerIDGrupo(w, r)
	if !ok {
		return
	}
	if err := s.banco.ExcluirGrupo(r.Context(), id); err != nil {
		s.responderErroGrupo(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// montarGrupo aplica defaults e validações sobre req (mesma semântica de
// montarProjeto). Devolve o grupo, os ids dos membros na ordem e, em falha de
// validação, uma mensagem não-vazia.
func montarGrupo(req reqGrupo, base db.Grupo, criando bool) (db.Grupo, []int64, string) {
	g := base

	g.Nome = strings.TrimSpace(req.Nome)
	if g.Nome == "" {
		return db.Grupo{}, nil, "nome é obrigatório"
	}
	if len(req.ProjectIDs) == 0 {
		return db.Grupo{}, nil, "o grupo precisa de pelo menos um projeto (o primeiro é o repositório principal)"
	}

	slug := gerarSlug(req.Slug)
	if slug == "" {
		if criando || base.Slug == "" {
			slug = gerarSlug(g.Nome)
		} else {
			slug = base.Slug
		}
	}
	if slug == "" {
		return db.Grupo{}, nil, "slug inválido: informe um slug ou um nome com caracteres alfanuméricos"
	}
	g.Slug = slug

	g.Descricao = strings.TrimSpace(req.Descricao)

	if req.Ativo != nil {
		g.Ativo = *req.Ativo
	} else if criando {
		g.Ativo = true
	}
	return g, req.ProjectIDs, ""
}

// lerIDGrupo extrai e valida o path param {id}. Em erro, escreve 400 e devolve false.
func lerIDGrupo(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		erroT(w, r, http.StatusBadRequest, "invalido", "erro.id_invalido")
		return 0, false
	}
	return id, true
}

// responderErroGrupo traduz os erros do store de grupos para respostas HTTP.
func (s *Servidor) responderErroGrupo(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, db.ErrNaoEncontrado):
		erroT(w, r, http.StatusNotFound, "nao_encontrado", "erro.grupo_nao_encontrado")
	case errors.Is(err, db.ErrSlugDuplicado):
		erroT(w, r, http.StatusConflict, "slug_duplicado", "erro.grupo_slug_duplicado")
	default:
		s.log.Error("erro no store de grupos", "erro", err)
		erroT(w, r, http.StatusInternalServerError, "erro_interno", "erro.interno")
	}
}
