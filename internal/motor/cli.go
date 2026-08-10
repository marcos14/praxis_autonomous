package motor

// Resolução do EXECUTÁVEL de cada harness em camadas (Fase D do
// PLANO_MULTIUSUARIO.md). Até aqui os motores invocavam o CLI pelo nome
// ("claude", "codex", "opencode") e dependiam 100% do PATH do serviço; agora a
// resolução é:
//
//  1. PRAXIS_CLI_<VENDOR> — override explícito por variável de ambiente (ex.:
//     PRAXIS_CLI_CLAUDE=C:\ferramentas\claude.exe), para quem tem layout próprio;
//  2. o PATH, como sempre;
//  3. o diretório GERENCIADO — PRAXIS_HOME/tools/harness/<vendor>/bin — onde o
//     instalador da tela Motores baixa o binário oficial do vendor.
//
// Sem nenhum dos três, devolve o próprio nome (o exec falha com a mensagem
// clássica de "não encontrado", que a detecção e o diagnóstico já explicam).
// Todos os pontos que executam um harness (motores, login web, diagnóstico,
// franquia e detecção) resolvem por aqui — um ponto único.

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// dirHarnessGerenciado devolve PRAXIS_HOME/tools/harness/<vendor>/bin ("" se o
// PRAXIS_HOME não resolve). Fica ao lado do tools/ do IDE web — as ferramentas
// baixadas pelo Praxis moram todas ali.
func dirHarnessGerenciado(vendor string) string {
	home, err := praxisHome()
	if err != nil {
		return ""
	}
	return filepath.Join(home, "tools", "harness", normalizarNomeMotor(vendor), "bin")
}

// praxisHome espelha a resolução de db.PraxisHome sem importar o pacote db (o
// motor é camada mais baixa): PRAXIS_HOME → %LOCALAPPDATA%\praxis (Windows) →
// os.UserConfigDir()/praxis.
func praxisHome() (string, error) {
	if h := strings.TrimSpace(os.Getenv("PRAXIS_HOME")); h != "" {
		return h, nil
	}
	if runtime.GOOS == "windows" {
		if la := strings.TrimSpace(os.Getenv("LOCALAPPDATA")); la != "" {
			return filepath.Join(la, "praxis"), nil
		}
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "praxis"), nil
}

// nomeExeVendor é o nome do executável do vendor na plataforma corrente.
func nomeExeVendor(vendor string) string {
	if runtime.GOOS == "windows" {
		return normalizarNomeMotor(vendor) + ".exe"
	}
	return normalizarNomeMotor(vendor)
}

// caminhoCLIGerenciado devolve o executável no diretório gerenciado e se ele
// existe.
func caminhoCLIGerenciado(vendor string) (string, bool) {
	dir := dirHarnessGerenciado(vendor)
	if dir == "" {
		return "", false
	}
	p := filepath.Join(dir, nomeExeVendor(vendor))
	if _, err := os.Stat(p); err != nil {
		return "", false
	}
	return p, true
}

// ResolverCLI devolve o executável a invocar para o vendor (ver a ordem no topo
// do arquivo). O retorno é sempre utilizável em exec.Command: um caminho
// absoluto quando resolvido, ou o nome puro como último recurso.
func ResolverCLI(vendor string) string {
	v := normalizarNomeMotor(vendor)
	if p := strings.TrimSpace(os.Getenv("PRAXIS_CLI_" + strings.ToUpper(v))); p != "" {
		return p
	}
	if p, err := exec.LookPath(v); err == nil {
		return p
	}
	if p, ok := caminhoCLIGerenciado(v); ok {
		return p
	}
	return v
}

// CLIDisponivel informa se o CLI do vendor resolve para um executável real
// (PATH, override ou gerenciado) e devolve o caminho.
func CLIDisponivel(vendor string) (string, bool) {
	p := ResolverCLI(vendor)
	if filepath.IsAbs(p) {
		return p, true
	}
	// nome puro: só está disponível se o LookPath achar (cobre o caso do
	// override por env apontando para algo relativo/quebrado).
	if abs, err := exec.LookPath(p); err == nil {
		return abs, true
	}
	return "", false
}
