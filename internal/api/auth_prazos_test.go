package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/marcos14/praxis-autonomous/internal/auth"
)

// definirConfigGlobal grava entradas na config global direto no store.
func definirConfigGlobal(t *testing.T, srv *Servidor, entradas map[string]any) {
	t.Helper()
	m := map[string]json.RawMessage{}
	for k, v := range entradas {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("codificar %q: %v", k, err)
		}
		m[k] = b
	}
	if err := srv.banco.DefinirConfigGlobal(context.Background(), m); err != nil {
		t.Fatalf("DefinirConfigGlobal: %v", err)
	}
}

func TestPrazosAuthDefaultsEConfig(t *testing.T) {
	srv := Novo(Opcoes{Banco: abrirBancoTemp(t)})
	ctx := context.Background()

	// sem config: defaults.
	p := srv.prazosAuth(ctx)
	if p.JWT != 60*time.Minute || p.Sessao.Inatividade != 30*24*time.Hour || p.Sessao.Maxima != 90*24*time.Hour {
		t.Fatalf("defaults = %+v", p)
	}

	// config numérica válida.
	definirConfigGlobal(t, srv, map[string]any{
		ChaveSessaoJWTMin: 1, ChaveSessaoInatividadeDias: 2, ChaveSessaoMaximaDias: 3,
	})
	p = srv.prazosAuth(ctx)
	if p.JWT != time.Minute || p.Sessao.Inatividade != 2*24*time.Hour || p.Sessao.Maxima != 3*24*time.Hour {
		t.Fatalf("config = %+v, quero 1min / 2d / 3d", p)
	}

	// valores inválidos caem no default da própria chave; string numérica vale.
	definirConfigGlobal(t, srv, map[string]any{
		ChaveSessaoJWTMin: "abc", ChaveSessaoInatividadeDias: 0, ChaveSessaoMaximaDias: "15",
	})
	p = srv.prazosAuth(ctx)
	if p.JWT != 60*time.Minute || p.Sessao.Inatividade != 30*24*time.Hour || p.Sessao.Maxima != 15*24*time.Hour {
		t.Fatalf("inválidos = %+v, quero 60min / 30d / 15d", p)
	}
	definirConfigGlobal(t, srv, map[string]any{ChaveSessaoJWTMin: -5, ChaveSessaoMaximaDias: true})
	p = srv.prazosAuth(ctx)
	if p.JWT != 60*time.Minute || p.Sessao.Maxima != 90*24*time.Hour {
		t.Fatalf("negativo/bool = %+v, quero defaults", p)
	}

	// sem banco: defaults, sem pânico.
	if p := Novo(Opcoes{}).prazosAuth(ctx); p.JWT != 60*time.Minute {
		t.Fatalf("sem banco = %+v", p)
	}
}

func TestTokenExpiraConformeConfig(t *testing.T) {
	srv := Novo(Opcoes{Banco: abrirBancoTemp(t)})
	definirConfigGlobal(t, srv, map[string]any{ChaveSessaoJWTMin: 1})

	antes := time.Now()
	rec := fazerReq(t, srv, http.MethodPost, "/api/v1/auth/setup", map[string]any{
		"nome": "Root", "email": "root@x.com", "senha": "senha-forte-123",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("setup: status %d (corpo=%q)", rec.Code, rec.Body.String())
	}
	var resp respAuth
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decodificar: %v", err)
	}

	// o exp gravado no token é ~1 min após a emissão…
	secret, err := srv.segredoJWT(context.Background())
	if err != nil {
		t.Fatalf("segredo: %v", err)
	}
	claims, err := auth.ValidarClaims(resp.Token, secret)
	if err != nil {
		t.Fatalf("validar: %v", err)
	}
	if claims.Exp.Before(antes.Add(55*time.Second)) || claims.Exp.After(antes.Add(65*time.Second)) {
		t.Fatalf("exp = %v, quero ~1min após %v", claims.Exp, antes)
	}
	// …a resposta informa o mesmo instante…
	expiraResp, err := time.Parse(time.RFC3339, resp.ExpiraEm)
	if err != nil {
		t.Fatalf("expira_em %q não é RFC3339: %v", resp.ExpiraEm, err)
	}
	if !expiraResp.Equal(claims.Exp) {
		t.Fatalf("expira_em %v ≠ exp do token %v", expiraResp, claims.Exp)
	}
	// …e o principal carrega a expiração da credencial.
	pr, err := srv.principalDeJWT(context.Background(), resp.Token)
	if err != nil {
		t.Fatalf("principalDeJWT: %v", err)
	}
	if !pr.expiraEm.Equal(claims.Exp) {
		t.Fatalf("principal.expiraEm %v ≠ %v", pr.expiraEm, claims.Exp)
	}

	// login relê a config: com 120 min o token vence em ~2h.
	definirConfigGlobal(t, srv, map[string]any{ChaveSessaoJWTMin: 120})
	tok := loginToken(t, srv, "root@x.com", "senha-forte-123")
	claims, err = auth.ValidarClaims(tok, secret)
	if err != nil {
		t.Fatalf("validar (login): %v", err)
	}
	if claims.Exp.Before(time.Now().Add(119 * time.Minute)) {
		t.Fatalf("exp após mudar a config = %v, quero ~2h", claims.Exp)
	}
}

func TestPrincipalDeTokenDeAPINaoExpira(t *testing.T) {
	banco := abrirBancoTemp(t)
	srv := Novo(Opcoes{Banco: banco})
	tok, err := banco.CriarToken(context.Background(), "integração", "leitor")
	if err != nil {
		t.Fatalf("criar token: %v", err)
	}
	pr, err := srv.principalDeToken(context.Background(), tok.Token)
	if err != nil {
		t.Fatalf("principalDeToken: %v", err)
	}
	if !pr.expiraEm.IsZero() {
		t.Fatalf("token de API não deveria ter expiração: %v", pr.expiraEm)
	}
}

func TestSegredoJWTNaoMemoizaErro(t *testing.T) {
	srv := Novo(Opcoes{Banco: abrirBancoTemp(t)})

	// 1ª chamada com contexto já cancelado: a leitura do banco falha.
	cancelado, cancelar := context.WithCancel(context.Background())
	cancelar()
	if _, err := srv.segredoJWT(cancelado); err == nil {
		t.Fatal("com contexto cancelado deveria falhar")
	}
	// 2ª chamada, contexto sadio: resolve — o erro anterior não ficou memoizado.
	secret, err := srv.segredoJWT(context.Background())
	if err != nil {
		t.Fatalf("segundo segredoJWT: %v (o erro da primeira chamada foi memoizado?)", err)
	}
	if len(secret) == 0 {
		t.Fatal("segredo vazio")
	}
	// 3ª chamada devolve o mesmo segredo (memoizado após sucesso).
	deNovo, err := srv.segredoJWT(context.Background())
	if err != nil || string(deNovo) != string(secret) {
		t.Fatalf("segredo mudou entre chamadas: %v", err)
	}
}
