package gitops

import (
	"fmt"
	"strings"
)

// PosicionarBranchPrincipal deixa o repositório PRINCIPAL do projeto pronto
// para uma leitura fiel do código (analista/planejador/consultor leem a pasta
// do projeto em vez de um worktree): faz fetch do origin, posiciona na branch
// principal e avança por fast-forward.
//
// Regra de ouro: NUNCA descarta trabalho local. Com mudanças pendentes, branch
// divergida ou checkout impossível, nada é forçado — a função devolve um AVISO
// legível (para virar evento/fala de sistema) e o chamador segue com o estado
// que existe. Aviso vazio = repo posicionado e atualizado. Erro é reservado a
// falhas que impedem inspecionar o repo (ex.: não é um repositório git).
//
// Sem remote configurado, só garante o posicionamento na branch (repo local
// puro não tem de onde puxar). Serializado pelo mutex do projeto — não corre
// em paralelo com fetch/worktree/merge do scheduler no mesmo repo.
func (o *Ops) PosicionarBranchPrincipal(repo, branch string) (string, error) {
	branch = strings.TrimSpace(branch)
	if branch == "" {
		branch = "main"
	}
	defer o.trava(repo)()

	temRemote := TemRemote(repo)
	if temRemote {
		if _, err := o.gitRede(repo, "fetch", "origin", branch); err != nil {
			return fmt.Sprintf("não foi possível atualizar do origin (%v); a análise usará o estado local", err), nil
		}
	}

	atualBruto, err := git(repo, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", fmt.Errorf("identificar a branch atual de %s: %w", repo, err)
	}
	atual := strings.TrimSpace(atualBruto)

	limpo, err := Limpo(repo)
	if err != nil {
		return "", err
	}

	if atual != branch {
		if !limpo {
			return fmt.Sprintf("o repositório está na branch %q com mudanças locais; a análise usará esse estado (esperava a branch %q)", atual, branch), nil
		}
		if _, err := git(repo, "checkout", branch); err != nil {
			return fmt.Sprintf("não foi possível posicionar na branch %q (%v); a análise usará a branch %q", branch, err, atual), nil
		}
	} else if !limpo {
		return fmt.Sprintf("a branch %q tem mudanças locais não commitadas; o pull não foi aplicado", branch), nil
	}

	if temRemote {
		// Só fast-forward: se a branch local divergiu do origin, não criamos
		// merge nem rebase por conta própria — avisamos e usamos o estado local.
		if _, err := git(repo, "merge", "--ff-only", "origin/"+branch); err != nil {
			return fmt.Sprintf("a branch %q local divergiu de origin/%s; a análise usará o estado local", branch, branch), nil
		}
	}
	return "", nil
}
