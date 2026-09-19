package api

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

// fazerReqSessao executa uma requisição com JWT (opcional) e cookie de sessão
// (opcional) e devolve o recorder.
func fazerReqSessao(t *testing.T, srv *Servidor, metodo, caminho, jwt string, cookie *http.Cookie, corpo any) *httptest.ResponseRecorder {
	t.Helper()
	body := bytes.NewBuffer(nil)
	if corpo != nil {
		b, _ := json.Marshal(corpo)
		body = bytes.NewBuffer(b)
	}
	req := httptest.NewRequest(metodo, caminho, body)
	if jwt != "" {
		req.Header.Set("Authorization", "Bearer "+jwt)
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

// cookieSessao extrai o cookie da sessão de uma resposta (nil se não veio).
func cookieSessao(t *testing.T, rec *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, c := range rec.Result().Cookies() {
		if c.Name == nomeCookieSessao {
			return c
		}
	}
	return nil
}

// autenticarComCookie faz setup ou login e devolve a resposta e o cookie gravado.
func autenticarComCookie(t *testing.T, srv *Servidor, caminho string, corpo map[string]any, status int) (respAuth, *http.Cookie) {
	t.Helper()
	rec := fazerReq(t, srv, http.MethodPost, caminho, corpo)
	if rec.Code != status {
		t.Fatalf("%s: status %d, quero %d (corpo=%q)", caminho, rec.Code, status, rec.Body.String())
	}
	var resp respAuth
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decodificar %s: %v", caminho, err)
	}
	c := cookieSessao(t, rec)
	if c == nil {
		t.Fatalf("%s não gravou o cookie %s", caminho, nomeCookieSessao)
	}
	return resp, c
}

// refresh chama POST /auth/refresh com o cookie e devolve o recorder.
func refresh(t *testing.T, srv *Servidor, c *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	return fazerReqSessao(t, srv, http.MethodPost, "/api/v1/auth/refresh", "", c, nil)
}

var (
	corpoSetupRoot = map[string]any{"nome": "Root", "email": "root@x.com", "senha": "senha-forte-123"}
	corpoLoginRoot = map[string]any{"email": "root@x.com", "senha": "senha-forte-123"}
)

// criarUsuarioComum cria, pelo admin, um usuário sem papéis e devolve o id.
func criarUsuarioComum(t *testing.T, srv *Servidor, admin, email string) int64 {
	t.Helper()
	rec := fazerReqToken(t, srv, http.MethodPost, "/api/v1/users", admin, map[string]any{
		"nome": "Comum", "email": email, "senha": "senha-forte-123", "ativo": true, "papeis": []int64{},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("criar usuário: status %d (corpo=%q)", rec.Code, rec.Body.String())
	}
	var u struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &u)
	return u.ID
}

func TestSetupELoginAbremSessaoComCookie(t *testing.T) {
	banco := abrirBancoTemp(t)
	srv := Novo(Opcoes{Banco: banco})

	resp, c := autenticarComCookie(t, srv, "/api/v1/auth/setup", corpoSetupRoot, http.StatusCreated)
	if !c.HttpOnly || c.SameSite != http.SameSiteStrictMode || c.Path != caminhoCookieSessao {
		t.Fatalf("atributos do cookie: %+v", c)
	}
	if c.Secure {
		t.Fatal("sem TLS nem proxy confiável o cookie não deve ser Secure")
	}
	if c.MaxAge != 30*24*3600 {
		t.Fatalf("MaxAge = %d, quero 30 dias", c.MaxAge)
	}
	if c.Value == "" || c.Value == resp.Token {
		t.Fatal("o cookie deve carregar o token opaco da sessão, não o JWT")
	}
	sessoes, err := banco.ListarSessoesDoUsuario(context.Background(), resp.Usuario.ID)
	if err != nil || len(sessoes) != 1 {
		t.Fatalf("sessões após setup = %d (%v), quero 1", len(sessoes), err)
	}

	_, c2 := autenticarComCookie(t, srv, "/api/v1/auth/login", corpoLoginRoot, http.StatusOK)
	if c2.Value == c.Value {
		t.Fatal("cada login deve abrir uma sessão nova")
	}
	if sessoes, _ = banco.ListarSessoesDoUsuario(context.Background(), resp.Usuario.ID); len(sessoes) != 2 {
		t.Fatalf("sessões após login = %d, quero 2", len(sessoes))
	}
}

func TestRefreshRenovaTokenPeloCookie(t *testing.T) {
	srv := Novo(Opcoes{Banco: abrirBancoTemp(t)})
	resp, c := autenticarComCookie(t, srv, "/api/v1/auth/setup", corpoSetupRoot, http.StatusCreated)

	recRefresh := refresh(t, srv, c)
	if recRefresh.Code != http.StatusOK {
		t.Fatalf("refresh: status %d, quero 200 (corpo=%q)", recRefresh.Code, recRefresh.Body.String())
	}
	var renovado respAuth
	if err := json.Unmarshal(recRefresh.Body.Bytes(), &renovado); err != nil {
		t.Fatalf("decodificar: %v", err)
	}
	if renovado.Token == "" || renovado.ExpiraEm == "" || renovado.Usuario.ID != resp.Usuario.ID {
		t.Fatalf("resposta do refresh incompleta: %+v", renovado)
	}
	if !contem(renovado.Usuario.Permissoes, "*") {
		t.Fatalf("permissões do refresh = %v, quero admin", renovado.Usuario.Permissoes)
	}
	// o JWT renovado autentica…
	if rec := fazerReqToken(t, srv, http.MethodGet, "/api/v1/auth/me", renovado.Token, nil); rec.Code != http.StatusOK {
		t.Fatalf("token renovado: status %d, quero 200", rec.Code)
	}
	// …e o cookie é reenviado (mesmo token da sessão, prazo renovado no navegador).
	if c2 := cookieSessao(t, recRefresh); c2 == nil || c2.Value != c.Value || c2.MaxAge <= 0 {
		t.Fatalf("refresh deveria reenviar o cookie da sessão: %+v", c2)
	}

	// sem cookie → 401 sessao_invalida (sem apagar nada).
	rec := refresh(t, srv, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("refresh sem cookie: status %d, quero 401", rec.Code)
	}
	var e struct {
		Erro struct{ Codigo string } `json:"erro"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &e)
	if e.Erro.Codigo != "sessao_invalida" {
		t.Fatalf("código = %q, quero sessao_invalida", e.Erro.Codigo)
	}

	// cookie lixo → 401 e o cookie é apagado.
	rec = refresh(t, srv, &http.Cookie{Name: nomeCookieSessao, Value: "lixo"})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("refresh com cookie inválido: status %d, quero 401", rec.Code)
	}
	if apagado := cookieSessao(t, rec); apagado == nil || apagado.MaxAge >= 0 {
		t.Fatalf("cookie inválido deveria ser apagado: %+v", apagado)
	}
}

func TestRefreshSessaoExpirada(t *testing.T) {
	banco := abrirBancoTemp(t)
	srv := Novo(Opcoes{Banco: banco})
	_, c := autenticarComCookie(t, srv, "/api/v1/auth/setup", corpoSetupRoot, http.StatusCreated)

	passado := time.Now().UTC().Add(-time.Minute).Format("2006-01-02T15:04:05.000Z")
	if _, err := banco.Escritor.Exec(`UPDATE sessoes SET expira_em = ?`, passado); err != nil {
		t.Fatalf("expirar sessão: %v", err)
	}
	rec := refresh(t, srv, c)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("refresh de sessão expirada: status %d, quero 401", rec.Code)
	}
	if apagado := cookieSessao(t, rec); apagado == nil || apagado.MaxAge >= 0 {
		t.Fatalf("cookie de sessão expirada deveria ser apagado: %+v", apagado)
	}
}

func TestLogoutRevogaSessaoEApagaCookie(t *testing.T) {
	banco := abrirBancoTemp(t)
	srv := Novo(Opcoes{Banco: banco})
	resp, c := autenticarComCookie(t, srv, "/api/v1/auth/setup", corpoSetupRoot, http.StatusCreated)

	rec := fazerReqSessao(t, srv, http.MethodPost, "/api/v1/auth/logout", "", c, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("logout: status %d, quero 204 (corpo=%q)", rec.Code, rec.Body.String())
	}
	if apagado := cookieSessao(t, rec); apagado == nil || apagado.MaxAge >= 0 {
		t.Fatalf("logout deveria apagar o cookie: %+v", apagado)
	}
	if rec := refresh(t, srv, c); rec.Code != http.StatusUnauthorized {
		t.Fatalf("refresh após logout: status %d, quero 401", rec.Code)
	}
	if sessoes, _ := banco.ListarSessoesDoUsuario(context.Background(), resp.Usuario.ID); len(sessoes) != 0 {
		t.Fatalf("sessões ativas após logout = %d, quero 0", len(sessoes))
	}
	// idempotente: de novo com o mesmo cookie, e sem cookie nenhum.
	if rec := fazerReqSessao(t, srv, http.MethodPost, "/api/v1/auth/logout", "", c, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("logout repetido: status %d, quero 204", rec.Code)
	}
	if rec := fazerReqSessao(t, srv, http.MethodPost, "/api/v1/auth/logout", "", nil, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("logout sem cookie: status %d, quero 204", rec.Code)
	}
}

func TestRefreshUsuarioDesativado(t *testing.T) {
	banco := abrirBancoTemp(t)
	srv := Novo(Opcoes{Banco: banco})
	admin, _ := autenticarComCookie(t, srv, "/api/v1/auth/setup", corpoSetupRoot, http.StatusCreated)
	uid := criarUsuarioComum(t, srv, admin.Token, "comum@x.com")
	_, cUser := autenticarComCookie(t, srv, "/api/v1/auth/login",
		map[string]any{"email": "comum@x.com", "senha": "senha-forte-123"}, http.StatusOK)
	if rec := refresh(t, srv, cUser); rec.Code != http.StatusOK {
		t.Fatalf("refresh antes de desativar: status %d, quero 200", rec.Code)
	}

	rec := fazerReqToken(t, srv, http.MethodPut, "/api/v1/users/"+strconv.FormatInt(uid, 10), admin.Token,
		map[string]any{"nome": "Comum", "email": "comum@x.com", "ativo": false, "papeis": []int64{}})
	if rec.Code != http.StatusOK {
		t.Fatalf("desativar: status %d (corpo=%q)", rec.Code, rec.Body.String())
	}
	if rec := refresh(t, srv, cUser); rec.Code != http.StatusUnauthorized {
		t.Fatalf("refresh de usuário desativado: status %d, quero 401", rec.Code)
	}
	if sessoes, _ := banco.ListarSessoesDoUsuario(context.Background(), uid); len(sessoes) != 0 {
		t.Fatalf("sessões ativas do desativado = %d, quero 0", len(sessoes))
	}
}

func TestCookieSecureComTLSOuProxyConfiavel(t *testing.T) {
	casos := []struct {
		nome   string
		proxy  bool
		tls    bool
		xfp    string
		secure bool
	}{
		{"http puro", false, false, "", false},
		{"tls direto no servidor", false, true, "", true},
		{"x-forwarded-proto https sem proxy confiável", false, false, "https", false},
		{"x-forwarded-proto https com proxy confiável", true, false, "https", true},
		{"proxy confiável mas x-forwarded-proto http", true, false, "http", false},
	}
	for _, tc := range casos {
		t.Run(tc.nome, func(t *testing.T) {
			srv := Novo(Opcoes{Banco: abrirBancoTemp(t), ProxyConfiavel: tc.proxy})
			b, _ := json.Marshal(corpoSetupRoot)
			req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/setup", bytes.NewReader(b))
			if tc.tls {
				req.TLS = &tls.ConnectionState{}
			}
			if tc.xfp != "" {
				req.Header.Set("X-Forwarded-Proto", tc.xfp)
			}
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)
			if rec.Code != http.StatusCreated {
				t.Fatalf("setup: status %d (corpo=%q)", rec.Code, rec.Body.String())
			}
			c := cookieSessao(t, rec)
			if c == nil {
				t.Fatal("sem cookie")
			}
			if c.Secure != tc.secure {
				t.Fatalf("Secure = %v, quero %v", c.Secure, tc.secure)
			}
		})
	}
}

func TestIPDaRequisicaoEUserAgentNaSessao(t *testing.T) {
	semProxy := Novo(Opcoes{})
	comProxy := Novo(Opcoes{ProxyConfiavel: true})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "203.0.113.7:4444"
	if ip := semProxy.ipDaRequisicao(req); ip != "203.0.113.7" {
		t.Fatalf("ip sem proxy = %q, quero 203.0.113.7", ip)
	}
	req.Header.Set("X-Forwarded-For", " 198.51.100.9 , 10.0.0.1")
	if ip := semProxy.ipDaRequisicao(req); ip != "203.0.113.7" {
		t.Fatalf("sem proxy confiável o X-Forwarded-For deve ser ignorado: %q", ip)
	}
	if ip := comProxy.ipDaRequisicao(req); ip != "198.51.100.9" {
		t.Fatalf("ip com proxy = %q, quero 198.51.100.9 (primeiro da cadeia)", ip)
	}

	// a sessão registra IP e user-agent (diagnóstico da lista de sessões).
	banco := abrirBancoTemp(t)
	srv := Novo(Opcoes{Banco: banco})
	b, _ := json.Marshal(corpoSetupRoot)
	req = httptest.NewRequest(http.MethodPost, "/api/v1/auth/setup", bytes.NewReader(b))
	req.RemoteAddr = "203.0.113.7:4444"
	req.Header.Set("User-Agent", "Navegador/1.0")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	var resp respAuth
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	sessoes, err := banco.ListarSessoesDoUsuario(context.Background(), resp.Usuario.ID)
	if err != nil || len(sessoes) != 1 {
		t.Fatalf("sessões = %d (%v)", len(sessoes), err)
	}
	if sessoes[0].IP != "203.0.113.7" || sessoes[0].UserAgent != "Navegador/1.0" {
		t.Fatalf("sessão = ip %q ua %q", sessoes[0].IP, sessoes[0].UserAgent)
	}
}

func TestTrocarSenhaRevogaOutrasSessoes(t *testing.T) {
	srv := Novo(Opcoes{Banco: abrirBancoTemp(t)})
	resp1, c1 := autenticarComCookie(t, srv, "/api/v1/auth/setup", corpoSetupRoot, http.StatusCreated)
	_, c2 := autenticarComCookie(t, srv, "/api/v1/auth/login", corpoLoginRoot, http.StatusOK)

	rec := fazerReqSessao(t, srv, http.MethodPut, "/api/v1/auth/senha", resp1.Token, c1,
		map[string]any{"atual": "senha-forte-123", "nova": "outra-senha-456"})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("trocar senha: status %d, quero 204 (corpo=%q)", rec.Code, rec.Body.String())
	}
	if rec := refresh(t, srv, c1); rec.Code != http.StatusOK {
		t.Fatalf("a sessão que trocou a senha deveria continuar: status %d", rec.Code)
	}
	if rec := refresh(t, srv, c2); rec.Code != http.StatusUnauthorized {
		t.Fatalf("a outra sessão deveria cair: status %d, quero 401", rec.Code)
	}
}

func TestResetSenhaPeloAdminRevogaSessoes(t *testing.T) {
	srv := Novo(Opcoes{Banco: abrirBancoTemp(t)})
	admin, _ := autenticarComCookie(t, srv, "/api/v1/auth/setup", corpoSetupRoot, http.StatusCreated)
	uid := criarUsuarioComum(t, srv, admin.Token, "comum@x.com")
	_, cUser := autenticarComCookie(t, srv, "/api/v1/auth/login",
		map[string]any{"email": "comum@x.com", "senha": "senha-forte-123"}, http.StatusOK)

	rec := fazerReqToken(t, srv, http.MethodPut, "/api/v1/users/"+strconv.FormatInt(uid, 10)+"/senha", admin.Token,
		map[string]any{"nova": "nova-senha-789"})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("reset: status %d, quero 204 (corpo=%q)", rec.Code, rec.Body.String())
	}
	if rec := refresh(t, srv, cUser); rec.Code != http.StatusUnauthorized {
		t.Fatalf("sessão após reset pelo admin: status %d, quero 401", rec.Code)
	}
	autenticarComCookie(t, srv, "/api/v1/auth/login",
		map[string]any{"email": "comum@x.com", "senha": "nova-senha-789"}, http.StatusOK)
}

func TestUsuarioComumGereAPropriaConta(t *testing.T) {
	srv := Novo(Opcoes{Banco: abrirBancoTemp(t)})
	admin, _ := autenticarComCookie(t, srv, "/api/v1/auth/setup", corpoSetupRoot, http.StatusCreated)
	criarUsuarioComum(t, srv, admin.Token, "comum@x.com")
	comum, cUser := autenticarComCookie(t, srv, "/api/v1/auth/login",
		map[string]any{"email": "comum@x.com", "senha": "senha-forte-123"}, http.StatusOK)

	// Sem nenhum papel, ainda assim gere a própria conta (antes caía em 403).
	rec := fazerReqToken(t, srv, http.MethodPut, "/api/v1/auth/idioma", comum.Token, map[string]any{"idioma": "en"})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("idioma: status %d, quero 204 (corpo=%q)", rec.Code, rec.Body.String())
	}
	rec = fazerReqSessao(t, srv, http.MethodPut, "/api/v1/auth/senha", comum.Token, cUser,
		map[string]any{"atual": "senha-forte-123", "nova": "minha-nova-senha"})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("senha: status %d, quero 204 (corpo=%q)", rec.Code, rec.Body.String())
	}
	// …mas continua sem gerir os outros.
	if rec := fazerReqToken(t, srv, http.MethodGet, "/api/v1/users", comum.Token, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("usuário comum listando usuários: status %d, quero 403", rec.Code)
	}
}
