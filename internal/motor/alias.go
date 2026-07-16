package motor

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Uma conta Claude secundaria e um par (alias, CLAUDE_CONFIG_DIR): o alias
// identifica a conta e o config dir guarda o login/sessao dela. No Praxis
// Autonomous isso corresponde a uma linha de engine_accounts (alias +
// config_dir) — a config vem do banco, nao mais de autopilot.json.
//
// Portado/adaptado de claude_alias.go do Praxis atual. Ficaram de fora as
// partes acopladas ao arquivo de config (cmdClaudeAliasCreate, edicao de
// motores.operacoes/fallback.ordem e o parsing do subcomando de CLI): no novo
// desenho isso vira cadastro de conta no banco e a ordem de fallback e a
// coluna engines.prioridade. Sobra o util reaproveitavel: validacao do alias,
// sugestao de config dir e o atalho de shell para o operador logar na conta.

var reAliasClaude = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)

// ValidarAlias verifica o formato do alias de uma conta Claude e recusa nomes
// reservados (a conta padrao "claude") ou que colidam com um motor registrado.
func ValidarAlias(alias string) error {
	alias = normalizarNomeMotor(alias)
	if !reAliasClaude.MatchString(alias) {
		return fmt.Errorf("alias invalido %q: use letras, numeros, _ ou -, comecando por letra", alias)
	}
	if alias == "claude" {
		return fmt.Errorf("alias %q e reservado para a conta Claude padrao", alias)
	}
	if _, ok := motoresRegistrados[alias]; ok {
		return fmt.Errorf("alias %q conflita com um motor existente", alias)
	}
	return nil
}

// SugerirConfigDir sugere um CLAUDE_CONFIG_DIR para um alias (~/.claude-<sufixo>).
func SugerirConfigDir(alias string) string {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return ""
	}
	sufixo := strings.TrimPrefix(alias, "claude-")
	sufixo = strings.TrimPrefix(sufixo, "claude_")
	if sufixo == "" || sufixo == alias {
		sufixo = alias
	}
	return filepath.Join(home, ".claude-"+sufixo)
}

// NomesComandoShell devolve os nomes de comando de shell candidatos para o
// alias (com hifen e com underscore), sem duplicar.
func NomesComandoShell(alias string) []string {
	alias = normalizarNomeMotor(alias)
	var out []string
	add := func(v string) {
		v = strings.TrimSpace(v)
		if v == "" || contemString(out, v) {
			return
		}
		out = append(out, v)
	}
	add(strings.ReplaceAll(alias, "_", "-"))
	add(alias)
	return out
}

// ResultadoShellAlias resume o que ConfigurarShellAlias fez.
type ResultadoShellAlias struct {
	Comandos   []string // nomes de funcao criados (ex.: claude-alt, claude_alt)
	Perfis     []string // perfis de PowerShell alterados
	ReloadHint string   // linha para recarregar o perfil
}

// ConfigurarShellAlias cria/atualiza, no(s) perfil(is) do PowerShell, uma
// funcao de atalho por nome (claude-<alias>) que exporta CLAUDE_CONFIG_DIR e
// chama `claude`, para o operador logar na conta secundaria. So funciona no
// Windows PowerShell; a operacao e idempotente (blocos marcados).
func ConfigurarShellAlias(alias, configDir string) (ResultadoShellAlias, error) {
	cmds := NomesComandoShell(alias)
	if len(cmds) == 0 {
		return ResultadoShellAlias{}, fmt.Errorf("nenhum nome de comando para alias de shell")
	}
	if strings.TrimSpace(configDir) == "" {
		return ResultadoShellAlias{}, fmt.Errorf("config dir vazio para o alias %q", alias)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ResultadoShellAlias{}, fmt.Errorf("nao consegui descobrir o HOME para configurar perfil do PowerShell: %w", err)
	}
	perfis := caminhosPerfilPowerShell(home)
	if len(perfis) == 0 {
		return ResultadoShellAlias{}, fmt.Errorf("nao consegui montar lista de perfis do PowerShell")
	}
	for _, p := range perfis {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return ResultadoShellAlias{}, fmt.Errorf("nao consegui preparar a pasta do perfil do PowerShell (%s): %w", p, err)
		}
		if _, err := os.Stat(p); err != nil {
			if os.IsNotExist(err) {
				if err := os.WriteFile(p, []byte(""), 0o600); err != nil {
					return ResultadoShellAlias{}, fmt.Errorf("nao consegui criar perfil do PowerShell (%s): %w", p, err)
				}
			} else {
				return ResultadoShellAlias{}, fmt.Errorf("falha ao acessar perfil do PowerShell (%s): %w", p, err)
			}
		}
		for _, nome := range cmds {
			if err := upsertBlocoAliasPowerShell(p, nome, configDir); err != nil {
				return ResultadoShellAlias{}, err
			}
		}
	}
	return ResultadoShellAlias{
		Comandos:   cmds,
		Perfis:     perfis,
		ReloadHint: fmt.Sprintf("if (Test-Path $PROFILE) { . $PROFILE } else { . '%s' }", perfis[0]),
	}, nil
}

func caminhosPerfilPowerShell(home string) []string {
	home = strings.TrimSpace(home)
	if home == "" {
		return nil
	}
	candidatos := []string{
		filepath.Join(home, "Documents", "PowerShell", "Microsoft.PowerShell_profile.ps1"),
		filepath.Join(home, "Documents", "WindowsPowerShell", "Microsoft.PowerShell_profile.ps1"),
		filepath.Join(home, "OneDrive", "Documents", "PowerShell", "Microsoft.PowerShell_profile.ps1"),
		filepath.Join(home, "OneDrive", "Documents", "WindowsPowerShell", "Microsoft.PowerShell_profile.ps1"),
		filepath.Join(home, "OneDrive", "Documentos", "PowerShell", "Microsoft.PowerShell_profile.ps1"),
		filepath.Join(home, "OneDrive", "Documentos", "WindowsPowerShell", "Microsoft.PowerShell_profile.ps1"),
		filepath.Join(home, "Documentos", "PowerShell", "Microsoft.PowerShell_profile.ps1"),
		filepath.Join(home, "Documentos", "WindowsPowerShell", "Microsoft.PowerShell_profile.ps1"),
	}
	vistos := map[string]bool{}
	var out []string
	for _, c := range candidatos {
		if c == "" || vistos[c] {
			continue
		}
		vistos[c] = true
		out = append(out, c)
	}
	return out
}

func upsertBlocoAliasPowerShell(profilePath, nomeCmd, configDir string) error {
	start := "# >>> praxis-claude-alias:" + nomeCmd + " >>>"
	end := "# <<< praxis-claude-alias:" + nomeCmd + " <<<"
	qDir := strings.ReplaceAll(configDir, "'", "''")
	bloco := strings.TrimSpace(fmt.Sprintf(`%s
function %s {
    $env:CLAUDE_CONFIG_DIR = '%s'
    claude @args
}
%s`, start, nomeCmd, qDir, end))
	conteudoBytes, err := os.ReadFile(profilePath)
	if err != nil {
		return fmt.Errorf("nao consegui ler o perfil do PowerShell (%s): %w", profilePath, err)
	}
	conteudoNovo := upsertBlocoMarcado(string(conteudoBytes), start, end, bloco)
	if err := os.WriteFile(profilePath, []byte(conteudoNovo), 0o600); err != nil {
		return fmt.Errorf("nao consegui escrever o perfil do PowerShell (%s): %w", profilePath, err)
	}
	return nil
}

// upsertBlocoMarcado insere ou substitui um bloco delimitado por start/end no
// conteudo, de forma idempotente.
func upsertBlocoMarcado(conteudo, start, end, bloco string) string {
	if strings.TrimSpace(conteudo) == "" {
		return bloco + "\n"
	}
	ini := strings.Index(conteudo, start)
	fim := strings.Index(conteudo, end)
	if ini >= 0 && fim >= ini {
		fim += len(end)
		antes := strings.TrimRight(conteudo[:ini], "\n")
		depois := strings.TrimLeft(conteudo[fim:], "\n")
		if antes == "" && depois == "" {
			return bloco + "\n"
		}
		if antes == "" {
			return bloco + "\n" + depois
		}
		if depois == "" {
			return antes + "\n" + bloco + "\n"
		}
		return antes + "\n" + bloco + "\n" + depois
	}
	if !strings.HasSuffix(conteudo, "\n") {
		conteudo += "\n"
	}
	return conteudo + "\n" + bloco + "\n"
}
