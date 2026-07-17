package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"

	"github.com/marcos14/praxis-autonomous/internal/db"
)

// fazerReqToken executa uma requisição com Authorization: Bearer <token> (vazio
// = sem header) e devolve o recorder.
func fazerReqToken(t *testing.T, srv *Servidor, metodo, caminho, token string, corpo any) *httptest.ResponseRecorder {
	t.Helper()
	var body *bytes.Buffer
	if corpo != nil {
		b, _ := json.Marshal(corpo)
		body = bytes.NewBuffer(b)
	} else {
		body = bytes.NewBuffer(nil)
	}
	req := httptest.NewRequest(metodo, caminho, body)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func TestTokenCRUDEValorUmaVez(t *testing.T) {
	srv := Novo(Opcoes{Banco: abrirBancoTemp(t)})

	// cria (sem token = admin local).
	rec := fazerReq(t, srv, http.MethodPost, "/api/v1/tokens",
		map[string]any{"nome": "chamados", "papel": "operador"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("criar token: status %d (corpo=%q)", rec.Code, rec.Body.String())
	}
	var tok db.Token
	if err := json.Unmarshal(rec.Body.Bytes(), &tok); err != nil {
		t.Fatalf("decodificar: %v", err)
	}
	if tok.Token == "" {
		t.Fatal("criação deveria trazer o valor em claro")
	}

	// listagem não vaza o valor.
	rec = fazerReq(t, srv, http.MethodGet, "/api/v1/tokens", nil)
	var lista []db.Token
	_ = json.Unmarshal(rec.Body.Bytes(), &lista)
	if len(lista) != 1 || lista[0].Token != "" {
		t.Fatalf("listagem inesperada: %+v", lista)
	}

	// revoga.
	rec = fazerReq(t, srv, http.MethodDelete, "/api/v1/tokens/"+strconv.FormatInt(tok.ID, 10), nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("revogar: status %d", rec.Code)
	}
}

func TestAuthLeitorBarradoDeEscrita(t *testing.T) {
	banco := abrirBancoTemp(t)
	srv := Novo(Opcoes{Banco: banco})

	leitor, err := banco.CriarToken(context.Background(), "só leitura", db.PapelLeitor)
	if err != nil {
		t.Fatalf("criar token: %v", err)
	}

	// leitor consegue LER.
	rec := fazerReqToken(t, srv, http.MethodGet, "/api/v1/projects", leitor.Token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("leitor GET projects: status %d, quero 200", rec.Code)
	}
	// leitor NÃO consegue ESCREVER (criar projeto).
	rec = fazerReqToken(t, srv, http.MethodPost, "/api/v1/projects", leitor.Token,
		map[string]any{"nome": "x", "pasta": repoGitTemp(t)})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("leitor POST projects: status %d, quero 403 (corpo=%q)", rec.Code, rec.Body.String())
	}
	// leitor NÃO consegue gerir tokens (requer admin).
	rec = fazerReqToken(t, srv, http.MethodGet, "/api/v1/tokens", leitor.Token, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("leitor GET tokens: status %d, quero 403", rec.Code)
	}
}

func TestAuthTokenInvalido(t *testing.T) {
	srv := Novo(Opcoes{Banco: abrirBancoTemp(t)})
	rec := fazerReqToken(t, srv, http.MethodGet, "/api/v1/projects", "token-que-nao-existe", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("token inválido: status %d, quero 401", rec.Code)
	}
}

// analisadorFake registra as demandas disparadas e as transita para
// aguardando_respostas (simula o analista sem harness).
type analisadorFake struct {
	mu       sync.Mutex
	banco    *db.DB
	chamadas []int64
}

func (a *analisadorFake) Disparar(demandaID int64) {
	a.mu.Lock()
	a.chamadas = append(a.chamadas, demandaID)
	a.mu.Unlock()
	dem, err := a.banco.ObterDemanda(context.Background(), demandaID)
	if err == nil {
		dem.Status = db.StatusDemandaAguardandoRespostas
		_, _ = a.banco.AtualizarDemanda(context.Background(), dem)
	}
}

func TestIntakeViaTokenOperadorConduzAteAguardandoRespostas(t *testing.T) {
	banco := abrirBancoTemp(t)
	analista := &analisadorFake{banco: banco}
	srv := Novo(Opcoes{Banco: banco, Intake: analista})

	proj := criarProjetoTeste(t, srv)
	op, err := banco.CriarToken(context.Background(), "chamados", db.PapelOperador)
	if err != nil {
		t.Fatalf("token: %v", err)
	}

	rec := fazerReqToken(t, srv, http.MethodPost, "/api/v1/projects/"+strconv.FormatInt(proj, 10)+"/demands",
		op.Token, map[string]any{"prd": "Como usuário quero X", "origem": "api"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("intake: status %d (corpo=%q)", rec.Code, rec.Body.String())
	}
	dem := decodDemanda(t, rec)

	// o analista foi disparado e conduziu a demanda até aguardando_respostas.
	if len(analista.chamadas) != 1 {
		t.Fatalf("analista disparado %d vezes, quero 1", len(analista.chamadas))
	}
	atual, _ := banco.ObterDemanda(context.Background(), dem.ID)
	if atual.Status != db.StatusDemandaAguardandoRespostas {
		t.Fatalf("status = %q, quero aguardando_respostas", atual.Status)
	}
}
