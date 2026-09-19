package api

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/marcos14/praxis-autonomous/internal/auth"
	"github.com/marcos14/praxis-autonomous/internal/db"
	"github.com/marcos14/praxis-autonomous/internal/i18n"
)

// registrarRotasAuth registra as rotas de autenticação de usuários. status/login/
// setup são públicas (o middleware as libera); me/senha exigem estar autenticado.
func (s *Servidor) registrarRotasAuth(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/auth/status", s.handleAuthStatus)
	mux.HandleFunc("POST /api/v1/auth/setup", s.handleAuthSetup)
	mux.HandleFunc("POST /api/v1/auth/login", s.handleAuthLogin)
	mux.HandleFunc("POST /api/v1/auth/refresh", s.handleAuthRefresh)
	mux.HandleFunc("POST /api/v1/auth/logout", s.handleAuthLogout)
	mux.HandleFunc("GET /api/v1/auth/me", s.handleAuthMe)
	mux.HandleFunc("PUT /api/v1/auth/senha", s.handleTrocarSenha)
	mux.HandleFunc("PUT /api/v1/auth/idioma", s.handleDefinirIdioma)
}

// respUsuario é a projeção de um usuário exposta à UI: sem hash, com as
// permissões efetivas já resolvidas (para montar a navegação).
type respUsuario struct {
	ID         int64      `json:"id"`
	Nome       string     `json:"nome"`
	Email      string     `json:"email"`
	Ativo      bool       `json:"ativo"`
	Idioma     string     `json:"idioma"`
	Permissoes []string   `json:"permissoes"`
	Papeis     []db.Papel `json:"papeis"`
}

// respAuth é o corpo de setup/login: o token recém-emitido, quando ele vence
// (RFC 3339 UTC — o cliente agenda a renovação antes disso) e o usuário.
type respAuth struct {
	Token    string      `json:"token"`
	ExpiraEm string      `json:"expira_em"`
	Usuario  respUsuario `json:"usuario"`
}

// permsOrdenadas converte o conjunto de permissões num slice ordenado e estável
// (a UI depende disso; ordena para respostas determinísticas).
func permsOrdenadas(perms map[string]bool) []string {
	out := make([]string, 0, len(perms))
	for p := range perms {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// emitirToken assina um JWT para userID válido por ttl (a validade vem da
// config global, sessao_jwt_min — ver prazosAuth) e devolve o token e quando
// ele vence.
func (s *Servidor) emitirToken(ctx context.Context, userID int64, ttl time.Duration) (string, time.Time, error) {
	secret, err := s.segredoJWT(ctx)
	if err != nil {
		return "", time.Time{}, err
	}
	token, claims, err := auth.AssinarClaims(userID, ttl, secret)
	if err != nil {
		return "", time.Time{}, err
	}
	return token, claims.Exp, nil
}

// respostaAutenticado monta respAuth (token + usuário com permissões) para um
// usuário recém-autenticado/criado ou com a sessão renovada.
func (s *Servidor) respostaAutenticado(ctx context.Context, u db.Usuario, ttl time.Duration) (respAuth, error) {
	token, expira, err := s.emitirToken(ctx, u.ID, ttl)
	if err != nil {
		return respAuth{}, err
	}
	perms, err := s.banco.PermissoesDoUsuario(ctx, u.ID)
	if err != nil {
		return respAuth{}, err
	}
	return respAuth{
		Token:    token,
		ExpiraEm: expira.UTC().Format(time.RFC3339),
		Usuario: respUsuario{
			ID: u.ID, Nome: u.Nome, Email: u.Email, Ativo: u.Ativo, Idioma: u.Idioma,
			Permissoes: permsOrdenadas(perms), Papeis: u.Papeis,
		},
	}, nil
}

// handleAuthStatus informa se ainda é preciso criar o primeiro admin (nenhum
// usuário cadastrado). A UI usa para decidir entre a tela de setup e a de login.
func (s *Servidor) handleAuthStatus(w http.ResponseWriter, r *http.Request) {
	n, err := s.banco.ContarUsuarios(r.Context())
	if err != nil {
		s.log.Error("contar usuários", "erro", err)
		erroT(w, r, http.StatusInternalServerError, "erro_interno", "erro.interno")
		return
	}
	responderJSON(w, http.StatusOK, map[string]bool{"setup_necessario": n == 0})
}

// reqSetup/reqLogin são os corpos de setup e login.
type reqSetup struct {
	Nome  string `json:"nome"`
	Email string `json:"email"`
	Senha string `json:"senha"`
}

type reqLogin struct {
	Email string `json:"email"`
	Senha string `json:"senha"`
}

// handleAuthSetup cria o PRIMEIRO usuário (admin) quando ainda não há nenhum. Se
// já existir usuário → 409 (o bootstrap acabou). Vincula o papel de sistema admin.
func (s *Servidor) handleAuthSetup(w http.ResponseWriter, r *http.Request) {
	var req reqSetup
	if !decodificarCorpo(w, r, &req) {
		return
	}
	n, err := s.banco.ContarUsuarios(r.Context())
	if err != nil {
		s.log.Error("contar usuários", "erro", err)
		erroT(w, r, http.StatusInternalServerError, "erro_interno", "erro.interno")
		return
	}
	if n > 0 {
		erroT(w, r, http.StatusConflict, "setup_concluido", "erro.setup_concluido")
		return
	}
	adminID, err := s.banco.IDPapelPorNome(r.Context(), "admin")
	if err != nil {
		s.log.Error("obter papel admin", "erro", err)
		erroT(w, r, http.StatusInternalServerError, "erro_interno", "erro.interno")
		return
	}
	u, err := s.banco.CriarUsuario(r.Context(), req.Nome, req.Email, req.Senha, []int64{adminID})
	if err != nil {
		s.responderErroUsuario(w, r, err)
		return
	}
	s.responderLogin(w, r, u, http.StatusCreated)
}

// responderLogin conclui setup/login: emite o JWT, abre a sessão persistida
// (cookie) e responde status com respAuth. Falha em qualquer passo → 500.
func (s *Servidor) responderLogin(w http.ResponseWriter, r *http.Request, u db.Usuario, status int) {
	prazos := s.prazosAuth(r.Context())
	resp, err := s.respostaAutenticado(r.Context(), u, prazos.JWT)
	if err != nil {
		s.log.Error("emitir token no login", "erro", err)
		erroT(w, r, http.StatusInternalServerError, "erro_interno", "erro.interno")
		return
	}
	if err := s.abrirSessao(w, r, u.ID, prazos); err != nil {
		s.log.Error("abrir sessão no login", "erro", err)
		erroT(w, r, http.StatusInternalServerError, "erro_interno", "erro.interno")
		return
	}
	responderJSON(w, status, resp)
}

// handleAuthLogin valida e-mail + senha e devolve token + usuário. Falha → 401.
func (s *Servidor) handleAuthLogin(w http.ResponseWriter, r *http.Request) {
	var req reqLogin
	if !decodificarCorpo(w, r, &req) {
		return
	}
	u, err := s.banco.AutenticarUsuario(r.Context(), req.Email, req.Senha)
	if err != nil {
		if errors.Is(err, db.ErrCredenciais) {
			erroT(w, r, http.StatusUnauthorized, "credenciais_invalidas", "erro.credenciais_invalidas")
			return
		}
		s.log.Error("autenticar usuário", "erro", err)
		erroT(w, r, http.StatusInternalServerError, "erro_interno", "erro.interno")
		return
	}
	s.responderLogin(w, r, u, http.StatusOK)
}

// handleAuthMe devolve o usuário atual (do JWT) com suas permissões. Para
// principais de token de API (sem usuário), devolve id 0 com as permissões do
// papel do token.
func (s *Servidor) handleAuthMe(w http.ResponseWriter, r *http.Request) {
	pr := principalDaRequisicao(r)
	if pr.userID > 0 {
		u, err := s.banco.ObterUsuario(r.Context(), pr.userID)
		if err != nil {
			s.responderErroUsuario(w, r, err)
			return
		}
		responderJSON(w, http.StatusOK, respUsuario{
			ID: u.ID, Nome: u.Nome, Email: u.Email, Ativo: u.Ativo, Idioma: u.Idioma,
			Permissoes: permsOrdenadas(pr.permissoes), Papeis: u.Papeis,
		})
		return
	}
	responderJSON(w, http.StatusOK, respUsuario{
		Nome: "integração (token de API)", Ativo: true,
		Permissoes: permsOrdenadas(pr.permissoes), Papeis: []db.Papel{},
	})
}

// reqIdioma é o corpo de PUT /auth/idioma.
type reqIdioma struct {
	Idioma string `json:"idioma"`
}

// handleDefinirIdioma grava o idioma preferido da UI do próprio usuário logado.
// Vazio limpa a preferência (volta a valer navegador/instância); qualquer outro
// valor é normalizado para um idioma suportado ou rejeitado. Principais de token
// de API não têm preferência de idioma → 400.
func (s *Servidor) handleDefinirIdioma(w http.ResponseWriter, r *http.Request) {
	pr := principalDaRequisicao(r)
	if pr.userID == 0 {
		erroT(w, r, http.StatusBadRequest, "invalido", "erro.credencial_sem_idioma")
		return
	}
	var req reqIdioma
	if !decodificarCorpo(w, r, &req) {
		return
	}
	idioma := ""
	if strings.TrimSpace(req.Idioma) != "" {
		if idioma = i18n.Normalizar(req.Idioma); idioma == "" {
			erroT(w, r, http.StatusBadRequest, "idioma_invalido", "erro.idioma_invalido",
				"idiomas", strings.Join(i18n.Suportados, ", "))
			return
		}
	}
	if err := s.banco.DefinirIdiomaUsuario(r.Context(), pr.userID, idioma); err != nil {
		s.responderErroUsuario(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// reqTrocarSenha é o corpo de PUT /auth/senha.
type reqTrocarSenha struct {
	Atual string `json:"atual"`
	Nova  string `json:"nova"`
}

// handleTrocarSenha troca a própria senha do usuário logado (exige a senha atual).
// Principais de token de API não têm senha → 400.
func (s *Servidor) handleTrocarSenha(w http.ResponseWriter, r *http.Request) {
	pr := principalDaRequisicao(r)
	if pr.userID == 0 {
		erroT(w, r, http.StatusBadRequest, "invalido", "erro.credencial_sem_senha")
		return
	}
	var req reqTrocarSenha
	if !decodificarCorpo(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Nova) == "" {
		erroT(w, r, http.StatusBadRequest, "invalido", "erro.senha_vazia")
		return
	}
	// Reautentica com a senha atual antes de trocar.
	if _, err := s.banco.AutenticarUsuario(r.Context(), pr.email, req.Atual); err != nil {
		erroT(w, r, http.StatusForbidden, "senha_atual_invalida", "erro.senha_atual_invalida")
		return
	}
	if err := s.banco.DefinirSenha(r.Context(), pr.userID, req.Nova); err != nil {
		s.responderErroUsuario(w, r, err)
		return
	}
	// Senha nova derruba as OUTRAS sessões (um dispositivo comprometido perde o
	// acesso); a sessão que fez a troca continua.
	s.revogarSessoesDoUsuario(r, pr.userID, s.sessaoAtualID(r, s.prazosAuth(r.Context()).Sessao.Inatividade))
	w.WriteHeader(http.StatusNoContent)
}
