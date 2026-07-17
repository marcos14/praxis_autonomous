package gitops

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// TemRemote informa se o repo tem ao menos um remote configurado.
func TemRemote(repo string) bool {
	out, err := git(repo, "remote")
	if err != nil {
		return false
	}
	return strings.TrimSpace(out) != ""
}

// Fetch traz a branch principal do remote origin, para que a branch da demanda
// parta da main atualizada. Sem remote configurado e no-op (parte da main
// local). Serializado pelo mutex do projeto.
func (o *Ops) Fetch(repo, branch string) error {
	defer o.trava(repo)()
	if !TemRemote(repo) {
		return nil
	}
	_, err := git(repo, "fetch", "origin", branch)
	return err
}

// WorktreeAdd cria um worktree em caminho com uma branch NOVA (branch) derivada
// de base (tipicamente a main atualizada). A branch precisa ter o prefixo
// praxis/. Garante core.longpaths no Windows e cria os diretorios pais do
// worktree antes. Serializado pelo mutex do projeto.
func (o *Ops) WorktreeAdd(repo, caminho, branch, base string) error {
	if err := validarBranchPraxis(branch); err != nil {
		return err
	}
	defer o.trava(repo)()
	if err := garantirLongPaths(repo); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(caminho), 0o755); err != nil {
		return err
	}
	_, err := git(repo, "worktree", "add", "-b", branch, caminho, base)
	return err
}

// WorktreeRemove remove o worktree em caminho (--force: descarta arquivos nao
// commitados/sobras). O processo do harness deve ser encerrado antes no Windows
// (Fase 2i). Serializado pelo mutex do projeto.
func (o *Ops) WorktreeRemove(repo, caminho string) error {
	defer o.trava(repo)()
	_, err := git(repo, "worktree", "remove", "--force", caminho)
	return err
}

// RemoverBranch apaga a branch local (git branch -D). Guarda de seguranca: so
// aceita branches praxis/*. Usada na limpeza pos-integracao (Fase 4e). O worktree
// que a usava deve ter sido removido antes. Serializado pelo mutex do projeto.
func (o *Ops) RemoverBranch(repo, branch string) error {
	if err := validarBranchPraxis(branch); err != nil {
		return err
	}
	defer o.trava(repo)()
	_, err := git(repo, "branch", "-D", branch)
	return err
}

// BranchIntegrada informa se a branch ja esta totalmente contida em base (todos
// os seus commits alcancaveis a partir de base) — ou seja, o merge ja aconteceu
// (na main local ou, apos fetch, em origin/main passado como base). Usada para
// detectar o fechamento no modo merge_request (Fase 4e). So leitura.
func BranchIntegrada(repo, base, branch string) (bool, error) {
	base = strings.TrimSpace(base)
	branch = strings.TrimSpace(branch)
	if base == "" || branch == "" {
		return false, nil
	}
	commits, err := CommitsAFrente(repo, base, branch)
	if err != nil {
		return false, err
	}
	return len(commits) == 0, nil
}

// WorktreePrune limpa registros de worktrees cujo diretorio sumiu (orfaos apos
// crash/remocao manual). Serializado pelo mutex do projeto.
func (o *Ops) WorktreePrune(repo string) error {
	defer o.trava(repo)()
	_, err := git(repo, "worktree", "prune")
	return err
}

// PrepararRepoNoBoot faz a manutencao de boot de um repo: ativa core.longpaths
// (Windows) e roda worktree prune para descartar worktrees orfaos apos reinicio
// do servico. Serializado pelo mutex do projeto.
func (o *Ops) PrepararRepoNoBoot(repo string) error {
	defer o.trava(repo)()
	if err := garantirLongPaths(repo); err != nil {
		return err
	}
	_, err := git(repo, "worktree", "prune")
	return err
}

// garantirLongPaths ativa core.longpaths no repo — necessario no Windows para
// caminhos de worktree profundos (PRAXIS_HOME/worktrees/<projeto>/<demanda>/...).
// Fora do Windows e no-op.
func garantirLongPaths(repo string) error {
	if runtime.GOOS != "windows" {
		return nil
	}
	_, err := git(repo, "config", "core.longpaths", "true")
	return err
}
