package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
)

// setupAdmin cria o primeiro admin via /auth/setup e devolve o token JWT emitido.
func setupAdmin(t *testing.T, srv *Servidor) string {
	t.Helper()
	rec := fazerReq(t, srv, http.MethodPost, "/api/v1/auth/setup", map[string]any{
		"nome": "Root", "email": "root@x.com", "senha": "senha-forte-123",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("setup: status %d (corpo=%q)", rec.Code, rec.Body.String())
	}
	var resp respAuth
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decodificar setup: %v", err)
	}
	if resp.Token == "" {
		t.Fatal("setup não devolveu token")
	}
	return resp.Token
}

// loginToken loga e devolve o token do usuário.
func loginToken(t *testing.T, srv *Servidor, email, senha string) string {
	t.Helper()
	rec := fazerReq(t, srv, http.MethodPost, "/api/v1/auth/login", map[string]any{"email": email, "senha": senha})
	if rec.Code != http.StatusOK {
		t.Fatalf("login: status %d (corpo=%q)", rec.Code, rec.Body.String())
	}
	var resp respAuth
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	return resp.Token
}

func TestSetupPrimeiroAdminEBloqueiaSegundo(t *testing.T) {
	srv := Novo(Opcoes{Banco: abrirBancoTemp(t)})

	// status inicial: setup necessário.
	rec := fazerReq(t, srv, http.MethodGet, "/api/v1/auth/status", nil)
	var st map[string]bool
	_ = json.Unmarshal(rec.Body.Bytes(), &st)
	if !st["setup_necessario"] {
		t.Fatalf("status inicial: %+v, quero setup_necessario=true", st)
	}

	tok := setupAdmin(t, srv)

	// segundo setup → 409.
	rec = fazerReq(t, srv, http.MethodPost, "/api/v1/auth/setup", map[string]any{
		"nome": "Outro", "email": "outro@x.com", "senha": "senha-forte-123",
	})
	if rec.Code != http.StatusConflict {
		t.Fatalf("segundo setup: status %d, quero 409", rec.Code)
	}

	// /auth/me com o token do admin traz o curinga.
	rec = fazerReqToken(t, srv, http.MethodGet, "/api/v1/auth/me", tok, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("me: status %d", rec.Code)
	}
	var me respUsuario
	_ = json.Unmarshal(rec.Body.Bytes(), &me)
	if !contem(me.Permissoes, "*") {
		t.Fatalf("admin sem curinga: %v", me.Permissoes)
	}
}

func TestModoProtegidoAposPrimeiroUsuario(t *testing.T) {
	srv := Novo(Opcoes{Banco: abrirBancoTemp(t)})
	setupAdmin(t, srv)

	// Agora que há usuário, requisição SEM credencial → 401.
	rec := fazerReq(t, srv, http.MethodGet, "/api/v1/projects", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("sem token após setup: status %d, quero 401", rec.Code)
	}
	// JWT malformado → 401.
	rec = fazerReqToken(t, srv, http.MethodGet, "/api/v1/projects", "a.b.c", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("jwt inválido: status %d, quero 401", rec.Code)
	}
}

func TestPermissoesGranulares(t *testing.T) {
	srv := Novo(Opcoes{Banco: abrirBancoTemp(t)})

	// Projeto criado no modo bootstrap (ainda sem usuários).
	proj := criarProjetoTeste(t, srv)

	admin := setupAdmin(t, srv)

	// Papel "criador" só com demandas.criar.
	rec := fazerReqToken(t, srv, http.MethodPost, "/api/v1/roles", admin, map[string]any{
		"nome": "criador", "permissoes": []string{"demandas.criar"},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("criar papel: status %d (corpo=%q)", rec.Code, rec.Body.String())
	}
	var papel struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &papel)

	// Usuário com o papel criador.
	rec = fazerReqToken(t, srv, http.MethodPost, "/api/v1/users", admin, map[string]any{
		"nome": "Cria", "email": "cria@x.com", "senha": "senha-forte-123", "ativo": true,
		"papeis": []int64{papel.ID},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("criar usuário: status %d (corpo=%q)", rec.Code, rec.Body.String())
	}
	criador := loginToken(t, srv, "cria@x.com", "senha-forte-123")

	rotaDemandas := "/api/v1/projects/" + strconv.FormatInt(proj, 10) + "/demands"

	// PERMITIDO: criar demanda.
	rec = fazerReqToken(t, srv, http.MethodPost, rotaDemandas, criador,
		map[string]any{"prd": "Como usuário quero X", "origem": "api"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("criador criar demanda: status %d (corpo=%q)", rec.Code, rec.Body.String())
	}
	dem := decodDemanda(t, rec)

	// PERMITIDO: visualizar (board).
	rec = fazerReqToken(t, srv, http.MethodGet, "/api/v1/board", criador, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("criador ver board: status %d, quero 200", rec.Code)
	}

	// NEGADO: gerir projetos (criar projeto).
	rec = fazerReqToken(t, srv, http.MethodPost, "/api/v1/projects", criador,
		map[string]any{"nome": "x", "pasta": repoGitTemp(t)})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("criador criar projeto: status %d, quero 403", rec.Code)
	}

	// NEGADO: integrar (requer integracao.gerir).
	rec = fazerReqToken(t, srv, http.MethodPost,
		"/api/v1/demands/"+strconv.FormatInt(dem.ID, 10)+"/actions", criador,
		map[string]any{"acao": "integrar"})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("criador integrar: status %d, quero 403 (corpo=%q)", rec.Code, rec.Body.String())
	}

	// NEGADO: gerir usuários.
	rec = fazerReqToken(t, srv, http.MethodGet, "/api/v1/users", criador, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("criador listar usuários: status %d, quero 403", rec.Code)
	}
}

func TestAuthViaQueryToken(t *testing.T) {
	// EventSource (SSE) não envia headers, então o token vai por ?token=. Aqui
	// exercitamos o mesmo caminho de autenticação numa rota GET comum.
	srv := Novo(Opcoes{Banco: abrirBancoTemp(t)})
	tok := setupAdmin(t, srv)

	// Sem credencial (já há usuário) → 401.
	rec := fazerReq(t, srv, http.MethodGet, "/api/v1/projects", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("sem credencial: status %d, quero 401", rec.Code)
	}
	// Token na query → 200.
	rec = fazerReq(t, srv, http.MethodGet, "/api/v1/projects?token="+tok, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("token na query: status %d, quero 200 (corpo=%q)", rec.Code, rec.Body.String())
	}
	// Token inválido na query → 401.
	rec = fazerReq(t, srv, http.MethodGet, "/api/v1/projects?token=a.b.c", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("token inválido na query: status %d, quero 401", rec.Code)
	}
}

func TestAdminNaoPodeSeAutoExcluir(t *testing.T) {
	srv := Novo(Opcoes{Banco: abrirBancoTemp(t)})
	admin := setupAdmin(t, srv)
	// o admin é o usuário 1.
	rec := fazerReqToken(t, srv, http.MethodDelete, "/api/v1/users/1", admin, nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("auto-exclusão: status %d, quero 409 (corpo=%q)", rec.Code, rec.Body.String())
	}
}

func TestTrocarPropriaSenha(t *testing.T) {
	srv := Novo(Opcoes{Banco: abrirBancoTemp(t)})
	admin := setupAdmin(t, srv)

	// senha atual errada → 403.
	rec := fazerReqToken(t, srv, http.MethodPut, "/api/v1/auth/senha", admin,
		map[string]any{"atual": "errada", "nova": "nova-senha-123"})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("senha atual errada: status %d, quero 403", rec.Code)
	}
	// correta → 204 e o login passa a valer com a nova.
	rec = fazerReqToken(t, srv, http.MethodPut, "/api/v1/auth/senha", admin,
		map[string]any{"atual": "senha-forte-123", "nova": "nova-senha-123"})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("trocar senha: status %d, quero 204 (corpo=%q)", rec.Code, rec.Body.String())
	}
	if tok := loginToken(t, srv, "root@x.com", "nova-senha-123"); tok == "" {
		t.Fatal("login com a nova senha falhou")
	}
}

// contem informa se s contém v.
func contem(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
