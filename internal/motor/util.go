package motor

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

// verificarNaoRoot barra a execução autônoma como root no Linux/macOS (Fase B):
// o CLI do claude recusa `--dangerously-skip-permissions` com UID 0. A exceção
// é IS_SANDBOX=1 — a válvula do próprio vendor para ambientes conteinerizados,
// onde rodar como root do CONTAINER é aceitável; o Praxis nunca a define no
// host (containers gerenciados são a Fase E).
func verificarNaoRoot() error {
	if runtime.GOOS == "windows" || os.Geteuid() != 0 || os.Getenv("IS_SANDBOX") == "1" {
		return nil
	}
	return errors.New("o harness não roda como root: registre o serviço com uma conta não-root " +
		"(sudo praxis service install cria a conta de sistema 'praxis' — ver o guia, seção 8.1); " +
		"em containers, use um usuário não-root ou defina IS_SANDBOX=1")
}

// agoraTS devolve um carimbo de tempo compacto para compor nomes de arquivo de
// log (`<rotulo>-<agoraTS>.jsonl`).
func agoraTS() string { return time.Now().Format("20060102-150405") }

// ultimasLinhas devolve as ultimas n linhas de s (usado para resumir stderr).
func ultimasLinhas(s string, n int) string {
	linhas := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(linhas) > n {
		linhas = linhas[len(linhas)-n:]
	}
	return strings.Join(linhas, "\n")
}

// primeirasLinhas devolve as primeiras n linhas de s, sinalizando corte.
func primeirasLinhas(s string, n int) string {
	linhas := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(linhas) > n {
		linhas = append(linhas[:n:n], "(...)")
	}
	return strings.Join(linhas, "\n")
}

// ResumoErro resume um ResultadoRun que terminou com IsError para compor
// mensagens de falha: o subtipo do harness sozinho ("success",
// "error_during_execution") esconde a causa; a primeira linha do texto do
// resultado costuma traze-la (ex.: "Not logged in · Please run /login").
func ResumoErro(res *ResultadoRun) string {
	if res == nil {
		return ""
	}
	sub := strings.TrimSpace(res.Subtipo)
	linha := strings.TrimSpace(res.Resultado)
	if i := strings.IndexByte(linha, '\n'); i >= 0 {
		linha = strings.TrimSpace(linha[:i]) + " (...)"
	}
	if r := []rune(linha); len(r) > 200 {
		linha = string(r[:200]) + " (...)"
	}
	switch {
	case linha == "":
		return sub
	case sub == "":
		return linha
	default:
		return sub + ": " + linha
	}
}

// indentar prefixa cada linha de s com prefixo (para o eco ao vivo no console).
func indentar(s, prefixo string) string {
	return prefixo + strings.ReplaceAll(s, "\n", "\n"+prefixo)
}

// normalizarNomeMotor deixa o nome de motor/alias em minusculas e sem espacos.
func normalizarNomeMotor(nome string) string {
	return strings.ToLower(strings.TrimSpace(nome))
}

// motoresConhecidos lista, em ordem alfabetica, os motores registrados.
func motoresConhecidos() []string {
	out := make([]string, 0, len(motoresRegistrados))
	for nome := range motoresRegistrados {
		out = append(out, nome)
	}
	sort.Strings(out)
	return out
}

// resolverDir transforma um add-dir relativo a raiz (worktree) num caminho
// utilizavel; caminhos absolutos passam intactos.
func resolverDir(raiz, dir string) string {
	if dir == "" || dir == "." {
		return raiz
	}
	if filepath.IsAbs(dir) {
		return dir
	}
	return filepath.Join(raiz, dir)
}

// contemString informa se alvo esta em vs.
func contemString(vs []string, alvo string) bool {
	for _, v := range vs {
		if v == alvo {
			return true
		}
	}
	return false
}
