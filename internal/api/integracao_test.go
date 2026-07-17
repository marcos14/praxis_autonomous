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
