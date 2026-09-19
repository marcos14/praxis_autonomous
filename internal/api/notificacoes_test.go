package api

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/marcos14/praxis-autonomous/internal/db"
)

// criarNotificacaoTeste grava uma notificação direto no banco (o despachante
// é testado em internal/notify).
func criarNotificacaoTeste(t *testing.T, banco *db.DB, userID int64, titulo, rota string) db.Notificacao {
	t.Helper()
	n, err := banco.CriarNotificacao(context.Background(), db.Notificacao{UserID: userID, Tipo: "consulta_respondida", Titulo: titulo, Rota: rota})
	if err != nil {
		t.Fatalf("criar notificação: %v", err)
	}
	return n
}

func listarNotificacoes(t *testing.T, srv *Servidor, token, query string) respListaNotificacoes {
	t.Helper()
	rec := fazerReqToken(t, srv, http.MethodGet, "/api/v1/notificacoes"+query, token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("listar notificações%s: status %d (corpo=%q)", query, rec.Code, rec.Body.String())
	}
	var resp respListaNotificacoes
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decodificar: %v", err)
	}
	return resp
}

func TestNotificacoesListarEMarcarLidasSoDoProprioUsuario(t *testing.T) {
	banco := abrirBancoTemp(t)
	srv := Novo(Opcoes{Banco: banco})
	admin, _ := autenticarComCookie(t, srv, "/api/v1/auth/setup", corpoSetupRoot, http.StatusCreated)
	idComum := criarUsuarioComum(t, srv, admin.Token, "comum@x.com")
	comum := loginToken(t, srv, "comum@x.com", "senha-forte-123")

	n1 := criarNotificacaoTeste(t, banco, idComum, "consulta 1", "#consultas/1")
	n2 := criarNotificacaoTeste(t, banco, idComum, "consulta 2", "#consultas/2")
	n3 := criarNotificacaoTeste(t, banco, idComum, "consulta 3", "#consultas/3")
	doAdmin := criarNotificacaoTeste(t, banco, admin.Usuario.ID, "do admin", "#demandas/9")

	// Lista do usuário comum: só as dele, mais recentes primeiro, com o contador.
	lista := listarNotificacoes(t, srv, comum, "")
	if len(lista.Itens) != 3 || lista.NaoLidas != 3 || lista.Itens[0].ID != n3.ID || lista.Itens[2].ID != n1.ID {
		t.Fatalf("lista = %+v", lista)
	}
	if lista.Itens[0].Rota != "#consultas/3" || lista.Itens[0].UserID != idComum {
		t.Fatalf("item = %+v", lista.Itens[0])
	}
	if l := listarNotificacoes(t, srv, comum, "?limite=2"); len(l.Itens) != 2 || l.NaoLidas != 3 {
		t.Fatalf("limite: %+v", l)
	}
	// O admin (curinga) também vê só as próprias: a caixa de entrada é pessoal.
	if l := listarNotificacoes(t, srv, admin.Token, ""); len(l.Itens) != 1 || l.Itens[0].ID != doAdmin.ID {
		t.Fatalf("lista do admin = %+v", l)
	}

	// Marcar uma: a de outro usuário é 404; a própria, 204 e idempotente.
	if rec := fazerReqToken(t, srv, http.MethodPost, "/api/v1/notificacoes/"+strconv.FormatInt(doAdmin.ID, 10)+"/lida", comum, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("marcar alheia: status %d, quero 404", rec.Code)
	}
	if rec := fazerReqToken(t, srv, http.MethodPost, "/api/v1/notificacoes/99999/lida", comum, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("marcar inexistente: status %d, quero 404", rec.Code)
	}
	for i := 0; i < 2; i++ {
		if rec := fazerReqToken(t, srv, http.MethodPost, "/api/v1/notificacoes/"+strconv.FormatInt(n2.ID, 10)+"/lida", comum, nil); rec.Code != http.StatusNoContent {
			t.Fatalf("marcar própria (%d): status %d (corpo=%q)", i, rec.Code, rec.Body.String())
		}
	}
	lista = listarNotificacoes(t, srv, comum, "?nao_lidas=1")
	if len(lista.Itens) != 2 || lista.NaoLidas != 2 || lista.Itens[0].ID != n3.ID || lista.Itens[1].ID != n1.ID {
		t.Fatalf("não lidas após marcar: %+v", lista)
	}
	if l := listarNotificacoes(t, srv, comum, ""); len(l.Itens) != 3 || l.Itens[1].LidaEm == "" {
		t.Fatalf("lista completa deveria manter a lida com lida_em: %+v", l)
	}

	// Marcar todas: devolve quantas; a do admin não é tocada.
	rec := fazerReqToken(t, srv, http.MethodPost, "/api/v1/notificacoes/lidas", comum, nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"marcadas":2`) {
		t.Fatalf("marcar todas: status %d corpo %q", rec.Code, rec.Body.String())
	}
	if l := listarNotificacoes(t, srv, comum, ""); l.NaoLidas != 0 {
		t.Fatalf("ainda há não lidas: %+v", l)
	}
	if l := listarNotificacoes(t, srv, admin.Token, ""); l.NaoLidas != 1 {
		t.Fatalf("a do admin deveria seguir não lida: %+v", l)
	}

	// Sem autenticação → 401; token de API → 400 (não tem caixa de entrada).
	if rec := fazerReqToken(t, srv, http.MethodGet, "/api/v1/notificacoes", "", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("sem token: status %d", rec.Code)
	}
	tok, err := banco.CriarToken(context.Background(), "integração", db.PapelAdmin)
	if err != nil {
		t.Fatalf("criar token: %v", err)
	}
	for _, c := range []struct{ metodo, caminho string }{
		{http.MethodGet, "/api/v1/notificacoes"},
		{http.MethodPost, "/api/v1/notificacoes/lidas"},
		{http.MethodPost, "/api/v1/notificacoes/1/lida"},
		{http.MethodGet, "/api/v1/notificacoes/stream"},
	} {
		if rec := fazerReqToken(t, srv, c.metodo, c.caminho, tok.Token, nil); rec.Code != http.StatusBadRequest {
			t.Fatalf("token de API em %s %s: status %d, quero 400", c.metodo, c.caminho, rec.Code)
		}
	}
}

func TestNotificacoesStreamEntregaSoAsDoUsuario(t *testing.T) {
	banco := abrirBancoTemp(t)
	srv := Novo(Opcoes{Banco: banco})
	srv.intervaloPollEventos = 10 * time.Millisecond
	admin, _ := autenticarComCookie(t, srv, "/api/v1/auth/setup", corpoSetupRoot, http.StatusCreated)
	idComum := criarUsuarioComum(t, srv, admin.Token, "comum@x.com")
	comum := loginToken(t, srv, "comum@x.com", "senha-forte-123")

	antiga := criarNotificacaoTeste(t, banco, idComum, "antiga", "#consultas/1")

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	ctx, cancelar := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelar()

	// Sem `after`: parte da última existente — a antiga não é reenviada.
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/api/v1/notificacoes/stream?token="+comum, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET stream: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("status %d content-type %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	leitor := bufio.NewScanner(resp.Body)
	esperarLinha := func(prefixo string) string {
		t.Helper()
		for leitor.Scan() {
			if l := leitor.Text(); strings.HasPrefix(l, prefixo) {
				return l
			}
		}
		t.Fatalf("stream fechou antes de %q: %v", prefixo, leitor.Err())
		return ""
	}
	esperarLinha(": conectado")

	criarNotificacaoTeste(t, banco, admin.Usuario.ID, "do admin", "#demandas/9") // não deve chegar
	nova := criarNotificacaoTeste(t, banco, idComum, "nova", "#consultas/2")

	esperarLinha("event: notificacao")
	var recebida db.Notificacao
	if err := json.Unmarshal([]byte(strings.TrimPrefix(esperarLinha("data: "), "data: ")), &recebida); err != nil {
		t.Fatalf("decodificar data: %v", err)
	}
	if recebida.ID != nova.ID || recebida.Titulo != "nova" || recebida.UserID != idComum || recebida.Rota != "#consultas/2" {
		t.Fatalf("recebida = %+v (a antiga e a do admin não deveriam chegar)", recebida)
	}
	cancelar()

	// Com `after=0`: o backlog inteiro do usuário chega, em ordem.
	ctx2, cancelar2 := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelar2()
	req2, _ := http.NewRequestWithContext(ctx2, http.MethodGet, ts.URL+"/api/v1/notificacoes/stream?token="+comum+"&after=0", nil)
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatalf("GET stream after=0: %v", err)
	}
	defer resp2.Body.Close()
	leitor = bufio.NewScanner(resp2.Body)
	var ids []int64
	for len(ids) < 2 {
		var n db.Notificacao
		esperarLinha("event: notificacao")
		if err := json.Unmarshal([]byte(strings.TrimPrefix(esperarLinha("data: "), "data: ")), &n); err != nil {
			t.Fatalf("decodificar: %v", err)
		}
		ids = append(ids, n.ID)
	}
	if ids[0] != antiga.ID || ids[1] != nova.ID {
		t.Fatalf("backlog = %v, quero [%d %d]", ids, antiga.ID, nova.ID)
	}
}
