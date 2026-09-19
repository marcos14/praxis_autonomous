package api

import (
	"errors"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/marcos14/praxis-autonomous/internal/db"
)

// Cookie da sessão persistida (M1 do PLANO_INTERNET). Carrega o token opaco
// cujo hash está em `sessoes`. HttpOnly (o JS nunca o lê), SameSite=Strict
// (nunca viaja a partir de outro site) e restrito ao caminho das rotas de auth:
// só /auth/refresh, /auth/logout e as demais rotas /auth/* o recebem — o resto
// da API continua autenticando pelo JWT no header, então nenhuma outra rota
// fica sujeita a CSRF por cookie.
const (
	nomeCookieSessao    = "praxis_sessao"
	caminhoCookieSessao = "/api/v1/auth"
)

// conexaoSegura informa se a requisição chegou por HTTPS: TLS direto no
// servidor ou, quando o operador declarou um proxy confiável (-proxy-confiavel),
// o cabeçalho X-Forwarded-Proto do proxy. Decide o atributo Secure do cookie.
func (s *Servidor) conexaoSegura(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return s.proxyConfiavel && strings.EqualFold(strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")), "https")
}

// ipDaRequisicao devolve o IP do cliente: o primeiro X-Forwarded-For quando há
// proxy confiável, senão o host de RemoteAddr. Só diagnóstico (lista de
// sessões) — sem proxy confiável o cabeçalho é ignorado, pois qualquer cliente
// pode forjá-lo.
func (s *Servidor) ipDaRequisicao(r *http.Request) string {
	if s.proxyConfiavel {
		xff := r.Header.Get("X-Forwarded-For")
		if i := strings.IndexByte(xff, ','); i >= 0 {
			xff = xff[:i]
		}
		if ip := strings.TrimSpace(xff); ip != "" {
			return ip
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// gravarCookieSessao envia o cookie com o token da sessão, válido por maxAge no
// navegador (a mesma inatividade do servidor). É reenviado a cada renovação
// para o navegador acompanhar o deslize da expiração.
func (s *Servidor) gravarCookieSessao(w http.ResponseWriter, r *http.Request, token string, maxAge time.Duration) {
	http.SetCookie(w, &http.Cookie{
		Name:     nomeCookieSessao,
		Value:    token,
		Path:     caminhoCookieSessao,
		MaxAge:   int(maxAge.Seconds()),
		HttpOnly: true,
		Secure:   s.conexaoSegura(r),
		SameSite: http.SameSiteStrictMode,
	})
}

// apagarCookieSessao manda o navegador descartar o cookie da sessão.
func (s *Servidor) apagarCookieSessao(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     nomeCookieSessao,
		Value:    "",
		Path:     caminhoCookieSessao,
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   s.conexaoSegura(r),
		SameSite: http.SameSiteStrictMode,
	})
}

// tokenSessaoDaRequisicao lê o token do cookie da sessão ("" se ausente).
func tokenSessaoDaRequisicao(r *http.Request) string {
	c, err := r.Cookie(nomeCookieSessao)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(c.Value)
}

// abrirSessao cria a sessão persistida do usuário recém-autenticado e grava o
// cookie. Precisa ser chamado antes de escrever o corpo da resposta.
func (s *Servidor) abrirSessao(w http.ResponseWriter, r *http.Request, userID int64, prazos prazosAuth) error {
	sess, err := s.banco.CriarSessao(r.Context(), userID, r.UserAgent(), s.ipDaRequisicao(r), prazos.Sessao)
	if err != nil {
		return err
	}
	s.gravarCookieSessao(w, r, sess.Token, prazos.Sessao.Inatividade)
	return nil
}

// sessaoAtualID devolve o id da sessão do cookie da requisição (0 quando não há
// cookie ou a sessão não está ativa). Usado para poupar a própria sessão ao
// "encerrar as outras" (troca de senha).
func (s *Servidor) sessaoAtualID(r *http.Request, inatividade time.Duration) int64 {
	token := tokenSessaoDaRequisicao(r)
	if token == "" {
		return 0
	}
	sess, err := s.banco.AutenticarSessao(r.Context(), token, inatividade)
	if err != nil {
		return 0
	}
	return sess.ID
}

// handleAuthRefresh renova o JWT a partir do cookie da sessão. É o boot do
// frontend (o JWT vive só em memória) e a renovação periódica. Sem cookie,
// sessão revogada/expirada, ou usuário removido/desativado → 401 e o cookie é
// apagado (o cliente cai no portão de login). Sucesso reenvia o cookie para o
// navegador acompanhar o deslize da inatividade.
func (s *Servidor) handleAuthRefresh(w http.ResponseWriter, r *http.Request) {
	token := tokenSessaoDaRequisicao(r)
	if token == "" {
		erroT(w, r, http.StatusUnauthorized, "sessao_invalida", "erro.sessao_invalida")
		return
	}
	prazos := s.prazosAuth(r.Context())
	sess, err := s.banco.AutenticarSessao(r.Context(), token, prazos.Sessao.Inatividade)
	if err != nil {
		if errors.Is(err, db.ErrSessaoInvalida) {
			s.apagarCookieSessao(w, r)
			erroT(w, r, http.StatusUnauthorized, "sessao_invalida", "erro.sessao_invalida")
			return
		}
		s.log.Error("autenticar sessão", "erro", err)
		erroT(w, r, http.StatusInternalServerError, "erro_interno", "erro.interno")
		return
	}
	u, err := s.banco.ObterUsuario(r.Context(), sess.UserID)
	if err != nil && !errors.Is(err, db.ErrNaoEncontrado) {
		s.log.Error("obter usuário da sessão", "erro", err)
		erroT(w, r, http.StatusInternalServerError, "erro_interno", "erro.interno")
		return
	}
	if err != nil || !u.Ativo {
		// Sessão órfã ou de usuário desativado: encerra e derruba o cookie.
		_ = s.banco.RevogarSessao(r.Context(), sess.ID, 0)
		s.apagarCookieSessao(w, r)
		erroT(w, r, http.StatusUnauthorized, "sessao_invalida", "erro.sessao_invalida")
		return
	}
	resp, err := s.respostaAutenticado(r.Context(), u, prazos.JWT)
	if err != nil {
		s.log.Error("emitir token na renovação", "erro", err)
		erroT(w, r, http.StatusInternalServerError, "erro_interno", "erro.interno")
		return
	}
	s.gravarCookieSessao(w, r, token, prazos.Sessao.Inatividade)
	responderJSON(w, http.StatusOK, resp)
}

// handleAuthLogout revoga a sessão do cookie (se houver) e apaga o cookie.
// Idempotente: sem cookie, ou com sessão desconhecida, responde 204 do mesmo
// jeito — para o cliente, sair sempre dá certo.
func (s *Servidor) handleAuthLogout(w http.ResponseWriter, r *http.Request) {
	if token := tokenSessaoDaRequisicao(r); token != "" {
		if err := s.banco.RevogarSessaoPorToken(r.Context(), token); err != nil && !errors.Is(err, db.ErrNaoEncontrado) {
			s.log.Error("revogar sessão no logout", "erro", err)
			erroT(w, r, http.StatusInternalServerError, "erro_interno", "erro.interno")
			return
		}
	}
	s.apagarCookieSessao(w, r)
	w.WriteHeader(http.StatusNoContent)
}

// revogarSessoesDoUsuario derruba as sessões do usuário (todas, ou todas menos
// `exceto`), registrando falha em log — best-effort: a ação principal (troca de
// senha, desativação) já foi persistida quando isto roda.
func (s *Servidor) revogarSessoesDoUsuario(r *http.Request, userID, exceto int64) {
	if _, err := s.banco.RevogarSessoesDoUsuario(r.Context(), userID, exceto); err != nil {
		s.log.Error("revogar sessões do usuário", "usuario", userID, "erro", err)
	}
}
