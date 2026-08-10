// Package servico registra, remove e controla o Praxis como serviço do sistema
// operacional — de verdade, não por instruções impressas para o operador copiar.
//
// O que havia antes era só um gerador de texto, e no Windows o texto gerado não
// funcionava: um binário registrado com `sc.exe create` que não conversa com o
// Gerenciador de Serviços (SCM) é derrubado no start com o erro 1053 ("o serviço
// não respondeu à solicitação de início a tempo"). Quem seguisse o passo a passo
// ficava com um serviço registrado que nunca sobe.
//
// Este pacote fecha os dois lados:
//
//   - Windows: fala com o SCM (criar, remover, iniciar, parar, consultar) e
//     oferece RodarComoServico — o handler de controle que o `praxis serve` usa
//     quando foi o SCM que o subiu (EhServicoSCM).
//   - Linux: escreve a unit systemd e chama systemctl (daemon-reload, enable,
//     restart).
//
// Instalar é idempotente nas duas plataformas: rodar de novo atualiza o registro
// e sobe o binário novo. Um serviço que aponta para um executável antigo — ou
// para a pasta de Downloads de onde alguém rodou o instalador, que desaparece no
// dia da limpeza — é RE-registrado no caminho definitivo, em vez de ser deixado
// como está.
package servico

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	// NomePadrao é o identificador do serviço no SCM e o nome da unit systemd
	// (sem o sufixo .service).
	NomePadrao = "praxis"
	// DisplayPadrao é o nome exibido em services.msc / systemctl status.
	DisplayPadrao = "Praxis Autonomous"
	// DescricaoPadrao explica o serviço para quem o encontra na lista.
	DescricaoPadrao = "Orquestrador de desenvolvimento autônomo do Praxis (praxis serve)."
)

// Log recebe avisos que não impedem a operação (uma ação de recuperação que o
// SCM recusou, um `systemctl disable` que reclamou). nil significa silêncio.
// Mesmo formato dos serviços de background do projeto (intake, manutenção...).
type Log func(msg string)

func (l Log) avisar(formato string, a ...any) {
	if l != nil {
		l(fmt.Sprintf(formato, a...))
	}
}

// Opcoes descreve o serviço a registrar.
type Opcoes struct {
	Nome      string   // identificador no SCM / nome da unit
	Display   string   // nome exibido
	Descricao string   // descrição
	Exe       string   // caminho absoluto do executável JÁ no destino definitivo
	Args      []string // argumentos do serviço (ex.: serve -addr 127.0.0.1:7799 -home ...)
	Home      string   // PRAXIS_HOME do serviço (dados, banco, logs)
	Usuario   string   // conta de logon ("" = LocalSystem no Windows, root no Linux)
	Senha     string   // senha da conta (só Windows; ignorada quando Usuario é vazio)
}

// ComPadroes devolve uma cópia de o com os campos de identificação preenchidos.
func (o Opcoes) ComPadroes() Opcoes {
	if o.Nome == "" {
		o.Nome = NomePadrao
	}
	if o.Display == "" {
		o.Display = DisplayPadrao
	}
	if o.Descricao == "" {
		o.Descricao = DescricaoPadrao
	}
	return o
}

// Estado é o retrato do serviço nesta máquina, como a plataforma o conhece.
type Estado struct {
	Nome         string
	Instalado    bool
	Rodando      bool
	Habilitado   bool   // sobe no boot
	Exe          string // executável registrado (só o binário)
	LinhaComando string // executável + argumentos, como registrado
	Conta        string // conta de logon do serviço
	Detalhe      string // estado em texto, no vocabulário da plataforma
}

// NomeExecutavel é o nome do binário do Praxis no diretório de instalação.
func NomeExecutavel() string {
	if runtime.GOOS == "windows" {
		return "praxis.exe"
	}
	return "praxis"
}

// citar coloca entre aspas o que tem espaço. Sem isso o SCM tentaria executar
// "C:\Program.exe" a partir de "C:\Program Files\Praxis\praxis.exe", e o mesmo
// vale para o -home de um usuário cujo nome tem espaço.
func citar(s string) string {
	if s == "" || strings.ContainsAny(s, " \t") {
		return `"` + s + `"`
	}
	return s
}

// LinhaComando monta a linha de comando do serviço: executável sempre entre
// aspas, argumentos entre aspas quando têm espaço.
func LinhaComando(exe string, args []string) string {
	partes := make([]string, 0, len(args)+1)
	partes = append(partes, `"`+exe+`"`)
	for _, a := range args {
		partes = append(partes, citar(a))
	}
	return strings.Join(partes, " ")
}

// ExeDoComando extrai o executável de uma linha de comando registrada, que vem
// com os argumentos junto ("C:\...\praxis.exe" serve -addr ... → o caminho do
// .exe). É o que permite comparar o binário que o serviço roda com o que
// acabamos de instalar e decidir se o registro precisa ser refeito.
func ExeDoComando(cmdline string) string {
	s := strings.TrimSpace(cmdline)
	if s == "" {
		return ""
	}
	if s[0] == '"' {
		if fim := strings.IndexByte(s[1:], '"'); fim >= 0 {
			return s[1 : 1+fim]
		}
		return strings.Trim(s, `"`)
	}
	if esp := strings.IndexByte(s, ' '); esp >= 0 {
		return s[:esp]
	}
	return s
}

// MesmoCaminho compara dois caminhos como a plataforma compara: sem diferenciar
// maiúsculas no Windows, e sempre depois de normalizar separadores e segmentos
// "." / "..".
func MesmoCaminho(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	a, b = filepath.Clean(a), filepath.Clean(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// CopiarBinario instala o executável de origem em destino, criando o diretório se
// preciso. Quando destino já existe e está em uso (é o caso de reinstalar com o
// serviço no ar) o arquivo antigo é renomeado para .old: o Windows não deixa
// sobrescrever um executável em execução, mas deixa renomeá-lo.
func CopiarBinario(origem, destino string) error {
	if MesmoCaminho(origem, destino) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(destino), 0o755); err != nil {
		return fmt.Errorf("criar %s: %w", filepath.Dir(destino), err)
	}
	if _, err := os.Stat(destino); err == nil {
		_ = os.Remove(destino + ".old")
		if err := os.Rename(destino, destino+".old"); err != nil {
			return fmt.Errorf("liberar %s (o serviço ainda está rodando?): %w", destino, err)
		}
	}
	entrada, err := os.Open(origem)
	if err != nil {
		return err
	}
	defer entrada.Close()
	saida, err := os.OpenFile(destino, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(saida, entrada); err != nil {
		saida.Close()
		return err
	}
	if err := saida.Close(); err != nil {
		return err
	}
	// Best-effort: o .old só sai depois que o processo antigo solta o arquivo.
	_ = os.Remove(destino + ".old")
	return nil
}

// UnitSystemd gera a unit systemd de o. Vive aqui, e não no arquivo específico de
// Linux, para que `praxis service print` mostre a unit em qualquer plataforma —
// quem administra um servidor Linux a partir de uma estação Windows precisa dela.
func UnitSystemd(o Opcoes) string {
	o = o.ComPadroes()
	execStart := make([]string, 0, len(o.Args)+1)
	execStart = append(execStart, citar(o.Exe))
	for _, a := range o.Args {
		execStart = append(execStart, citar(a))
	}
	l := []string{
		"[Unit]",
		"Description=" + o.Display + " — " + o.Descricao,
		"After=network-online.target",
		"Wants=network-online.target",
		"",
		"[Service]",
		"Type=simple",
		"ExecStart=" + strings.Join(execStart, " "),
	}
	if o.Home != "" {
		l = append(l,
			"Environment=PRAXIS_HOME="+o.Home,
			"WorkingDirectory="+o.Home,
		)
	}
	if o.Usuario != "" {
		// Sem Group=: o systemd usa o grupo primário da conta, que é o que essa
		// conta já usa para o git, o ssh e a config do harness.
		l = append(l, "User="+o.Usuario)
	}
	l = append(l,
		"Restart=on-failure",
		"RestartSec=5",
		// O praxis dispara harnesses como processos filhos e faz o shutdown
		// gracioso por conta própria: KillMode=mixed manda o SIGTERM só ao
		// processo principal (que drena as conexões e encerra os filhos) e deixa
		// o SIGKILL do grupo como último recurso, depois do TimeoutStopSec.
		"KillMode=mixed",
		"TimeoutStopSec=30",
		"",
		"[Install]",
		"WantedBy=multi-user.target",
		"",
	)
	return strings.Join(l, "\n")
}

// ComandoSC gera o `sc.exe create` equivalente ao que Instalar faz no Windows.
// Serve ao `praxis service print`, para quem prefere revisar ou versionar o
// registro em vez de deixar o instalador fazê-lo.
func ComandoSC(o Opcoes) string {
	o = o.ComPadroes()
	// O binPath do sc.exe é UMA string com executável e argumentos; as aspas
	// internas do caminho precisam ser escapadas.
	bin := strings.ReplaceAll(LinhaComando(o.Exe, o.Args), `"`, `\"`)
	cmd := fmt.Sprintf(`sc.exe create %s binPath= "%s" start= auto DisplayName= "%s"`, o.Nome, bin, o.Display)
	if o.Usuario != "" {
		cmd += fmt.Sprintf(` obj= "%s" password= "%s"`, o.Usuario, o.Senha)
	}
	return cmd
}
