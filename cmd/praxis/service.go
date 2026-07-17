package main

import (
	"flag"
	"fmt"
	"io"
	"runtime"
	"strings"
)

// service é o subcomando que ajuda a instalar o Praxis como serviço do sistema
// (Fase 5d). O registro efetivo exige privilégios de administrador na máquina —
// por isso, por padrão, apenas IMPRIME as instruções/artefatos (unit systemd no
// Linux; comando `sc.exe` no Windows). Assim o passo automatizável (gerar a
// configuração) fica coberto, e o passo que precisa de admin fica explícito.
func service(args []string, out, errOut io.Writer) error {
	fs := flag.NewFlagSet("service", flag.ContinueOnError)
	fs.SetOutput(errOut)
	exe := fs.String("exe", "praxis", "caminho do executável praxis")
	addr := fs.String("addr", enderecoPadrao, "endereço de bind do serviço")
	nome := fs.String("nome", "praxis", "nome do serviço")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if runtime.GOOS == "windows" {
		fmt.Fprintln(out, "# Instale como serviço do Windows (execute como Administrador):")
		fmt.Fprintln(out, comandoServicoWindows(*nome, *exe, *addr))
		fmt.Fprintln(out, "\n# Para iniciar:   sc.exe start "+*nome)
		fmt.Fprintln(out, "# Para remover:  sc.exe delete "+*nome)
		return nil
	}

	fmt.Fprintln(out, "# Salve como /etc/systemd/system/"+*nome+".service (como root) e rode:")
	fmt.Fprintln(out, "#   systemctl daemon-reload && systemctl enable --now "+*nome)
	fmt.Fprintln(out)
	fmt.Fprint(out, unitSystemd(*exe, *addr))
	return nil
}

// unitSystemd gera o conteúdo de uma unit systemd para o `praxis serve`.
func unitSystemd(exe, addr string) string {
	return strings.Join([]string{
		"[Unit]",
		"Description=Praxis Autonomous (orquestrador de desenvolvimento)",
		"After=network.target",
		"",
		"[Service]",
		"Type=simple",
		"ExecStart=" + exe + " serve -addr " + addr,
		"Restart=on-failure",
		"RestartSec=5",
		"",
		"[Install]",
		"WantedBy=multi-user.target",
		"",
	}, "\n")
}

// comandoServicoWindows gera o comando `sc.exe create` para registrar o serviço.
func comandoServicoWindows(nome, exe, addr string) string {
	// binPath do sc.exe exige o executável + argumentos numa string só.
	return fmt.Sprintf(`sc.exe create %s binPath= "\"%s\" serve -addr %s" start= auto DisplayName= "Praxis Autonomous"`,
		nome, exe, addr)
}
