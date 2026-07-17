package gitops

import (
	"fmt"
	"os/exec"
	"strings"
)

// Previa e o resultado de uma simulacao de merge (git merge-tree): informa se o
// merge seria limpo e, se nao, quais arquivos entram em conflito.
type Previa struct {
	Limpo     bool     // true = merge sem conflitos
	Conflitos []string // arquivos em conflito (vazio quando Limpo)
	ArvoreOID string   // OID da arvore resultante (informativo)
}

// PreviaMerge simula o merge de branch em base SEM tocar a arvore de trabalho
// nem as refs, via `git merge-tree --write-tree --name-only`. Nao toma o mutex
// do projeto (nao altera estado). Codigo de saida 0 = limpo, 1 = conflito,
// demais = erro.
func (o *Ops) PreviaMerge(repo, base, branch string) (Previa, error) {
	cmd := exec.Command("git", "-C", repo, "merge-tree", "--write-tree", "--name-only", base, branch)
	out, err := cmd.Output()

	codigo := 0
	var stderr string
	if err != nil {
		saida, ok := err.(*exec.ExitError)
		if !ok {
			return Previa{}, fmt.Errorf("git merge-tree em %s: %w", repo, err)
		}
		codigo = saida.ExitCode()
		stderr = strings.TrimSpace(string(saida.Stderr))
	}

	linhas := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	oid := ""
	if len(linhas) > 0 {
		oid = strings.TrimSpace(linhas[0])
	}

	switch codigo {
	case 0:
		return Previa{Limpo: true, ArvoreOID: oid}, nil
	case 1:
		// Formato de conflito do git merge-tree --write-tree --name-only:
		//   <OID>
		//   <arquivos em conflito, um por linha>
		//   <linha em branco>
		//   <mensagens informativas: "Auto-merging ...", "CONFLICT ...">
		// A secao de arquivos termina na PRIMEIRA linha em branco; tudo depois
		// dela sao mensagens informativas que nao devem entrar em Conflitos.
		var conflitos []string
		for _, l := range linhas[1:] {
			if strings.TrimSpace(l) == "" {
				break
			}
			conflitos = append(conflitos, strings.TrimSpace(l))
		}
		return Previa{Limpo: false, ArvoreOID: oid, Conflitos: conflitos}, nil
	default:
		detalhe := stderr
		if detalhe == "" {
			detalhe = strings.TrimSpace(string(out))
		}
		return Previa{}, fmt.Errorf("git merge-tree em %s falhou (codigo %d): %s", repo, codigo, detalhe)
	}
}

// MergeNaBranch traz `incoming` (ex.: "main" ou "origin/main") para dentro da
// branch atualmente em check-out no worktree, via `git merge --no-edit`. É a
// operação do "Atualizar branch" (traz a main para a branch da demanda, Fase 4d).
// Se o merge conflitar, ABORTA (`git merge --abort`) e devolve erro — o worktree
// fica intacto; o chamador deve ter checado antes com PreviaMerge. Serializado
// pelo mutex do projeto (o worktree compartilha a chave do repo principal).
func (o *Ops) MergeNaBranch(worktree, incoming, msg string) error {
	incoming = strings.TrimSpace(incoming)
	if incoming == "" {
		return fmt.Errorf("gitops: ref de origem vazia")
	}
	defer o.trava(worktree)()
	args := []string{"merge", "--no-edit", incoming}
	if msg != "" {
		args = append(args, "-m", msg)
	}
	if out, err := git(worktree, args...); err != nil {
		// deixa o worktree limpo (sem estado de merge pendente).
		_, _ = git(worktree, "merge", "--abort")
		return fmt.Errorf("git merge de %s no worktree %s: %w — %s", incoming, worktree, err, out)
	}
	return nil
}

// CommitInfo descreve um commit para exibição no card (hash curto + assunto).
type CommitInfo struct {
	Hash    string `json:"hash"`    // hash abreviado
	Assunto string `json:"assunto"` // primeira linha da mensagem
}

// CommitsAFrente lista os commits presentes em branch e ausentes em base (o que
// a demanda acrescenta sobre a main), do mais recente para o mais antigo. Só
// leitura — não toma o mutex. Base ou branch inexistente devolve erro.
func CommitsAFrente(repo, base, branch string) ([]CommitInfo, error) {
	base = strings.TrimSpace(base)
	branch = strings.TrimSpace(branch)
	if base == "" || branch == "" {
		return nil, fmt.Errorf("gitops: base/branch vazia")
	}
	out, err := git(repo, "log", "--format=%h%x1f%s", base+".."+branch)
	if err != nil {
		return nil, err
	}
	commits := []CommitInfo{}
	for _, linha := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if strings.TrimSpace(linha) == "" {
			continue
		}
		partes := strings.SplitN(linha, "\x1f", 2)
		c := CommitInfo{Hash: strings.TrimSpace(partes[0])}
		if len(partes) == 2 {
			c.Assunto = strings.TrimSpace(partes[1])
		}
		commits = append(commits, c)
	}
	return commits, nil
}

// MergeNoFF integra branch em base com --no-ff (sempre gera commit de merge),
// para o modo de integracao merge_local. Faz checkout de base no repo principal
// e o merge. Serializado pelo mutex do projeto. Em conflito, devolve erro e
// deixa o repo em estado de merge (o tratamento — status conflito + resolucao —
// e da Fase 4d); o chamador pode inspecionar com PreviaMerge antes.
func (o *Ops) MergeNoFF(repo, base, branch, msg string) error {
	defer o.trava(repo)()
	if out, err := git(repo, "checkout", base); err != nil {
		return fmt.Errorf("checkout de %s: %w — %s", base, err, out)
	}
	args := []string{"merge", "--no-ff", branch}
	if msg != "" {
		args = append(args, "-m", msg)
	}
	if out, err := git(repo, args...); err != nil {
		return fmt.Errorf("merge --no-ff de %s em %s: %w — %s", branch, base, err, out)
	}
	return nil
}
