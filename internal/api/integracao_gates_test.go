package api

// Gates de estado do fechamento (regressão do incidente "integrada sozinha"):
// a reconciliação de MR não pode rodar antes de a demanda concluir — uma branch
// recém-criada, ainda sem commits, está trivialmente contida na main e seria
// detectada como "já mesclada", removendo o worktree debaixo do executor. As
// ações manuais integrar/atualizar_branch também recusam estados em que o
// scheduler pode escrever no worktree.

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/marcos14/praxis-autonomous/internal/db"
)

// repoComBranchVazia cria um repo com um commit na main e uma branch da demanda
// SEM commits à frente (tip idêntico ao da main) — o estado de uma execução que
// acabou de começar.
func repoComBranchVazia(t *testing.T, branch string) string {
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
	gitTeste(t, dir, "branch", branch)
	return dir
}

func TestMergePreviewNaoReconciliaDemandaExecutando(t *testing.T) {
	banco := abrirBancoTemp(t)
	srv := Novo(Opcoes{Banco: banco})
	branch := "praxis/d1-x"
	repo := repoComBranchVazia(t, branch)
	proj := criarProjetoEmRepo(t, srv, repo, db.ModoIntegracaoMergeRequest)

	wt := t.TempDir() // worktree "vivo" da execução: não pode ser removido
	dem, err := banco.CriarDemanda(context.Background(), db.Demanda{
		ProjectID: proj, Titulo: "d1", Status: db.StatusDemandaExecutando,
		Branch: branch, WorktreePath: wt})
	if err != nil {
		t.Fatalf("criar demanda: %v", err)
	}

	rec := fazerReq(t, srv, http.MethodGet, "/api/v1/demands/"+strconv.FormatInt(dem.ID, 10)+"/merge-preview", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("merge-preview: status %d (corpo=%q)", rec.Code, rec.Body.String())
	}

	atual, err := banco.ObterDemanda(context.Background(), dem.ID)
	if err != nil {
		t.Fatalf("obter demanda: %v", err)
	}
	if atual.Status != db.StatusDemandaExecutando {
		t.Fatalf("status = %q — a reconciliação integrou uma demanda EXECUTANDO (branch vazia ≠ MR mesclado)", atual.Status)
	}
	if atual.WorktreePath != wt {
		t.Fatalf("worktree_path = %q, quero %q preservado durante a execução", atual.WorktreePath, wt)
	}
}

func TestMergePreviewReconciliaDemandaConcluida(t *testing.T) {
	banco := abrirBancoTemp(t)
	srv := Novo(Opcoes{Banco: banco})
	branch := "praxis/d1-x"
	// Branch contida na main + demanda CONCLUÍDA = MR mesclado → reconcilia.
	repo := repoComBranchVazia(t, branch)
	proj := criarProjetoEmRepo(t, srv, repo, db.ModoIntegracaoMergeRequest)

	dem, err := banco.CriarDemanda(context.Background(), db.Demanda{
		ProjectID: proj, Titulo: "d1", Status: db.StatusDemandaConcluida, Branch: branch})
	if err != nil {
		t.Fatalf("criar demanda: %v", err)
	}

	rec := fazerReq(t, srv, http.MethodGet, "/api/v1/demands/"+strconv.FormatInt(dem.ID, 10)+"/merge-preview", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("merge-preview: status %d (corpo=%q)", rec.Code, rec.Body.String())
	}
	atual, err := banco.ObterDemanda(context.Background(), dem.ID)
	if err != nil {
		t.Fatalf("obter demanda: %v", err)
	}
	if atual.Status != db.StatusDemandaIntegrada {
		t.Fatalf("status = %q, quero integrada (reconciliação após conclusão)", atual.Status)
	}
}

func TestIntegrarRecusaDemandaExecutando(t *testing.T) {
	banco := abrirBancoTemp(t)
	srv := Novo(Opcoes{Banco: banco})
	branch := "praxis/d1-x"
	repo := repoComBranchDemanda(t, branch) // com commit à frente
	proj := criarProjetoEmRepo(t, srv, repo, db.ModoIntegracaoMergeLocal)

	dem, err := banco.CriarDemanda(context.Background(), db.Demanda{
		ProjectID: proj, Titulo: "d1", Status: db.StatusDemandaExecutando, Branch: branch})
	if err != nil {
		t.Fatalf("criar demanda: %v", err)
	}

	rec := fazerReq(t, srv, http.MethodPost, "/api/v1/demands/"+strconv.FormatInt(dem.ID, 10)+"/actions",
		map[string]any{"acao": "integrar"})
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "estado_invalido") {
		t.Fatalf("integrar executando: status=%d corpo=%q, quero 409 estado_invalido", rec.Code, rec.Body.String())
	}
	atual, _ := banco.ObterDemanda(context.Background(), dem.ID)
	if atual.Status != db.StatusDemandaExecutando {
		t.Fatalf("status = %q, quero executando intacto", atual.Status)
	}
}

func TestAtualizarBranchRecusaDemandaExecutando(t *testing.T) {
	banco := abrirBancoTemp(t)
	srv := Novo(Opcoes{Banco: banco})
	branch := "praxis/d1-x"
	repo := repoComBranchDemanda(t, branch)
	proj := criarProjetoEmRepo(t, srv, repo, db.ModoIntegracaoMergeRequest)

	dem, err := banco.CriarDemanda(context.Background(), db.Demanda{
		ProjectID: proj, Titulo: "d1", Status: db.StatusDemandaExecutando,
		Branch: branch, WorktreePath: t.TempDir()})
	if err != nil {
		t.Fatalf("criar demanda: %v", err)
	}

	rec := fazerReq(t, srv, http.MethodPost, "/api/v1/demands/"+strconv.FormatInt(dem.ID, 10)+"/actions",
		map[string]any{"acao": "atualizar_branch"})
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "estado_invalido") {
		t.Fatalf("atualizar_branch executando: status=%d corpo=%q, quero 409 estado_invalido", rec.Code, rec.Body.String())
	}
}
