//go:build !windows

package main

// Casca do Linux (ADR 0001, §1.3): o "modo serviço" não existe como código —
// é o `serve` comum, foreground, sob um unit do systemd (Type=simple,
// Restart=always). Nunca daemonizar. Este arquivo cuida do registro: copiar o
// binário para um lugar estável, escrever o unit, daemon-reload, enable e
// restart (nunca `enable --now`, que deixaria a versão antiga em memória — D7).
//
// Dois modos, mesma mecânica:
//   - sistema  (default): unit em /etc/systemd/system, exige root, roda como o
//     usuário de -usuario, estado em /var/lib/praxis.
//   - usuário  (-logon):  unit em ~/.config/systemd/user, `systemctl --user`,
//     sem root, roda como você com seu perfil (os CLIs dos motores guardam
//     login lá), estado em ~/.config/praxis — o mesmo default do `serve`
//     manual, nada a migrar. Com `loginctl enable-linger` sobe no boot e
//     sobrevive ao logoff. É o "processo residente por usuário" que a ADR
//     separa do agente de máquina (§4).

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Caminhos do contrato (D5): estado em /var/lib, config em /etc, binário em
// /usr/local/bin. Aparecem por extenso no unit gerado, com o mesmo valor.
func homeServicoPadrao() string                 { return "/var/lib/praxis" }
func dirBinarioPadrao() string                  { return "/usr/local/bin" }
func caminhoBinarioInstalado(dir string) string { return filepath.Join(dir, "praxis") }
func caminhoUnitDe(nome string) string          { return "/etc/systemd/system/" + nome + ".service" }

// caminhoUnitUsuarioDe é o unit de usuário dentro do home dado.
func caminhoUnitUsuarioDe(homeDir, nome string) string {
	return filepath.Join(homeDir, ".config", "systemd", "user", nome+".service")
}

func flagsServicoPlataforma(fs *flag.FlagSet, o *opcoesServico) {
	fs.StringVar(&o.Usuario, "usuario", usuarioServicoPadrao(),
		"usuário do unit de sistema (User=). Default: quem chamou o sudo — reaproveita os logins dos CLIs dos motores nesse perfil — ou root")
	fs.BoolVar(&o.Logon, "logon", false,
		"em vez de unit de sistema, unit de usuário do systemd (systemctl --user): sem root, com o seu perfil "+
			"(PATH, logins dos motores, repositórios). Com `loginctl enable-linger` sobe no boot e sobrevive ao logoff. "+
			"Defaults: -home ~/.config/praxis, -dir ~/.local/bin")
}

// homeLogonPadrao é o PRAXIS_HOME do modo usuário: o mesmo default do `serve`
// rodado à mão (db.PraxisHome → <UserConfigDir>/praxis), de propósito.
func homeLogonPadrao() string {
	if base, err := os.UserConfigDir(); err == nil && base != "" {
		return filepath.Join(base, "praxis")
	}
	return homeServicoPadrao()
}

// dirLogonPadrao é a pasta do binário no modo usuário: ~/.local/bin.
func dirLogonPadrao() string {
	if h, err := os.UserHomeDir(); err == nil && h != "" {
		return filepath.Join(h, ".local", "bin")
	}
	return dirBinarioPadrao()
}

// aplicarDefaultsLogon troca os defaults de máquina pelos de usuário quando
// -logon foi pedido e o operador não fixou -home/-dir.
func aplicarDefaultsLogon(o *opcoesServico) {
	if !o.Logon {
		return
	}
	if !o.explicitas["home"] {
		o.Home = homeLogonPadrao()
	}
	if !o.explicitas["dir"] {
		o.Dir = dirLogonPadrao()
	}
}

// usuarioServicoPadrao: sob `sudo`, o usuário que invocou; senão root. Rodar
// como o usuário de desenvolvimento é a escolha pragmática — os CLIs dos
// motores (claude, codex…) guardam credenciais no perfil dele.
func usuarioServicoPadrao() string {
	if u := strings.TrimSpace(os.Getenv("SUDO_USER")); u != "" && u != "root" {
		return u
	}
	return "root"
}

func artefatoRegistro(exe string, o *opcoesServico) string {
	if o.Logon {
		h, _ := os.UserHomeDir()
		return "# Salve como " + caminhoUnitUsuarioDe(h, nomeServico) + " (sem root) e rode:\n" +
			"#   systemctl --user daemon-reload && systemctl --user enable " + nomeServico + " && systemctl --user restart " + nomeServico + "\n" +
			"#   sudo loginctl enable-linger $USER   # para subir no boot e sobreviver ao logoff\n\n" +
			unitSystemd(exe, o)
	}
	return "# Salve como " + caminhoUnitDe(nomeServico) + " (como root) e rode:\n" +
		"#   systemctl daemon-reload && systemctl enable " + nomeServico + " && systemctl restart " + nomeServico + "\n\n" +
		unitSystemd(exe, o)
}

// ---------------------------------------------------------------------------
// systemd: sistema × usuário
// ---------------------------------------------------------------------------

// modoSystemd seleciona o gerenciador alvo: o de sistema ou o do usuário
// (`systemctl --user`). Toda a mecânica de registro é a mesma; muda o prefixo,
// a pasta do unit e a exigência de root.
type modoSystemd struct{ usuario bool }

var (
	modoSistema = modoSystemd{usuario: false}
	modoUsuario = modoSystemd{usuario: true}
)

func (m modoSystemd) String() string {
	if m.usuario {
		return "unit de usuário (systemctl --user)"
	}
	return "serviço do sistema"
}

func (m modoSystemd) args(a ...string) []string {
	if m.usuario {
		return append([]string{"--user"}, a...)
	}
	return a
}

// systemctl executa `systemctl [--user] args...` e devolve a saída; erro
// inclui a saída.
func (m modoSystemd) systemctl(a ...string) (string, error) {
	args := m.args(a...)
	saida, err := exec.Command("systemctl", args...).CombinedOutput()
	if err != nil {
		return string(saida), fmt.Errorf("systemctl %s: %w\n%s", strings.Join(args, " "), err, strings.TrimSpace(string(saida)))
	}
	return string(saida), nil
}

func (m modoSystemd) isActive(nome string) string {
	saida, _ := exec.Command("systemctl", m.args("is-active", nome)...).Output()
	return strings.TrimSpace(string(saida))
}

func (m modoSystemd) unitAtivo(nome string) bool { return m.isActive(nome) == "active" }

// esperarAtivo consulta is-active até o unit ficar active ou o prazo vencer;
// `failed` é falha imediata.
func (m modoSystemd) esperarAtivo(nome string, prazo time.Duration) error {
	limite := time.Now().Add(prazo)
	for {
		switch estado := m.isActive(nome); estado {
		case "active":
			return nil
		case "failed":
			return fmt.Errorf("serviço %q entrou em failed", nome)
		default:
			if time.Now().After(limite) {
				return fmt.Errorf("serviço %q não ficou ativo em %s (estado: %s)", nome, prazo, estado)
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// caminhoUnit é onde o unit deste modo vive para o processo atual.
func (m modoSystemd) caminhoUnit(nome string) string {
	if m.usuario {
		h, _ := os.UserHomeDir()
		return caminhoUnitUsuarioDe(h, nome)
	}
	return caminhoUnitDe(nome)
}

func (m modoSystemd) journalctl(nome string) string {
	if m.usuario {
		return "journalctl --user -u " + nome
	}
	return "journalctl -u " + nome
}

// exigirPrivilegio: unit de sistema exige root; unit de usuário exige NÃO ser
// root (sob sudo o home e o gerenciador seriam os do root).
func (m modoSystemd) exigirPrivilegio() error {
	if m.usuario {
		if os.Geteuid() == 0 {
			return errors.New("-logon é o modo do seu usuário: rode sem sudo")
		}
		return nil
	}
	if os.Geteuid() != 0 {
		return errors.New("execute como root (ex.: sudo praxis service install …) — ou use -logon para instalar só para o seu usuário, sem root")
	}
	return nil
}

// exigirGerenciador confere que o systemd alvo está acessível. Para o modo
// usuário, o gerenciador de sessão precisa estar de pé (não há em contêiner ou
// WSL sem systemd).
func (m modoSystemd) exigirGerenciador() error {
	if _, err := exec.LookPath("systemctl"); err != nil {
		return errors.New("systemctl não encontrado: só o systemd é suportado. Use `praxis service unit` para gerar o unit, " +
			"ou rode `praxis serve` sob o supervisor da sua distro")
	}
	if m.usuario {
		if _, err := m.systemctl("show-environment"); err != nil {
			return fmt.Errorf("gerenciador de usuário do systemd indisponível (contêiner/WSL sem systemd, ou sessão sem logind?): %w\n"+
				"use `praxis service unit -logon` para gerar o unit e aplicá-lo à mão", err)
		}
	}
	return nil
}

// modoInstalado descobre o que está registrado nesta máquina para o processo
// atual: unit de sistema (nome atual ou legado — D6) tem precedência; senão o
// unit de usuário.
func modoInstalado() (m modoSystemd, nome string, ok bool) {
	for _, mm := range []modoSystemd{modoSistema, modoUsuario} {
		for _, n := range append([]string{nomeServico}, nomesServicoLegados...) {
			if existe(mm.caminhoUnit(n)) {
				return mm, n, true
			}
		}
	}
	return modoSistema, nomeServico, false
}

// resolverUsuario devolve uid/gid e home do usuário do unit.
func resolverUsuario(nome string) (uid, gid int, homeDir string, err error) {
	u, err := user.Lookup(nome)
	if err != nil {
		return 0, 0, "", fmt.Errorf("usuário %q não existe (crie-o ou informe -usuario): %w", nome, err)
	}
	if uid, err = strconv.Atoi(u.Uid); err != nil {
		return 0, 0, "", err
	}
	if gid, err = strconv.Atoi(u.Gid); err != nil {
		return 0, 0, "", err
	}
	return uid, gid, u.HomeDir, nil
}

func existe(caminho string) bool {
	_, err := os.Stat(caminho)
	return err == nil
}

// modeloEnvServico é o EnvironmentFile inicial do unit de sistema (só comentários).
const modeloEnvServico = `# Variáveis de ambiente do serviço praxis, lidas pelo unit via EnvironmentFile.
# Uma por linha, CHAVE=valor. Depois de editar: systemctl restart praxis
#
# Confia nos cabeçalhos X-Forwarded-* de um proxy reverso (mesmo que -proxy-confiavel):
#PRAXIS_PROXY_CONFIAVEL=1
#
# O systemd NÃO herda o PATH do shell. Se os CLIs dos motores (claude, codex, code…)
# estão em ~/.local/bin ou no npm global, acrescente aqui:
#PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin:/home/usuario/.local/bin
`

// modeloEnvUsuario é o EnvironmentFile inicial do unit de usuário. Grava o
// PATH do shell de quem rodou o install: o `systemd --user` não herda o PATH
// do shell, e é assim que `claude` em ~/.local/bin ou no npm global some.
func modeloEnvUsuario(path string) string {
	return `# Variáveis de ambiente do praxis (unit de usuário), lidas via EnvironmentFile.
# Uma por linha, CHAVE=valor. Depois de editar: systemctl --user restart praxis
#
# PATH copiado do shell no momento do install — o systemd --user não herda o PATH
# do shell, e os CLIs dos motores (claude, codex, code…) costumam viver em
# ~/.local/bin ou no npm global. Reinstalar não sobrescreve este arquivo.
PATH=` + path + `
#
# Confia nos cabeçalhos X-Forwarded-* de um proxy reverso (mesmo que -proxy-confiavel):
#PRAXIS_PROXY_CONFIAVEL=1
`
}

// caminhoEnvUsuario é o EnvironmentFile do unit de usuário: dentro do PRAXIS_HOME.
func caminhoEnvUsuario(home string) string { return filepath.Join(home, "praxis.env") }

// ---------------------------------------------------------------------------
// install
// ---------------------------------------------------------------------------

func serviceInstall(_ context.Context, args []string, out, errOut io.Writer) error {
	fs := flag.NewFlagSet("service install", flag.ContinueOnError)
	fs.SetOutput(errOut)
	o := flagsServico(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	o.registrarExplicitas(fs)
	aplicarDefaultsLogon(o)
	m := modoSistema
	if o.Logon {
		if o.explicitas["usuario"] {
			return errors.New("-logon roda com o usuário atual; não combine com -usuario")
		}
		m = modoUsuario
	}
	if err := m.exigirPrivilegio(); err != nil {
		return err
	}
	if err := m.exigirGerenciador(); err != nil {
		return err
	}
	home, err := filepath.Abs(o.Home)
	if err != nil {
		return err
	}
	o.Home = home
	dir, err := filepath.Abs(o.Dir)
	if err != nil {
		return err
	}
	origem, err := executavelAtual()
	if err != nil {
		return err
	}
	destino := caminhoBinarioInstalado(dir)

	// Os dois modos não convivem (brigariam pela porta e, com o mesmo home,
	// pelo banco). Quem instala é avisado para remover o outro primeiro.
	uid, gid := 0, 0
	if m.usuario {
		if existe(caminhoUnitDe(nomeServico)) {
			return errors.New("já existe o serviço do sistema " + caminhoUnitDe(nomeServico) + "; remova-o antes (sudo praxis service remove)")
		}
	} else {
		var homeUsuario string
		if uid, gid, homeUsuario, err = resolverUsuario(o.Usuario); err != nil {
			return err
		}
		if unitUsuario := caminhoUnitUsuarioDe(homeUsuario, nomeServico); existe(unitUsuario) {
			return errors.New("já existe o unit de usuário " + unitUsuario + "; remova-o antes, sem sudo (praxis service remove)")
		}
	}

	// D6: unit sob nome legado é desativado e removido, não deixado conviver.
	for _, n := range nomesServicoLegados {
		if !existe(m.caminhoUnit(n)) {
			continue
		}
		fmt.Fprintf(out, "unit legado %q encontrado: desativando e removendo antes de registrar %q\n", n, nomeServico)
		if _, err := m.systemctl("disable", "--now", n); err != nil {
			fmt.Fprintf(errOut, "aviso: %v\n", err)
		}
		if err := os.Remove(m.caminhoUnit(n)); err != nil {
			return err
		}
	}

	// Porta livre? Um serviço já ativo ocupa a própria porta: para antes de
	// conferir e, se a checagem falhar, volta com ele — a instalação falhou, não
	// a máquina. Falha de configuração aborta ANTES de tocar em binário e unit (D7).
	unit := m.caminhoUnit(nomeServico)
	estavaAtivo := existe(unit) && m.unitAtivo(nomeServico)
	if estavaAtivo {
		if _, err := m.systemctl("stop", nomeServico); err != nil {
			return err
		}
	}
	if err := verificarPorta(o.Addr); err != nil {
		if estavaAtivo {
			_, _ = m.systemctl("start", nomeServico)
		}
		return err
	}

	// Binário: cópia + rename atômico. O processo antigo (se rodando) segue com
	// o inode antigo até o restart abaixo.
	copiado, err := instalarBinario(origem, destino)
	if err != nil {
		return err
	}
	if copiado {
		fmt.Fprintf(out, "binário copiado para %s\n", destino)
	} else {
		fmt.Fprintf(out, "binário já em %s\n", destino)
	}

	// Diretórios do contrato (D5) e EnvironmentFile (criado uma vez; edições
	// do operador são preservadas).
	if err := os.MkdirAll(home, 0o750); err != nil {
		return fmt.Errorf("criar PRAXIS_HOME %q: %w", home, err)
	}
	var envFile string
	if m.usuario {
		envFile = caminhoEnvUsuario(home)
		if !existe(envFile) {
			if err := os.WriteFile(envFile, []byte(modeloEnvUsuario(os.Getenv("PATH"))), 0o600); err != nil {
				return fmt.Errorf("criar %s: %w", envFile, err)
			}
			fmt.Fprintf(out, "%s criado com o PATH do seu shell (edite se precisar)\n", envFile)
		}
	} else {
		// O home pertence ao usuário do unit; o env file é root:grupo 0640
		// (pode conter variáveis sensíveis).
		if err := os.Chown(home, uid, gid); err != nil {
			return fmt.Errorf("chown %q: %w", home, err)
		}
		envFile = caminhoEnvServico
		if err := os.MkdirAll(filepath.Dir(envFile), 0o755); err != nil {
			return err
		}
		if !existe(envFile) {
			if err := os.WriteFile(envFile, []byte(modeloEnvServico), 0o640); err != nil {
				return fmt.Errorf("criar %s: %w", envFile, err)
			}
			_ = os.Chown(envFile, 0, gid)
			fmt.Fprintf(out, "%s criado (só comentários; edite se precisar)\n", envFile)
		}
	}

	// Unit: reescrito só se mudou (idempotente — D7). daemon-reload é sempre
	// seguro; sem ele o systemd seguiria com a versão em cache.
	conteudo := unitSystemd(destino, o)
	if atual, err := os.ReadFile(unit); err != nil || string(atual) != conteudo {
		if err := os.MkdirAll(filepath.Dir(unit), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(unit, []byte(conteudo), 0o644); err != nil {
			return fmt.Errorf("escrever %s: %w", unit, err)
		}
		fmt.Fprintf(out, "unit escrito em %s\n", unit)
	} else {
		fmt.Fprintf(out, "unit em %s já atualizado\n", unit)
	}
	if _, err := m.systemctl("daemon-reload"); err != nil {
		return err
	}
	if _, err := m.systemctl("enable", nomeServico); err != nil {
		return err
	}
	// restart, não `enable --now`: numa reinstalação o serviço ativo continuaria
	// rodando o binário antigo em memória (D7).
	if _, err := m.systemctl("restart", nomeServico); err != nil {
		return err
	}
	if err := m.esperarAtivo(nomeServico, 10*time.Second); err != nil {
		return fmt.Errorf("%w — veja: %s -n 50", err, m.journalctl(nomeServico))
	}
	// "active" só diz que o processo não morreu; confirma que o HTTP responde.
	if err := esperarSaude(o.Addr, o.comTLS(), 20*time.Second); err != nil {
		fmt.Fprintf(errOut, "aviso: serviço ativo mas ainda sem resposta HTTP (%v); veja: %s -n 50\n", err, m.journalctl(nomeServico))
	} else {
		fmt.Fprintf(out, "serviço respondendo em %s\n", o.urlAcesso())
	}

	usuario := o.Usuario
	if m.usuario {
		if u, err := user.Current(); err == nil {
			usuario = u.Username
		}
	}
	fmt.Fprintf(out, "\nmodo:     %s\nserviço:  %s (%s)\nbinário:  %s\nhome:     %s\nusuário:  %s\nendereço: %s (TLS: %s)\nacesso:   %s\nunit:     %s\nenv:      %s\nlogs:     %s -f\nstatus:   praxis service status\n",
		m, nomeServico, nomeExibicaoServico, destino, home, usuario, o.Addr, simNao(o.comTLS()), o.urlAcesso(), unit, envFile, m.journalctl(nomeServico))
	if m.usuario {
		avisarLinger(usuario, out)
	}
	return nil
}

// lingerAtivo consulta o logind: com linger, o gerenciador de usuário sobe no
// boot e sobrevive ao logoff. Devolve "yes"/"no" ou "" se não deu para saber.
func lingerAtivo(usuario string) string {
	saida, err := exec.Command("loginctl", "show-user", usuario, "-p", "Linger", "--value").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(saida))
}

// avisarLinger imprime a situação do linger e, se faltar, o comando (que pode
// exigir sudo — o install não eleva sozinho).
func avisarLinger(usuario string, out io.Writer) {
	switch lingerAtivo(usuario) {
	case "yes":
		fmt.Fprintln(out, "linger:   ativo — sobe no boot e sobrevive ao logoff")
	case "no":
		fmt.Fprintf(out, "linger:   INATIVO — hoje só roda enquanto houver sessão sua aberta. Para subir no boot e sobreviver ao logoff:\n          sudo loginctl enable-linger %s\n", usuario)
	default:
		fmt.Fprintf(out, "linger:   não foi possível consultar (loginctl). Para subir no boot e sobreviver ao logoff: sudo loginctl enable-linger %s\n", usuario)
	}
}

// ---------------------------------------------------------------------------
// remove / start / stop / restart / status / run
// ---------------------------------------------------------------------------

func serviceRemove(_ context.Context, args []string, out, errOut io.Writer) error {
	fs := flag.NewFlagSet("service remove", flag.ContinueOnError)
	fs.SetOutput(errOut)
	if err := fs.Parse(args); err != nil {
		return err
	}
	m, nome, instalado := modoInstalado()
	if !instalado {
		fmt.Fprintf(out, "serviço %q não está instalado (nem unit de sistema, nem de usuário) — nada a fazer\n", nomeServico)
		return nil
	}
	if err := m.exigirPrivilegio(); err != nil {
		return err
	}
	if err := m.exigirGerenciador(); err != nil {
		return err
	}
	if _, err := m.systemctl("disable", "--now", nome); err != nil {
		fmt.Fprintf(errOut, "aviso: %v\n", err)
	}
	if err := os.Remove(m.caminhoUnit(nome)); err != nil {
		return err
	}
	if _, err := m.systemctl("daemon-reload"); err != nil {
		return err
	}
	dados := homeServicoPadrao() + ", " + filepath.Dir(caminhoEnvServico)
	if m.usuario {
		dados = homeLogonPadrao()
	}
	fmt.Fprintf(out, "%s %q removido; dados em %s e o binário foram mantidos\n", m, nome, dados)
	return nil
}

func serviceStart(_ context.Context, args []string, out, errOut io.Writer) error {
	return acaoSystemctl("start", args, out, errOut)
}

func serviceStop(_ context.Context, args []string, out, errOut io.Writer) error {
	return acaoSystemctl("stop", args, out, errOut)
}

func serviceRestart(_ context.Context, args []string, out, errOut io.Writer) error {
	return acaoSystemctl("restart", args, out, errOut)
}

func acaoSystemctl(verbo string, args []string, out, errOut io.Writer) error {
	fs := flag.NewFlagSet("service "+verbo, flag.ContinueOnError)
	fs.SetOutput(errOut)
	if err := fs.Parse(args); err != nil {
		return err
	}
	m, nome, instalado := modoInstalado()
	if !instalado {
		return fmt.Errorf("serviço %q não está instalado (sudo praxis service install, ou praxis service install -logon)", nomeServico)
	}
	if err := m.exigirPrivilegio(); err != nil {
		return err
	}
	if _, err := m.systemctl(verbo, nome); err != nil {
		return err
	}
	fmt.Fprintf(out, "serviço %q: %s\n", nome, m.isActive(nome))
	return nil
}

// serviceStatus consulta o unit via `systemctl show` — não exige root (D4).
func serviceStatus(_ context.Context, args []string, out, errOut io.Writer) error {
	fs := flag.NewFlagSet("service status", flag.ContinueOnError)
	fs.SetOutput(errOut)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := modoSistema.exigirGerenciador(); err != nil {
		return err
	}
	m, nome, instalado := modoInstalado()
	if !instalado {
		fmt.Fprintf(out, "serviço %q: não instalado (nem unit de sistema, nem de usuário, nem sob nomes legados)\n", nomeServico)
		return nil
	}
	saida, err := m.systemctl("show", nome, "-p", "ActiveState,SubState,UnitFileState,MainPID,ExecStart,User")
	if err != nil {
		return err
	}
	props := map[string]string{}
	sc := bufio.NewScanner(strings.NewReader(saida))
	for sc.Scan() {
		if k, v, ok := strings.Cut(sc.Text(), "="); ok {
			props[k] = v
		}
	}
	usuario := props["User"]
	if m.usuario {
		if u, err := user.Current(); err == nil {
			usuario = u.Username
		}
	}
	fmt.Fprintf(out, "modo:     %s\nserviço:  %s (%s)\nestado:   %s (%s)\nboot:     %s\npid:      %s\nusuário:  %s\nunit:     %s\nexec:     %s\nlogs:     %s -f\n",
		m, nome, nomeExibicaoServico, props["ActiveState"], props["SubState"], props["UnitFileState"],
		props["MainPID"], usuario, m.caminhoUnit(nome), props["ExecStart"], m.journalctl(nome))
	if m.usuario {
		avisarLinger(usuario, out)
	}
	if nome != nomeServico {
		fmt.Fprintf(out, "aviso:    registrado sob nome legado; `praxis service install` migra para %q\n", nomeServico)
	}
	return nil
}

// serviceRun: no Linux o corpo do serviço é o próprio serve (o unit chama
// `serve` direto). Existe para simetria com o Windows e para depurar uma linha
// de comando com -home.
func serviceRun(ctx context.Context, args []string, out, errOut io.Writer) error {
	home, resto := extrairFlagHome(args)
	if err := definirHome(home); err != nil {
		return err
	}
	return serve(ctx, resto, out, errOut)
}
