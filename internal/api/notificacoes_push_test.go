package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/marcos14/praxis-autonomous/internal/db"
)

func TestPushChaveAssinarECancelar(t *testing.T) {
	banco := abrirBancoTemp(t)
	srv := Novo(Opcoes{Banco: banco})
	admin, _ := autenticarComCookie(t, srv, "/api/v1/auth/setup", corpoSetupRoot, http.StatusCreated)
	idComum := criarUsuarioComum(t, srv, admin.Token, "comum@x.com")
	comum := loginToken(t, srv, "comum@x.com", "senha-forte-123")

	// Chave pública VAPID: estável entre chamadas e no formato base64url de 65 bytes.
	rec := fazerReqToken(t, srv, http.MethodGet, "/api/v1/notificacoes/push/chave", comum, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("chave: status %d (corpo=%q)", rec.Code, rec.Body.String())
	}
	var chave struct {
		Chave string `json:"chave"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &chave)
	if len(chave.Chave) != 87 || strings.ContainsAny(chave.Chave, "+/=") {
		t.Fatalf("chave vapid = %q", chave.Chave)
	}
	rec = fazerReqToken(t, srv, http.MethodGet, "/api/v1/notificacoes/push/chave", admin.Token, nil)
	if !strings.Contains(rec.Body.String(), chave.Chave) {
		t.Fatal("a chave deveria ser a mesma para todos os usuários")
	}

	// Assinar: formato PushSubscription.toJSON().
	corpo := map[string]any{
		"endpoint": "https://push.example/abc", "expirationTime": nil,
		"keys": map[string]string{"p256dh": "BP...", "auth": "aa"},
	}
	rec = fazerReqToken(t, srv, http.MethodPost, "/api/v1/notificacoes/push", comum, corpo)
	if rec.Code != http.StatusCreated {
		t.Fatalf("assinar: status %d (corpo=%q)", rec.Code, rec.Body.String())
	}
	var resp respAssinaturaPush
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.ID == 0 || resp.Endpoint != "https://push.example/abc" || strings.Contains(rec.Body.String(), "p256dh") {
		t.Fatalf("resposta = %s", rec.Body.String())
	}
	// Reassinar o mesmo endpoint atualiza em vez de duplicar.
	rec = fazerReqToken(t, srv, http.MethodPost, "/api/v1/notificacoes/push", comum, corpo)
	if rec.Code != http.StatusCreated {
		t.Fatalf("reassinar: status %d", rec.Code)
	}
	if lista, _ := banco.ListarAssinaturasDoUsuario(context.Background(), idComum); len(lista) != 1 || lista[0].UserAgent == "" && false {
		t.Fatalf("assinaturas = %+v", lista)
	}
	// Inválidas: endpoint sem https, sem chaves, campo desconhecido.
	for _, c := range []map[string]any{
		{"endpoint": "http://push.example/x", "keys": map[string]string{"p256dh": "a", "auth": "b"}},
		{"endpoint": "https://push.example/x", "keys": map[string]string{"p256dh": "", "auth": "b"}},
		{"endpoint": "https://push.example/x", "keys": map[string]string{"p256dh": "a", "auth": "b"}, "extra": 1},
	} {
		if rec := fazerReqToken(t, srv, http.MethodPost, "/api/v1/notificacoes/push", comum, c); rec.Code != http.StatusBadRequest {
			t.Fatalf("assinatura inválida %v: status %d", c, rec.Code)
		}
	}

	// Cancelar: só a própria (o admin não consegue apagar a do comum); idempotente.
	if rec := fazerReqToken(t, srv, http.MethodDelete, "/api/v1/notificacoes/push", admin.Token, map[string]string{"endpoint": "https://push.example/abc"}); rec.Code != http.StatusNoContent {
		t.Fatalf("cancelar alheia: status %d", rec.Code)
	}
	if lista, _ := banco.ListarAssinaturasDoUsuario(context.Background(), idComum); len(lista) != 1 {
		t.Fatal("o admin não deveria apagar a assinatura do comum")
	}
	for i := 0; i < 2; i++ {
		if rec := fazerReqToken(t, srv, http.MethodDelete, "/api/v1/notificacoes/push", comum, map[string]string{"endpoint": "https://push.example/abc"}); rec.Code != http.StatusNoContent {
			t.Fatalf("cancelar (%d): status %d", i, rec.Code)
		}
	}
	if lista, _ := banco.ListarAssinaturasDoUsuario(context.Background(), idComum); len(lista) != 0 {
		t.Fatalf("assinaturas após cancelar = %+v", lista)
	}
	if rec := fazerReqToken(t, srv, http.MethodDelete, "/api/v1/notificacoes/push", comum, map[string]string{"endpoint": ""}); rec.Code != http.StatusBadRequest {
		t.Fatalf("cancelar sem endpoint: status %d", rec.Code)
	}

	// Token de API → 400 em todas.
	tok, _ := banco.CriarToken(context.Background(), "integração", db.PapelAdmin)
	for _, c := range []struct{ metodo, caminho string }{
		{http.MethodGet, "/api/v1/notificacoes/push/chave"},
		{http.MethodPost, "/api/v1/notificacoes/push"},
		{http.MethodDelete, "/api/v1/notificacoes/push"},
		{http.MethodGet, "/api/v1/auth/preferencias"},
		{http.MethodPut, "/api/v1/auth/preferencias"},
	} {
		if rec := fazerReqToken(t, srv, c.metodo, c.caminho, tok.Token, nil); rec.Code != http.StatusBadRequest {
			t.Fatalf("token de API em %s %s: status %d, quero 400", c.metodo, c.caminho, rec.Code)
		}
	}
}

func TestPreferenciasDeNotificacao(t *testing.T) {
	banco := abrirBancoTemp(t)
	srv := Novo(Opcoes{Banco: banco})
	admin, _ := autenticarComCookie(t, srv, "/api/v1/auth/setup", corpoSetupRoot, http.StatusCreated)
	criarUsuarioComum(t, srv, admin.Token, "comum@x.com")
	comum := loginToken(t, srv, "comum@x.com", "senha-forte-123")

	obter := func() respPreferencias {
		t.Helper()
		rec := fazerReqToken(t, srv, http.MethodGet, "/api/v1/auth/preferencias", comum, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("obter: status %d (corpo=%q)", rec.Code, rec.Body.String())
		}
		var p respPreferencias
		if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
			t.Fatal(err)
		}
		return p
	}
	// Padrão: navegador e push ligados, tipos do catálogo padrão, sem assinaturas.
	p := obter()
	if !p.Navegador || !p.Push || !p.Eventos["consulta_respondida"] || p.Eventos["fase_iniciada"] || p.Assinaturas != 0 || len(p.PadraoTipos) == 0 {
		t.Fatalf("padrão = %+v", p)
	}

	// Gravar: push desligado, um tipo padrão desligado e um extra ligado.
	rec := fazerReqToken(t, srv, http.MethodPut, "/api/v1/auth/preferencias", comum, map[string]any{
		"navegador": true, "push": false,
		"eventos": map[string]bool{"consulta_respondida": false, "fase_iniciada": true},
	})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("definir: status %d (corpo=%q)", rec.Code, rec.Body.String())
	}
	p = obter()
	if p.Push || !p.Navegador || p.Eventos["consulta_respondida"] || !p.Eventos["fase_iniciada"] || !p.Eventos["consulta_falhou"] {
		t.Fatalf("após gravar = %+v", p)
	}
	// Tipo fora do catálogo → 400 e nada muda.
	rec = fazerReqToken(t, srv, http.MethodPut, "/api/v1/auth/preferencias", comum, map[string]any{
		"navegador": true, "push": true, "eventos": map[string]bool{"inexistente": true},
	})
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "inexistente") {
		t.Fatalf("tipo desconhecido: status %d corpo %q", rec.Code, rec.Body.String())
	}
	if p = obter(); p.Push {
		t.Fatal("preferências não deveriam ter mudado")
	}
	// Corpo só com os flags: eventos volta ao padrão.
	rec = fazerReqToken(t, srv, http.MethodPut, "/api/v1/auth/preferencias", comum, map[string]any{"navegador": false, "push": true})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("definir sem eventos: status %d", rec.Code)
	}
	if p = obter(); p.Navegador || !p.Push || !p.Eventos["consulta_respondida"] || p.Eventos["fase_iniciada"] {
		t.Fatalf("sem eventos = %+v", p)
	}
	// Uma assinatura entra no contador.
	fazerReqToken(t, srv, http.MethodPost, "/api/v1/notificacoes/push", comum, map[string]any{
		"endpoint": "https://push.example/1", "keys": map[string]string{"p256dh": "a", "auth": "b"}})
	if p = obter(); p.Assinaturas != 1 {
		t.Fatalf("assinaturas = %d", p.Assinaturas)
	}
	// O admin tem as próprias preferências (padrão), independentes.
	rec = fazerReqToken(t, srv, http.MethodGet, "/api/v1/auth/preferencias", admin.Token, nil)
	if !strings.Contains(rec.Body.String(), `"push":true`) {
		t.Fatalf("preferências do admin = %s", rec.Body.String())
	}
}
