//go:build !windows

package servico

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
)

// dirUnits é onde a unit é escrita. A variável de ambiente existe para o teste
// automatizado poder exercer o instalador fora de uma máquina de verdade (e para
// quem tem um layout diferente); numa instalação normal ninguém mexe nisso.
func dirUnits() string {
	if d := strings.TrimSpace(os.Getenv("PRAXIS_UNIT_DIR")); d != "" {
		return d
	}
	return "/etc/systemd/system"
}

func caminhoUnit(nome string) string {
	return filepath.Join(dirUnits(), nome+".service")
}

// temSystemd informa se o systemd é o init desta máquina. Sem ele não há o que
// instalar: macOS (launchd) e containers com outro init caem aqui, e a mensagem
// aponta para o `praxis service print`, que ainda entrega a unit pronta.
func temSystemd() bool {
	fi, err := os.Stat("/run/systemd/system")
	return err == nil && fi.IsDir()
}

func erroSemSystemd() error {
	return errors.New("systemd não encontrado nesta máquina; use `praxis service print` para gerar a unit " +
		"(ou o equivalente do seu init) e registre-a à mão")
}

// systemctl roda o systemctl e devolve a saída combinada. O erro já traz a saída,
// que é onde o systemd explica o que recusou.
func systemctl(args ...string) (string, error) {
	saida, err := exec.Command("systemctl", args...).CombinedOutput()
	texto := strings.TrimSpace(string(saida))
	if err != nil {
		return texto, fmt.Errorf("systemctl %s: %w: %s", strings.Join(args, " "), err, texto)
	}
	return texto, nil
}

// systemctlTexto roda o systemctl ignorando o código de saída: `is-active` e
// `is-enabled` respondem por status (3 para inativo, 1 para desabilitado), o que
// é resposta e não falha.
func systemctlTexto(args ...string) string {
	saida, _ := exec.Command("systemctl", args...).CombinedOutput()
	return strings.TrimSpace(string(saida))
}

// Instalar escreve a unit e sobe o serviço. É idempotente: rodar de novo
// sobrescreve a unit e faz `restart` (não `enable --now`), porque numa
// reinstalação o serviço já está ativo e continuaria com o binário antigo em
// memória.
func Instalar(o Opcoes, log Log) error {
	o = o.ComPadroes()
	if !temSystemd() {
		return erroSemSystemd()
	}
	if !filepath.IsAbs(o.Exe) {
		return fmt.Errorf("o executável do serviço precisa de caminho absoluto: %q", o.Exe)
	}
	if err := os.MkdirAll(dirUnits(), 0o755); err != nil {
		return fmt.Errorf("criar %s: %w", dirUnits(), err)
	}
	if err := os.WriteFile(caminhoUnit(o.Nome), []byte(UnitSystemd(o)), 0o644); err != nil {
		return fmt.Errorf("escrever %s: %w", caminhoUnit(o.Nome), err)
	}
	if _, err := systemctl("daemon-reload"); err != nil {
		return err
	}
	if _, err := systemctl("enable", o.Nome); err != nil {
		return err
	}
	if _, err := systemctl("restart", o.Nome); err != nil {
		return err
	}
	return nil
}

// Remover desabilita, para e apaga a unit. PRAXIS_HOME (banco, backups, logs)
// fica intacto.
func Remover(nome string, log Log) error {
	unit := caminhoUnit(nome)
	if _, err := os.Stat(unit); err != nil {
		return fmt.Errorf("serviço %q não está instalado (%s não existe)", nome, unit)
	}
	if temSystemd() {
		// disable --now: desliga do boot e para agora. Erro aqui não impede apagar
		// a unit — uma unit meio-registrada é pior que nenhuma.
		if _, err := systemctl("disable", "--now", nome); err != nil {
			log.avisar("%v", err)
		}
	}
	if err := os.Remove(unit); err != nil {
		return fmt.Errorf("remover %s: %w", unit, err)
	}
	if temSystemd() {
		if _, err := systemctl("daemon-reload"); err != nil {
			return err
		}
	}
	return nil
}

// Iniciar sobe o serviço.
func Iniciar(nome string) error {
	if !temSystemd() {
		return erroSemSystemd()
	}
	_, err := systemctl("start", nome)
	return err
}

// Parar para o serviço.
func Parar(nome string) error {
	if !temSystemd() {
		return erroSemSystemd()
	}
	_, err := systemctl("stop", nome)
	return err
}

// Consultar lê a unit em disco (que é a fonte do que foi registrado) e pergunta ao
// systemd o estado atual.
func Consultar(nome string) (Estado, error) {
	e := Estado{Nome: nome}
	conteudo, err := os.ReadFile(caminhoUnit(nome))
	if err != nil {
		if os.IsNotExist(err) {
			return e, nil
		}
		return e, fmt.Errorf("ler %s: %w", caminhoUnit(nome), err)
	}
	e.Instalado = true
	e.LinhaComando, e.Conta = execStartEUsuario(string(conteudo))
	e.Exe = ExeDoComando(e.LinhaComando)
	if e.Conta == "" {
		e.Conta = "root"
	}
	if temSystemd() {
		ativo := systemctlTexto("is-active", nome)
		e.Rodando = ativo == "active" || ativo == "activating"
		e.Detalhe = ativo
		e.Habilitado = systemctlTexto("is-enabled", nome) == "enabled"
	} else {
		e.Detalhe = "desconhecido (sem systemd)"
	}
	return e, nil
}

// execStartEUsuario extrai ExecStart= e User= da unit — o suficiente para dizer
// qual binário o serviço roda e com que conta.
func execStartEUsuario(unit string) (execStart, usuario string) {
	sc := bufio.NewScanner(strings.NewReader(unit))
	for sc.Scan() {
		linha := strings.TrimSpace(sc.Text())
		switch {
		case strings.HasPrefix(linha, "ExecStart="):
			execStart = strings.TrimSpace(strings.TrimPrefix(linha, "ExecStart="))
		case strings.HasPrefix(linha, "User="):
			usuario = strings.TrimSpace(strings.TrimPrefix(linha, "User="))
		}
	}
	return execStart, usuario
}

// Privilegiado informa se este processo pode escrever a unit e falar com o
// systemd.
func Privilegiado() (bool, string) {
	if os.Geteuid() == 0 {
		return true, ""
	}
	return false, "rode com sudo (ex.: sudo praxis service install)"
}

// ContaServicoPadrao é a conta de sistema criada pelo install quando não há
// conta não-root para registrar o serviço (login root de verdade, sem
// SUDO_USER). O Claude recusa execução autônoma com UID 0 — o serviço NUNCA é
// registrado como root.
const ContaServicoPadrao = "praxis"

// GarantirContaSistema devolve uma conta não-root para o serviço, criando a
// conta de sistema `praxis` quando ela não existe. Com --create-home de
// propósito: o harness e o git precisam de um HOME de verdade (~/.claude,
// ~/.gitconfig, caches). Devolve criada=true quando o useradd rodou agora.
func GarantirContaSistema() (conta string, criada bool, err error) {
	if _, err := user.Lookup(ContaServicoPadrao); err == nil {
		return ContaServicoPadrao, false, nil
	}
	saida, err := exec.Command("useradd",
		"--system",
		"--create-home",
		"--home-dir", "/var/lib/"+ContaServicoPadrao,
		// nologin: ninguém loga COMO a conta; o systemd não passa pelo shell.
		"--shell", "/usr/sbin/nologin",
		ContaServicoPadrao).CombinedOutput()
	if err != nil {
		return "", false, fmt.Errorf("criar a conta de sistema %q: %v — %s",
			ContaServicoPadrao, err, strings.TrimSpace(string(saida)))
	}
	return ContaServicoPadrao, true, nil
}

// DestinoPadrao é onde o binário do serviço fica. /usr/local/bin é o lugar
// canônico para binários instalados fora do gerenciador de pacotes, e é estável:
// a unit não pode apontar para o diretório de onde alguém descompactou o release.
func DestinoPadrao() string { return "/usr/local/bin" }

// EhServicoSCM é sempre false fora do Windows: o systemd roda o serviço como um
// processo comum, e tratar SIGTERM (o que o `serve` já faz) é tudo que ele espera.
func EhServicoSCM() bool { return false }

// RodarComoServico existe para o código chamador não precisar de build tags. Fora
// do Windows não há handler nenhum a registrar, então só executa o corpo.
func RodarComoServico(_ string, corpo func(context.Context) error) error {
	return corpo(context.Background())
}
