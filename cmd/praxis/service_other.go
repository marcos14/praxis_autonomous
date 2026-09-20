//go:build !windows

package main

// Casca do Linux (ADR 0001, §1.3): o "modo serviço" não existe como código —
// é o `serve` comum, foreground, sob um unit do systemd (Type=simple,
// Restart=always). Nunca daemonizar. Este arquivo cuida do registro: copiar o
// binário para um lugar estável, escrever o unit, daemon-reload, enable e
// restart (nunca `enable --now`, que deixaria a versão antiga em memória — D7).

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
func flagsServicoPlataforma(fs *flag.FlagSet, o *opcoesServico) {
	fs.StringVar(&o.Usuario, "usuario", usuarioServicoPadrao(),
		"usuário do unit (User=). Default: quem chamou o sudo — reaproveita os logins dos CLIs dos motores nesse perfil — ou root")
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
	return "# Salve como " + caminhoUnitDe(nomeServico) + " (como root) e rode:\n" +
		"#   systemctl daemon-reload && systemctl enable " + nomeServico + " && systemctl restart " + nomeServico + "\n\n" +
		unitSystemd(exe, o)
}

func exigirRoot() error {
	if os.Geteuid() != 0 {
		return errors.New("execute como root (ex.: sudo praxis service install …)")
	}
	return nil
}

func exigirSystemctl() error {
	if _, err := exec.LookPath("systemctl"); err != nil {
		return errors.New("systemctl não encontrado: só o systemd é suportado. Use `praxis service unit` para gerar o unit, " +
			"ou rode `praxis serve` sob o supervisor da sua distro")
	}
	return nil
}

// systemctl executa `systemctl args...` e devolve a saída; erro inclui a saída.
func systemctl(args ...string) (string, error) {
	cmd := exec.Command("systemctl", args...)
	saida, err := cmd.CombinedOutput()
	if err != nil {
		return string(saida), fmt.Errorf("systemctl %s: %w\n%s", strings.Join(args, " "), err, strings.TrimSpace(string(saida)))
	}
	return string(saida), nil
}

// resolverUsuario devolve uid/gid do usuário do unit.
func resolverUsuario(nome string) (uid, gid int, err error) {
	u, err := user.Lookup(nome)
	if err != nil {
		return 0, 0, fmt.Errorf("usuário %q não existe (crie-o ou informe -usuario): %w", nome, err)
	}
	if uid, err = strconv.Atoi(u.Uid); err != nil {
		return 0, 0, err
	}
	if gid, err = strconv.Atoi(u.Gid); err != nil {
		return 0, 0, err
	}
	return uid, gid, nil
}

func existe(caminho string) bool {
	_, err := os.Stat(caminho)
	return err == nil
}

// modeloEnvServico é o EnvironmentFile inicial (só comentários).
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

func serviceInstall(_ context.Context, args []string, out, errOut io.Writer) error {
	fs := flag.NewFlagSet("service install", flag.ContinueOnError)
	fs.SetOutput(errOut)
	o := flagsServico(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	o.registrarExplicitas(fs)
	if err := exigirRoot(); err != nil {
		return err
	}
	if err := exigirSystemctl(); err != nil {
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
	uid, gid, err := resolverUsuario(o.Usuario)
	if err != nil {
		return err
	}

	// D6: unit sob nome legado é desativado e removido, não deixado conviver.
	for _, n := range nomesServicoLegados {
		if !existe(caminhoUnitDe(n)) {
			continue
		}
		fmt.Fprintf(out, "unit legado %q encontrado: desativando e removendo antes de registrar %q\n", n, nomeServico)
		if _, err := systemctl("disable", "--now", n); err != nil {
			fmt.Fprintf(errOut, "aviso: %v\n", err)
		}
		if err := os.Remove(caminhoUnitDe(n)); err != nil {
			return err
		}
	}

	// Porta livre? Um serviço já ativo ocupa a própria porta: para antes de
	// conferir e, se a checagem falhar, volta com ele — a instalação falhou, não
	// a máquina. Falha de configuração aborta ANTES de tocar em binário e unit (D7).
	estavaAtivo := existe(caminhoUnitDe(nomeServico)) && unitAtivo(nomeServico)
	if estavaAtivo {
		if _, err := systemctl("stop", nomeServico); err != nil {
			return err
		}
	}
	if err := verificarPorta(o.Addr); err != nil {
		if estavaAtivo {
			_, _ = systemctl("start", nomeServico)
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

	// Diretórios do contrato (D5). O home pertence ao usuário do unit; o env
	// file é root:grupo 0640 (pode conter variáveis sensíveis).
	if err := os.MkdirAll(home, 0o750); err != nil {
		return fmt.Errorf("criar PRAXIS_HOME %q: %w", home, err)
	}
	if err := os.Chown(home, uid, gid); err != nil {
		return fmt.Errorf("chown %q: %w", home, err)
	}
	if err := os.MkdirAll(filepath.Dir(caminhoEnvServico), 0o755); err != nil {
		return err
	}
	if !existe(caminhoEnvServico) {
		if err := os.WriteFile(caminhoEnvServico, []byte(modeloEnvServico), 0o640); err != nil {
			return fmt.Errorf("criar %s: %w", caminhoEnvServico, err)
		}
		_ = os.Chown(caminhoEnvServico, 0, gid)
		fmt.Fprintf(out, "%s criado (só comentários; edite se precisar)\n", caminhoEnvServico)
	}

	// Unit: reescrito só se mudou (idempotente — D7). daemon-reload é sempre
	// seguro; sem ele o systemd seguiria com a versão em cache.
	unit := caminhoUnitDe(nomeServico)
	conteudo := unitSystemd(destino, o)
	if atual, err := os.ReadFile(unit); err != nil || string(atual) != conteudo {
		if err := os.WriteFile(unit, []byte(conteudo), 0o644); err != nil {
			return fmt.Errorf("escrever %s: %w", unit, err)
		}
		fmt.Fprintf(out, "unit escrito em %s\n", unit)
	} else {
		fmt.Fprintf(out, "unit em %s já atualizado\n", unit)
	}
	if _, err := systemctl("daemon-reload"); err != nil {
		return err
	}
	if _, err := systemctl("enable", nomeServico); err != nil {
		return err
	}
	// restart, não `enable --now`: numa reinstalação o serviço ativo continuaria
	// rodando o binário antigo em memória (D7).
	if _, err := systemctl("restart", nomeServico); err != nil {
		return err
	}
	if err := esperarAtivo(nomeServico, 10*time.Second); err != nil {
		return fmt.Errorf("%w — veja: journalctl -u %s -n 50", err, nomeServico)
	}
	// "active" só diz que o processo não morreu; confirma que o HTTP responde.
	if err := esperarSaude(o.Addr, o.comTLS(), 20*time.Second); err != nil {
		fmt.Fprintf(errOut, "aviso: serviço ativo mas ainda sem resposta HTTP (%v); veja: journalctl -u %s -n 50\n", err, nomeServico)
	} else {
		fmt.Fprintf(out, "serviço respondendo em %s\n", o.urlAcesso())
	}
	fmt.Fprintf(out, "\nserviço:  %s (%s)\nbinário:  %s\nhome:     %s\nusuário:  %s\nendereço: %s (TLS: %s)\nacesso:   %s\nunit:     %s\nlogs:     journalctl -u %s -f\nstatus:   praxis service status\n",
		nomeServico, nomeExibicaoServico, destino, home, o.Usuario, o.Addr, simNao(o.comTLS()), o.urlAcesso(), unit, nomeServico)
	return nil
}

// unitAtivo informa se `systemctl is-active` responde active.
func unitAtivo(nome string) bool {
	saida, _ := exec.Command("systemctl", "is-active", nome).Output()
	return strings.TrimSpace(string(saida)) == "active"
}

// esperarAtivo consulta `systemctl is-active` até o unit ficar active ou o
// prazo vencer; `failed` é falha imediata.
func esperarAtivo(nome string, prazo time.Duration) error {
	limite := time.Now().Add(prazo)
	for {
		saida, _ := exec.Command("systemctl", "is-active", nome).Output()
		estado := strings.TrimSpace(string(saida))
		switch estado {
		case "active":
			return nil
		case "failed":
			return fmt.Errorf("serviço %q entrou em failed", nome)
		}
		if time.Now().After(limite) {
			return fmt.Errorf("serviço %q não ficou ativo em %s (estado: %s)", nome, prazo, estado)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// nomeServicoInstalado resolve sob qual nome esta máquina registrou o unit: o
// atual ou um legado (D6).
func nomeServicoInstalado() (string, bool) {
	for _, n := range append([]string{nomeServico}, nomesServicoLegados...) {
		if existe(caminhoUnitDe(n)) {
			return n, true
		}
	}
	return nomeServico, false
}

func serviceRemove(_ context.Context, args []string, out, errOut io.Writer) error {
	fs := flag.NewFlagSet("service remove", flag.ContinueOnError)
	fs.SetOutput(errOut)
	if err := fs.Parse(args); err != nil {
		return err
	}
	nome, instalado := nomeServicoInstalado()
	if !instalado {
		fmt.Fprintf(out, "serviço %q não está instalado — nada a fazer\n", nomeServico)
		return nil
	}
	if err := exigirRoot(); err != nil {
		return err
	}
	if err := exigirSystemctl(); err != nil {
		return err
	}
	if _, err := systemctl("disable", "--now", nome); err != nil {
		fmt.Fprintf(errOut, "aviso: %v\n", err)
	}
	if err := os.Remove(caminhoUnitDe(nome)); err != nil {
		return err
	}
	if _, err := systemctl("daemon-reload"); err != nil {
		return err
	}
	fmt.Fprintf(out, "serviço %q removido; dados em %s, %s e o binário foram mantidos\n",
		nome, homeServicoPadrao(), filepath.Dir(caminhoEnvServico))
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
	nome, instalado := nomeServicoInstalado()
	if !instalado {
		return fmt.Errorf("serviço %q não está instalado (sudo praxis service install)", nomeServico)
	}
	if err := exigirRoot(); err != nil {
		return err
	}
	if _, err := systemctl(verbo, nome); err != nil {
		return err
	}
	saida, _ := exec.Command("systemctl", "is-active", nome).Output()
	fmt.Fprintf(out, "serviço %q: %s\n", nome, strings.TrimSpace(string(saida)))
	return nil
}

// serviceStatus consulta o unit via `systemctl show` — não exige root (D4).
func serviceStatus(_ context.Context, args []string, out, errOut io.Writer) error {
	fs := flag.NewFlagSet("service status", flag.ContinueOnError)
	fs.SetOutput(errOut)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := exigirSystemctl(); err != nil {
		return err
	}
	nome, instalado := nomeServicoInstalado()
	if !instalado {
		fmt.Fprintf(out, "serviço %q: não instalado (nem sob nomes legados)\n", nomeServico)
		return nil
	}
	saida, err := systemctl("show", nome, "-p", "ActiveState,SubState,UnitFileState,MainPID,ExecStart,User")
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
	fmt.Fprintf(out, "serviço:  %s (%s)\nestado:   %s (%s)\nboot:     %s\npid:      %s\nusuário:  %s\nunit:     %s\nexec:     %s\n",
		nome, nomeExibicaoServico, props["ActiveState"], props["SubState"], props["UnitFileState"],
		props["MainPID"], props["User"], caminhoUnitDe(nome), props["ExecStart"])
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
