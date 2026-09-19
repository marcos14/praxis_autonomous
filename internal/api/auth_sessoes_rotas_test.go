package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/marcos14/praxis-autonomous/internal/db"
)

// listarSessoes chama GET /auth/sessoes com JWT e cookie (opcional).
func listarSessoes(t *testing.T, srv *Servidor, jwt string, c *http.Cookie) []db.Sessao {
	t.Helper()
	rec := fazerReqSessao(t, srv, http.MethodGet, "/api/v1/auth/sessoes", jwt, c, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("listar sessões: status %d (corpo=%q)", rec.Code, rec.Body.String())
	}
	var lista []db.Sessao
	if err := json.Unmarshal(rec.Body.Bytes(), &lista); err != nil {
		t.Fatalf("decodificar sessões: %v", err)
	}
	return lista
}

func TestListarEEncerrarSessoes(t *testing.T) {
	srv := Novo(Opcoes{Banco: abrirBancoTemp(t)})
	root, c1 := autenticarComCookie(t, srv, "/api/v1/auth/setup", corpoSetupRoot, http.StatusCreated)
	_, c2 := autenticarComCookie(t, srv, "/api/v1/auth/login", corpoLoginRoot, http.StatusOK)
	_, c3 := autenticarComCookie(t, srv, "/api/v1/auth/login", corpoLoginRoot, http.StatusOK)

	lista := listarSessoes(t, srv, root.Token, c1)
	if len(lista) != 3 {
		t.Fatalf("sessões = %d, quero 3", len(lista))
	}
	var atualID, idC2 int64
	atuais := 0
	for _, s := range lista {
		if s.Atual {
			atuais++
			atualID = s.ID
		}
		if s.Token != "" {
			t.Fatalf("listagem não deve expor o token: %+v", s)
		}
	}
	if atuais != 1 {
		t.Fatalf("sessões marcadas como atual = %d, quero exatamente 1", atuais)
	}
	// As outras duas foram criadas depois da atual (c2 antes de c3): c2 é a de menor id entre elas.
	for _, s := range lista {
		if s.ID != atualID && (idC2 == 0 || s.ID < idC2) {
			idC2 = s.ID
		}
	}

	// encerra c2 pelo id → 204; c2 cai, c3 segue.
	rec := fazerReqSessao(t, srv, http.MethodDelete, "/api/v1/auth/sessoes/"+strconv.FormatInt(idC2, 10), root.Token, c1, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("encerrar sessão: status %d, quero 204 (corpo=%q)", rec.Code, rec.Body.String())
	}
	if rec := refresh(t, srv, c2); rec.Code != http.StatusUnauthorized {
		t.Fatalf("sessão encerrada ainda renova: status %d", rec.Code)
	}
	if rec := refresh(t, srv, c3); rec.Code != http.StatusOK {
		t.Fatalf("sessão não encerrada deveria renovar: status %d", rec.Code)
	}
	// id inexistente → 404.
	rec = fazerReqSessao(t, srv, http.MethodDelete, "/api/v1/auth/sessoes/9999", root.Token, c1, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("encerrar inexistente: status %d, quero 404", rec.Code)
	}

	// sem cookie na listagem, nenhuma sessão é marcada como atual.
	for _, s := range listarSessoes(t, srv, root.Token, nil) {
		if s.Atual {
			t.Fatalf("sem cookie nenhuma sessão deveria ser a atual: %+v", s)
		}
	}

	// encerrar as outras (com o cookie de c1): só c3 cai.
	rec = fazerReqSessao(t, srv, http.MethodDelete, "/api/v1/auth/sessoes", root.Token, c1, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("encerrar outras: status %d, quero 200 (corpo=%q)", rec.Code, rec.Body.String())
	}
	var resp struct {
		Revogadas int64 `json:"revogadas"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Revogadas != 1 {
		t.Fatalf("revogadas = %d, quero 1", resp.Revogadas)
	}
	if rec := refresh(t, srv, c1); rec.Code != http.StatusOK {
		t.Fatalf("a sessão atual deveria sobreviver: status %d", rec.Code)
	}
	if rec := refresh(t, srv, c3); rec.Code != http.StatusUnauthorized {
		t.Fatalf("c3 deveria ter caído: status %d", rec.Code)
	}
	if lista := listarSessoes(t, srv, root.Token, c1); len(lista) != 1 || !lista[0].Atual {
		t.Fatalf("após encerrar as outras: %+v, quero só a atual", lista)
	}
}

func TestSessoesNaoAlcancamOutroUsuarioNemTokenDeAPI(t *testing.T) {
	banco := abrirBancoTemp(t)
	srv := Novo(Opcoes{Banco: banco})
	admin, cAdmin := autenticarComCookie(t, srv, "/api/v1/auth/setup", corpoSetupRoot, http.StatusCreated)
	criarUsuarioComum(t, srv, admin.Token, "comum@x.com")
	comum, cComum := autenticarComCookie(t, srv, "/api/v1/auth/login",
		map[string]any{"email": "comum@x.com", "senha": "senha-forte-123"}, http.StatusOK)

	var idAdmin int64
	for _, s := range listarSessoes(t, srv, admin.Token, cAdmin) {
		if s.Atual {
			idAdmin = s.ID
		}
	}
	if idAdmin == 0 {
		t.Fatal("sessão atual do admin não identificada")
	}
	// o usuário comum só vê as próprias e não encerra a do admin.
	if lista := listarSessoes(t, srv, comum.Token, cComum); len(lista) != 1 || lista[0].ID == idAdmin {
		t.Fatalf("lista do usuário comum: %+v", lista)
	}
	rec := fazerReqSessao(t, srv, http.MethodDelete, "/api/v1/auth/sessoes/"+strconv.FormatInt(idAdmin, 10), comum.Token, cComum, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("encerrar sessão alheia: status %d, quero 404", rec.Code)
	}
	if rec := refresh(t, srv, cAdmin); rec.Code != http.StatusOK {
		t.Fatalf("sessão do admin deveria continuar: status %d", rec.Code)
	}

	// token de API não tem sessões → 400.
	tok, err := banco.CriarToken(context.Background(), "integração", db.PapelAdmin)
	if err != nil {
		t.Fatalf("criar token: %v", err)
	}
	if rec := fazerReqToken(t, srv, http.MethodGet, "/api/v1/auth/sessoes", tok.Token, nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("token de API listando sessões: status %d, quero 400", rec.Code)
	}
	if rec := fazerReqToken(t, srv, http.MethodDelete, "/api/v1/auth/sessoes", tok.Token, nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("token de API encerrando sessões: status %d, quero 400", rec.Code)
	}
}

// loginDe faz POST /auth/login a partir do IP dado.
func loginDe(t *testing.T, srv *Servidor, ip, email, senha string) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(map[string]any{"email": email, "senha": senha})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(b))
	req.RemoteAddr = ip + ":1234"
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func TestLoginBloqueiaAposMuitasFalhas(t *testing.T) {
	srv := Novo(Opcoes{Banco: abrirBancoTemp(t)})
	setupAdmin(t, srv)

	for i := 0; i < limiteFalhasLogin; i++ {
		if rec := loginDe(t, srv, "203.0.113.1", "root@x.com", "errada"); rec.Code != http.StatusUnauthorized {
			t.Fatalf("falha %d: status %d, quero 401", i+1, rec.Code)
		}
	}
	// a 11ª tentativa é barrada, mesmo com a senha certa.
	rec := loginDe(t, srv, "203.0.113.1", "root@x.com", "senha-forte-123")
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("após o limite: status %d, quero 429 (corpo=%q)", rec.Code, rec.Body.String())
	}
	if ra, err := strconv.Atoi(rec.Header().Get("Retry-After")); err != nil || ra < 1 || ra > int(janelaFalhasLogin.Seconds()) {
		t.Fatalf("Retry-After = %q, quero segundos entre 1 e %d", rec.Header().Get("Retry-After"), int(janelaFalhasLogin.Seconds()))
	}
	var e struct {
		Erro struct{ Codigo string } `json:"erro"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &e)
	if e.Erro.Codigo != "muitas_tentativas" {
		t.Fatalf("código = %q, quero muitas_tentativas", e.Erro.Codigo)
	}

	// passada a janela, o login volta a funcionar.
	srv.limiteLogin.agora = func() time.Time { return time.Now().Add(janelaFalhasLogin + time.Minute) }
	if rec := loginDe(t, srv, "203.0.113.1", "root@x.com", "senha-forte-123"); rec.Code != http.StatusOK {
		t.Fatalf("após a janela: status %d, quero 200 (corpo=%q)", rec.Code, rec.Body.String())
	}
}

func TestLoginLimitePorEmailEPorIP(t *testing.T) {
	srv := Novo(Opcoes{Banco: abrirBancoTemp(t)})
	admin := setupAdmin(t, srv)
	criarUsuarioComum(t, srv, admin, "comum@x.com")

	// 10 falhas para root vindas do IP A.
	for i := 0; i < limiteFalhasLogin; i++ {
		loginDe(t, srv, "203.0.113.1", "root@x.com", "errada")
	}
	// root fica bloqueado de qualquer IP (chave do e-mail)…
	if rec := loginDe(t, srv, "203.0.113.2", "root@x.com", "senha-forte-123"); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("root de outro IP: status %d, quero 429", rec.Code)
	}
	// …o IP A fica bloqueado para qualquer e-mail (chave do IP)…
	if rec := loginDe(t, srv, "203.0.113.1", "comum@x.com", "senha-forte-123"); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("outro e-mail do IP bloqueado: status %d, quero 429", rec.Code)
	}
	// …mas outro usuário, de outro IP, entra normalmente.
	if rec := loginDe(t, srv, "203.0.113.2", "comum@x.com", "senha-forte-123"); rec.Code != http.StatusOK {
		t.Fatalf("outro usuário de outro IP: status %d, quero 200 (corpo=%q)", rec.Code, rec.Body.String())
	}

	// sucesso zera as falhas do e-mail: 9 erros + acerto + 9 erros não bloqueiam
	// (IPs distintos para não esbarrar no limite por IP).
	for i := 0; i < limiteFalhasLogin-1; i++ {
		loginDe(t, srv, "203.0.113.3", "comum@x.com", "errada")
	}
	if rec := loginDe(t, srv, "203.0.113.4", "comum@x.com", "senha-forte-123"); rec.Code != http.StatusOK {
		t.Fatalf("acerto após 9 falhas: status %d, quero 200", rec.Code)
	}
	for i := 0; i < limiteFalhasLogin-1; i++ {
		loginDe(t, srv, "203.0.113.5", "comum@x.com", "errada")
	}
	if rec := loginDe(t, srv, "203.0.113.6", "comum@x.com", "senha-forte-123"); rec.Code != http.StatusOK {
		t.Fatalf("o acerto deveria ter zerado as falhas do e-mail: status %d, quero 200", rec.Code)
	}
}
