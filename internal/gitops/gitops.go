package gitops

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// PrefixoBranch e o prefixo obrigatorio das branches geridas pelo Praxis. Push
// e criacao de worktree so operam em branches com esse prefixo — a main (e
// qualquer outra branch) nunca e empurrada por engano.
const PrefixoBranch = "praxis/"

// ErrBranchNaoPraxis sinaliza que uma operacao restrita (push, worktree add) foi
// pedida para uma branch fora do prefixo praxis/.
var ErrBranchNaoPraxis = errors.New("gitops: operacao permitida apenas em branches praxis/*")

// Ops encapsula as operacoes git com serializacao por repositorio: um mutex por
// projeto garante que operacoes que mudam refs/worktrees do mesmo repo nunca
// correm em paralelo (evita corrida no index/HEAD compartilhado). Metodos que
// so leem (Toplevel, ArquivosMudados, PreviaMerge) nao tomam o mutex.
type Ops struct {
	mu      sync.Mutex
	porRepo map[string]*sync.Mutex
}

// Novo cria um Ops pronto para uso.
func Novo() *Ops {
	return &Ops{porRepo: map[string]*sync.Mutex{}}
}

// trava obtem (criando se preciso) o mutex do repo e o tranca; devolve a funcao
// de destrava, para uso com defer.
func (o *Ops) trava(repo string) func() {
	chave := chaveRepo(repo)
	o.mu.Lock()
	m := o.porRepo[chave]
	if m == nil {
		m = &sync.Mutex{}
		o.porRepo[chave] = m
	}
	o.mu.Unlock()
	m.Lock()
	return m.Unlock
}

// git executa `git -C dir <args...>` e devolve o stdout; em erro embute o stderr
// na mensagem, no mesmo estilo do git.go do Praxis atual.
func git(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return out.String(), fmt.Errorf("git %s em %s: %v — %s",
			strings.Join(args, " "), dir, err, strings.TrimSpace(errb.String()))
	}
	return out.String(), nil
}

// EhRepoGit informa se dir esta dentro de uma arvore de trabalho git (mesma
// deteccao usada em git.go:gitToplevel do Praxis atual).
func EhRepoGit(dir string) bool {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--is-inside-work-tree").Output()
	return err == nil && strings.TrimSpace(string(out)) == "true"
}

// Toplevel devolve a raiz do repositorio que contem dir ("" se nao for repo).
func Toplevel(dir string) string {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// Limpo reporta se a arvore de trabalho em dir esta sem mudancas pendentes.
func Limpo(dir string) (bool, error) {
	out, err := git(dir, "status", "--porcelain")
	if err != nil {
		return false, err
	}
	return len(bytes.TrimSpace([]byte(out))) == 0, nil
}

// ArquivosMudados lista os caminhos com mudancas (staged, unstaged e untracked),
// relativos a raiz do repo, com barras normais.
func ArquivosMudados(dir string) ([]string, error) {
	out, err := git(dir, "status", "--porcelain")
	if err != nil {
		return nil, err
	}
	var nomes []string
	for _, l := range strings.Split(out, "\n") {
		if len(l) < 4 {
			continue
		}
		nome := strings.TrimSpace(l[3:])
		// renomeios vem como "antigo -> novo"
		if i := strings.Index(nome, " -> "); i >= 0 {
			nome = nome[i+4:]
		}
		nomes = append(nomes, strings.Trim(nome, `"`))
	}
	return nomes, nil
}

// Commit registra todas as mudancas de dir num commit com a mensagem dada
// (git add -A + git commit). Serializado pelo mutex do repo. O commit e sempre
// do orquestrador — o harness continua proibido de commitar.
func (o *Ops) Commit(dir, msg string) error {
	defer o.trava(dir)()
	if out, err := git(dir, "add", "-A"); err != nil {
		return fmt.Errorf("git add em %s: %w — %s", dir, err, out)
	}
	cmd := exec.Command("git", "-C", dir, "commit", "-F", "-")
	cmd.Stdin = strings.NewReader(msg)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git commit em %s: %v — %s", dir, err, out)
	}
	return nil
}

// validarBranchPraxis rejeita nomes de branch fora do prefixo praxis/ (e o
// prefixo sozinho, sem sufixo).
func validarBranchPraxis(branch string) error {
	branch = strings.TrimSpace(branch)
	if !strings.HasPrefix(branch, PrefixoBranch) || branch == PrefixoBranch {
		return ErrBranchNaoPraxis
	}
	return nil
}

// chaveRepo normaliza o caminho de um repo para servir de chave do mutex por
// projeto: usa o git-common-dir (o .git compartilhado), de modo que o repo
// principal e todos os seus worktrees vinculados caiam no MESMO lock — a
// serializacao e por projeto, nao por arvore de trabalho.
func chaveRepo(repo string) string {
	out, err := exec.Command("git", "-C", repo, "rev-parse", "--path-format=absolute", "--git-common-dir").Output()
	if err == nil {
		if s := strings.TrimSpace(string(out)); s != "" {
			return filepath.Clean(s)
		}
	}
	if abs, err := filepath.Abs(repo); err == nil {
		return filepath.Clean(abs)
	}
	return filepath.Clean(repo)
}
