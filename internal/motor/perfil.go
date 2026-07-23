package motor

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// VendorsComPerfilIsolado são os harnesses cujo CLI documenta um diretório raiz
// capaz de isolar configuração e credenciais. OpenCode fica de fora enquanto o
// diretório de dados/autenticação não tiver um contrato equivalente validado.
func VendorComPerfilIsolado(vendor string) bool {
	switch normalizarNomeMotor(vendor) {
	case "claude", "codex":
		return true
	default:
		return false
	}
}

// VariavelPerfil devolve a variável de ambiente que seleciona o diretório do
// perfil no CLI do vendor.
func VariavelPerfil(vendor string) string {
	switch normalizarNomeMotor(vendor) {
	case "claude":
		return "CLAUDE_CONFIG_DIR"
	case "codex":
		return "CODEX_HOME"
	default:
		return ""
	}
}

// DiretorioPerfilGerenciado monta o caminho de um perfil criado pelo Praxis.
// Os ids, e não o alias editável, tornam o caminho estável após renomeações.
func DiretorioPerfilGerenciado(praxisHome, vendor string, engineID, accountID int64) string {
	return filepath.Join(praxisHome, "engine-profiles", normalizarNomeMotor(vendor),
		strconv.FormatInt(engineID, 10), strconv.FormatInt(accountID, 10))
}

// PrepararPerfil garante que o diretório exista e devolve seu caminho absoluto.
// CODEX_HOME precisa existir antes do processo; fazemos o mesmo para Claude para
// manter permissões e diagnóstico consistentes.
func PrepararPerfil(vendor, dir string) (string, error) {
	if !VendorComPerfilIsolado(vendor) {
		return "", fmt.Errorf("motor %q não suporta perfis isolados", vendor)
	}
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return "", fmt.Errorf("diretório do perfil não informado")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("resolver diretório do perfil: %w", err)
	}
	if err := os.MkdirAll(abs, 0o700); err != nil {
		return "", fmt.Errorf("criar diretório do perfil %q: %w", abs, err)
	}
	return abs, nil
}

// aplicarPerfil configura o ambiente de cmd sem manter uma segunda ocorrência
// herdada da variável do vendor. Remover a ocorrência anterior evita que o
// comportamento dependa de qual valor duplicado o sistema operacional escolhe.
func aplicarPerfil(cmd *exec.Cmd, vendor, dir string) error {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil
	}
	// CODEX_HOME precisa existir antes do CLI. Claude aceita criar o diretório;
	// não o criamos aqui para preservar integrações legadas que apenas injetam
	// CLAUDE_CONFIG_DIR (perfis gerenciados já são preparados no cadastro/login).
	if normalizarNomeMotor(vendor) == "codex" {
		var err error
		dir, err = PrepararPerfil(vendor, dir)
		if err != nil {
			return err
		}
	}
	chave := VariavelPerfil(vendor)
	env := cmd.Environ()
	prefixo := chave + "="
	filtrado := env[:0]
	for _, item := range env {
		if strings.EqualFold(strings.SplitN(item, "=", 2)[0]+"=", prefixo) {
			continue
		}
		filtrado = append(filtrado, item)
	}
	cmd.Env = append(filtrado, chave+"="+dir)
	return nil
}

// perfilDirDaOp mantém compatibilidade com integrações que ainda preencham o
// campo antigo ClaudeConfigDir, enquanto o código novo usa PerfilDir para ambos
// os vendors.
func perfilDirDaOp(op OpcoesRun) string {
	if dir := strings.TrimSpace(op.PerfilDir); dir != "" {
		return dir
	}
	return strings.TrimSpace(op.ClaudeConfigDir)
}
