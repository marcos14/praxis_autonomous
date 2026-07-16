package motor

import (
	"path/filepath"
	"sort"
	"strings"
	"time"
)

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
