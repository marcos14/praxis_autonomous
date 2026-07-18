package api

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/marcos14/praxis-autonomous/internal/db"
)

// gitTeste roda um comando git no dir, falhando o teste em erro.
func gitTeste(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

// repoComBranchDemanda cria um repo git com um commit inicial na main e uma
// branch praxis/<branch> com um commit à frente. Devolve o caminho do repo e o
// nome da branch. Usado para exercitar o merge-preview.
func repoComBranchDemanda(t *testing.T, branch string) string {
	t.Helper()
	dir := t.TempDir()
	gitTeste(t, dir, "init", "-q", "-b", "main")
	gitTeste(t, dir, "config", "user.email", "t@praxis.local")
	gitTeste(t, dir, "config", "user.name", "Praxis Teste")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("v0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitTeste(t, dir, "add", "-A")
	gitTeste(t, dir, "commit", "-q", "-m", "inicial")
	gitTeste(t, dir, "checkout", "-q", "-b", branch, "main")
	if err := os.WriteFile(filepath.Join(dir, "b.txt"), []byte("feature\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitTeste(t, dir, "add", "-A")
	gitTeste(t, dir, "commit", "-q", "-m", "fase 1: b.txt")
	gitTeste(t, dir, "checkout", "-q", "main")
	return dir
}

// criarProjetoEmRepo cria um projeto apontando para um repo já existente (com
// modo de integração dado) e devolve seu id.
func criarProjetoEmRepo(t *testing.T, srv *Servidor, pasta, modo string) int64 {
	t.Helper()
	rec := fazerReq(t, srv, http.MethodPost, "/api/v1/projects", map[string]any{
		"nome":            "Proj Integração",
		"pasta":           pasta,
		"modo_integracao": modo,
		"url_plataforma":  "https://gitlab.com/acme/proj",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("criar projeto: status %d (corpo=%q)", rec.Code, rec.Body.String())
	}
	return decodProjeto(t, rec).ID
}

func TestMergePreviewLimpoComLinkMR(t *testing.T) {
	banco := abrirBancoTemp(t)
	srv := Novo(Opcoes{Banco: banco})
	branch := "praxis/d1-x"
	repo := repoComBranchDemanda(t, branch)
	proj := criarProjetoEmRepo(t, srv, repo, db.ModoIntegracaoMergeRequest)

	// demanda concluída com a branch preenchida.
	dem, err := banco.CriarDemanda(context.Background(), db.Demanda{
		ProjectID: proj, Titulo: "d1", Status: db.StatusDemandaConcluida, Branch: branch})
	if err != nil {
		t.Fatalf("criar demanda: %v", err)
	}

	rec := fazerReq(t, srv, http.MethodGet, "/api/v1/demands/"+strconv.FormatInt(dem.ID, 10)+"/merge-preview", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, quero 200 (corpo=%q)", rec.Code, rec.Body.String())
	}
	var mp respMergePreview
	if err := json.Unmarshal(rec.Body.Bytes(), &mp); err != nil {
		t.Fatalf("decodificar: %v", err)
	}
	if !mp.Limpo {
		t.Fatalf("preview deveria ser limpo (conflitos=%v aviso=%q)", mp.Conflitos, mp.Aviso)
	}
	if len(mp.Commits) != 1 {
		t.Fatalf("commits = %d, quero 1", len(mp.Commits))
	}
	if mp.URLMR == "" || !strings.Contains(mp.URLMR, "merge_requests/new") {
		t.Fatalf("url_mr = %q, quero link de MR do GitLab", mp.URLMR)
	}
	if !strings.Contains(mp.URLMR, "d1-x") {
		t.Fatalf("url_mr não referencia a branch: %q", mp.URLMR)
	}
}

func TestMergePreviewSemBranch(t *testing.T) {
	banco := abrirBancoTemp(t)
	srv := Novo(Opcoes{Banco: banco})
	proj := criarProjetoTeste(t, srv)
	dem, err := banco.CriarDemanda(context.Background(), db.Demanda{
		ProjectID: proj, Titulo: "sem branch", Status: db.StatusDemandaPronta})
	if err != nil {
		t.Fatalf("criar: %v", err)
	}
	rec := fazerReq(t, srv, http.MethodGet, "/api/v1/demands/"+strconv.FormatInt(dem.ID, 10)+"/merge-preview", nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, quero 409 (corpo=%q)", rec.Code, rec.Body.String())
	}
}

func TestDiffDemandaCompletoEPorFase(t *testing.T) {
	banco := abrirBancoTemp(t)
	srv := Novo(Opcoes{Banco: banco})
	branch := "praxis/d1-diff"
	repo := repoComBranchDemanda(t, branch)
	proj := criarProjetoEmRepo(t, srv, repo, db.ModoIntegracaoMergeLocal)
	dem, err := banco.CriarDemanda(context.Background(), db.Demanda{
		ProjectID: proj, Titulo: "d1", Status: db.StatusDemandaConcluida, Branch: branch})
	if err != nil {
		t.Fatalf("criar demanda: %v", err)
	}
	base := "/api/v1/demands/" + strconv.FormatInt(dem.ID, 10) + "/diff"

	// diff completo: menciona o arquivo alterado.
	rec := fazerReq(t, srv, http.MethodGet, base, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, quero 200 (corpo=%q)", rec.Code, rec.Body.String())
	}
	var d respDiff
	if err := json.Unmarshal(rec.Body.Bytes(), &d); err != nil {
		t.Fatalf("decodificar: %v", err)
	}
	if d.Fase != "" || !strings.Contains(d.Diff, "b.txt") {
		t.Fatalf("diff completo inesperado: fase=%q diff=%q", d.Fase, d.Diff)
	}

	// diff por fase inexistente: 200 com diff vazio (o repo commitou "fase 1:...",
	// que não casa com o prefixo "Fase 99:").
	rec = fazerReq(t, srv, http.MethodGet, base+"?fase=99", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, quero 200 (corpo=%q)", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &d); err != nil {
		t.Fatalf("decodificar: %v", err)
	}
	if d.Fase != "99" || strings.TrimSpace(d.Diff) != "" {
		t.Fatalf("diff de fase inexistente devia ser vazio: fase=%q diff=%q", d.Fase, d.Diff)
	}
}

func TestDiffDemandaSemBranch(t *testing.T) {
	banco := abrirBancoTemp(t)
	srv := Novo(Opcoes{Banco: banco})
	proj := criarProjetoTeste(t, srv)
	dem, err := banco.CriarDemanda(context.Background(), db.Demanda{
		ProjectID: proj, Titulo: "sem branch", Status: db.StatusDemandaPronta})
	if err != nil {
		t.Fatalf("criar: %v", err)
	}
	rec := fazerReq(t, srv, http.MethodGet, "/api/v1/demands/"+strconv.FormatInt(dem.ID, 10)+"/diff", nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, quero 409 (corpo=%q)", rec.Code, rec.Body.String())
	}
}

func TestMontarURLMR(t *testing.T) {
	gl := montarURLMR(db.Projeto{URLPlataforma: "https://gitlab.com/acme/p", BranchPrincipal: "main"}, "praxis/d2-y", "main")
	if !strings.Contains(gl, "/-/merge_requests/new") {
		t.Fatalf("gitlab url = %q", gl)
	}
	gh := montarURLMR(db.Projeto{URLPlataforma: "https://github.com/acme/p", BranchPrincipal: "main"}, "praxis/d2-y", "main")
	if !strings.Contains(gh, "/compare/") || !strings.Contains(gh, "expand=1") {
		t.Fatalf("github url = %q", gh)
	}
	vazio := montarURLMR(db.Projeto{URLPlataforma: ""}, "praxis/d2-y", "main")
	if vazio != "" {
		t.Fatalf("sem url_plataforma deveria ser vazio, got %q", vazio)
	}
}

// repoMergeLocal cria um repo com main e uma branch praxis/<branch> com um
// commit em b.txt (não conflitante). Devolve o caminho do repo.
func repoMergeLocalLimpo(t *testing.T, branch string) string {
	return repoComBranchDemanda(t, branch)
}

// repoConflito cria um repo onde a branch e a main tocam o MESMO arquivo em
// conteúdos divergentes (conflito garantido no merge).
func repoConflito(t *testing.T, branch string) string {
	t.Helper()
	dir := t.TempDir()
	gitTeste(t, dir, "init", "-q", "-b", "main")
	gitTeste(t, dir, "config", "user.email", "t@praxis.local")
	gitTeste(t, dir, "config", "user.name", "Praxis Teste")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitTeste(t, dir, "add", "-A")
	gitTeste(t, dir, "commit", "-q", "-m", "inicial")
	gitTeste(t, dir, "checkout", "-q", "-b", branch, "main")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("versao da branch\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitTeste(t, dir, "add", "-A")
	gitTeste(t, dir, "commit", "-q", "-m", "branch muda a.txt")
	gitTeste(t, dir, "checkout", "-q", "main")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("versao da main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitTeste(t, dir, "add", "-A")
	gitTeste(t, dir, "commit", "-q", "-m", "main muda a.txt")
	return dir
}

func TestIntegrarMergeLocalLimpo(t *testing.T) {
	banco := abrirBancoTemp(t)
	srv := Novo(Opcoes{Banco: banco})
	branch := "praxis/d10-ml"
	repo := repoMergeLocalLimpo(t, branch)
	proj := criarProjetoEmRepo(t, srv, repo, db.ModoIntegracaoMergeLocal)
	dem, err := banco.CriarDemanda(context.Background(), db.Demanda{
		ProjectID: proj, Titulo: "d10", Status: db.StatusDemandaConcluida, Branch: branch})
	if err != nil {
		t.Fatalf("criar: %v", err)
	}

	rec := fazerReq(t, srv, http.MethodPost, "/api/v1/demands/"+strconv.FormatInt(dem.ID, 10)+"/actions",
		map[string]any{"acao": "integrar"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, quero 200 (corpo=%q)", rec.Code, rec.Body.String())
	}
	d := decodDemanda(t, rec)
	if d.Status != db.StatusDemandaIntegrada {
		t.Fatalf("status = %q, quero integrada", d.Status)
	}
	// a main agora contém o commit de merge.
	log := func() string {
		cmd := exec.Command("git", "-C", repo, "log", "--oneline", "main")
		out, _ := cmd.CombinedOutput()
		return string(out)
	}()
	if !strings.Contains(log, "Merge da demanda") {
		t.Fatalf("main sem commit de merge:\n%s", log)
	}
}

func TestIntegrarConflitoMarcaStatus(t *testing.T) {
	banco := abrirBancoTemp(t)
	srv := Novo(Opcoes{Banco: banco})
	branch := "praxis/d11-cf"
	repo := repoConflito(t, branch)
	proj := criarProjetoEmRepo(t, srv, repo, db.ModoIntegracaoMergeLocal)
	dem, err := banco.CriarDemanda(context.Background(), db.Demanda{
		ProjectID: proj, Titulo: "d11", Status: db.StatusDemandaConcluida, Branch: branch})
	if err != nil {
		t.Fatalf("criar: %v", err)
	}

	rec := fazerReq(t, srv, http.MethodPost, "/api/v1/demands/"+strconv.FormatInt(dem.ID, 10)+"/actions",
		map[string]any{"acao": "integrar"})
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, quero 409 (corpo=%q)", rec.Code, rec.Body.String())
	}
	// a demanda deve estar em conflito.
	atual, err := banco.ObterDemanda(context.Background(), dem.ID)
	if err != nil {
		t.Fatalf("obter: %v", err)
	}
	if atual.Status != db.StatusDemandaConflito {
		t.Fatalf("status = %q, quero conflito", atual.Status)
	}
	if !strings.Contains(atual.Erro, "a.txt") {
		t.Fatalf("erro = %q, quero citar a.txt", atual.Erro)
	}
}

func TestIntegrarModoMergeRequestRecusa(t *testing.T) {
	banco := abrirBancoTemp(t)
	srv := Novo(Opcoes{Banco: banco})
	branch := "praxis/d12-mr"
	repo := repoComBranchDemanda(t, branch)
	proj := criarProjetoEmRepo(t, srv, repo, db.ModoIntegracaoMergeRequest)
	dem, err := banco.CriarDemanda(context.Background(), db.Demanda{
		ProjectID: proj, Titulo: "d12", Status: db.StatusDemandaConcluida, Branch: branch})
	if err != nil {
		t.Fatalf("criar: %v", err)
	}
	rec := fazerReq(t, srv, http.MethodPost, "/api/v1/demands/"+strconv.FormatInt(dem.ID, 10)+"/actions",
		map[string]any{"acao": "integrar"})
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, quero 409 modo_invalido (corpo=%q)", rec.Code, rec.Body.String())
	}
}

func TestAtualizarBranchLimpo(t *testing.T) {
	banco := abrirBancoTemp(t)
	srv := Novo(Opcoes{Banco: banco})
	branch := "praxis/d13-up"
	// repo com main; cria a branch, adiciona um worktree, depois a main avança.
	dir := t.TempDir()
	gitTeste(t, dir, "init", "-q", "-b", "main")
	gitTeste(t, dir, "config", "user.email", "t@praxis.local")
	gitTeste(t, dir, "config", "user.name", "Praxis Teste")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("v0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitTeste(t, dir, "add", "-A")
	gitTeste(t, dir, "commit", "-q", "-m", "inicial")
	wt := filepath.Join(t.TempDir(), "wt13")
	gitTeste(t, dir, "worktree", "add", "-q", "-b", branch, wt, "main")
	if err := os.WriteFile(filepath.Join(wt, "b.txt"), []byte("feature\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitTeste(t, wt, "add", "-A")
	gitTeste(t, wt, "commit", "-q", "-m", "fase 1")
	// main avança (arquivo distinto → merge limpo).
	if err := os.WriteFile(filepath.Join(dir, "c.txt"), []byte("na main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitTeste(t, dir, "add", "-A")
	gitTeste(t, dir, "commit", "-q", "-m", "main avanca")

	proj := criarProjetoEmRepo(t, srv, dir, db.ModoIntegracaoMergeRequest)
	dem, err := banco.CriarDemanda(context.Background(), db.Demanda{
		ProjectID: proj, Titulo: "d13", Status: db.StatusDemandaConcluida, Branch: branch, WorktreePath: wt})
	if err != nil {
		t.Fatalf("criar: %v", err)
	}
	rec := fazerReq(t, srv, http.MethodPost, "/api/v1/demands/"+strconv.FormatInt(dem.ID, 10)+"/actions",
		map[string]any{"acao": "atualizar_branch"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, quero 200 (corpo=%q)", rec.Code, rec.Body.String())
	}
	// o arquivo da main veio para o worktree da branch.
	if _, err := os.Stat(filepath.Join(wt, "c.txt")); err != nil {
		t.Fatalf("main não foi trazida para a branch: %v", err)
	}
}

func TestIntegrarLimpaWorktreeEBranch(t *testing.T) {
	banco := abrirBancoTemp(t)
	srv := Novo(Opcoes{Banco: banco})
	branch := "praxis/d21-clean"
	// repo com main; branch num worktree, com um commit não conflitante.
	dir := t.TempDir()
	gitTeste(t, dir, "init", "-q", "-b", "main")
	gitTeste(t, dir, "config", "user.email", "t@praxis.local")
	gitTeste(t, dir, "config", "user.name", "Praxis Teste")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("v0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitTeste(t, dir, "add", "-A")
	gitTeste(t, dir, "commit", "-q", "-m", "inicial")
	wt := filepath.Join(t.TempDir(), "wt21")
	gitTeste(t, dir, "worktree", "add", "-q", "-b", branch, wt, "main")
	if err := os.WriteFile(filepath.Join(wt, "b.txt"), []byte("feature\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitTeste(t, wt, "add", "-A")
	gitTeste(t, wt, "commit", "-q", "-m", "fase 1")

	proj := criarProjetoEmRepo(t, srv, dir, db.ModoIntegracaoMergeLocal)
	dem, err := banco.CriarDemanda(context.Background(), db.Demanda{
		ProjectID: proj, Titulo: "d21", Status: db.StatusDemandaConcluida, Branch: branch, WorktreePath: wt})
	if err != nil {
		t.Fatalf("criar: %v", err)
	}
	rec := fazerReq(t, srv, http.MethodPost, "/api/v1/demands/"+strconv.FormatInt(dem.ID, 10)+"/actions",
		map[string]any{"acao": "integrar"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, quero 200 (corpo=%q)", rec.Code, rec.Body.String())
	}
	// worktree removido do disco.
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Fatalf("worktree deveria ter sido removido (stat err=%v)", err)
	}
	// branch removida do repo.
	out, _ := exec.Command("git", "-C", dir, "branch", "--list", branch).CombinedOutput()
	if strings.TrimSpace(string(out)) != "" {
		t.Fatalf("branch deveria ter sido removida, got %q", out)
	}
	// worktree_path zerado no registro.
	atual, _ := banco.ObterDemanda(context.Background(), dem.ID)
	if atual.WorktreePath != "" {
		t.Fatalf("worktree_path deveria estar vazio, got %q", atual.WorktreePath)
	}
}
