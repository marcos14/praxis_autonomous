package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/marcos14/praxis-autonomous/internal/db"
	"github.com/marcos14/praxis-autonomous/internal/servico"
)

// service é o subcomando que instala e controla o Praxis como serviço do sistema.
//
// Antes daqui ele apenas IMPRIMIA a unit systemd ou o comando `sc.exe` para o
// operador copiar — e no Windows o comando impresso registrava um serviço que
// nunca subia (erro 1053: um binário que não conversa com o Gerenciador de
// Serviços é derrubado no start). Agora o próprio praxis faz a instalação nas
// duas plataformas, é idempotente (reinstalar troca o binário e o registro) e o
// `print` continua disponível para quem prefere revisar/versionar a unit.
//
//	praxis service install [-addr ... -home ... -usuario ...]
//	praxis service status | start | stop | restart | remove
//	praxis service print
func service(args []string, out, errOut io.Writer) error {
	if len(args) == 0 {
		usoServico(errOut)
		return errors.New("subcomando de service obrigatório (ex.: install)")
	}
	switch args[0] {
	case "install", "instalar":
		return serviceInstall(args[1:], out, errOut)
	case "remove", "uninstall", "remover":
		return serviceRemove(args[1:], out, errOut)
	case "start", "iniciar":
		return serviceControle(args[1:], out, errOut, "iniciado", servico.Iniciar)
	case "stop", "parar":
		return serviceControle(args[1:], out, errOut, "parado", servico.Parar)
	case "restart", "reiniciar":
		return serviceRestart(args[1:], out, errOut)
	case "status":
		return serviceStatus(args[1:], out, errOut)
	case "print", "imprimir":
		return servicePrint(args[1:], out, errOut)
	default:
		usoServico(errOut)
		return fmt.Errorf("subcomando de service desconhecido: %q", args[0])
	}
}

func usoServico(errOut io.Writer) {
	fmt.Fprintln(errOut, "uso: praxis service <install|remove|start|stop|restart|status|print> [flags]")
	fmt.Fprintln(errOut, "  install   instala e sobe o Praxis como serviço (exige Administrador/root)")
	fmt.Fprintln(errOut, "  remove    remove o serviço; os dados em PRAXIS_HOME ficam onde estão")
	fmt.Fprintln(errOut, "  start     sobe o serviço")
	fmt.Fprintln(errOut, "  stop      para o serviço (shutdown gracioso)")
	fmt.Fprintln(errOut, "  restart   para e sobe de novo")
	fmt.Fprintln(errOut, "  status    mostra o estado do serviço nesta máquina")
	fmt.Fprintln(errOut, "  print     imprime a unit systemd / o comando sc.exe equivalente, sem instalar")
}

// flagsServico são as flags de `service install` e `service print`. As duas
// descrevem o MESMO serviço: print tem de mostrar exatamente o que install faria,
// senão o operador que revisa a unit à mão registra outra coisa.
type flagsServico struct {
	nome     *string
	addr     *string
	home     *string
	exe      *string
	destino  *string
	usuario  *string
	senha    *string
	tls      *bool
	tlsCert  *string
	tlsKey   *string
	semCopia *bool
}

func registrarFlagsServico(fs *flag.FlagSet) *flagsServico {
	return &flagsServico{
		nome:    fs.String("nome", servico.NomePadrao, "nome do serviço (SCM no Windows, unit systemd no Linux)"),
		addr:    fs.String("addr", enderecoPadrao, "endereço de bind do serviço (o mesmo -addr do serve)"),
		home:    fs.String("home", "", "PRAXIS_HOME do serviço: banco, logs e backups (padrão: o home resolvido agora)"),
		exe:     fs.String("exe", "", "registra este executável em vez de instalar o binário atual em -destino"),
		destino: fs.String("destino", servico.DestinoPadrao(), "diretório onde o binário do serviço é instalado"),
		usuario: fs.String("usuario", contaPadrao(), "conta de logon do serviço (vazio = LocalSystem no Windows, root no Linux)"),
		senha:   fs.String("senha", "", "senha da conta de -usuario (só Windows)"),
		tls:     fs.Bool("tls", false, "sobe o serviço com HTTPS e certificado autoassinado (igual ao serve -tls)"),
		tlsCert: fs.String("tls-cert", "", "certificado TLS (PEM) próprio para o serviço"),
		tlsKey:  fs.String("tls-key", "", "chave privada TLS (PEM) do -tls-cert"),
		semCopia: fs.Bool("sem-copia", false, "registra o binário no caminho atual, sem copiá-lo para -destino "+
			"(o serviço para de subir se a pasta for movida ou apagada)"),
	}
}

// opcoes traduz as flags para o que o pacote servico precisa: caminho absoluto do
// executável já no destino definitivo, argumentos do `serve` e o PRAXIS_HOME.
func (f *flagsServico) opcoes() (servico.Opcoes, error) {
	home := strings.TrimSpace(*f.home)
	if home == "" {
		h, err := homePadraoServico(strings.TrimSpace(*f.usuario))
		if err != nil {
			return servico.Opcoes{}, fmt.Errorf("resolver o PRAXIS_HOME do serviço (use -home): %w", err)
		}
		home = h
	}
	home, err := filepath.Abs(home)
	if err != nil {
		return servico.Opcoes{}, fmt.Errorf("-home: %w", err)
	}

	exe := strings.TrimSpace(*f.exe)
	switch {
	case exe != "":
	case *f.semCopia:
		// O binário fica onde está: o serviço passa a depender desta pasta.
		p, err := os.Executable()
		if err != nil {
			return servico.Opcoes{}, fmt.Errorf("descobrir o executável atual: %w", err)
		}
		exe = p
	default:
		exe = filepath.Join(*f.destino, servico.NomeExecutavel())
	}
	exe, err = filepath.Abs(exe)
	if err != nil {
		return servico.Opcoes{}, fmt.Errorf("-exe: %w", err)
	}

	return servico.Opcoes{
		Nome:    *f.nome,
		Exe:     exe,
		Args:    argsServe(*f.addr, home, *f.tls, *f.tlsCert, *f.tlsKey),
		Home:    home,
		Usuario: strings.TrimSpace(*f.usuario),
		Senha:   *f.senha,
	}, nil
}

// argsServe monta a linha de comando do serviço. O -home vai explícito de
// propósito: um serviço do Windows roda como LocalSystem (ou outra conta), e
// LOCALAPPDATA — de onde o `praxis serve` interativo resolve o PRAXIS_HOME —
// aponta para o perfil DESSA conta. Sem o -home, o serviço abriria um banco vazio
// em C:\Windows\system32\config\systemprofile em vez do banco do operador.
func argsServe(addr, home string, autoTLS bool, tlsCert, tlsKey string) []string {
	args := []string{"serve", "-addr", addr, "-home", home}
	if autoTLS {
		args = append(args, "-tls")
	}
	if tlsCert != "" {
		args = append(args, "-tls-cert", tlsCert)
	}
	if tlsKey != "" {
		args = append(args, "-tls-key", tlsKey)
	}
	return args
}

// contaPadrao é a conta de logon padrão do serviço. No Linux, quando o install
// roda por sudo, é o usuário que chamou o sudo: o Praxis dispara harnesses (claude
// e afins) e faz git/ssh, que vivem na configuração DESSA conta — rodar como root
// acharia um ~/.claude e um ~/.ssh vazios. No Windows fica vazio (LocalSystem),
// porque registrar com outra conta exige senha.
func contaPadrao() string {
	if u := strings.TrimSpace(os.Getenv("SUDO_USER")); u != "" && u != "root" {
		return u
	}
	return ""
}

// homePadraoServico é o PRAXIS_HOME que o serviço vai usar quando -home não é
// passado: o da CONTA que vai rodar o serviço — sob sudo, os.UserConfigDir()
// responderia com o home do root, e para a conta de sistema dedicada (Fase B) o
// home é o dela (/var/lib/praxis). No Windows a conta é do SCM (LocalSystem ou
// domínio\usuário) e o lookup não se aplica — vale o home resolvido agora.
func homePadraoServico(usuario string) (string, error) {
	if h := strings.TrimSpace(os.Getenv("PRAXIS_HOME")); h != "" {
		return h, nil
	}
	if usuario != "" && runtime.GOOS != "windows" {
		if u, err := user.Lookup(usuario); err == nil && u.HomeDir != "" {
			// Mesmo layout de os.UserConfigDir no Linux (XDG: ~/.config).
			return filepath.Join(u.HomeDir, ".config", "praxis"), nil
		}
	}
	return db.PraxisHome()
}

func serviceInstall(args []string, out, errOut io.Writer) error {
	fs := flag.NewFlagSet("service install", flag.ContinueOnError)
	fs.SetOutput(errOut)
	f := registrarFlagsServico(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if ok, comoFazer := servico.Privilegiado(); !ok {
		return fmt.Errorf("instalar o serviço exige privilégios administrativos: %s", comoFazer)
	}
	// Fase B: no Linux o serviço NUNCA roda como root (o claude recusa execução
	// autônoma com UID 0). Sem -usuario e sem SUDO_USER — login root de verdade —
	// o install cria/reusa a conta de sistema dedicada e registra a unit com ela.
	if runtime.GOOS != "windows" && strings.TrimSpace(*f.usuario) == "" {
		conta, criada, err := servico.GarantirContaSistema()
		if err != nil {
			return fmt.Errorf("o serviço não roda como root e não há SUDO_USER: %w", err)
		}
		*f.usuario = conta
		if criada {
			fmt.Fprintf(out, "conta de sistema %q criada para o serviço (home em /var/lib/%s)\n", conta, conta)
		}
	}
	o, err := f.opcoes()
	if err != nil {
		return err
	}
	avisar := func(msg string) { fmt.Fprintln(errOut, "aviso:", msg) }

	// PRAXIS_HOME antes do serviço subir: é onde o banco, os logs e os backups
	// ficam, e o serviço já sobe procurando por eles.
	if err := os.MkdirAll(o.Home, 0o755); err != nil {
		return fmt.Errorf("criar %s: %w", o.Home, err)
	}
	if err := ajustarDono(o.Home, o.Usuario); err != nil {
		avisar(fmt.Sprintf("ajustar o dono de %s para %q: %v (o serviço pode não conseguir escrever)", o.Home, o.Usuario, err))
	}

	// Binário no destino definitivo. Um serviço apontando para a pasta de onde
	// alguém descompactou o release para de subir no dia da limpeza.
	if *f.exe == "" && !*f.semCopia {
		origem, err := os.Executable()
		if err != nil {
			return fmt.Errorf("descobrir o executável atual: %w", err)
		}
		if !servico.MesmoCaminho(origem, o.Exe) {
			// Reinstalação com o serviço no ar: o arquivo em uso não pode ser
			// substituído sem parar quem o roda.
			if st, err := servico.Consultar(o.Nome); err == nil && st.Rodando && servico.MesmoCaminho(st.Exe, o.Exe) {
				fmt.Fprintf(out, "parando %q para trocar o binário...\n", o.Nome)
				if err := servico.Parar(o.Nome); err != nil {
					avisar(err.Error())
				}
			}
			if err := servico.CopiarBinario(origem, o.Exe); err != nil {
				return fmt.Errorf("instalar o binário em %s: %w", o.Exe, err)
			}
			fmt.Fprintf(out, "binário instalado em %s\n", o.Exe)
		}
	}

	if err := servico.Instalar(o, avisar); err != nil {
		return err
	}
	fmt.Fprintf(out, "serviço %q instalado e no ar.\n", o.Nome)
	fmt.Fprintf(out, "  executável: %s\n", servico.LinhaComando(o.Exe, o.Args))
	fmt.Fprintf(out, "  dados:      %s\n", o.Home)
	fmt.Fprintf(out, "  conta:      %s\n", contaEmTexto(o.Usuario))
	fmt.Fprintln(out)
	for _, linha := range proximosPassos(o) {
		fmt.Fprintln(out, linha)
	}
	return nil
}

// contaEmTexto nomeia a conta padrão de cada plataforma, para o operador não ficar
// olhando um campo vazio no resumo da instalação.
func contaEmTexto(usuario string) string {
	if usuario != "" {
		return usuario
	}
	if runtime.GOOS == "windows" {
		return "LocalSystem"
	}
	return "root"
}

// proximosPassos são as linhas de "e agora?" ao fim da instalação: onde ver o log,
// e — no Windows com LocalSystem — o aviso que evita o chamado mais provável.
func proximosPassos(o servico.Opcoes) []string {
	l := []string{"Próximos passos:"}
	if runtime.GOOS == "windows" {
		l = append(l,
			fmt.Sprintf("  estado:  praxis service status -nome %s", o.Nome),
			fmt.Sprintf("  log:     %s", filepath.Join(o.Home, "logs", nomeLogServico)),
		)
		if o.Usuario == "" {
			l = append(l, "",
				"ATENÇÃO: o serviço roda como LocalSystem, que NÃO vê a configuração da sua conta",
				"(credenciais do harness em %USERPROFILE%\\.claude, chaves SSH e git config). Se as",
				"execuções falharem por autenticação, reinstale com a sua conta:",
				`  praxis service install -usuario ".\SEU_USUARIO" -senha ...`,
				`(a conta precisa do direito "Fazer logon como serviço" em secpol.msc)`)
		}
		return l
	}
	return append(l,
		fmt.Sprintf("  estado:  systemctl status %s", o.Nome),
		fmt.Sprintf("  log:     journalctl -u %s -f", o.Nome),
		fmt.Sprintf("  CLI:     export PRAXIS_HOME=%s  (para `praxis usuario` falar com o mesmo banco)", o.Home))
}

// ajustarDono entrega PRAXIS_HOME à conta que vai rodar o serviço. Sem isso, um
// -home criado pelo root (ex.: /var/lib/praxis) ficaria ilegível para o User= da
// unit e o serviço subiria só para falhar ao abrir o banco. No Windows não se
// aplica: os.Chown não existe lá, e LocalSystem escreve em qualquer lugar.
func ajustarDono(caminho, usuario string) error {
	if runtime.GOOS == "windows" || usuario == "" {
		return nil
	}
	u, err := user.Lookup(usuario)
	if err != nil {
		return err
	}
	uid, err := strconv.Atoi(u.Uid)
	if err != nil {
		return err
	}
	gid, err := strconv.Atoi(u.Gid)
	if err != nil {
		return err
	}
	return os.Chown(caminho, uid, gid)
}

func serviceRemove(args []string, out, errOut io.Writer) error {
	fs := flag.NewFlagSet("service remove", flag.ContinueOnError)
	fs.SetOutput(errOut)
	nome := fs.String("nome", servico.NomePadrao, "nome do serviço")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if ok, comoFazer := servico.Privilegiado(); !ok {
		return fmt.Errorf("remover o serviço exige privilégios administrativos: %s", comoFazer)
	}
	st, err := servico.Consultar(*nome)
	if err != nil {
		return err
	}
	if err := servico.Remover(*nome, func(msg string) { fmt.Fprintln(errOut, "aviso:", msg) }); err != nil {
		return err
	}
	fmt.Fprintf(out, "serviço %q removido.\n", *nome)
	// Nada é apagado além do registro: o banco é o trabalho do operador, e o
	// binário pode ter sido instalado por ele. Só dizemos onde ficaram.
	if st.Exe != "" {
		fmt.Fprintf(out, "o executável continua em %s (apague se não for mais usar)\n", st.Exe)
	}
	fmt.Fprintln(out, "os dados em PRAXIS_HOME (banco, backups, logs) foram preservados")
	return nil
}

func serviceControle(args []string, out, errOut io.Writer, verbo string, acao func(string) error) error {
	fs := flag.NewFlagSet("service "+verbo, flag.ContinueOnError)
	fs.SetOutput(errOut)
	nome := fs.String("nome", servico.NomePadrao, "nome do serviço")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := acao(*nome); err != nil {
		return err
	}
	fmt.Fprintf(out, "serviço %q %s.\n", *nome, verbo)
	return nil
}

func serviceRestart(args []string, out, errOut io.Writer) error {
	fs := flag.NewFlagSet("service restart", flag.ContinueOnError)
	fs.SetOutput(errOut)
	nome := fs.String("nome", servico.NomePadrao, "nome do serviço")
	if err := fs.Parse(args); err != nil {
		return err
	}
	// Parar um serviço já parado não é erro no pacote servico, mas um serviço
	// inexistente é — e aí o start seguinte falharia com a mesma mensagem.
	if err := servico.Parar(*nome); err != nil {
		fmt.Fprintln(errOut, "aviso:", err)
	}
	if err := servico.Iniciar(*nome); err != nil {
		return err
	}
	fmt.Fprintf(out, "serviço %q reiniciado.\n", *nome)
	return nil
}

func serviceStatus(args []string, out, errOut io.Writer) error {
	fs := flag.NewFlagSet("service status", flag.ContinueOnError)
	fs.SetOutput(errOut)
	nome := fs.String("nome", servico.NomePadrao, "nome do serviço")
	if err := fs.Parse(args); err != nil {
		return err
	}
	st, err := servico.Consultar(*nome)
	if err != nil {
		return err
	}
	if !st.Instalado {
		fmt.Fprintf(out, "serviço %q: não instalado (rode `praxis service install`)\n", *nome)
		return nil
	}
	fmt.Fprintf(out, "serviço %q\n", st.Nome)
	fmt.Fprintf(out, "  estado:     %s\n", primeiroNaoVazio(st.Detalhe, seEntao(st.Rodando, "rodando", "parado")))
	fmt.Fprintf(out, "  no boot:    %s\n", seEntao(st.Habilitado, "sim", "não"))
	fmt.Fprintf(out, "  comando:    %s\n", primeiroNaoVazio(st.LinhaComando, "(desconhecido)"))
	fmt.Fprintf(out, "  conta:      %s\n", contaEmTexto(st.Conta))
	return nil
}

func servicePrint(args []string, out, errOut io.Writer) error {
	fs := flag.NewFlagSet("service print", flag.ContinueOnError)
	fs.SetOutput(errOut)
	f := registrarFlagsServico(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	o, err := f.opcoes()
	if err != nil {
		return err
	}
	// Sempre para o sistema que está rodando: gerar a unit de Linux a partir do
	// Windows imprimiria caminhos "C:\Program Files\..." dentro dela.
	if runtime.GOOS == "windows" {
		fmt.Fprintln(out, "# Equivalente a `praxis service install`. Cole no Prompt de Comando (cmd.exe)")
		fmt.Fprintln(out, "# como Administrador — o escape \\\" das aspas é o do cmd, não o do PowerShell:")
		fmt.Fprintln(out, servico.ComandoSC(o))
		fmt.Fprintln(out, "# Um serviço criado assim precisa deste MESMO binário: é o `serve` que detecta")
		fmt.Fprintln(out, "# ter sido subido pelo SCM e roda sob o handler de controle. Registrar outro")
		fmt.Fprintln(out, "# executável qualquer resulta no erro 1053 no start.")
		return nil
	}
	fmt.Fprintf(out, "# Salve como /etc/systemd/system/%s.service (como root) e rode:\n", o.Nome)
	fmt.Fprintf(out, "#   systemctl daemon-reload && systemctl enable --now %s\n", o.Nome)
	fmt.Fprintln(out, "# Ou deixe o praxis fazer isso: sudo praxis service install")
	fmt.Fprintln(out)
	fmt.Fprint(out, servico.UnitSystemd(o))
	return nil
}

func primeiroNaoVazio(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}

func seEntao(cond bool, sim, nao string) string {
	if cond {
		return sim
	}
	return nao
}
