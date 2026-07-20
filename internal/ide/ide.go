// Package ide gerencia a instância ÚNICA do VS Code Web (`code serve-web`) que
// permite ao desenvolvedor editar manualmente o worktree de uma demanda pelo
// navegador. O serve-web sobe SOB DEMANDA (primeiro "solicitar acesso"), escuta
// só no loopback em uma porta efêmera com connection-token gerado, e é exposto
// para fora exclusivamente pelo proxy reverso /ide/* do próprio servidor Praxis
// (mesma porta, mesmo TLS, mesma autenticação/permissão).
//
// Uma instância única basta porque o serve-web abre qualquer pasta via
// ?folder=<caminho> — instância por demanda não daria isolamento nenhum (o
// workbench enxerga o filesystem do usuário do serviço de qualquer forma; quem
// controla o acesso é a permissão codigo.editar do Praxis).
//
// Ciclo de vida: parado → preparando (download do CLI, se preciso, + subida) →
// pronto → (ocioso por Ociosidade, queda ou shutdown) → parado. O PID entra no
// registro de PIDs do serviço, então uma queda do Praxis deixa o serve-web órfão
// apenas até o próximo boot (MatarOrfaos).
package ide

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/marcos14/praxis-autonomous/internal/procs"
)

// Estados do Gerente (expostos em GET /api/v1/ide/status e usados pela UI).
const (
	EstadoParado     = "parado"
	EstadoPreparando = "preparando"
	EstadoPronto     = "pronto"
	EstadoErro       = "erro"
)

// ociosidadePadrao é o tempo sem requisições proxiadas após o qual o serve-web
// é derrubado (libera memória; a próxima solicitação sobe de novo em segundos,
// pois o CLI e o workbench já estão baixados).
const ociosidadePadrao = 30 * time.Minute

// timeoutSubida limita a espera pela primeira resposta HTTP do serve-web. É
// generoso porque a primeira subida baixa o workbench (~dezenas de MB).
const timeoutSubida = 5 * time.Minute

// Opcoes configura o Gerente.
type Opcoes struct {
	// Home é o PRAXIS_HOME: o CLI vai para tools/vscode-cli, os dados do
	// serve-web para tools/vscode-data e o log da instância para logs/.
	Home string
	// Ctx é o contexto de vida do serviço: cancelado no shutdown, mata a árvore
	// do serve-web.
	Ctx context.Context
	// BasePath é o prefixo público do proxy (default "/ide/"). Passado ao
	// serve-web via --server-base-path para os assets resolverem certo.
	BasePath string
	// Log recebe mensagens de progresso (nil = silencioso).
	Log func(string)
	// Registrar registra o PID do serve-web no registro de PIDs do serviço (para
	// matar órfãos no boot) e devolve a função de desregistro. Nil = sem registro.
	Registrar func(pid int) func()
	// Ociosidade derruba a instância após esse tempo sem uso (0 = default 30min).
	Ociosidade time.Duration
}

// Gerente controla a instância única do serve-web. Todos os métodos são seguros
// para uso concorrente. Um Gerente nil é inerte (a API trata como desligado).
type Gerente struct {
	home      string
	ctx       context.Context
	basePath  string
	logf      func(string)
	registrar func(pid int) func()
	ocioso    time.Duration

	mu        sync.Mutex
	estado    string
	detalhe   string // mensagem do último erro (estado erro)
	porta     int
	token     string
	pid       int
	ultimoUso time.Time
}

// Novo cria o Gerente (sem subir nada — a subida acontece no primeiro Solicitar).
func Novo(opts Opcoes) *Gerente {
	g := &Gerente{
		home:      opts.Home,
		ctx:       opts.Ctx,
		basePath:  opts.BasePath,
		logf:      opts.Log,
		registrar: opts.Registrar,
		ocioso:    opts.Ociosidade,
		estado:    EstadoParado,
	}
	if g.ctx == nil {
		g.ctx = context.Background()
	}
	if g.basePath == "" {
		g.basePath = "/ide/"
	}
	if g.logf == nil {
		g.logf = func(string) {}
	}
	if g.registrar == nil {
		g.registrar = func(int) func() { return func() {} }
	}
	if g.ocioso <= 0 {
		g.ocioso = ociosidadePadrao
	}
	return g
}

// Solicitar garante que a instância esteja no ar ou subindo: dispara a subida em
// background quando parada (ou em erro — nova tentativa) e devolve true só nesse
// caso. Idempotente: com a instância pronta ou preparando, é no-op (false).
func (g *Gerente) Solicitar() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.estado == EstadoPronto || g.estado == EstadoPreparando {
		return false
	}
	g.estado = EstadoPreparando
	g.detalhe = ""
	go g.subir()
	return true
}

// Estado devolve o estado corrente e o detalhe (mensagem do último erro).
func (g *Gerente) Estado() (estado, detalhe string) {
	if g == nil {
		return "desligado", ""
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.estado, g.detalhe
}

// Alvo devolve a URL local do serve-web e o connection-token para o proxy
// reverso. ok=false quando a instância não está pronta.
func (g *Gerente) Alvo() (alvo *url.URL, token string, ok bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.estado != EstadoPronto {
		return nil, "", false
	}
	u := &url.URL{Scheme: "http", Host: "127.0.0.1:" + strconv.Itoa(g.porta)}
	return u, g.token, true
}

// Tocar marca atividade (uma requisição proxiada), adiando o desligamento por
// ociosidade.
func (g *Gerente) Tocar() {
	g.mu.Lock()
	g.ultimoUso = time.Now()
	g.mu.Unlock()
}

// subir baixa o CLI se preciso, inicia o serve-web e espera a primeira resposta
// HTTP. Roda em goroutine própria; o resultado vira estado pronto ou erro.
func (g *Gerente) subir() {
	cli, err := garantirCLI(g.home, g.logf)
	if err != nil {
		g.falhar(err)
		return
	}
	porta, err := portaLivre()
	if err != nil {
		g.falhar(fmt.Errorf("reservar porta local: %w", err))
		return
	}
	token, err := novoToken()
	if err != nil {
		g.falhar(fmt.Errorf("gerar connection-token: %w", err))
		return
	}

	// --cli-data-dir e --server-data-dir isolam tudo sob PRAXIS_HOME (nada no
	// perfil do usuário do serviço). O token nunca chega ao navegador: quem o
	// injeta é o proxy do Praxis.
	cmd := exec.CommandContext(g.ctx, cli,
		"--cli-data-dir", filepath.Join(g.home, "tools", "vscode-data", "cli"),
		"serve-web",
		"--host", "127.0.0.1",
		"--port", strconv.Itoa(porta),
		"--connection-token", token,
		"--server-base-path", g.basePath,
		"--server-data-dir", filepath.Join(g.home, "tools", "vscode-data", "server"),
		"--accept-server-license-terms",
	)
	procs.ConfigurarGrupoProcesso(cmd)
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return procs.MatarArvore(cmd.Process.Pid)
	}

	// stdout/stderr vão para logs/ide-serve-web.log (diagnóstico de subida).
	caminhoLog := filepath.Join(g.home, "logs", "ide-serve-web.log")
	_ = os.MkdirAll(filepath.Dir(caminhoLog), 0o755)
	arqLog, errLog := os.Create(caminhoLog)
	if errLog == nil {
		cmd.Stdout, cmd.Stderr = arqLog, arqLog
	}

	if err := cmd.Start(); err != nil {
		if arqLog != nil {
			arqLog.Close()
		}
		g.falhar(fmt.Errorf("iniciar %s serve-web: %w", filepath.Base(cli), err))
		return
	}
	pid := cmd.Process.Pid
	desregistrar := g.registrar(pid)
	g.logf("IDE web: serve-web iniciado (pid " + strconv.Itoa(pid) + ", porta " + strconv.Itoa(porta) + ")")

	// Acompanha o término em paralelo: se o processo morrer durante a espera, a
	// subida falha na hora (com apontador para o log) em vez de estourar timeout.
	terminou := make(chan error, 1)
	go func() {
		err := cmd.Wait()
		desregistrar()
		if arqLog != nil {
			arqLog.Close()
		}
		terminou <- err
	}()

	if err := esperarResposta(g.ctx, porta, g.basePath, token, terminou, caminhoLog); err != nil {
		_ = procs.MatarArvore(pid) // garante que não fique um processo semivivo
		g.falhar(err)
		return
	}

	g.mu.Lock()
	g.estado = EstadoPronto
	g.porta = porta
	g.token = token
	g.pid = pid
	g.ultimoUso = time.Now()
	g.mu.Unlock()
	g.logf("IDE web: pronto (proxy em " + g.basePath + ")")

	g.vigiar(pid, terminou)
}

// vigiar acompanha uma instância pronta: derruba por ociosidade e, quando o
// processo termina (por ociosidade, queda ou shutdown), volta o estado a parado.
func (g *Gerente) vigiar(pid int, terminou <-chan error) {
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	ctxDone := g.ctx.Done()
	for {
		select {
		case <-terminou:
			g.mu.Lock()
			if g.pid == pid { // não sobrescreve uma instância mais nova
				g.estado = EstadoParado
				g.porta, g.token, g.pid = 0, "", 0
			}
			g.mu.Unlock()
			g.logf("IDE web: serve-web encerrado")
			return
		case <-ctxDone:
			// O CommandContext já matou a árvore; o caso terminou acima fecha o
			// ciclo. Anula o canal para o select não repetir este caso em loop.
			ctxDone = nil
		case <-tick.C:
			g.mu.Lock()
			inativo := time.Since(g.ultimoUso)
			g.mu.Unlock()
			if inativo > g.ocioso {
				g.logf("IDE web: ocioso há " + inativo.Truncate(time.Minute).String() + ", encerrando")
				_ = procs.MatarArvore(pid)
			}
		}
	}
}

// falhar registra o erro e põe o Gerente em estado erro (novo Solicitar tenta de
// novo do zero).
func (g *Gerente) falhar(err error) {
	g.logf("IDE web: " + err.Error())
	g.mu.Lock()
	g.estado = EstadoErro
	g.detalhe = err.Error()
	g.porta, g.token, g.pid = 0, "", 0
	g.mu.Unlock()
}

// esperarResposta aguarda o serve-web responder HTTP no loopback (qualquer
// status serve — só interessa que está escutando). Aborta se o processo morrer
// (canal terminou) ou o timeout de subida estourar.
func esperarResposta(ctx context.Context, porta int, basePath, token string, terminou <-chan error, caminhoLog string) error {
	alvo := fmt.Sprintf("http://127.0.0.1:%d%s?tkn=%s", porta, basePath, url.QueryEscape(token))
	cliente := &http.Client{Timeout: 5 * time.Second}
	prazo := time.After(timeoutSubida)
	for {
		resp, err := cliente.Get(alvo)
		if err == nil {
			resp.Body.Close()
			return nil
		}
		select {
		case errProc := <-terminou:
			return fmt.Errorf("serve-web encerrou durante a subida (%v) — veja %s", errProc, caminhoLog)
		case <-ctx.Done():
			return ctx.Err()
		case <-prazo:
			return fmt.Errorf("serve-web não respondeu em %s — veja %s", timeoutSubida, caminhoLog)
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// portaLivre pede ao SO uma porta TCP livre no loopback. Há uma janela teórica
// entre fechar o listener e o serve-web abrir a porta; na prática o SO não
// recicla a porta nesse intervalo.
func portaLivre() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	porta := ln.Addr().(*net.TCPAddr).Port
	return porta, ln.Close()
}

// novoToken gera o connection-token (32 hex chars aleatórios).
func novoToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
