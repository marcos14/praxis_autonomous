package api

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/marcos14/praxis-autonomous/internal/auth"
	"github.com/marcos14/praxis-autonomous/internal/db"
)

// ideFake implementa IDEWeb para os testes de handler.
type ideFake struct {
	estado      string
	detalhe     string
	alvo        *url.URL
	token       string
	solicitados int
	tocados     int
}

func (f *ideFake) Solicitar() bool { f.solicitados++; return f.estado == "parado" }
func (f *ideFake) Estado() (string, string) {
	return f.estado, f.detalhe
}
func (f *ideFake) Alvo() (*url.URL, string, bool) {
	if f.estado != "pronto" {
		return nil, "", false
	}
	return f.alvo, f.token, true
}
func (f *ideFake) Tocar() { f.tocados++ }

// demandaEditavel cria (em modo bootstrap) projeto + demanda e a põe no estado
// dado, com worktree em um diretório real quando worktree=true.
func demandaEditavel(t *testing.T, srv *Servidor, banco *db.DB, status string, worktree bool) int64 {
	t.Helper()
	proj := criarProjetoTeste(t, srv)
	rec := fazerReq(t, srv, http.MethodPost, "/api/v1/projects/"+strconv.FormatInt(proj, 10)+"/demands",
		map[string]any{"titulo": "Ajuste manual", "plano_md": "# plano",
			"fases": []map[string]any{{"codigo": "1", "titulo": "Fase"}}})
	if rec.Code != http.StatusCreated {
		t.Fatalf("criar demanda: status %d (corpo=%q)", rec.Code, rec.Body.String())
	}
	dem := decodDemanda(t, rec)

	d, err := banco.ObterDemanda(context.Background(), dem.ID)
	if err != nil {
		t.Fatalf("obter demanda: %v", err)
	}
	d.Status = status
	if worktree {
		d.WorktreePath = t.TempDir()
	}
	if _, err := banco.AtualizarDemanda(context.Background(), d); err != nil {
		t.Fatalf("atualizar demanda: %v", err)
	}
	return dem.ID
}

func TestSessaoIDEDesligado(t *testing.T) {
	banco := abrirBancoTemp(t)
	srv := Novo(Opcoes{Banco: banco}) // sem IDE
	id := demandaEditavel(t, srv, banco, db.StatusDemandaPausada, true)
	tok := setupAdmin(t, srv)

	rec := fazerReqToken(t, srv, http.MethodPost, "/api/v1/ide/sessao", tok, map[string]any{"demand_id": id})
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, quero 503 (corpo=%q)", rec.Code, rec.Body.String())
	}
}

func TestSessaoIDEGateDeEstado(t *testing.T) {
	banco := abrirBancoTemp(t)
	fake := &ideFake{estado: "pronto"}
	srv := Novo(Opcoes{Banco: banco, IDE: fake})
	// Demanda executando: edição manual bloqueada (pausar antes).
	id := demandaEditavel(t, srv, banco, db.StatusDemandaExecutando, true)
	tok := setupAdmin(t, srv)

	rec := fazerReqToken(t, srv, http.MethodPost, "/api/v1/ide/sessao", tok, map[string]any{"demand_id": id})
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "estado_invalido") {
		t.Fatalf("executando: status=%d corpo=%q, quero 409 estado_invalido", rec.Code, rec.Body.String())
	}
	if fake.solicitados != 0 {
		t.Fatal("não deveria solicitar o IDE com o gate fechado")
	}
}

func TestSessaoIDESemWorktree(t *testing.T) {
	banco := abrirBancoTemp(t)
	srv := Novo(Opcoes{Banco: banco, IDE: &ideFake{estado: "pronto"}})
	id := demandaEditavel(t, srv, banco, db.StatusDemandaPausada, false)
	tok := setupAdmin(t, srv)

	rec := fazerReqToken(t, srv, http.MethodPost, "/api/v1/ide/sessao", tok, map[string]any{"demand_id": id})
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "sem_worktree") {
		t.Fatalf("status=%d corpo=%q, quero 409 sem_worktree", rec.Code, rec.Body.String())
	}
}

func TestURLIDEParaWorktree(t *testing.T) {
	casos := []struct{ worktree, quer string }{
		// Windows: o "/" inicial é obrigatório — sem ele o workbench faz
		// URI.parse("C:/…") e o "C:" vira scheme, deixando a raiz sem resolver.
		{`C:\Users\marco\wt`, "/ide/?folder=%2FC%3A%2FUsers%2Fmarco%2Fwt"},
		{`/home/user/wt`, "/ide/?folder=%2Fhome%2Fuser%2Fwt"},
	}
	for _, c := range casos {
		if got := urlIDEParaWorktree(c.worktree); got != c.quer {
			t.Errorf("urlIDEParaWorktree(%q) = %q, quero %q", c.worktree, got, c.quer)
		}
	}
}

func TestSessaoIDEProntaEmiteCookieEURL(t *testing.T) {
	banco := abrirBancoTemp(t)
	fake := &ideFake{estado: "pronto"}
	srv := Novo(Opcoes{Banco: banco, IDE: fake})
	id := demandaEditavel(t, srv, banco, db.StatusDemandaPausada, true)
	tok := setupAdmin(t, srv)

	rec := fazerReqToken(t, srv, http.MethodPost, "/api/v1/ide/sessao", tok, map[string]any{"demand_id": id})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, quero 200 (corpo=%q)", rec.Code, rec.Body.String())
	}
	var resp respSessaoIDE
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decodificar: %v", err)
	}
	if resp.Estado != "pronto" || !strings.HasPrefix(resp.URL, "/ide/?folder=") {
		t.Fatalf("resp = %+v, quero estado pronto e url /ide/?folder=…", resp)
	}
	cookie := rec.Header().Get("Set-Cookie")
	if !strings.Contains(cookie, cookieSessaoIDE+"=") || !strings.Contains(cookie, "Path=/ide/") ||
		!strings.Contains(cookie, "HttpOnly") {
		t.Fatalf("Set-Cookie = %q, quero cookie HttpOnly restrito a /ide/", cookie)
	}
	if fake.tocados == 0 {
		t.Fatal("sessão pronta deveria Tocar o IDE (adiar ociosidade)")
	}
}

func TestSessaoIDEPreparandoDevolve202(t *testing.T) {
	banco := abrirBancoTemp(t)
	srv := Novo(Opcoes{Banco: banco, IDE: &ideFake{estado: "preparando"}})
	id := demandaEditavel(t, srv, banco, db.StatusDemandaPausada, true)
	tok := setupAdmin(t, srv)

	rec := fazerReqToken(t, srv, http.MethodPost, "/api/v1/ide/sessao", tok, map[string]any{"demand_id": id})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, quero 202 (corpo=%q)", rec.Code, rec.Body.String())
	}
}

func TestSessaoIDEExigePermissao(t *testing.T) {
	banco := abrirBancoTemp(t)
	srv := Novo(Opcoes{Banco: banco, IDE: &ideFake{estado: "pronto"}})
	id := demandaEditavel(t, srv, banco, db.StatusDemandaPausada, true)
	tokAdmin := setupAdmin(t, srv)

	// Token de API papel leitor: sem codigo.editar → 403 já no middleware.
	rec := fazerReqToken(t, srv, http.MethodPost, "/api/v1/tokens", tokAdmin,
		map[string]any{"nome": "leitor", "papel": "leitor"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("criar token: status %d (corpo=%q)", rec.Code, rec.Body.String())
	}
	var tk struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &tk)
	if tk.Token == "" {
		t.Fatalf("token de API sem valor em claro (corpo=%q)", rec.Body.String())
	}

	rec = fazerReqToken(t, srv, http.MethodPost, "/api/v1/ide/sessao", tk.Token, map[string]any{"demand_id": id})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, quero 403 (corpo=%q)", rec.Code, rec.Body.String())
	}
}

func TestProxyIDESemCookie(t *testing.T) {
	banco := abrirBancoTemp(t)
	srv := Novo(Opcoes{Banco: banco, IDE: &ideFake{estado: "pronto"}})

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/ide/", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, quero 401", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Fatalf("Content-Type = %q, quero text/html (página para o navegador)", ct)
	}
}

func TestProxyIDEComCookieProxiaEInjetaToken(t *testing.T) {
	banco := abrirBancoTemp(t)

	// Backend fake no lugar do serve-web: ecoa caminho, Host e cookies recebidos.
	var caminho, tkn, sessaoVazou, tknVelho, hostRecebido string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		caminho = r.URL.Path
		hostRecebido = r.Host
		if c, err := r.Cookie("vscode-tkn"); err == nil {
			tkn = c.Value
		}
		if c, err := r.Cookie(cookieSessaoIDE); err == nil {
			sessaoVazou = c.Value
		}
		tknVelho = "" // qualquer segundo vscode-tkn indicaria o cookie velho não filtrado
		for _, c := range r.Cookies() {
			if c.Name == "vscode-tkn" && c.Value != "segredo-tkn" {
				tknVelho = c.Value
			}
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()
	alvo, _ := url.Parse(backend.URL)
	fake := &ideFake{estado: "pronto", alvo: alvo, token: "segredo-tkn"}
	srv := Novo(Opcoes{Banco: banco, IDE: fake})
	setupAdmin(t, srv) // usuário id 1, ativo

	secret, err := banco.ObterOuGerarJWTSecret(context.Background())
	if err != nil {
		t.Fatalf("segredo jwt: %v", err)
	}
	tokIDE, err := auth.Assinar(1, time.Hour, secret)
	if err != nil {
		t.Fatalf("assinar: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/ide/stable/workbench.html?x=1", nil)
	req.AddCookie(&http.Cookie{Name: cookieSessaoIDE, Value: tokIDE})
	// Cookie de uma instância anterior do serve-web: o proxy deve descartá-lo em
	// favor do token corrente (o velho não vale mais e causaria 403).
	req.AddCookie(&http.Cookie{Name: "vscode-tkn", Value: "token-velho"})
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, quero 200 (proxy até o backend)", rec.Code)
	}
	if caminho != "/ide/stable/workbench.html" {
		t.Fatalf("backend recebeu caminho %q, quero /ide/stable/workbench.html (base path preservado)", caminho)
	}
	if tkn != "segredo-tkn" {
		t.Fatalf("backend recebeu vscode-tkn %q, quero o connection-token injetado pelo proxy (como cookie, não query)", tkn)
	}
	if sessaoVazou != "" {
		t.Fatal("o cookie de sessão do Praxis vazou para o backend")
	}
	if tknVelho != "" {
		t.Fatalf("o vscode-tkn velho do navegador (%q) não foi filtrado", tknVelho)
	}
	// O Host do CLIENTE deve chegar ao backend: o serve-web o embute no
	// workbench como remoteAuthority — é o endereço a que o navegador conecta o
	// WebSocket. Host interno aqui = ws por fora do proxy = 1006.
	if hostRecebido != req.Host {
		t.Fatalf("backend recebeu Host %q, quero o Host original do cliente %q", hostRecebido, req.Host)
	}
	if fake.tocados == 0 {
		t.Fatal("proxy deveria Tocar o IDE a cada requisição")
	}
}

// TestProxyIDEUpgradeWebSocket faz um handshake de WebSocket REAL através da
// cadeia completa (middlewares comRecover/comLog + proxy): regressão do 502
// "can't switch protocols using non-Hijacker ResponseWriter" — o capturaStatus
// do log precisa delegar o Hijack para o upgrade funcionar.
func TestProxyIDEUpgradeWebSocket(t *testing.T) {
	banco := abrirBancoTemp(t)

	// Backend fake no lugar do serve-web: aceita o upgrade com um 101 cru.
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Upgrade") != "websocket" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		conn, buf, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Errorf("hijack no backend: %v", err)
			return
		}
		defer conn.Close()
		buf.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n")
		buf.Flush()
	}))
	defer backend.Close()

	alvo, _ := url.Parse(backend.URL)
	srv := Novo(Opcoes{Banco: banco, IDE: &ideFake{estado: "pronto", alvo: alvo, token: "tkn"}})
	setupAdmin(t, srv)

	praxis := httptest.NewServer(srv.Handler())
	defer praxis.Close()

	secret, err := banco.ObterOuGerarJWTSecret(context.Background())
	if err != nil {
		t.Fatalf("segredo jwt: %v", err)
	}
	tokIDE, err := auth.Assinar(1, time.Hour, secret)
	if err != nil {
		t.Fatalf("assinar: %v", err)
	}

	conn, err := net.Dial("tcp", praxis.Listener.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	fmt.Fprintf(conn, "GET /ide/ws HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n"+
		"Sec-WebSocket-Version: 13\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nCookie: %s=%s\r\n\r\n",
		praxis.Listener.Addr().String(), cookieSessaoIDE, tokIDE)

	linha, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		t.Fatalf("ler resposta do upgrade: %v", err)
	}
	if !strings.Contains(linha, " 101 ") {
		t.Fatalf("resposta = %q, quero HTTP/1.1 101 (upgrade de WebSocket através dos middlewares)", strings.TrimSpace(linha))
	}
}

func TestProxyIDEPreparandoRecarrega(t *testing.T) {
	banco := abrirBancoTemp(t)
	srv := Novo(Opcoes{Banco: banco, IDE: &ideFake{estado: "preparando"}})
	setupAdmin(t, srv)

	secret, _ := banco.ObterOuGerarJWTSecret(context.Background())
	tokIDE, _ := auth.Assinar(1, time.Hour, secret)
	req := httptest.NewRequest(http.MethodGet, "/ide/?folder=/x", nil)
	req.AddCookie(&http.Cookie{Name: cookieSessaoIDE, Value: tokIDE})
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, quero 503 enquanto prepara", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "http-equiv=\"refresh\"") {
		t.Fatalf("página de preparo sem auto-refresh: %q", rec.Body.String())
	}
}

func TestStatusIDE(t *testing.T) {
	banco := abrirBancoTemp(t)
	srv := Novo(Opcoes{Banco: banco, IDE: &ideFake{estado: "erro", detalhe: "download falhou"}})
	tok := setupAdmin(t, srv)

	rec := fazerReqToken(t, srv, http.MethodGet, "/api/v1/ide/status", tok, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, quero 200", rec.Code)
	}
	var resp map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp["estado"] != "erro" || resp["detalhe"] != "download falhou" {
		t.Fatalf("resp = %+v", resp)
	}
}
