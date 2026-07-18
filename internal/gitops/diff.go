package gitops

import (
	"fmt"
	"strings"
)

// DiffCompleto devolve o diff textual completo do que a branch acrescenta sobre a
// base (git diff base...branch, three-dot = a partir do merge-base), no formato
// unified padrão do git. É o "o que esta demanda alterou" por inteiro. Só leitura
// — não toma o mutex. Base/branch vazia ou inexistente devolve erro.
func DiffCompleto(repo, base, branch string) (string, error) {
	base = strings.TrimSpace(base)
	branch = strings.TrimSpace(branch)
	if base == "" || branch == "" {
		return "", fmt.Errorf("gitops: base/branch vazia")
	}
	out, err := git(repo, "diff", base+"..."+branch)
	if err != nil {
		return "", err
	}
	return out, nil
}

// DiffDaFase devolve o diff textual das mudanças de UMA fase da demanda. As fases
// commitam com a mensagem "Fase <codigo>: ..." (ver pipeline), então esta função
// localiza os commits da fase em base..branch (assunto com o prefixo "Fase
// <codigo>:") e concatena o diff de cada um, do mais antigo para o mais recente.
// Devolve "" (sem erro) quando a fase ainda não tem commit. Só leitura.
func DiffDaFase(repo, base, branch, codigoFase string) (string, error) {
	base = strings.TrimSpace(base)
	branch = strings.TrimSpace(branch)
	codigoFase = strings.TrimSpace(codigoFase)
	if base == "" || branch == "" {
		return "", fmt.Errorf("gitops: base/branch vazia")
	}
	if codigoFase == "" {
		return "", fmt.Errorf("gitops: código de fase vazio")
	}
	out, err := git(repo, "log", "--format=%H%x1f%s", base+".."+branch)
	if err != nil {
		return "", err
	}
	prefixo := "Fase " + codigoFase + ":"
	var hashes []string
	for _, linha := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if strings.TrimSpace(linha) == "" {
			continue
		}
		partes := strings.SplitN(linha, "\x1f", 2)
		if len(partes) != 2 {
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(partes[1]), prefixo) {
			hashes = append(hashes, strings.TrimSpace(partes[0]))
		}
	}
	if len(hashes) == 0 {
		return "", nil
	}
	// git log lista do mais recente para o mais antigo; invertemos para exibir a
	// fase em ordem cronológica (como ela foi construída).
	var b strings.Builder
	for i := len(hashes) - 1; i >= 0; i-- {
		d, err := git(repo, "diff", hashes[i]+"~1.."+hashes[i])
		if err != nil {
			return "", err
		}
		b.WriteString(d)
	}
	return b.String(), nil
}
