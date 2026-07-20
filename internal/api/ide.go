package api

// Acesso manual ao código: expõe o VS Code Web (instância única de `code
// serve-web`, gerida por internal/ide) atrás do proxy reverso /ide/* do próprio
// servidor — mesma porta, mesmo TLS, mesma origem.
//
// Autenticação em duas camadas:
//   - POST /api/v1/ide/sessao (JWT normal da API, permissão codigo.editar +
//     gate de estado da demanda) emite um COOKIE HttpOnly restrito a /ide/. O
//     cookie existe porque o navegador não envia o header Authorization ao
//     abrir o IDE em outra aba; ele carrega um JWT curto assinado com o mesmo
//     segredo da sessão.
//   - Toda requisição a /ide/* valida esse cookie (e se o usuário segue ativo)
//     antes de proxiar; o connection-token do serve-web é injetado AQUI, no
//     servidor — nunca chega ao navegador.

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/marcos14/praxis-autonomous/internal/auth"
	"github.com/marcos14/praxis-autonomous/internal/db"
)

// IDEWeb é o seam do gerente do VS Code Web (em produção, *ide.Gerente). Nil =
// recurso desligado (PRAXIS_HOME indisponível).
type IDEWeb interface {
	// Solicitar dispara a subida em background quando parada/erro (true só
	// nesse caso); idempotente quando pronta ou preparando.
	Solicitar() bool
	// Estado devolve o estado ("parado"|"preparando"|"pronto"|"erro"|
	// "desligado") e o detalhe do último erro.
	Estado() (estado, detalhe string)
	// Alvo devolve a URL local do serve-web e o connection-token para o proxy
	// (ok=false quando não está pronto).
	Alvo() (alvo *url.URL, token string, ok bool)
	// Tocar marca atividade, adiando o desligamento por ociosidade.
	Tocar()
}

// cookieSessaoIDE é o cookie HttpOnly (Path=/ide/) que autentica o navegador no
// proxy do IDE. ttlSessaoIDE limita a vida do JWT que ele carrega; cada POST
// /ide/sessao renova.
const (
	cookieSessaoIDE = "praxis_ide"
	ttlSessaoIDE    = 8 * time.Hour
)

// registrarRotasIDE registra a sessão/status do IDE na API e o proxy /ide/*
// (fora de /api/v1 — a autenticação dele é o cookie, não o middleware comAuth).
func (s *Servidor) registrarRotasIDE(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/ide/sessao", s.handleCriarSessaoIDE)
	mux.HandleFunc("GET /api/v1/ide/status", s.handleStatusIDE)
	// Métodos explícitos: o padrão sem método ("/ide/") conflitaria com o
	// catch-all "GET /" dos assets no ServeMux. WebSocket entra pelo GET.
	for _, m := range []string{"GET", "HEAD", "POST", "PUT", "DELETE", "PATCH", "OPTIONS"} {
		mux.HandleFunc(m+" /ide/", s.handleProxyIDE)
	}
}

// reqSessaoIDE é o corpo de POST /api/v1/ide/sessao.
type reqSessaoIDE struct {
	DemandID int64 `json:"demand_id"`
}

// respSessaoIDE é a resposta: estado corrente e, quando pronto, a URL do IDE já
// apontando para o worktree da demanda.
type respSessaoIDE struct {
	Estado string `json:"estado"`
	URL    string `json:"url,omitempty"`
}

// statusPermiteEdicaoManual diz em quais estados o worktree pode ser editado à
// mão sem corromper o ciclo de execução: nunca com o scheduler podendo escrever
// nele (pronta/executando/aguardando_franquia) nem antes de existir worktree.
// Para editar uma demanda em execução, pause-a primeiro.
func statusPermiteEdicaoManual(status string) bool {
	switch status {
	case db.StatusDemandaPausada, db.StatusDemandaFalhou, db.StatusDemandaConcluida,
		db.StatusDemandaConflito, db.StatusDemandaCancelada, db.StatusDemandaIntegrada:
		return true
	}
	return false
}

// handleCriarSessaoIDE valida demanda/estado/permissão, emite (ou renova) o
// cookie de sessão do IDE e garante a instância do serve-web no ar. Enquanto a
// instância prepara, responde 202 {estado:"preparando"} — a UI faz poll
// repetindo o POST até receber 200 com a URL.
func (s *Servidor) handleCriarSessaoIDE(w http.ResponseWriter, r *http.Request) {
	if s.ideWeb == nil {
		responderErro(w, http.StatusServiceUnavailable, "ide_indisponivel",
			"IDE web indisponível neste servidor (PRAXIS_HOME não resolvido)")
		return
	}
	pr := principalDaRequisicao(r)
	if pr.userID <= 0 {
		responderErro(w, http.StatusForbidden, "sem_usuario",
			"o IDE web exige um usuário logado (tokens de API não abrem sessão)")
		return
	}
	var req reqSessaoIDE
	if !decodificarCorpo(w, r, &req) {
		return
	}
	dem, err := s.banco.ObterDemanda(r.Context(), req.DemandID)
	if err != nil {
		if errors.Is(err, db.ErrNaoEncontrado) {
			responderErro(w, http.StatusNotFound, "nao_encontrado", "demanda não encontrada")
			return
		}
		s.log.Error("obter demanda para IDE", "erro", err)
		responderErro(w, http.StatusInternalServerError, "erro_interno", "erro interno do servidor")
		return
	}
	// ACL de projetos: o id veio no corpo, então a visibilidade não foi checada
	// pelo middleware (que só olha o caminho). Responde 404 como lá.
	if uid := filtroVisibilidade(pr); uid != nil {
		ve, err := s.banco.UsuarioVeDemanda(r.Context(), *uid, dem.ID)
		if err != nil {
			s.log.Error("checar visibilidade para IDE", "erro", err)
			responderErro(w, http.StatusInternalServerError, "erro_interno", "erro interno do servidor")
			return
		}
		if !ve {
			responderErro(w, http.StatusNotFound, "nao_encontrado", "demanda não encontrada")
			return
		}
	}
	if !statusPermiteEdicaoManual(dem.Status) {
		responderErro(w, http.StatusConflict, "estado_invalido",
			"edição manual só com a demanda pausada, falhada, em conflito ou encerrada — pause a demanda antes de editar (status atual: "+dem.Status+")")
		return
	}
	worktree := strings.TrimSpace(dem.WorktreePath)
	if worktree == "" {
		responderErro(w, http.StatusConflict, "sem_worktree",
			"a demanda ainda não tem worktree (a execução não começou)")
		return
	}
	if _, err := os.Stat(worktree); err != nil {
		responderErro(w, http.StatusConflict, "sem_worktree",
			"o worktree da demanda não existe mais no disco: "+worktree)
		return
	}

	// Cookie de sessão do IDE: JWT curto com o mesmo segredo da sessão da API.
	secret, err := s.segredoJWT(r.Context())
	if err != nil {
		s.log.Error("segredo do jwt para IDE", "erro", err)
		responderErro(w, http.StatusInternalServerError, "erro_interno", "erro interno do servidor")
		return
	}
	tok, err := auth.Assinar(pr.userID, ttlSessaoIDE, secret)
	if err != nil {
		s.log.Error("assinar sessão do IDE", "erro", err)
		responderErro(w, http.StatusInternalServerError, "erro_interno", "erro interno do servidor")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     cookieSessaoIDE,
		Value:    tok,
		Path:     "/ide/",
		MaxAge:   int(ttlSessaoIDE / time.Second),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   r.TLS != nil,
	})

	s.ideWeb.Solicitar()
	estado, detalhe := s.ideWeb.Estado()
	switch estado {
	case "pronto":
		s.ideWeb.Tocar()
		s.registrarEventoIDE(r, dem)
		responderJSON(w, http.StatusOK, respSessaoIDE{Estado: estado, URL: urlIDEParaWorktree(worktree)})
	case "preparando":
		responderJSON(w, http.StatusAccepted, respSessaoIDE{Estado: estado})
	default: // erro (a subida disparada acima falhou antes ou está falhando)
		msg := "IDE web falhou ao subir"
		if detalhe != "" {
			msg += ": " + detalhe
		}
		responderErro(w, http.StatusBadGateway, "ide_erro", msg)
	}
}

// urlIDEParaWorktree monta a URL do IDE apontando para o worktree. O valor de
// ?folder= precisa COMEÇAR com "/": o workbench só monta a URI remota
// (vscode-remote://host/C:/…) para valores com "/" inicial; sem ele, faz
// URI.parse e um caminho Windows "C:/…" vira uma URI de scheme "c" — o
// explorer mostra o nome da pasta mas nunca resolve a raiz (cinza com "!").
// Caminhos Unix já começam com "/" e passam intactos.
func urlIDEParaWorktree(worktree string) string {
	p := filepath.ToSlash(worktree)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return "/ide/?folder=" + url.QueryEscape(p)
}

// registrarEventoIDE registra (best-effort) o evento de acesso ao código — é a
// trilha de auditoria de quem abriu o IDE em qual demanda.
func (s *Servidor) registrarEventoIDE(r *http.Request, dem db.Demanda) {
	pr := principalDaRequisicao(r)
	ev := db.Evento{Tipo: "codigo_acessado", Titulo: "Praxis: código aberto no IDE web",
		Detalhe: fmt.Sprintf("%s abriu o worktree da demanda d%d no IDE web.", pr.nome, dem.ID)}
	if dem.ProjectID > 0 {
		pid := dem.ProjectID
		ev.ProjectID = &pid
	}
	did := dem.ID
	ev.DemandID = &did
	if _, err := s.banco.RegistrarEvento(r.Context(), ev); err != nil {
		s.log.Warn("registrar evento de acesso ao IDE", "erro", err)
	}
}

// handleStatusIDE devolve o estado corrente do IDE web (para diagnóstico e para
// a UI decidir rótulos). Basta estar autenticado.
func (s *Servidor) handleStatusIDE(w http.ResponseWriter, r *http.Request) {
	if s.ideWeb == nil {
		responderJSON(w, http.StatusOK, respSessaoIDE{Estado: "desligado"})
		return
	}
	estado, detalhe := s.ideWeb.Estado()
	resp := map[string]string{"estado": estado}
	if detalhe != "" {
		resp["detalhe"] = detalhe
	}
	responderJSON(w, http.StatusOK, resp)
}

// handleProxyIDE proxia /ide/* para o serve-web local, injetando o
// connection-token. Autentica pelo cookie de sessão do IDE; as respostas de
// falha são HTML mínimo (o cliente aqui é o navegador abrindo uma aba, não o
// fetch da SPA).
func (s *Servidor) handleProxyIDE(w http.ResponseWriter, r *http.Request) {
	if s.ideWeb == nil {
		http.NotFound(w, r)
		return
	}
	if !s.autenticarCookieIDE(w, r) {
		return
	}
	alvo, token, ok := s.ideWeb.Alvo()
	if !ok {
		estado, detalhe := s.ideWeb.Estado()
		if estado == "preparando" {
			// A aba foi aberta antes de a instância terminar de subir: recarrega
			// sozinha até o serve-web responder.
			paginaIDE(w, http.StatusServiceUnavailable, true,
				"Preparando o IDE… a página recarrega sozinha.")
			return
		}
		msg := "O IDE não está em execução. Volte ao Praxis e clique em “Editar código” na demanda."
		if detalhe != "" {
			msg += " (último erro: " + detalhe + ")"
		}
		paginaIDE(w, http.StatusServiceUnavailable, false, msg)
		return
	}
	s.ideWeb.Tocar()

	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(alvo) // preserva o caminho (/ide/…) — o serve-web usa o mesmo base path
			// Preserva o Host ORIGINAL do cliente (SetURL o troca pelo alvo): o
			// serve-web embute o Host no workbench como `remoteAuthority`, o
			// endereço a que o navegador conecta o WebSocket. Com o Host interno
			// (127.0.0.1:<porta>), o workbench tentaria o ws por fora do proxy —
			// endereço inalcançável de outra máquina e HTTP puro (bloqueado como
			// conteúdo misto em página https) → "WebSocket close 1006" sem
			// nenhum rastro nos logs.
			pr.Out.Host = pr.In.Host
			// O connection-token vai como COOKIE vscode-tkn, nunca como ?tkn=: a
			// forma query recebe do serve-web um 302 que limpa o token da URL, e
			// um proxy que o reinjeta a cada requisição vira loop infinito de
			// redirects ("too many redirects" no navegador). Os cookies do
			// navegador passam adiante, exceto o de sessão do Praxis (não é da
			// conta do backend) e qualquer vscode-tkn velho (de uma instância
			// anterior, com token que não vale mais).
			cookies := pr.In.Cookies()
			pr.Out.Header.Del("Cookie")
			for _, c := range cookies {
				if c.Name == cookieSessaoIDE || c.Name == "vscode-tkn" {
					continue
				}
				pr.Out.AddCookie(c)
			}
			pr.Out.AddCookie(&http.Cookie{Name: "vscode-tkn", Value: token})
		},
		// Flush imediato: o workbench usa streams longos além dos websockets.
		FlushInterval: -1,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			s.log.Warn("proxy do IDE", "erro", err)
			paginaIDE(w, http.StatusBadGateway, false, "Falha ao falar com o IDE: "+err.Error())
		},
	}
	proxy.ServeHTTP(w, r)
}

// autenticarCookieIDE valida o cookie de sessão do IDE e se o usuário segue
// ativo. Em falha, responde a página de 401 e devolve false.
func (s *Servidor) autenticarCookieIDE(w http.ResponseWriter, r *http.Request) bool {
	c, err := r.Cookie(cookieSessaoIDE)
	if err != nil || strings.TrimSpace(c.Value) == "" {
		paginaIDE(w, http.StatusUnauthorized, false,
			"Sessão do IDE ausente ou expirada. Volte ao Praxis e clique em “Editar código” na demanda.")
		return false
	}
	secret, err := s.segredoJWT(r.Context())
	if err != nil {
		paginaIDE(w, http.StatusInternalServerError, false, "Erro interno ao validar a sessão.")
		return false
	}
	uid, err := auth.Validar(c.Value, secret)
	if err != nil {
		paginaIDE(w, http.StatusUnauthorized, false,
			"Sessão do IDE inválida ou expirada. Volte ao Praxis e clique em “Editar código” na demanda.")
		return false
	}
	// Usuário desativado perde o acesso imediatamente (mesma política da API).
	if s.banco != nil {
		u, err := s.banco.ObterUsuario(r.Context(), uid)
		if err != nil || !u.Ativo {
			paginaIDE(w, http.StatusUnauthorized, false, "Usuário sem acesso.")
			return false
		}
	}
	return true
}

// paginaIDE escreve uma página HTML mínima para as respostas do proxy que não
// vêm do serve-web (auth, preparo, erro). Com recarregar=true, inclui um
// meta-refresh para a aba se recuperar sozinha quando o IDE subir.
func paginaIDE(w http.ResponseWriter, status int, recarregar bool, msg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	refresh := ""
	if recarregar {
		refresh = `<meta http-equiv="refresh" content="3">`
	}
	fmt.Fprintf(w, `<!doctype html><html lang="pt-br"><head><meta charset="utf-8">%s<title>Praxis — IDE</title>
<style>body{font-family:system-ui,sans-serif;display:flex;align-items:center;justify-content:center;height:100vh;margin:0;background:#111;color:#ddd}div{max-width:36rem;text-align:center;line-height:1.5}</style>
</head><body><div>%s</div></body></html>`, refresh, htmlEscape(msg))
}

// htmlEscape escapa o mínimo para interpolar texto na paginaIDE.
func htmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")
	return r.Replace(s)
}
