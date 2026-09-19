package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/marcos14/praxis-autonomous/internal/auth"
)

// TestStreamEncerraQuandoTokenVence: um SSE aberto com um JWT curto fecha
// sozinho quando o token vence, avisando com o evento token_expirado.
func TestStreamEncerraQuandoTokenVence(t *testing.T) {
	srv := Novo(Opcoes{Banco: abrirBancoTemp(t)})
	srv.intervaloPollEventos = 10 * time.Millisecond
	root, _ := autenticarComCookie(t, srv, "/api/v1/auth/setup", corpoSetupRoot, http.StatusCreated)

	// JWT de ~2s (o exp é em segundos inteiros: vence entre 1s e 2s).
	secret, err := srv.segredoJWT(context.Background())
	if err != nil {
		t.Fatalf("segredo: %v", err)
	}
	curto, err := auth.Assinar(root.Usuario.ID, 2*time.Second, secret)
	if err != nil {
		t.Fatalf("assinar: %v", err)
	}

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	ctx, cancelar := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelar()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/api/v1/events?token="+curto, nil)
	inicio := time.Now()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET events: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, quero 200", resp.StatusCode)
	}
	// ReadAll só volta quando o SERVIDOR fecha o stream.
	corpo, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ler stream: %v", err)
	}
	if dur := time.Since(inicio); dur > 5*time.Second {
		t.Fatalf("stream demorou %v para encerrar; deveria fechar com o token (~2s)", dur)
	}
	if !strings.HasSuffix(strings.TrimSpace(string(corpo)), "event: token_expirado\ndata: {}") {
		t.Fatalf("stream deveria terminar com o evento token_expirado:\n%s", corpo)
	}
}

func TestContextoDoStreamSegueAExpiracaoDaCredencial(t *testing.T) {
	// token de API / bootstrap: sem prazo.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/events", nil)
	req = req.WithContext(context.WithValue(req.Context(), chaveCtxPrincipal, &principal{viaToken: true}))
	ctx, cancelar := contextoDoStream(req)
	defer cancelar()
	if _, tem := ctx.Deadline(); tem {
		t.Fatal("credencial sem expiração não deveria impor prazo ao stream")
	}

	// JWT: o prazo do stream é o exp do token.
	exp := time.Now().Add(time.Hour).Truncate(time.Second)
	req = httptest.NewRequest(http.MethodGet, "/api/v1/events", nil)
	req = req.WithContext(context.WithValue(req.Context(), chaveCtxPrincipal, &principal{userID: 1, expiraEm: exp}))
	ctx2, cancelar2 := contextoDoStream(req)
	defer cancelar2()
	prazo, tem := ctx2.Deadline()
	if !tem || !prazo.Equal(exp) {
		t.Fatalf("prazo do stream = %v (%v), quero %v", prazo, tem, exp)
	}

	// avisarTokenExpirado só fala quando foi o prazo que encerrou (não o cliente).
	rec := httptest.NewRecorder()
	ctxVencido, cancelarVencido := context.WithDeadline(req.Context(), time.Now().Add(-time.Second))
	defer cancelarVencido()
	avisarTokenExpirado(ctxVencido, req, rec, rec)
	if !strings.Contains(rec.Body.String(), "event: token_expirado") {
		t.Fatalf("prazo vencido deveria emitir token_expirado: %q", rec.Body.String())
	}
	rec = httptest.NewRecorder()
	ctxCliente, cancelarCliente := context.WithCancel(req.Context())
	cancelarCliente()
	avisarTokenExpirado(ctxCliente, req, rec, rec)
	if rec.Body.Len() != 0 {
		t.Fatalf("cancelamento comum não deveria emitir nada: %q", rec.Body.String())
	}
}
