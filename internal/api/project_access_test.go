package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

func TestACLDeProjetosNaAPI(t *testing.T) {
	srv := Novo(Opcoes{Banco: abrirBancoTemp(t)})

	// Dois projetos criados pelo admin de teste.
	projA := criarProjetoNomeado(t, srv, "Restrito")
	projB := criarProjetoNomeado(t, srv, "Aberto")

	admin := tokenAdminTeste(t, srv)

	// Demanda em cada projeto (como admin).
	rec := fazerReqToken(t, srv, http.MethodPost,
		"/api/v1/projects/"+strconv.FormatInt(projA, 10)+"/demands", admin,
		map[string]any{"prd": "demanda do restrito", "origem": "api"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("demanda projA: status %d (corpo=%q)", rec.Code, rec.Body.String())
	}
	demA := decodDemanda(t, rec)
	rec = fazerReqToken(t, srv, http.MethodPost,
		"/api/v1/projects/"+strconv.FormatInt(projB, 10)+"/demands", admin,
		map[string]any{"prd": "demanda do aberto", "origem": "api"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("demanda projB: status %d", rec.Code)
	}

	// Usuário comum, sem papéis (só visualização implícita).
	rec = fazerReqToken(t, srv, http.MethodPost, "/api/v1/users", admin, map[string]any{
		"nome": "Vis", "email": "vis@x.com", "senha": "senha-forte-123", "ativo": true,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("criar usuário: status %d (corpo=%q)", rec.Code, rec.Body.String())
	}
	vis := loginToken(t, srv, "vis@x.com", "senha-forte-123")

	// Antes da ACL: o usuário comum vê os dois projetos.
	rec = fazerReqToken(t, srv, http.MethodGet, "/api/v1/projects", vis, nil)
	if n := contarLista(t, rec); n != 2 {
		t.Fatalf("projetos antes da ACL = %d, quero 2", n)
	}

	// Admin restringe projA ao próprio admin (usuário 1).
	rec = fazerReqToken(t, srv, http.MethodPut,
		"/api/v1/projects/"+strconv.FormatInt(projA, 10)+"/access", admin,
		map[string]any{"usuarios": []int64{1}, "grupos": []int64{}})
	if rec.Code != http.StatusOK {
		t.Fatalf("definir acesso: status %d (corpo=%q)", rec.Code, rec.Body.String())
	}
	var acl respAcesso
	_ = json.Unmarshal(rec.Body.Bytes(), &acl)
	if !acl.Restrito || len(acl.Usuarios) != 1 {
		t.Fatalf("acl = %+v, quero restrito com 1 usuário", acl.AcessoProjeto)
	}
	if len(acl.Disponiveis.Usuarios) == 0 {
		t.Fatalf("acl sem opções disponíveis para a UI")
	}

	// Usuário comum: lista só projB; projA some (lista, detalhe e demandas).
	rec = fazerReqToken(t, srv, http.MethodGet, "/api/v1/projects", vis, nil)
	if n := contarLista(t, rec); n != 1 {
		t.Fatalf("projetos após ACL = %d, quero 1 (corpo=%q)", n, rec.Body.String())
	}
	rec = fazerReqToken(t, srv, http.MethodGet,
		"/api/v1/projects/"+strconv.FormatInt(projA, 10), vis, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("obter projA restrito: status %d, quero 404", rec.Code)
	}
	rec = fazerReqToken(t, srv, http.MethodGet, "/api/v1/demands", vis, nil)
	if n := contarLista(t, rec); n != 1 {
		t.Fatalf("demandas após ACL = %d, quero 1", n)
	}
	rec = fazerReqToken(t, srv, http.MethodGet, "/api/v1/board", vis, nil)
	if n := contarLista(t, rec); n != 1 {
		t.Fatalf("board após ACL = %d, quero 1", n)
	}
	rec = fazerReqToken(t, srv, http.MethodGet,
		"/api/v1/demands/"+strconv.FormatInt(demA.ID, 10), vis, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("obter demanda de projA restrito: status %d, quero 404", rec.Code)
	}

	// ACL só é lida/escrita por quem tem projetos.gerir.
	rec = fazerReqToken(t, srv, http.MethodGet,
		"/api/v1/projects/"+strconv.FormatInt(projB, 10)+"/access", vis, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("usuário comum ler ACL: status %d, quero 403", rec.Code)
	}

	// Admin continua vendo tudo.
	rec = fazerReqToken(t, srv, http.MethodGet, "/api/v1/projects", admin, nil)
	if n := contarLista(t, rec); n != 2 {
		t.Fatalf("projetos do admin = %d, quero 2", n)
	}
	rec = fazerReqToken(t, srv, http.MethodGet,
		"/api/v1/demands/"+strconv.FormatInt(demA.ID, 10), admin, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin obter demanda de projA: status %d, quero 200", rec.Code)
	}

	// Reabrir o projeto (listas vazias) devolve a visão a todos.
	rec = fazerReqToken(t, srv, http.MethodPut,
		"/api/v1/projects/"+strconv.FormatInt(projA, 10)+"/access", admin,
		map[string]any{"usuarios": []int64{}, "grupos": []int64{}})
	if rec.Code != http.StatusOK {
		t.Fatalf("limpar acesso: status %d", rec.Code)
	}
	rec = fazerReqToken(t, srv, http.MethodGet, "/api/v1/projects", vis, nil)
	if n := contarLista(t, rec); n != 2 {
		t.Fatalf("projetos após reabrir = %d, quero 2", n)
	}
}

// contarLista decodifica um corpo JSON de array e devolve o tamanho (falha o
// teste se o status não for 200 ou o corpo não for um array).
func contarLista(t *testing.T, rec *httptest.ResponseRecorder) int {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("listagem: status %d (corpo=%q)", rec.Code, rec.Body.String())
	}
	var itens []json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &itens); err != nil {
		t.Fatalf("decodificar lista: %v (corpo=%q)", err, rec.Body.String())
	}
	return len(itens)
}
