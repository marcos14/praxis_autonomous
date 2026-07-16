package gitops

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// gitT roda um comando git num diretorio, falhando o teste em erro.
func gitT(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s em %s: %v\n%s", strings.Join(args, " "), dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

// escrever cria/sobrescreve um arquivo com conteudo.
func escrever(t *testing.T, dir, nome, conteudo string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, nome), []byte(conteudo), 0o644); err != nil {
		t.Fatal(err)
	}
}

// repoComRemote monta um repo de trabalho (repo) com um commit inicial na main e
// um remote origin bare (origin), com a main ja publicada. Devolve os dois
// caminhos.
func repoComRemote(t *testing.T) (repo, origin string) {
	t.Helper()
	base := t.TempDir()
	origin = filepath.Join(base, "origin.git")
	if out, err := exec.Command("git", "init", "--bare", "-b", "main", origin).CombinedOutput(); err != nil {
		t.Fatalf("init bare: %v\n%s", err, out)
	}
	repo = filepath.Join(base, "repo")
	if out, err := exec.Command("git", "init", "-b", "main", repo).CombinedOutput(); err != nil {
		t.Fatalf("init repo: %v\n%s", err, out)
	}
	gitT(t, repo, "config", "user.email", "teste@praxis.local")
	gitT(t, repo, "config", "user.name", "Praxis Teste")
	escrever(t, repo, "a.txt", "v0\n")
	gitT(t, repo, "add", "-A")
	gitT(t, repo, "commit", "-m", "commit inicial")
	gitT(t, repo, "remote", "add", "origin", origin)
	gitT(t, repo, "push", "-u", "origin", "main")
	return repo, origin
}

func TestEhRepoGitEToplevel(t *testing.T) {
	repo, _ := repoComRemote(t)
	if !EhRepoGit(repo) {
		t.Fatal("EhRepoGit devia ser true para o repo")
	}
	naoRepo := t.TempDir()
	if EhRepoGit(naoRepo) {
		t.Fatal("EhRepoGit devia ser false fora de repo git")
	}
	if got := Toplevel(repo); filepath.Clean(got) != filepath.Clean(repo) {
		// em alguns SOs o TempDir tem symlink; compara pelo basename
		if filepath.Base(got) != filepath.Base(repo) {
			t.Fatalf("Toplevel = %q, esperava conter %q", got, repo)
		}
	}
	if Toplevel(naoRepo) != "" {
		t.Fatal("Toplevel de nao-repo devia ser vazio")
	}
}

func TestCommitELimpo(t *testing.T) {
	repo, _ := repoComRemote(t)
	o := Novo()

	if limpo, err := Limpo(repo); err != nil || !limpo {
		t.Fatalf("repo recem-criado devia estar limpo (limpo=%v err=%v)", limpo, err)
	}
	escrever(t, repo, "b.txt", "novo\n")
	if limpo, _ := Limpo(repo); limpo {
		t.Fatal("apos escrever arquivo, repo nao devia estar limpo")
	}
	mudados, err := ArquivosMudados(repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(mudados) != 1 || mudados[0] != "b.txt" {
		t.Fatalf("ArquivosMudados = %v, esperava [b.txt]", mudados)
	}
	if err := o.Commit(repo, "adiciona b.txt"); err != nil {
		t.Fatal(err)
	}
	if limpo, _ := Limpo(repo); !limpo {
		t.Fatal("apos commit, repo devia estar limpo")
	}
	if msg := gitT(t, repo, "log", "-1", "--pretty=%s"); msg != "adiciona b.txt" {
		t.Fatalf("mensagem do commit = %q", msg)
	}
}

func TestWorktreeAddRemovePrune(t *testing.T) {
	repo, _ := repoComRemote(t)
	o := Novo()
	wt := filepath.Join(t.TempDir(), "sub", "wt-d1")
	branch := "praxis/d1-teste"

	if err := o.WorktreeAdd(repo, wt, branch, "main"); err != nil {
		t.Fatalf("WorktreeAdd: %v", err)
	}
	if _, err := os.Stat(filepath.Join(wt, "a.txt")); err != nil {
		t.Fatalf("worktree devia conter a.txt: %v", err)
	}
	if br := gitT(t, wt, "rev-parse", "--abbrev-ref", "HEAD"); br != branch {
		t.Fatalf("branch do worktree = %q, esperava %q", br, branch)
	}
	if lista := gitT(t, repo, "worktree", "list"); !strings.Contains(lista, branch) {
		t.Fatalf("worktree list nao mostra a branch:\n%s", lista)
	}

	if err := o.WorktreeRemove(repo, wt); err != nil {
		t.Fatalf("WorktreeRemove: %v", err)
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Fatalf("worktree devia ter sido removido (err=%v)", err)
	}
	if err := o.WorktreePrune(repo); err != nil {
		t.Fatalf("WorktreePrune: %v", err)
	}
}

func TestWorktreeAddRecusaBranchNaoPraxis(t *testing.T) {
	repo, _ := repoComRemote(t)
	o := Novo()
	wt := filepath.Join(t.TempDir(), "wt")
	if err := o.WorktreeAdd(repo, wt, "feature/x", "main"); err != ErrBranchNaoPraxis {
		t.Fatalf("esperava ErrBranchNaoPraxis, obteve %v", err)
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Fatal("nao devia ter criado worktree para branch invalida")
	}
}

func TestPushPublicaBranch(t *testing.T) {
	repo, origin := repoComRemote(t)
	o := Novo()
	wt := filepath.Join(t.TempDir(), "wt-d2")
	branch := "praxis/d2-push"
	if err := o.WorktreeAdd(repo, wt, branch, "main"); err != nil {
		t.Fatal(err)
	}
	escrever(t, wt, "c.txt", "conteudo\n")
	if err := o.Commit(wt, "adiciona c.txt"); err != nil {
		t.Fatal(err)
	}
	if err := o.Push(repo, branch, 1); err != nil {
		t.Fatalf("Push: %v", err)
	}
	// a branch agora existe no origin bare
	if out, err := exec.Command("git", "-C", origin, "rev-parse", "--verify", branch).CombinedOutput(); err != nil {
		t.Fatalf("branch nao publicada no origin: %v\n%s", err, out)
	}
}

func TestPushRecusaBranchNaoPraxis(t *testing.T) {
	repo, _ := repoComRemote(t)
	o := Novo()
	if err := o.Push(repo, "main", 1); err != ErrBranchNaoPraxis {
		t.Fatalf("esperava ErrBranchNaoPraxis ao empurrar main, obteve %v", err)
	}
}

func TestPushRetentaEFalha(t *testing.T) {
	repo, _ := repoComRemote(t)
	// aponta o origin para um caminho inexistente → push sempre falha
	gitT(t, repo, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "nao-existe.git"))
	branch := "praxis/d3-falha"
	gitT(t, repo, "branch", branch, "main")

	o := Novo()
	orig := EsperaEntreTentativas
	EsperaEntreTentativas = time.Millisecond
	defer func() { EsperaEntreTentativas = orig }()

	inicio := time.Now()
	if err := o.Push(repo, branch, 3); err == nil {
		t.Fatal("esperava erro de push para remote invalido")
	}
	if dur := time.Since(inicio); dur < 2*time.Millisecond {
		// 3 tentativas ⇒ 2 esperas (1x + 2x = 3ms); tolerancia frouxa
		t.Logf("duracao %v (esperas curtas ok)", dur)
	}
}

func TestCommitsNaoPublicados(t *testing.T) {
	repo, _ := repoComRemote(t)
	o := Novo()
	wt := filepath.Join(t.TempDir(), "wt-np")
	branch := "praxis/d9-naopub"
	if err := o.WorktreeAdd(repo, wt, branch, "main"); err != nil {
		t.Fatal(err)
	}

	// branch recem-criada de origin/main: nada proprio ainda → 0.
	if n, err := CommitsNaoPublicados(wt, branch); err != nil || n != 0 {
		t.Fatalf("branch nova: n=%d err=%v, esperava 0", n, err)
	}

	// dois commits locais, nenhum publicado → 2.
	escrever(t, wt, "c1.txt", "1\n")
	if err := o.Commit(wt, "c1"); err != nil {
		t.Fatal(err)
	}
	escrever(t, wt, "c2.txt", "2\n")
	if err := o.Commit(wt, "c2"); err != nil {
		t.Fatal(err)
	}
	if n, err := CommitsNaoPublicados(wt, branch); err != nil || n != 2 {
		t.Fatalf("2 commits locais: n=%d err=%v, esperava 2", n, err)
	}

	// apos publicar, tudo esta no origin → 0.
	if err := o.Push(repo, branch, 1); err != nil {
		t.Fatal(err)
	}
	if n, err := CommitsNaoPublicados(wt, branch); err != nil || n != 0 {
		t.Fatalf("apos push: n=%d err=%v, esperava 0", n, err)
	}

	// mais um commit apos o push → 1 pendente.
	escrever(t, wt, "c3.txt", "3\n")
	if err := o.Commit(wt, "c3"); err != nil {
		t.Fatal(err)
	}
	if n, err := CommitsNaoPublicados(wt, branch); err != nil || n != 1 {
		t.Fatalf("commit apos push: n=%d err=%v, esperava 1", n, err)
	}
}

func TestPreviaMergeLimpo(t *testing.T) {
	repo, _ := repoComRemote(t)
	o := Novo()
	gitT(t, repo, "checkout", "-b", "praxis/d4-limpo", "main")
	escrever(t, repo, "novo.txt", "sem conflito\n")
	gitT(t, repo, "add", "-A")
	gitT(t, repo, "commit", "-m", "arquivo novo")
	gitT(t, repo, "checkout", "main")

	p, err := o.PreviaMerge(repo, "main", "praxis/d4-limpo")
	if err != nil {
		t.Fatal(err)
	}
	if !p.Limpo || len(p.Conflitos) != 0 {
		t.Fatalf("esperava merge limpo, obteve %+v", p)
	}
	if p.ArvoreOID == "" {
		t.Fatal("esperava ArvoreOID preenchido")
	}
}

func TestPreviaMergeConflito(t *testing.T) {
	repo, _ := repoComRemote(t)
	o := Novo()
	// branch muda a.txt
	gitT(t, repo, "checkout", "-b", "praxis/d5-conf", "main")
	escrever(t, repo, "a.txt", "versao-branch\n")
	gitT(t, repo, "add", "-A")
	gitT(t, repo, "commit", "-m", "branch muda a.txt")
	// main muda a mesma linha
	gitT(t, repo, "checkout", "main")
	escrever(t, repo, "a.txt", "versao-main\n")
	gitT(t, repo, "add", "-A")
	gitT(t, repo, "commit", "-m", "main muda a.txt")

	p, err := o.PreviaMerge(repo, "main", "praxis/d5-conf")
	if err != nil {
		t.Fatal(err)
	}
	if p.Limpo {
		t.Fatal("esperava conflito")
	}
	// Conflitos deve conter EXATAMENTE os arquivos em conflito, sem as mensagens
	// informativas do git merge-tree ("Auto-merging ...", "CONFLICT ...").
	if len(p.Conflitos) != 1 || p.Conflitos[0] != "a.txt" {
		t.Fatalf("esperava Conflitos == [a.txt], obteve %v", p.Conflitos)
	}
}

func TestMergeNoFF(t *testing.T) {
	repo, _ := repoComRemote(t)
	o := Novo()
	gitT(t, repo, "checkout", "-b", "praxis/d6-merge", "main")
	escrever(t, repo, "feature.txt", "feita\n")
	gitT(t, repo, "add", "-A")
	gitT(t, repo, "commit", "-m", "feature")
	gitT(t, repo, "checkout", "main")

	if err := o.MergeNoFF(repo, "main", "praxis/d6-merge", "merge da d6"); err != nil {
		t.Fatalf("MergeNoFF: %v", err)
	}
	// main agora tem o arquivo
	if _, err := os.Stat(filepath.Join(repo, "feature.txt")); err != nil {
		t.Fatalf("main devia conter feature.txt apos merge: %v", err)
	}
	// existe um commit de merge (--no-ff nunca faz fast-forward)
	if merges := gitT(t, repo, "log", "--merges", "--pretty=%s"); !strings.Contains(merges, "merge da d6") {
		t.Fatalf("esperava commit de merge, log de merges:\n%q", merges)
	}
}

func TestGarantirLongPathsEBoot(t *testing.T) {
	repo, _ := repoComRemote(t)
	o := Novo()
	if err := o.PrepararRepoNoBoot(repo); err != nil {
		t.Fatalf("PrepararRepoNoBoot: %v", err)
	}
	// no Windows core.longpaths deve estar setado; nos demais SOs, e no-op
	out, err := exec.Command("git", "-C", repo, "config", "--get", "core.longpaths").CombinedOutput()
	val := strings.TrimSpace(string(out))
	if isWindows() {
		if err != nil || val != "true" {
			t.Fatalf("no Windows core.longpaths devia ser true (val=%q err=%v)", val, err)
		}
	}
}

func TestMutexPorProjetoMesmaChave(t *testing.T) {
	repo, _ := repoComRemote(t)
	o := Novo()
	// o repo principal e um worktree vinculado compartilham o mesmo lock (chave
	// = git-common-dir), serializando operacoes por projeto.
	wt := filepath.Join(t.TempDir(), "wt-lock")
	if err := o.WorktreeAdd(repo, wt, "praxis/d7-lock", "main"); err != nil {
		t.Fatal(err)
	}
	if chaveRepo(repo) != chaveRepo(wt) {
		t.Fatalf("repo e worktree deviam ter a mesma chave de mutex:\n%q\n%q", chaveRepo(repo), chaveRepo(wt))
	}
}

func isWindows() bool { return os.PathSeparator == '\\' }
