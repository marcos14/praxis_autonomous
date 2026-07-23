package motor

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	AuthAutenticado = "autenticado"
	AuthDeslogado   = "deslogado"
	AuthErro        = "erro"

	LoginIniciando  = "iniciando"
	LoginAguardando = "aguardando_navegador"
	LoginConcluido  = "concluido"
	LoginErro       = "erro"
	LoginCancelado  = "cancelado"
	LoginExpirado   = "expirado"
)

var (
	ErrSessaoLoginNaoEncontrada = errors.New("sessão de login não encontrada")
	ErrLoginEmAndamento         = errors.New("já existe um login em andamento para este perfil")
	ErrCodigoLoginIndisponivel  = errors.New("esta sessão não está aguardando um código")
)

type fabricaComando func(context.Context, string, ...string) *exec.Cmd

// DiagnosticoAutenticacao é a visão segura do estado de login. Não inclui
// email, ids de workspace, token nem conteúdo dos arquivos do perfil.
type DiagnosticoAutenticacao struct {
	Vendor       string    `json:"vendor"`
	Estado       string    `json:"estado"`
	Autenticado  bool      `json:"autenticado"`
	Metodo       string    `json:"metodo,omitempty"`
	Mensagem     string    `json:"mensagem,omitempty"`
	VerificadoEm time.Time `json:"verificado_em"`
}

// VerificarAutenticacao consulta o CLI no diretório do perfil. O exit code e a
// saída estruturada são interpretados localmente e nunca repassados crus à API.
func VerificarAutenticacao(ctx context.Context, vendor, perfilDir string) DiagnosticoAutenticacao {
	return verificarAutenticacaoCom(ctx, vendor, perfilDir, exec.CommandContext)
}

func verificarAutenticacaoCom(ctx context.Context, vendor, perfilDir string, comando fabricaComando) DiagnosticoAutenticacao {
	vendor = normalizarNomeMotor(vendor)
	d := DiagnosticoAutenticacao{Vendor: vendor, Estado: AuthErro, VerificadoEm: time.Now().UTC()}
	if !VendorComPerfilIsolado(vendor) {
		d.Mensagem = "motor sem suporte a perfis/login assistido"
		return d
	}
	dir, err := PrepararPerfil(vendor, perfilDir)
	if err != nil {
		d.Mensagem = "diretório isolado do perfil indisponível"
		return d
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	switch vendor {
	case "claude":
		cmd := comando(ctx, "claude", "auth", "status", "--json")
		if err := aplicarPerfil(cmd, vendor, dir); err != nil {
			d.Mensagem = "não foi possível aplicar o perfil Claude"
			return d
		}
		out, runErr := cmd.CombinedOutput()
		var status struct {
			LoggedIn   bool   `json:"loggedIn"`
			AuthMethod string `json:"authMethod"`
		}
		if err := json.Unmarshal(extrairObjetoJSON(out), &status); err != nil {
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				d.Mensagem = "a verificação do Claude excedeu o tempo limite"
			} else if erroExecutavelAusente(runErr) {
				d.Mensagem = "CLI claude não encontrado no PATH do serviço"
			} else {
				d.Mensagem = "o Claude não devolveu um estado de autenticação reconhecível"
			}
			return d
		}
		d.Metodo = strings.TrimSpace(status.AuthMethod)
		d.Autenticado = status.LoggedIn
		if status.LoggedIn {
			d.Estado = AuthAutenticado
		} else {
			d.Estado = AuthDeslogado
		}
		return d

	case "codex":
		cmd := comando(ctx, "codex", "login", "status")
		if err := aplicarPerfil(cmd, vendor, dir); err != nil {
			d.Mensagem = "não foi possível aplicar o perfil Codex"
			return d
		}
		out, runErr := cmd.CombinedOutput()
		texto := strings.ToLower(string(out))
		if runErr == nil && !strings.Contains(texto, "not logged in") && !strings.Contains(texto, "não autenticado") {
			d.Estado, d.Autenticado = AuthAutenticado, true
			switch {
			case strings.Contains(texto, "api key"):
				d.Metodo = "api_key"
			case strings.Contains(texto, "chatgpt"):
				d.Metodo = "chatgpt"
			default:
				d.Metodo = "codex"
			}
			return d
		}
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			d.Mensagem = "a verificação do Codex excedeu o tempo limite"
			return d
		}
		if erroExecutavelAusente(runErr) {
			d.Mensagem = "CLI codex não encontrado no PATH do serviço"
			return d
		}
		d.Estado, d.Autenticado = AuthDeslogado, false
		return d
	}
	return d
}

func extrairObjetoJSON(out []byte) []byte {
	ini := bytes.IndexByte(out, '{')
	fim := bytes.LastIndexByte(out, '}')
	if ini < 0 || fim < ini {
		return out
	}
	return out[ini : fim+1]
}

func erroExecutavelAusente(err error) bool {
	var execErr *exec.Error
	return errors.As(err, &execErr) || errors.Is(err, exec.ErrNotFound)
}

// SessaoLogin é o estado público e sanitizado de um login assistido.
type SessaoLogin struct {
	ID           string     `json:"id"`
	Vendor       string     `json:"vendor"`
	Estado       string     `json:"estado"`
	URL          string     `json:"url,omitempty"`
	Codigo       string     `json:"codigo,omitempty"`
	RequerCodigo bool       `json:"requer_codigo,omitempty"`
	Mensagem     string     `json:"mensagem,omitempty"`
	CriadoEm     time.Time  `json:"criado_em"`
	ExpiraEm     time.Time  `json:"expira_em"`
	ConcluidoEm  *time.Time `json:"concluido_em,omitempty"`
}

type sessaoLoginInterna struct {
	publico   SessaoLogin
	perfilDir string
	cancelar  context.CancelFunc
	escrever  func(string) error
}

// GerenteLogin mantém apenas sessões efêmeras em memória. Reiniciar o serviço
// cancela os fluxos, mas as credenciais já concluídas permanecem no perfil do
// próprio vendor.
type GerenteLogin struct {
	mu      sync.Mutex
	sessoes map[string]*sessaoLoginInterna
	duracao time.Duration
	agora   func() time.Time
	comando fabricaComando
}

func NovoGerenteLogin() *GerenteLogin {
	return &GerenteLogin{
		sessoes: make(map[string]*sessaoLoginInterna),
		duracao: 10 * time.Minute,
		agora:   time.Now,
		comando: exec.CommandContext,
	}
}

// IniciarLogin cria a sessão e dispara o CLI em background. A URL aparece na
// própria sessão assim que o vendor a devolver.
func (g *GerenteLogin) IniciarLogin(vendor, perfilDir string) (SessaoLogin, error) {
	if g == nil {
		return SessaoLogin{}, errors.New("gerente de login indisponível")
	}
	vendor = normalizarNomeMotor(vendor)
	dir, err := PrepararPerfil(vendor, perfilDir)
	if err != nil {
		return SessaoLogin{}, err
	}
	agora := g.agora().UTC()
	id, err := novoIDSessao()
	if err != nil {
		return SessaoLogin{}, err
	}

	g.mu.Lock()
	for _, atual := range g.sessoes {
		if atual.publico.Vendor == vendor && atual.perfilDir == dir && !loginTerminal(atual.publico.Estado) {
			g.mu.Unlock()
			return SessaoLogin{}, ErrLoginEmAndamento
		}
	}
	ctx, cancelar := context.WithTimeout(context.Background(), g.duracao)
	s := &sessaoLoginInterna{
		publico:   SessaoLogin{ID: id, Vendor: vendor, Estado: LoginIniciando, CriadoEm: agora, ExpiraEm: agora.Add(g.duracao)},
		perfilDir: dir,
		cancelar:  cancelar,
	}
	g.sessoes[id] = s
	publico := s.publico
	g.mu.Unlock()

	go g.executar(ctx, id, vendor, dir)
	return publico, nil
}

func novoIDSessao() (string, error) {
	b := make([]byte, 18)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("gerar id da sessão: %w", err)
	}
	return hex.EncodeToString(b), nil
}

func (g *GerenteLogin) ObterLogin(id string) (SessaoLogin, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	s, ok := g.sessoes[strings.TrimSpace(id)]
	if !ok {
		return SessaoLogin{}, ErrSessaoLoginNaoEncontrada
	}
	return s.publico, nil
}

func (g *GerenteLogin) CancelarLogin(id string) (SessaoLogin, error) {
	g.mu.Lock()
	s, ok := g.sessoes[strings.TrimSpace(id)]
	if !ok {
		g.mu.Unlock()
		return SessaoLogin{}, ErrSessaoLoginNaoEncontrada
	}
	if loginTerminal(s.publico.Estado) {
		publico := s.publico
		g.mu.Unlock()
		return publico, nil
	}
	agora := g.agora().UTC()
	s.publico.Estado = LoginCancelado
	s.publico.Mensagem = "login cancelado"
	s.publico.ConcluidoEm = &agora
	cancelar := s.cancelar
	publico := s.publico
	g.mu.Unlock()
	cancelar()
	return publico, nil
}

// EnviarCodigo escreve o código somente no stdin do processo Claude. O valor
// não é copiado para a sessão, para logs ou para o banco.
func (g *GerenteLogin) EnviarCodigo(id, codigo string) (SessaoLogin, error) {
	codigo = strings.TrimSpace(codigo)
	if codigo == "" || len(codigo) > 4096 || strings.ContainsAny(codigo, "\r\n") {
		return SessaoLogin{}, errors.New("código de login inválido")
	}
	g.mu.Lock()
	s, ok := g.sessoes[strings.TrimSpace(id)]
	if !ok {
		g.mu.Unlock()
		return SessaoLogin{}, ErrSessaoLoginNaoEncontrada
	}
	if loginTerminal(s.publico.Estado) || s.publico.Vendor != "claude" || s.escrever == nil {
		g.mu.Unlock()
		return SessaoLogin{}, ErrCodigoLoginIndisponivel
	}
	escrever := s.escrever
	g.mu.Unlock()
	if err := escrever(codigo); err != nil {
		return SessaoLogin{}, fmt.Errorf("enviar código ao Claude: %w", err)
	}
	g.mu.Lock()
	s.publico.Mensagem = "código enviado; aguardando confirmação do Claude"
	publico := s.publico
	g.mu.Unlock()
	return publico, nil
}

func loginTerminal(estado string) bool {
	switch estado {
	case LoginConcluido, LoginErro, LoginCancelado, LoginExpirado:
		return true
	default:
		return false
	}
}

func (g *GerenteLogin) executar(ctx context.Context, id, vendor, dir string) {
	switch vendor {
	case "claude":
		g.executarClaude(ctx, id, dir)
	case "codex":
		g.executarCodex(ctx, id, dir)
	default:
		g.finalizar(id, LoginErro, "motor sem login assistido")
	}
}

var (
	reURL  = regexp.MustCompile(`https?://[^\s"'<>]+`)
	reANSI = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)
)

func urlPublica(linha string) string {
	linha = reANSI.ReplaceAllString(linha, "")
	bruta := reURL.FindString(linha)
	bruta = strings.TrimRight(bruta, ").,;]")
	u, err := url.Parse(bruta)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return ""
	}
	return u.String()
}

func (g *GerenteLogin) executarClaude(ctx context.Context, id, dir string) {
	cmd := g.comando(ctx, "claude", "auth", "login")
	if err := aplicarPerfil(cmd, "claude", dir); err != nil {
		g.finalizar(id, LoginErro, "não foi possível aplicar o perfil Claude")
		return
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		g.finalizar(id, LoginErro, "não foi possível preparar a entrada do login Claude")
		return
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		g.finalizar(id, LoginErro, "não foi possível ler o login Claude")
		return
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		g.finalizar(id, LoginErro, "não foi possível ler o login Claude")
		return
	}
	prepararProcessoFilho(cmd)
	if err := cmd.Start(); err != nil {
		if erroExecutavelAusente(err) {
			g.finalizar(id, LoginErro, "CLI claude não encontrado no PATH do serviço")
		} else {
			g.finalizar(id, LoginErro, "não foi possível iniciar o login Claude")
		}
		return
	}

	var escritaMu sync.Mutex
	g.definirEscritor(id, func(codigo string) error {
		escritaMu.Lock()
		defer escritaMu.Unlock()
		_, err := io.WriteString(stdin, codigo+"\n")
		return err
	})

	linhas := make(chan string, 16)
	var wg sync.WaitGroup
	ler := func(r io.Reader) {
		defer wg.Done()
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 64*1024), 2*1024*1024)
		for sc.Scan() {
			select {
			case linhas <- sc.Text():
			case <-ctx.Done():
				return
			}
		}
	}
	wg.Add(2)
	go ler(stdout)
	go ler(stderr)
	go func() { wg.Wait(); close(linhas) }()
	for linha := range linhas {
		if u := urlPublica(linha); u != "" {
			g.disponibilizarNavegador(id, u, "", true)
		}
	}
	errWait := cmd.Wait()
	_ = stdin.Close()
	g.definirEscritor(id, nil)
	if ctx.Err() != nil {
		g.finalizarPorContexto(id, ctx)
		return
	}
	if g.confirmarLogin("claude", dir) {
		g.finalizar(id, LoginConcluido, "perfil Claude autenticado")
		return
	}
	if errWait != nil {
		g.finalizar(id, LoginErro, "o Claude encerrou o login antes da autenticação")
	} else {
		g.finalizar(id, LoginErro, "o Claude encerrou o fluxo sem confirmar a autenticação")
	}
}

type mensagemRPC struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
	Error  json.RawMessage `json:"error"`
}

func (g *GerenteLogin) executarCodex(ctx context.Context, id, dir string) {
	cmd := g.comando(ctx, "codex", "app-server", "--listen", "stdio://")
	if err := aplicarPerfil(cmd, "codex", dir); err != nil {
		g.finalizar(id, LoginErro, "não foi possível aplicar o perfil Codex")
		return
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		g.finalizar(id, LoginErro, "não foi possível preparar o app-server do Codex")
		return
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		g.finalizar(id, LoginErro, "não foi possível ler o app-server do Codex")
		return
	}
	cmd.Stderr = io.Discard
	prepararProcessoFilho(cmd)
	if err := cmd.Start(); err != nil {
		if erroExecutavelAusente(err) {
			g.finalizar(id, LoginErro, "CLI codex não encontrado no PATH do serviço")
		} else {
			g.finalizar(id, LoginErro, "não foi possível iniciar o app-server do Codex")
		}
		return
	}
	enc := json.NewEncoder(stdin)
	if err := enc.Encode(map[string]any{
		"id": 1, "method": "initialize",
		"params": map[string]any{"clientInfo": map[string]any{"name": "praxis-autonomous", "title": "Praxis Autonomous", "version": "1"}},
	}); err != nil {
		_ = cmd.Cancel()
		_ = cmd.Wait()
		g.finalizar(id, LoginErro, "não foi possível inicializar o app-server do Codex")
		return
	}

	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 128*1024), 4*1024*1024)
	loginIniciado := false
	concluido := false
	for sc.Scan() {
		var msg mensagemRPC
		if json.Unmarshal(sc.Bytes(), &msg) != nil {
			continue
		}
		rpcID := strings.Trim(string(msg.ID), `"`)
		switch {
		case rpcID == "1":
			if len(msg.Error) > 0 && string(msg.Error) != "null" {
				g.finalizar(id, LoginErro, "a versão instalada do Codex recusou a inicialização do app-server")
				concluido = true
				break
			}
			_ = enc.Encode(map[string]any{"method": "initialized"})
			if err := enc.Encode(map[string]any{
				"id": 2, "method": "account/login/start", "params": map[string]any{"type": "chatgptDeviceCode"},
			}); err != nil {
				g.finalizar(id, LoginErro, "não foi possível iniciar o login Codex")
				concluido = true
				break
			}
			loginIniciado = true

		case rpcID == "2":
			if len(msg.Error) > 0 && string(msg.Error) != "null" {
				g.finalizar(id, LoginErro, "a versão instalada do Codex não oferece login por código de dispositivo")
				concluido = true
				break
			}
			var resp struct {
				Type            string `json:"type"`
				VerificationURL string `json:"verificationUrl"`
				AuthURL         string `json:"authUrl"`
				UserCode        string `json:"userCode"`
			}
			if json.Unmarshal(msg.Result, &resp) != nil {
				g.finalizar(id, LoginErro, "o Codex devolveu uma resposta de login incompatível")
				concluido = true
				break
			}
			u := strings.TrimSpace(resp.VerificationURL)
			if u == "" {
				u = strings.TrimSpace(resp.AuthURL)
			}
			if urlPublica(u) == "" {
				g.finalizar(id, LoginErro, "o Codex não devolveu uma URL de autenticação válida")
				concluido = true
				break
			}
			g.disponibilizarNavegador(id, u, strings.TrimSpace(resp.UserCode), false)

		case msg.Method == "account/login/completed":
			var nota struct {
				Success bool   `json:"success"`
				Error   string `json:"error"`
			}
			if json.Unmarshal(msg.Params, &nota) != nil || !nota.Success {
				g.finalizar(id, LoginErro, "o Codex não concluiu a autenticação")
			} else if g.confirmarLogin("codex", dir) {
				g.finalizar(id, LoginConcluido, "perfil Codex autenticado")
			} else {
				g.finalizar(id, LoginErro, "o Codex concluiu o fluxo, mas o perfil continua deslogado")
			}
			concluido = true
		}
		if concluido {
			break
		}
	}
	_ = stdin.Close()
	if cmd.ProcessState == nil {
		_ = cmd.Cancel()
	}
	_ = cmd.Wait()
	if concluido {
		return
	}
	if ctx.Err() != nil {
		g.finalizarPorContexto(id, ctx)
	} else if !loginIniciado {
		g.finalizar(id, LoginErro, "a versão instalada do Codex não respondeu ao contrato de login")
	} else {
		g.finalizar(id, LoginErro, "o app-server do Codex encerrou antes da autenticação")
	}
}

func (g *GerenteLogin) confirmarLogin(vendor, dir string) bool {
	for tentativa := 0; tentativa < 3; tentativa++ {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		d := verificarAutenticacaoCom(ctx, vendor, dir, g.comando)
		cancel()
		if d.Autenticado {
			return true
		}
		if tentativa < 2 {
			time.Sleep(300 * time.Millisecond)
		}
	}
	return false
}

func (g *GerenteLogin) definirEscritor(id string, escrever func(string) error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if s, ok := g.sessoes[id]; ok && !loginTerminal(s.publico.Estado) {
		s.escrever = escrever
	}
}

func (g *GerenteLogin) disponibilizarNavegador(id, endereco, codigo string, requerCodigo bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	s, ok := g.sessoes[id]
	if !ok || loginTerminal(s.publico.Estado) {
		return
	}
	s.publico.Estado = LoginAguardando
	s.publico.URL = endereco
	s.publico.Codigo = codigo
	s.publico.RequerCodigo = requerCodigo
	if requerCodigo {
		s.publico.Mensagem = "abra a URL e, se o Claude exibir um código, cole-o aqui"
	} else {
		s.publico.Mensagem = "abra a URL e informe o código exibido"
	}
}

func (g *GerenteLogin) finalizarPorContexto(id string, ctx context.Context) {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		g.finalizar(id, LoginExpirado, "a sessão de login expirou")
		return
	}
	// CancelarLogin já carimba a sessão antes de cancelar o contexto. Este
	// fallback cobre cancelamentos internos sem sobrescrever um estado terminal.
	g.finalizar(id, LoginCancelado, "login cancelado")
}

func (g *GerenteLogin) finalizar(id, estado, mensagem string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	s, ok := g.sessoes[id]
	if !ok || loginTerminal(s.publico.Estado) {
		return
	}
	agora := g.agora().UTC()
	s.publico.Estado = estado
	s.publico.Mensagem = mensagem
	s.publico.ConcluidoEm = &agora
	s.escrever = nil
	if s.cancelar != nil {
		s.cancelar()
	}
}
