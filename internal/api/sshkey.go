package api

// Endpoints da chave SSH POR USUÁRIO (Fase C do PLANO_MULTIUSUARIO.md). São
// rotas de AUTOSSERVIÇO: cada usuário gerencia a própria chave — por isso vivem
// em /me/ e exigem um usuário logado (tokens de API não têm chave). A privada
// nunca sai do servidor; a API devolve só a pública e o fingerprint.

import (
	"errors"
	"net/http"

	"github.com/marcos14/praxis-autonomous/internal/chavessh"
)

// registrarRotasSSH registra as rotas da chave SSH do usuário logado.
func (s *Servidor) registrarRotasSSH(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/me/ssh-key", s.handleObterChaveSSH)
	mux.HandleFunc("POST /api/v1/me/ssh-key", s.handleGerarChaveSSH)
	mux.HandleFunc("POST /api/v1/me/ssh-key/testar", s.handleTestarChaveSSH)
}

// usuarioParaChaveSSH resolve o usuário logado e o gerente de chaves; escreve o
// erro (403/503) e devolve ok=false quando algum falta.
func (s *Servidor) usuarioParaChaveSSH(w http.ResponseWriter, r *http.Request) (int64, bool) {
	if s.ssh == nil {
		erroT(w, r, http.StatusServiceUnavailable, "indisponivel", "erro.ssh_indisponivel")
		return 0, false
	}
	uid := usuarioDaRequisicao(r)
	if uid == nil {
		erroT(w, r, http.StatusForbidden, "sem_usuario", "erro.ssh_sem_usuario")
		return 0, false
	}
	return *uid, true
}

// handleObterChaveSSH devolve a chave PÚBLICA + fingerprint do usuário logado
// (404 quando ainda não gerada — a UI mostra o botão de gerar).
func (s *Servidor) handleObterChaveSSH(w http.ResponseWriter, r *http.Request) {
	uid, ok := s.usuarioParaChaveSSH(w, r)
	if !ok {
		return
	}
	chave, err := s.ssh.Chave(uid)
	if err != nil {
		if errors.Is(err, chavessh.ErrNaoExiste) {
			erroT(w, r, http.StatusNotFound, "sem_chave", "erro.ssh_sem_chave")
			return
		}
		s.log.Error("ler chave ssh", "usuario", uid, "erro", err)
		erroT(w, r, http.StatusInternalServerError, "erro_interno", "erro.interno")
		return
	}
	responderJSON(w, http.StatusOK, chave)
}

// handleGerarChaveSSH gera o par do usuário logado e devolve a pública (409 se
// já existe — regenerar invalidaria o cadastro nas plataformas; não fazemos).
func (s *Servidor) handleGerarChaveSSH(w http.ResponseWriter, r *http.Request) {
	uid, ok := s.usuarioParaChaveSSH(w, r)
	if !ok {
		return
	}
	// O comentário identifica a chave na lista da plataforma (Settings → Keys).
	comentario := "praxis-" + principalDaRequisicao(r).email
	chave, err := s.ssh.Gerar(r.Context(), uid, comentario)
	if err != nil {
		if errors.Is(err, chavessh.ErrJaExiste) {
			erroT(w, r, http.StatusConflict, "chave_existente", "erro.ssh_chave_existente")
			return
		}
		s.log.Error("gerar chave ssh", "usuario", uid, "erro", err)
		erroT(w, r, http.StatusInternalServerError, "erro_geracao", "erro.ssh_falha_gerar")
		return
	}
	responderJSON(w, http.StatusCreated, chave)
}

// reqTestarSSH é o corpo de POST /me/ssh-key/testar.
type reqTestarSSH struct {
	URL string `json:"url"`
}

// respTestarSSH é o veredito do teste de conexão (sanitizado: a última linha do
// git — "Permission denied (publickey)" e afins — sem despejar stderr inteiro).
type respTestarSSH struct {
	OK      bool   `json:"ok"`
	Detalhe string `json:"detalhe,omitempty"`
}

// handleTestarChaveSSH roda `git ls-remote` com a chave do usuário contra a URL
// informada e devolve o veredito.
func (s *Servidor) handleTestarChaveSSH(w http.ResponseWriter, r *http.Request) {
	uid, ok := s.usuarioParaChaveSSH(w, r)
	if !ok {
		return
	}
	var req reqTestarSSH
	if !decodificarCorpo(w, r, &req) {
		return
	}
	if err := s.ssh.Testar(r.Context(), uid, req.URL); err != nil {
		responderJSON(w, http.StatusOK, respTestarSSH{OK: false, Detalhe: err.Error()})
		return
	}
	responderJSON(w, http.StatusOK, respTestarSSH{OK: true})
}
