package gitops

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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

// ErrBranchInvalida sinaliza que o nome de branch informado pelo usuario, apos
// retirar o prefixo praxis/, nao e um sufixo de ref git valido.
var ErrBranchInvalida = errors.New("gitops: nome de branch invalido")

// reSufixoBranch valida o sufixo de uma branch praxis/<sufixo>: comeca por
// letra/digito e segue com letras, digitos, ponto, hifen, sublinhado ou barra.
var reSufixoBranch = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]*$`)

// NomeBranch valida e normaliza o nome de branch ESCOLHIDO pelo usuario na
// criacao da demanda, garantindo o prefixo praxis/ (push e worktree add so operam
// nesse prefixo — ver validarBranchPraxis). Regras:
//   - entrada vazia devolve "" (o pipeline cai no nome automatico
//     praxis/d<id>-<slug>);
//   - um praxis/ ja digitado nao e duplicado;
//   - o sufixo precisa ser uma ref git valida: sem espacos, sem ".." nem "//",
//     sem terminar em ponto — caso contrario devolve ErrBranchInvalida.
func NormalizarBranch(entrada string) (string, error) {
	s := strings.TrimSpace(entrada)
	if s == "" {
		return "", nil
	}
	s = strings.TrimPrefix(s, PrefixoBranch)
	s = strings.Trim(s, "/")
	if s == "" || !reSufixoBranch.MatchString(s) ||
		strings.Contains(s, "..") || strings.Contains(s, "//") ||
		strings.HasSuffix(s, ".") {
		return "", ErrBranchInvalida
	}
	return PrefixoBranch + s, nil
}

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
	return gitEnv(dir, nil, args...)
}

// gitEnv é o git com variáveis extras no ambiente do comando — o caminho dos
// comandos que criam commit (commit, merge), que recebem a identidade
// autor/committer por GIT_AUTHOR_*/GIT_COMMITTER_* (ver Identidade.env).
func gitEnv(dir string, extra []string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if len(extra) > 0 {
		cmd.Env = append(os.Environ(), extra...)
	}
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return out.String(), fmt.Errorf("git %s em %s: %v — %s",
			strings.Join(args, " "), dir, err, strings.TrimSpace(errb.String()))
	}
	return out.String(), nil
}

// StatusPorcelain devolve a saida de `git status --porcelain` do repo — a
// fotografia do working tree usada pela rede de seguranca do estrategista
// (comparar o estado antes/depois de um turno para detectar escrita indevida).
func StatusPorcelain(repo string) (string, error) {
	return git(repo, "status", "--porcelain")
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
// do orquestrador — o harness continua proibido de commitar. A identidade vem
// SEMPRE de autor (env por comando), nunca da config git da maquina: autor =
// usuario da plataforma (valor zero = o proprio Praxis), committer = Praxis.
func (o *Ops) Commit(dir, msg string, autor Identidade) error {
	defer o.trava(dir)()
	if out, err := git(dir, "add", "-A"); err != nil {
		return fmt.Errorf("git add em %s: %w — %s", dir, err, out)
	}
	cmd := exec.Command("git", "-C", dir, "commit", "-F", "-")
	cmd.Stdin = strings.NewReader(msg)
	cmd.Env = append(os.Environ(), autor.env()...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git commit em %s: %v — %s", dir, err, out)
	}
	return nil
}

// DescartarMudancas descarta TODAS as mudancas nao commitadas da arvore de
// trabalho em dir (git reset --hard HEAD + git clean -fd), inclusive arquivos
// novos. Usado pelo "reiniciar fase": a sobra de um run interrompido pertence a
// fase que sera reexecutada do zero — sem o descarte, a pre-checagem de arvore
// limpa barraria a fase reiniciada. Serializado pelo mutex do repo.
func (o *Ops) DescartarMudancas(dir string) error {
	defer o.trava(dir)()
	if out, err := git(dir, "reset", "--hard", "HEAD"); err != nil {
		return fmt.Errorf("git reset --hard em %s: %w — %s", dir, err, out)
	}
	if out, err := git(dir, "clean", "-fd"); err != nil {
		return fmt.Errorf("git clean em %s: %w — %s", dir, err, out)
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
