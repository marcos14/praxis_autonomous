package main

// Serviço do sistema (ADR 0001 — docs/adr/0001-servicos-instalaveis-windows-linux.md).
//
// O mesmo binário é o serviço, o instalador e a ferramenta de diagnóstico (D1):
//
//	praxis serve                         # foreground (dev, diagnóstico, Linux sob systemd)
//	praxis service install|remove        # registra/remove o serviço (exige admin/root)
//	praxis service start|stop|restart    # administra (exige admin/root)
//	praxis service status                # consulta — NUNCA exige elevação (D4)
//	praxis service run                   # corpo do serviço no Windows (invocado pelo SCM)
//	praxis service unit                  # imprime o unit/comando sem tocar na máquina
//
// O corpo do serviço é a função serve, que recebe um contexto de cancelamento
// (D2): o handler do SCM no Windows, o SIGTERM do systemd no Linux e o Ctrl+C
// do console são três cascas em volta da mesma função. Este arquivo tem a parte
// portável; service_windows.go e service_other.go têm as cascas por plataforma.

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// nomeServico é o nome interno registrado no SO. É contrato eterno (D6): toda
// consulta e toda ação procuram também os nomes em nomesServicoLegados, e o
// instalador remove o serviço antigo em vez de conviver com ele. Renomear o
// produto não renomeia as máquinas já instaladas.
const nomeServico = "praxis"

// nomeExibicaoServico e descricaoServico aparecem no services.msc / systemctl.
const (
	nomeExibicaoServico = "Praxis Autonomous"
	descricaoServico    = "Praxis Autonomous — orquestrador de desenvolvimento (HTTP + scheduler)"
)

// nomesServicoLegados são os nomes sob os quais versões anteriores registraram
// o serviço. A lista só cresce e nunca é podada (D6). Criada vazia desde o
// primeiro dia, de propósito.
var nomesServicoLegados = []string{}

// nomeArquivoLogServico é o log em arquivo usado quando o processo é iniciado
// pelo SCM do Windows, onde não há console (D8). Vive na raiz do PRAXIS_HOME —
// e não em PRAXIS_HOME/logs — porque a manutenção limpa aquela pasta por data.
// No Linux não existe arquivo próprio: stdout/stderr vão ao journald.
const nomeArquivoLogServico = "servico.log"

// timeoutParadaServico é o prazo que o handler do SCM dá ao serve para drenar
// após um Stop/Shutdown antes de reportar STOPPED mesmo assim — o SCM não
// espera indefinidamente. Um pouco acima do timeoutShutdown do servidor HTTP.
const timeoutParadaServico = timeoutShutdown + 5*time.Second

// opcoesServico são as opções de instalação/registro do serviço. As de execução
// (Addr, TLS…) são as mesmas flags do serve; só as pedidas explicitamente na
// instalação entram no binPath/ExecStart (ver argsServe). As demais são do
// próprio registro.
type opcoesServico struct {
	// Flags do serve embutidas no registro.
	Addr           string
	TLS            bool
	TLSCert        string
	TLSKey         string
	ProxyConfiavel bool

	// Home é o PRAXIS_HOME do serviço: diretório de estado da máquina (D5),
	// nunca perfil de usuário. Sempre explícito no registro.
	Home string
	// Dir é a pasta onde o binário é instalado (copiado do executável atual).
	Dir string

	// Windows: conta do serviço (LocalSystem por padrão) e senha, quando for
	// uma conta de usuário.
	Conta string
	Senha string
	// Linux: usuário do unit (User=).
	Usuario string
	// Windows: em vez de serviço do SCM, tarefa de logon do usuário atual
	// (processo residente por usuário — sem senha, roda enquanto logado).
	Logon bool

	// explicitas registra quais flags do serve foram passadas na linha de
	// comando — só essas vão para o registro.
	explicitas map[string]bool
}

// flagsServico registra as flags comuns de instalação em fs e devolve as opções
// com os defaults da plataforma. As flags exclusivas de cada SO (conta/senha no
// Windows, usuario no Linux) são registradas por flagsServicoPlataforma.
func flagsServico(fs *flag.FlagSet) *opcoesServico {
	o := &opcoesServico{explicitas: map[string]bool{}}
	fs.StringVar(&o.Addr, "addr", enderecoPadrao, "endereço TCP de bind do servidor HTTP (use 0.0.0.0:7799 para acesso pela rede — com TLS)")
	fs.BoolVar(&o.TLS, "tls", false, "habilita HTTPS com certificado autoassinado gerado/reutilizado em PRAXIS_HOME/tls")
	fs.StringVar(&o.TLSCert, "tls-cert", "", "certificado TLS (PEM) próprio; habilita HTTPS (exige -tls-key)")
	fs.StringVar(&o.TLSKey, "tls-key", "", "chave privada TLS (PEM) do -tls-cert")
	fs.BoolVar(&o.ProxyConfiavel, "proxy-confiavel", false, "confia nos cabeçalhos X-Forwarded-* de um proxy reverso à frente do Praxis")
	fs.StringVar(&o.Home, "home", homeServicoPadrao(), "PRAXIS_HOME do serviço (diretório de estado da máquina)")
	fs.StringVar(&o.Dir, "dir", dirBinarioPadrao(), "pasta onde o binário é instalado")
	flagsServicoPlataforma(fs, o)
	return o
}

// registrarExplicitas anota quais flags foram passadas de fato. Chamar após
// fs.Parse.
func (o *opcoesServico) registrarExplicitas(fs *flag.FlagSet) {
	fs.Visit(func(f *flag.Flag) { o.explicitas[f.Name] = true })
}

// argsServe monta os argumentos do serve a embutir no registro do serviço.
// Regra da ADR (§1.2): só entra o que foi pedido explicitamente na instalação;
// o resto segue os defaults do binário — assim uma atualização do executável
// pode melhorar defaults sem reinstalar o serviço.
func (o *opcoesServico) argsServe() []string {
	var args []string
	if o.explicitas["addr"] {
		args = append(args, "-addr", o.Addr)
	}
	if o.explicitas["tls"] && o.TLS {
		args = append(args, "-tls")
	}
	if o.explicitas["tls-cert"] {
		args = append(args, "-tls-cert", o.TLSCert)
	}
	if o.explicitas["tls-key"] {
		args = append(args, "-tls-key", o.TLSKey)
	}
	if o.explicitas["proxy-confiavel"] && o.ProxyConfiavel {
		args = append(args, "-proxy-confiavel")
	}
	return args
}

// service despacha os modos de administração do serviço.
func service(ctx context.Context, args []string, out, errOut io.Writer) error {
	if len(args) == 0 {
		usoService(errOut)
		return errors.New("subcomando de service obrigatório")
	}
	resto := args[1:]
	switch args[0] {
	case "install":
		return serviceInstall(ctx, resto, out, errOut)
	case "remove", "uninstall":
		return serviceRemove(ctx, resto, out, errOut)
	case "start":
		return serviceStart(ctx, resto, out, errOut)
	case "stop":
		return serviceStop(ctx, resto, out, errOut)
	case "restart":
		return serviceRestart(ctx, resto, out, errOut)
	case "status":
		return serviceStatus(ctx, resto, out, errOut)
	case "run":
		return serviceRun(ctx, resto, out, errOut)
	case "unit":
		return serviceUnit(ctx, resto, out, errOut)
	case "-h", "-help", "--help", "help":
		usoService(out)
		return nil
	default:
		usoService(errOut)
		return fmt.Errorf("subcomando de service desconhecido: %q", args[0])
	}
}

func usoService(w io.Writer) {
	fmt.Fprintln(w, "uso: praxis service <install|remove|start|stop|restart|status|run|unit> [flags]")
	fmt.Fprintln(w, "  install  instala/atualiza o serviço (idempotente; exige Administrador/root)")
	fmt.Fprintln(w, "  remove   para e remove o serviço (mantém os dados em PRAXIS_HOME)")
	fmt.Fprintln(w, "  start    inicia o serviço          (exige Administrador/root)")
	fmt.Fprintln(w, "  stop     para o serviço            (exige Administrador/root)")
	fmt.Fprintln(w, "  restart  reinicia o serviço        (exige Administrador/root)")
	fmt.Fprintln(w, "  status   mostra se está instalado/rodando (não exige privilégio)")
	fmt.Fprintln(w, "  run      corpo do serviço (Windows: invocado pelo SCM; em console roda como serve)")
	fmt.Fprintln(w, "  unit     imprime o unit systemd / comando sc.exe equivalente, sem tocar na máquina")
	fmt.Fprintln(w, "flags de install: -addr -tls -tls-cert -tls-key -proxy-confiavel -home -dir e as da plataforma (-h para ver)")
}

// serviceUnit imprime o artefato de registro (unit systemd no Linux; comando
// sc.exe no Windows) sem tocar na máquina — para quem prefere revisar ou
// empacotar (deb/rpm/MSI) em vez de usar o `install`.
func serviceUnit(_ context.Context, args []string, out, errOut io.Writer) error {
	fs := flag.NewFlagSet("service unit", flag.ContinueOnError)
	fs.SetOutput(errOut)
	o := flagsServico(fs)
	exe := fs.String("exe", "", "caminho do executável registrado (default: o instalado em -dir)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	o.registrarExplicitas(fs)
	aplicarDefaultsLogon(o)
	if *exe == "" {
		*exe = caminhoBinarioInstalado(o.Dir)
	}
	fmt.Fprint(out, artefatoRegistro(*exe, o))
	return nil
}

// extrairFlagHome retira `-home <dir>` / `-home=<dir>` de args e devolve o valor
// e os argumentos restantes. Usada pelo `service run`, cujo resto dos argumentos
// é repassado intacto ao serve.
func extrairFlagHome(args []string) (home string, resto []string) {
	return extrairFlag(args, "home")
}

// extrairFlag retira `-<nome> <valor>` / `-<nome>=<valor>` (aceitando também
// `--<nome>`) de args e devolve o valor e os argumentos restantes.
func extrairFlag(args []string, flagNome string) (valor string, resto []string) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		nome := strings.TrimLeft(a, "-")
		if len(a)-len(nome) == 0 || len(a)-len(nome) > 2 {
			resto = append(resto, a)
			continue
		}
		switch {
		case nome == flagNome && i+1 < len(args):
			valor = args[i+1]
			i++
		case strings.HasPrefix(nome, flagNome+"="):
			valor = strings.TrimPrefix(nome, flagNome+"=")
		default:
			resto = append(resto, a)
		}
	}
	return valor, resto
}

// definirHome fixa o PRAXIS_HOME do processo quando o registro do serviço trouxe
// um -home explícito (contrato D5: instalador, registro e serviço apontam para o
// mesmo diretório). Vazio = respeita o ambiente/default do binário.
func definirHome(home string) error {
	if strings.TrimSpace(home) == "" {
		return nil
	}
	if err := os.Setenv("PRAXIS_HOME", home); err != nil {
		return fmt.Errorf("definir PRAXIS_HOME: %w", err)
	}
	return nil
}

// supervisionadoPorSystemd informa se o processo foi iniciado pelo unit do
// systemd (variável PRAXIS_MANAGED gravada no unit — D3/D9). Decide, por
// exemplo, que o log não precisa de carimbo de hora próprio: o journald carimba.
func supervisionadoPorSystemd() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("PRAXIS_MANAGED")), "systemd")
}

// novoLogger cria o logger do serve. Sob systemd, omite o atributo time — o
// journald já carimba cada linha e o carimbo duplicado só polui (D8).
func novoLogger(w io.Writer) *slog.Logger {
	var opts *slog.HandlerOptions
	if supervisionadoPorSystemd() {
		opts = &slog.HandlerOptions{
			ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
				if len(groups) == 0 && a.Key == slog.TimeKey {
					return slog.Attr{}
				}
				return a
			},
		}
	}
	return slog.New(slog.NewTextHandler(w, opts))
}

// unitSystemd gera o unit do systemd para o serviço (§1.3 da ADR). Os caminhos
// aparecem por extenso porque são contrato entre instalador, unit e binário
// (D5). Sem daemonização: Type=simple, foreground, stdout→journald, e o reinício
// pertence ao supervisor (Restart=always — D3).
func unitSystemd(exe string, o *opcoesServico) string {
	usuario := o.Usuario
	if usuario == "" {
		usuario = "root"
	}
	exec := citarUnit(exe) + " serve"
	for _, a := range o.argsServe() {
		exec += " " + citarUnit(a)
	}
	// Porta privilegiada com usuário comum: a capability permite o bind sem root
	// (só no unit de sistema; o gerenciador de usuário não concede capabilities).
	capacidades := "# Porta < 1024 com usuário comum exigiria AmbientCapabilities=CAP_NET_BIND_SERVICE."
	if _, p, err := net.SplitHostPort(o.Addr); err == nil && !o.Logon {
		if porta, err := strconv.Atoi(p); err == nil && porta > 0 && porta < 1024 && usuario != "root" {
			capacidades = "AmbientCapabilities=CAP_NET_BIND_SERVICE"
		}
	}
	// Unit de sistema: roda como User=; unit de usuário (-logon): já é o
	// usuário, o User= não existe e o alvo de boot é o default.target do
	// gerenciador de sessão.
	identidade := []string{
		"# Usuário sob o qual o Praxis roda git e os CLIs dos motores (claude, codex…);",
		"# as credenciais desses CLIs ficam no perfil deste usuário. root só se justificado.",
		"User=" + usuario,
	}
	envFile, alvo := caminhoEnvServico, "multi-user.target"
	if o.Logon {
		identidade = []string{
			"# Unit de usuário (systemctl --user): roda como você, com o seu perfil — é onde",
			"# os CLIs dos motores (claude, codex…) guardam login. Com `loginctl enable-linger`",
			"# sobe no boot e sobrevive ao logoff.",
		}
		// path (não filepath): o unit é um artefato Linux mesmo quando gerado
		// noutro SO (`service unit -logon` para revisar).
		envFile, alvo = path.Join(o.Home, "praxis.env"), "default.target"
	}
	linhas := []string{
		"[Unit]",
		"Description=" + descricaoServico,
		"Documentation=https://github.com/marcos14/praxis-autonomous",
		"# Ordena após a rede estar de pé — conforto, não garantia: o Praxis tolera",
		"# rede ausente e tenta de novo.",
		"After=network-online.target",
		"Wants=network-online.target",
		"",
		"[Service]",
		"# Processo foreground comum; o systemd supervisiona. Nunca daemonizar (D2).",
		"Type=simple",
	}
	linhas = append(linhas, identidade...)
	linhas = append(linhas,
		"# Diretório de estado — mesmo valor no instalador e no binário (D5).",
		"Environment=PRAXIS_HOME="+o.Home,
		"# Avisa o binário que há supervisor: o reinício pertence ao systemd (D3).",
		"Environment=PRAXIS_MANAGED=systemd",
		"# Variáveis extras (PATH dos CLIs dos motores, PRAXIS_PROXY_CONFIAVEL=1…); \"-\" = opcional.",
		"EnvironmentFile=-"+envFile,
		"WorkingDirectory="+o.Home,
		"ExecStart="+exec,
		"# O supervisor é dono do reinício; RestartSec evita tropeçar no StartLimit em crash-loop.",
		"Restart=always",
		"RestartSec=5",
		"# stop = SIGTERM ao processo principal (parada graciosa: drena HTTP e aguarda os",
		"# harnesses em voo); após o prazo, SIGKILL em todo o cgroup (filhos inclusos).",
		"KillMode=mixed",
		"TimeoutStopSec=30",
		capacidades,
		"# Sem ProtectHome/ProtectSystem de propósito: o serviço roda ferramentas de",
		"# desenvolvimento no perfil do usuário e escreve worktrees em PRAXIS_HOME.",
		"",
		"[Install]",
		"WantedBy="+alvo,
		"",
	)
	return strings.Join(linhas, "\n")
}

// citarUnit envolve s em aspas duplas quando tem espaço — a sintaxe do
// ExecStart do systemd exige.
func citarUnit(s string) string {
	if strings.ContainsAny(s, " \t\"") {
		return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
	}
	return s
}

// caminhoEnvServico é o EnvironmentFile opcional do unit (Linux).
const caminhoEnvServico = "/etc/praxis/praxis.env"

func simNao(b bool) string {
	if b {
		return "sim"
	}
	return "não"
}

// comTLS informa se o serviço vai responder em HTTPS (autoassinado ou próprio).
func (o *opcoesServico) comTLS() bool { return o.TLS || o.TLSCert != "" }

// urlAcesso é a URL para o operador abrir. Bind em todas as interfaces
// (0.0.0.0/::) é mostrado como "<ip-desta-máquina>".
func (o *opcoesServico) urlAcesso() string {
	esquema := "http"
	if o.comTLS() {
		esquema = "https"
	}
	host, porta, err := net.SplitHostPort(o.Addr)
	if err != nil {
		return esquema + "://" + o.Addr
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "<ip-desta-máquina>"
	} else if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	return esquema + "://" + host + ":" + porta
}

// verificarPorta tenta abrir o listener em addr e o fecha em seguida: um
// endereço inválido ou uma porta ocupada abortam a instalação ANTES de
// registrar e subir o serviço (D7) — um serviço que não consegue escutar só
// ficaria em loop de reinício no log.
func verificarPorta(addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("endereço %s indisponível (porta ocupada ou endereço inválido): %w", addr, err)
	}
	return ln.Close()
}

// hostSaude devolve a URL do /healthz alcançável a partir desta máquina para
// o bind addr: bind em todas as interfaces é consultado pelo loopback.
func hostSaude(addr string, tls bool) string {
	esquema := "http"
	if tls {
		esquema = "https"
	}
	host, porta, err := net.SplitHostPort(addr)
	if err != nil {
		return esquema + "://" + addr + "/healthz"
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	} else if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	return esquema + "://" + host + ":" + porta + "/healthz"
}

// esperarSaude consulta o /healthz até obter qualquer resposta HTTP ou o prazo
// vencer. "Rodando" para o supervisor só significa que o processo não morreu;
// isto confirma que o servidor de fato está escutando. O certificado
// autoassinado não é verificado — só se quer saber se há resposta.
func esperarSaude(addr string, comTLS bool, prazo time.Duration) error {
	url := hostSaude(addr, comTLS)
	cliente := &http.Client{
		Timeout:   2 * time.Second,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}, //nolint:gosec — só sondagem local
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	limite := time.Now().Add(prazo)
	var ultimo error
	for {
		resp, err := cliente.Get(url)
		if err == nil {
			resp.Body.Close()
			return nil
		}
		ultimo = err
		if time.Now().After(limite) {
			return fmt.Errorf("sem resposta em %s após %s: %w", url, prazo, ultimo)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// executavelAtual devolve o caminho real (sem symlinks) do binário em execução —
// a origem da cópia feita pelo instalador.
func executavelAtual() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("localizar o executável atual: %w", err)
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	return exe, nil
}

// instalarBinario copia o executável atual para destino, a menos que já seja o
// mesmo arquivo (instalador rodado a partir do binário instalado). A cópia vai
// para um arquivo temporário ao lado e é renomeada por cima — atômico no Linux;
// no Windows exige que o serviço esteja parado (o exe em uso está travado).
// Devolve true quando copiou.
func instalarBinario(origem, destino string) (bool, error) {
	if mesmoArquivo(origem, destino) {
		return false, nil
	}
	if err := os.MkdirAll(filepath.Dir(destino), 0o755); err != nil {
		return false, fmt.Errorf("criar pasta %q: %w", filepath.Dir(destino), err)
	}
	src, err := os.Open(origem)
	if err != nil {
		return false, fmt.Errorf("abrir %q: %w", origem, err)
	}
	defer src.Close()
	tmp := destino + ".novo"
	dst, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return false, fmt.Errorf("criar %q: %w", tmp, err)
	}
	if _, err := io.Copy(dst, src); err != nil {
		dst.Close()
		os.Remove(tmp)
		return false, fmt.Errorf("copiar para %q: %w", tmp, err)
	}
	if err := dst.Close(); err != nil {
		os.Remove(tmp)
		return false, err
	}
	if err := os.Rename(tmp, destino); err != nil {
		os.Remove(tmp)
		return false, fmt.Errorf("substituir %q: %w (o serviço está parado?)", destino, err)
	}
	return true, nil
}

// mesmoArquivo compara os dois caminhos (case-insensitive após Clean) e, se
// ambos existem, também por identidade do arquivo (os.SameFile).
func mesmoArquivo(a, b string) bool {
	if strings.EqualFold(filepath.Clean(a), filepath.Clean(b)) {
		return true
	}
	ia, err1 := os.Stat(a)
	ib, err2 := os.Stat(b)
	return err1 == nil && err2 == nil && os.SameFile(ia, ib)
}

// comandoServicoWindows gera o comando `sc.exe create` equivalente ao que o
// `service install` faz por API — útil para diagnóstico e para instaladores
// (MSI) que prefiram o sc.exe. O binPath registra `service run -home <dir>`:
// é o modo que fala o protocolo do SCM (registrar `serve` direto faz o SCM
// matar o processo com o erro 1053).
func comandoServicoWindows(nome, exe string, o *opcoesServico) string {
	bin := `\"` + exe + `\" service run -home \"` + o.Home + `\"`
	for _, a := range o.argsServe() {
		if strings.ContainsAny(a, " \t") {
			a = `\"` + a + `\"`
		}
		bin += " " + a
	}
	return fmt.Sprintf(`sc.exe create %s binPath= "%s" start= auto DisplayName= "%s"`, nome, bin, nomeExibicaoServico)
}
