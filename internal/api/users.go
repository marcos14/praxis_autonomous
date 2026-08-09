package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/marcos14/praxis-autonomous/internal/db"
)

// registrarRotasUsuarios registra as rotas de gestão de usuários, papéis e o
// catálogo de permissões. Todas exigem a permissão usuarios.gerir (garantida pelo
// middleware via prefixo em requisitoRota).
func (s *Servidor) registrarRotasUsuarios(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/users", s.handleListarUsuarios)
	mux.HandleFunc("POST /api/v1/users", s.handleCriarUsuario)
	mux.HandleFunc("PUT /api/v1/users/{id}", s.handleAtualizarUsuario)
	mux.HandleFunc("PUT /api/v1/users/{id}/senha", s.handleResetarSenha)
	mux.HandleFunc("DELETE /api/v1/users/{id}", s.handleExcluirUsuario)

	mux.HandleFunc("GET /api/v1/roles", s.handleListarPapeis)
	mux.HandleFunc("POST /api/v1/roles", s.handleCriarPapel)
	mux.HandleFunc("PUT /api/v1/roles/{id}", s.handleAtualizarPapel)
	mux.HandleFunc("DELETE /api/v1/roles/{id}", s.handleExcluirPapel)

	mux.HandleFunc("GET /api/v1/permissions", s.handleListarPermissoes)
}

// ---------------------------------------------------------------------------
// Usuários
// ---------------------------------------------------------------------------

type reqUsuario struct {
	Nome    string  `json:"nome"`
	Email   string  `json:"email"`
	Senha   string  `json:"senha"`
	Ativo   bool    `json:"ativo"`
	Papeis  []int64 `json:"papeis"`   // ids dos papéis
	GrupoID *int64  `json:"grupo_id"` // grupo de usuários (consultas); nil/0 = sem grupo
}

func (s *Servidor) handleListarUsuarios(w http.ResponseWriter, r *http.Request) {
	usuarios, err := s.banco.ListarUsuarios(r.Context())
	if err != nil {
		s.responderErroUsuario(w, r, err)
		return
	}
	responderJSON(w, http.StatusOK, usuarios)
}

func (s *Servidor) handleCriarUsuario(w http.ResponseWriter, r *http.Request) {
	var req reqUsuario
	if !decodificarCorpo(w, r, &req) {
		return
	}
	u, err := s.banco.CriarUsuario(r.Context(), req.Nome, req.Email, req.Senha, req.Papeis)
	if err != nil {
		s.responderErroUsuario(w, r, err)
		return
	}
	if u, err = s.aplicarGrupoDoUsuario(r, u.ID, req.GrupoID); err != nil {
		s.responderErroUsuario(w, r, err)
		return
	}
	responderJSON(w, http.StatusCreated, u)
}

// aplicarGrupoDoUsuario grava o vínculo do usuário com o grupo de usuários
// (consultas) e devolve o usuário relido. grupoID nulo ou <= 0 remove o vínculo.
func (s *Servidor) aplicarGrupoDoUsuario(r *http.Request, userID int64, grupoID *int64) (db.Usuario, error) {
	alvo := grupoID
	if alvo != nil && *alvo <= 0 {
		alvo = nil
	}
	if err := s.banco.DefinirGrupoDoUsuario(r.Context(), userID, alvo); err != nil {
		return db.Usuario{}, err
	}
	return s.banco.ObterUsuario(r.Context(), userID)
}

func (s *Servidor) handleAtualizarUsuario(w http.ResponseWriter, r *http.Request) {
	id, ok := idDaRota(w, r)
	if !ok {
		return
	}
	var req reqUsuario
	if !decodificarCorpo(w, r, &req) {
		return
	}
	// Anti-lockout: não deixar o último admin ativo perder o acesso (desativar ou
	// remover todos os papéis com curinga) por edição própria/alheia.
	if err := s.protegerUltimoAdmin(r, id, req.Ativo, req.Papeis); err != nil {
		responderErro(w, http.StatusConflict, "ultimo_admin", err.Error())
		return
	}
	u, err := s.banco.AtualizarUsuario(r.Context(), id, req.Nome, req.Email, req.Ativo, req.Papeis)
	if err != nil {
		s.responderErroUsuario(w, r, err)
		return
	}
	if u, err = s.aplicarGrupoDoUsuario(r, u.ID, req.GrupoID); err != nil {
		s.responderErroUsuario(w, r, err)
		return
	}
	responderJSON(w, http.StatusOK, u)
}

type reqResetSenha struct {
	Nova string `json:"nova"`
}

func (s *Servidor) handleResetarSenha(w http.ResponseWriter, r *http.Request) {
	id, ok := idDaRota(w, r)
	if !ok {
		return
	}
	var req reqResetSenha
	if !decodificarCorpo(w, r, &req) {
		return
	}
	if err := s.banco.DefinirSenha(r.Context(), id, req.Nova); err != nil {
		s.responderErroUsuario(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Servidor) handleExcluirUsuario(w http.ResponseWriter, r *http.Request) {
	id, ok := idDaRota(w, r)
	if !ok {
		return
	}
	// Não permitir excluir a si mesmo (evita o admin se auto-remover por engano) ...
	if pr := principalDaRequisicao(r); pr.userID == id {
		erroT(w, r, http.StatusConflict, "auto_exclusao", "erro.usuario_auto_exclusao")
		return
	}
	// ... nem remover o último admin ativo.
	if err := s.protegerUltimoAdmin(r, id, false, nil); err != nil {
		responderErro(w, http.StatusConflict, "ultimo_admin", err.Error())
		return
	}
	if err := s.banco.ExcluirUsuario(r.Context(), id); err != nil {
		s.responderErroUsuario(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// protegerUltimoAdmin garante que a operação sobre o usuário id (desativação,
// remoção, ou troca de papéis que lhe tire o curinga) não deixe a instalação sem
// nenhum admin ativo. novosPapeis/ativo descrevem o estado APÓS a operação (para
// exclusão: ativo=false, papéis=nil). Devolve erro se seria o último admin.
func (s *Servidor) protegerUltimoAdmin(r *http.Request, id int64, ativo bool, novosPapeis []int64) error {
	atual, err := s.banco.ObterUsuario(r.Context(), id)
	if err != nil {
		return nil // inexistente: deixa o handler tratar (404 no passo seguinte)
	}
	eraAdmin := false
	for _, p := range atual.Papeis {
		for _, perm := range mustPerms(s, r, p.ID) {
			if perm == db.PermCuringa {
				eraAdmin = true
			}
		}
	}
	if !atual.Ativo || !eraAdmin {
		return nil // não era admin ativo: a operação não reduz o número de admins
	}
	// Continuará admin ativo? (mantém curinga e segue ativo)
	if ativo && contemPapelComCuringa(s, r, novosPapeis) {
		return nil
	}
	n, err := s.banco.ContarAdmins(r.Context())
	if err != nil {
		return nil // em caso de erro de contagem, não bloqueia (o handler seguinte trata)
	}
	if n <= 1 {
		return errors.New("não é possível remover o acesso do último administrador ativo")
	}
	return nil
}

// mustPerms devolve as permissões de um papel (silenciando erro — usado só nas
// checagens de anti-lockout, onde um erro não deve bloquear a operação).
func mustPerms(s *Servidor, r *http.Request, roleID int64) []string {
	p, err := s.banco.ObterPapel(r.Context(), roleID)
	if err != nil {
		return nil
	}
	return p.Permissoes
}

// contemPapelComCuringa informa se algum dos papéis dados concede o curinga.
func contemPapelComCuringa(s *Servidor, r *http.Request, roleIDs []int64) bool {
	for _, rid := range roleIDs {
		for _, perm := range mustPerms(s, r, rid) {
			if perm == db.PermCuringa {
				return true
			}
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Papéis
// ---------------------------------------------------------------------------

type reqPapel struct {
	Nome       string   `json:"nome"`
	Descricao  string   `json:"descricao"`
	Permissoes []string `json:"permissoes"`
}

func (s *Servidor) handleListarPapeis(w http.ResponseWriter, r *http.Request) {
	papeis, err := s.banco.ListarPapeis(r.Context())
	if err != nil {
		s.responderErroUsuario(w, r, err)
		return
	}
	responderJSON(w, http.StatusOK, papeis)
}

func (s *Servidor) handleCriarPapel(w http.ResponseWriter, r *http.Request) {
	var req reqPapel
	if !decodificarCorpo(w, r, &req) {
		return
	}
	p, err := s.banco.CriarPapel(r.Context(), req.Nome, req.Descricao, req.Permissoes)
	if err != nil {
		s.responderErroUsuario(w, r, err)
		return
	}
	responderJSON(w, http.StatusCreated, p)
}

func (s *Servidor) handleAtualizarPapel(w http.ResponseWriter, r *http.Request) {
	id, ok := idDaRota(w, r)
	if !ok {
		return
	}
	var req reqPapel
	if !decodificarCorpo(w, r, &req) {
		return
	}
	p, err := s.banco.AtualizarPapel(r.Context(), id, req.Nome, req.Descricao, req.Permissoes)
	if err != nil {
		s.responderErroUsuario(w, r, err)
		return
	}
	responderJSON(w, http.StatusOK, p)
}

func (s *Servidor) handleExcluirPapel(w http.ResponseWriter, r *http.Request) {
	id, ok := idDaRota(w, r)
	if !ok {
		return
	}
	if err := s.banco.ExcluirPapel(r.Context(), id); err != nil {
		s.responderErroUsuario(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleListarPermissoes devolve o catálogo de permissões atribuíveis (para a UI
// montar as caixas de seleção de um papel).
func (s *Servidor) handleListarPermissoes(w http.ResponseWriter, r *http.Request) {
	responderJSON(w, http.StatusOK, db.CatalogoPermissoes)
}

// ---------------------------------------------------------------------------
// Utilitários
// ---------------------------------------------------------------------------

// idDaRota extrai e valida o {id} numérico da rota; responde 400 e devolve false
// quando inválido.
func idDaRota(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		erroT(w, r, http.StatusBadRequest, "invalido", "erro.id_invalido")
		return 0, false
	}
	return id, true
}

// responderErroUsuario mapeia os erros da camada db (usuários/papéis) para HTTP.
func (s *Servidor) responderErroUsuario(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, db.ErrNaoEncontrado):
		erroT(w, r, http.StatusNotFound, "nao_encontrado", "erro.usuario_registro_nao_encontrado")
	case errors.Is(err, db.ErrEmailDuplicado):
		responderErro(w, http.StatusConflict, "email_duplicado", err.Error())
	case errors.Is(err, db.ErrPapelDuplicado):
		responderErro(w, http.StatusConflict, "papel_duplicado", err.Error())
	case errors.Is(err, db.ErrPapelSistema):
		responderErro(w, http.StatusConflict, "papel_sistema", err.Error())
	case errors.Is(err, db.ErrPermissaoInvalida):
		responderErro(w, http.StatusBadRequest, "permissao_invalida", err.Error())
	case errors.Is(err, db.ErrPapelInexistente):
		responderErro(w, http.StatusBadRequest, "papel_invalido", err.Error())
	case errors.Is(err, db.ErrSenhaVazia):
		responderErro(w, http.StatusBadRequest, "senha_vazia", err.Error())
	default:
		s.log.Error("operação de usuários/papéis", "erro", err)
		erroT(w, r, http.StatusInternalServerError, "erro_interno", "erro.interno")
	}
}
