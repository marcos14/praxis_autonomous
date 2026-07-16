package pipeline

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/marcos14/praxis-autonomous/internal/db"
	"github.com/marcos14/praxis-autonomous/internal/gitops"
)

// gitCmd roda um comando git em dir, falhando o teste em erro.
func gitCmd(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s em %s: %v — %s", strings.Join(args, " "), dir, err, out)
	}
	return string(out)
}

// repoMain cria um repo git com a branch principal `main` e um commit inicial,
// devolvendo o diretorio. Diferente do gitInit (pipeline_test.go), fixa o nome da
// branch em main para casar com projects.branch_principal.
func repoMain(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitCmd(t, dir, "init", "-q", "-b", "main")
	gitCmd(t, dir, "config", "user.email", "praxis@test.local")
	gitCmd(t, dir, "config", "user.name", "Praxis Teste")
	gitCmd(t, dir, "config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("inicial\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, dir, "add", "-A")
	gitCmd(t, dir, "commit", "-q", "-m", "inicial")
	return dir
}

// bareOrigin cria um repo bare (para servir de remote origin) e devolve o dir.
func bareOrigin(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitCmd(t, dir, "init", "-q", "--bare", "-b", "main")
	return dir
}

// runnerComProjeto monta um banco temporario, um projeto apontando para `repo` e
// uma demanda com uma fase pendente. Devolve o Runner (Home isolado, motor stub
// feliz), a demanda e a fase.
func runnerComProjeto(t *testing.T, repo string) (*Runner, db.Demanda, db.Fase) {
	t.Helper()
	d, err := db.Abrir(filepath.Join(t.TempDir(), "praxis.db"))
	if err != nil {
		t.Fatalf("Abrir db: %v", err)
	}
	t.Cleanup(func() { d.Fechar() })

	ctx := context.Background()
	proj, err := d.CriarProjeto(ctx, db.Projeto{
		Nome: "Meu Projeto", Slug: "meu-projeto", Pasta: repo,
		BranchPrincipal: "main", ModoIntegracao: "merge_request",
	})
	if err != nil {
		t.Fatalf("CriarProjeto: %v", err)
	}
	dem, err := d.CriarDemanda(ctx, db.Demanda{
		ProjectID: proj.ID, Titulo: "Ajustar Login!", PlanoMD: "# plano",
		Status: db.StatusDemandaPronta,
	})
	if err != nil {
		t.Fatalf("CriarDemanda: %v", err)
	}
	fase, err := d.CriarFase(ctx, db.Fase{DemandID: dem.ID, Codigo: "1", Titulo: "Fase um", Status: db.StatusFasePendente})
	if err != nil {
		t.Fatalf("CriarFase: %v", err)
	}

	r := &Runner{
		Store:      d,
		Git:        gitops.Novo(),
		Home:       t.TempDir(),
		Prompt:     func(string) (string, error) { return "prompt {FASE} {TITULO}", nil },
		Selecionar: seletorStub(motorHappy("claude")),
	}
	return r, dem, fase
}

func configTeste() Config { return Config{MaxCorrecoes: 2, MaxCiclosRevisao: 2} }

// TestRunnerCicloDeFaseGeraCommitNaBranch e o criterio da Fase 2e: uma demanda de
// 1 fase cria branch/worktree, roda o ciclo completo e gera 1 commit na branch.
func TestRunnerCicloDeFaseGeraCommitNaBranch(t *testing.T) {
	repo := repoMain(t)
	r, dem, fase := runnerComProjeto(t, repo)
	ctx := context.Background()

	dem, err := r.Preparar(ctx, dem)
	if err != nil {
		t.Fatalf("Preparar: %v", err)
	}

	// branch e worktree dedicados nomeados por convencao e persistidos na demanda.
	wantBranch := "praxis/d" + strconv.FormatInt(dem.ID, 10) + "-ajustar-login"
	if dem.Branch != wantBranch {
		t.Fatalf("branch = %q, esperava %q", dem.Branch, wantBranch)
	}
	if dem.WorktreePath == "" {
		t.Fatal("worktree_path vazio apos Preparar")
	}
	if !gitops.EhRepoGit(dem.WorktreePath) {
		t.Fatalf("worktree %q nao e um repo git valido", dem.WorktreePath)
	}
	base := filepath.Base(dem.WorktreePath)
	if base != "d"+strconv.FormatInt(dem.ID, 10)+"-ajustar-login" {
		t.Fatalf("nome do dir do worktree = %q", base)
	}
	// persistiu no banco
	relido, err := r.Store.ObterDemanda(ctx, dem.ID)
	if err != nil {
		t.Fatalf("ObterDemanda: %v", err)
	}
	if relido.Branch != wantBranch || relido.WorktreePath != dem.WorktreePath {
		t.Fatalf("banco nao persistiu branch/worktree: %+v", relido)
	}

	// HEAD do worktree esta na branch da demanda.
	if h := strings.TrimSpace(gitCmd(t, dem.WorktreePath, "rev-parse", "--abbrev-ref", "HEAD")); h != wantBranch {
		t.Fatalf("HEAD do worktree = %q, esperava %q", h, wantBranch)
	}

	// roda o ciclo da fase → 1 commit.
	res, err := r.RodarFase(ctx, dem, fase, configTeste(), nil)
	if err != nil {
		t.Fatalf("RodarFase: %v", err)
	}
	if res.Situacao != SituacaoConcluida {
		t.Fatalf("situacao = %q, esperava concluida (erro: %s)", res.Situacao, res.Erro)
	}
	if !res.CommitFeito {
		t.Fatal("esperava commit feito na fase")
	}

	// exatamente 1 commit novo (inicial + fase) na branch da demanda.
	log := gitCmd(t, dem.WorktreePath, "log", "--oneline")
	if n := strings.Count(strings.TrimSpace(log), "\n"); n != 1 {
		t.Fatalf("esperava 2 commits (1 novo), log:\n%s", log)
	}
	if !strings.Contains(log, "Fase 1: Fase um [praxis]") {
		t.Fatalf("mensagem do commit da fase ausente:\n%s", log)
	}

	// a branch existe no repo principal e a main NAO foi tocada.
	branches := gitCmd(t, repo, "branch", "--list", wantBranch)
	if !strings.Contains(branches, wantBranch) {
		t.Fatalf("branch %q nao encontrada no repo principal:\n%s", wantBranch, branches)
	}
	if n := strings.Count(strings.TrimSpace(gitCmd(t, repo, "log", "--oneline", "main")), "\n"); n != 0 {
		t.Fatalf("a main ganhou commits (esperava so o inicial)")
	}
}

// TestRunnerPrepararBaseiaNaMainAtualizada: com remote, a branch parte de
// origin/<main> apos o fetch — inclui commits que ainda nao estao na main local.
func TestRunnerPrepararBaseiaNaMainAtualizada(t *testing.T) {
	origin := bareOrigin(t)
	repo := repoMain(t)
	gitCmd(t, repo, "remote", "add", "origin", origin)
	gitCmd(t, repo, "push", "-q", "-u", "origin", "main")

	// segundo clone avanca a main remota com um commit novo que o `repo` nao tem.
	outro := t.TempDir()
	gitCmd(t, outro, "clone", "-q", origin, ".")
	gitCmd(t, outro, "config", "user.email", "praxis@test.local")
	gitCmd(t, outro, "config", "user.name", "Praxis Teste")
	if err := os.WriteFile(filepath.Join(outro, "NOVO.md"), []byte("commit remoto\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, outro, "add", "-A")
	gitCmd(t, outro, "commit", "-q", "-m", "avanco remoto")
	gitCmd(t, outro, "push", "-q", "origin", "main")

	r, dem, _ := runnerComProjeto(t, repo)
	ctx := context.Background()

	// sanidade: o commit remoto ainda nao esta na main local.
	if _, err := os.Stat(filepath.Join(repo, "NOVO.md")); !os.IsNotExist(err) {
		t.Fatalf("NOVO.md nao deveria existir na main local ainda")
	}

	dem, err := r.Preparar(ctx, dem)
	if err != nil {
		t.Fatalf("Preparar: %v", err)
	}
	// o worktree, criado de origin/main atualizada, contem o commit remoto.
	if _, err := os.Stat(filepath.Join(dem.WorktreePath, "NOVO.md")); err != nil {
		t.Fatalf("worktree nao partiu da main atualizada (NOVO.md ausente): %v", err)
	}
}

// TestRunnerPrepararIdempotente: chamar Preparar de novo reaproveita o mesmo
// worktree/branch (ex.: 2a fase da demanda) sem recriar nem falhar.
func TestRunnerPrepararIdempotente(t *testing.T) {
	repo := repoMain(t)
	r, dem, _ := runnerComProjeto(t, repo)
	ctx := context.Background()

	dem, err := r.Preparar(ctx, dem)
	if err != nil {
		t.Fatalf("Preparar 1: %v", err)
	}
	branch1, wt1 := dem.Branch, dem.WorktreePath

	dem2, err := r.Preparar(ctx, dem)
	if err != nil {
		t.Fatalf("Preparar 2 (idempotente): %v", err)
	}
	if dem2.Branch != branch1 || dem2.WorktreePath != wt1 {
		t.Fatalf("Preparar nao foi idempotente: %q/%q → %q/%q", branch1, wt1, dem2.Branch, dem2.WorktreePath)
	}
	// apenas uma branch praxis/ no repo.
	branches := gitCmd(t, repo, "branch", "--list", "praxis/*")
	if n := strings.Count(strings.TrimSpace(branches), "\n"); n != 0 {
		t.Fatalf("esperava exatamente 1 branch praxis/*, veio:\n%s", branches)
	}
}

// TestRunnerRodarFaseSemPreparar: RodarFase sem worktree preparado devolve erro
// de infraestrutura, sem tocar no git.
func TestRunnerRodarFaseSemPreparar(t *testing.T) {
	repo := repoMain(t)
	r, dem, fase := runnerComProjeto(t, repo)
	res, err := r.RodarFase(context.Background(), dem, fase, configTeste(), nil)
	if err == nil {
		t.Fatal("esperava erro por worktree nao preparado")
	}
	if res.Situacao != SituacaoFalhou {
		t.Fatalf("situacao = %q, esperava falhou", res.Situacao)
	}
}

// TestNomeBranchECaminhoWorktree cobre as convencoes de nome de forma isolada.
func TestNomeBranchECaminhoWorktree(t *testing.T) {
	casos := []struct {
		id         int64
		titulo     string
		wantBranch string
		wantTail   string
	}{
		{7, "Ajustar Login!", "praxis/d7-ajustar-login", "d7-ajustar-login"},
		{12, "   ", "praxis/d12", "d12"},
		{3, "Ação: Corrigir --- Bug", "praxis/d3-a-o-corrigir-bug", "d3-a-o-corrigir-bug"},
	}
	for _, c := range casos {
		if got := NomeBranch(c.id, c.titulo); got != c.wantBranch {
			t.Errorf("NomeBranch(%d,%q) = %q, esperava %q", c.id, c.titulo, got, c.wantBranch)
		}
		wt := CaminhoWorktree("/home", "proj", c.id, c.titulo)
		if base := filepath.Base(wt); base != c.wantTail {
			t.Errorf("CaminhoWorktree tail = %q, esperava %q", base, c.wantTail)
		}
	}
	// slug longo e truncado.
	longo := NomeBranch(1, strings.Repeat("abcde ", 20))
	if len(longo) > len(gitops.PrefixoBranch)+len("d1-")+maxSlugDemanda {
		t.Fatalf("slug nao truncado: %q (len %d)", longo, len(longo))
	}
}
