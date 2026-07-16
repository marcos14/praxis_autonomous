package pipeline

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/marcos14/praxis-autonomous/internal/db"
	"github.com/marcos14/praxis-autonomous/internal/gitops"
	"github.com/marcos14/praxis-autonomous/internal/motor"
)

// repoComOrigin monta um repo main com um bare origin configurado e a main ja
// publicada, devolvendo (repo, origin).
func repoComOrigin(t *testing.T) (repo, origin string) {
	t.Helper()
	origin = bareOrigin(t)
	repo = repoMain(t)
	gitCmd(t, repo, "remote", "add", "origin", origin)
	gitCmd(t, repo, "push", "-q", "-u", "origin", "main")
	return repo, origin
}

// runnerPush monta um Runner (motor stub feliz) ligado a um projeto no modo dado,
// apontando para repo, com uma demanda pronta e uma fase pendente.
func runnerPush(t *testing.T, repo, modo string) (*Runner, db.Demanda, db.Fase) {
	t.Helper()
	d, err := db.Abrir(filepath.Join(t.TempDir(), "praxis.db"))
	if err != nil {
		t.Fatalf("Abrir db: %v", err)
	}
	t.Cleanup(func() { d.Fechar() })

	ctx := context.Background()
	proj, err := d.CriarProjeto(ctx, db.Projeto{
		Nome: "Proj Push", Slug: "proj-push", Pasta: repo,
		BranchPrincipal: "main", ModoIntegracao: modo,
	})
	if err != nil {
		t.Fatalf("CriarProjeto: %v", err)
	}
	dem, err := d.CriarDemanda(ctx, db.Demanda{
		ProjectID: proj.ID, Titulo: "Publicar Branch", PlanoMD: "# plano",
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

// motorPorFase escreve um arquivo UNICO por fase (derivado do RotuloLog, que
// carrega o codigo da fase) e aprova no revisor — garante que cada fase produza
// um commit proprio (o motorHappy padrao escreveria sempre entrega.txt, e a 2a
// fase nao mudaria nada).
func motorPorFase() stubMotor {
	return stubMotor{nome: "claude", fn: func(op motor.OpcoesRun) (*motor.ResultadoRun, error) {
		if strings.Contains(op.RotuloLog, "revisor") {
			return &motor.ResultadoRun{Resultado: `{"veredito":"APROVADO","problemas":[]}`, LogPath: "rev"}, nil
		}
		nome := "entrega-" + strings.ReplaceAll(op.RotuloLog, "/", "_") + ".txt"
		if err := os.WriteFile(filepath.Join(op.Dir, nome), []byte(op.RotuloLog+"\n"), 0o644); err != nil {
			return nil, err
		}
		return &motor.ResultadoRun{Resultado: "ok", CustoUSD: 0.05, LogPath: "e"}, nil
	}}
}

// branchNoOrigin informa se a branch existe no repo origin.
func branchNoOrigin(origin, branch string) bool {
	return exec.Command("git", "-C", origin, "rev-parse", "--verify", "--quiet", branch).Run() == nil
}

// temEventoTipo informa se algum evento da demanda tem o tipo dado; devolve
// tambem o primeiro titulo encontrado.
func temEventoTipo(t *testing.T, r *Runner, demID int64, tipo string) (bool, string) {
	t.Helper()
	evs, err := r.Store.ListarEventos(context.Background(), db.FiltroEventos{DemandID: &demID})
	if err != nil {
		t.Fatalf("ListarEventos: %v", err)
	}
	for _, e := range evs {
		if e.Tipo == tipo {
			return true, e.Titulo
		}
	}
	return false, ""
}

// TestRodarFasePushPublicaBranch e o caminho feliz da Fase 2f: no modo
// merge_request, o commit de fase e seguido de push automatico que publica a
// branch no origin.
func TestRodarFasePushPublicaBranch(t *testing.T) {
	repo, origin := repoComOrigin(t)
	r, dem, fase := runnerPush(t, repo, db.ModoIntegracaoMergeRequest)
	ctx := context.Background()

	dem, err := r.Preparar(ctx, dem)
	if err != nil {
		t.Fatalf("Preparar: %v", err)
	}
	res, err := r.RodarFase(ctx, dem, fase, configTeste(), nil)
	if err != nil {
		t.Fatalf("RodarFase: %v", err)
	}
	if res.Situacao != SituacaoConcluida || !res.CommitFeito {
		t.Fatalf("fase deveria concluir com commit: situacao=%q commit=%v erro=%s", res.Situacao, res.CommitFeito, res.Erro)
	}
	if !res.Publicado {
		t.Fatal("esperava Publicado=true no modo merge_request com remote")
	}
	if res.CommitsNaoPublicados != 0 {
		t.Fatalf("apos push ok, CommitsNaoPublicados = %d, esperava 0", res.CommitsNaoPublicados)
	}
	if !branchNoOrigin(origin, dem.Branch) {
		t.Fatalf("branch %q nao foi publicada no origin", dem.Branch)
	}
	if ok, _ := temEventoTipo(t, r, dem.ID, "branch_publicada"); !ok {
		t.Fatal("esperava evento branch_publicada")
	}
}

// TestRodarFasePushFalhaNaoBloqueiaERetentaNoProximoCommit e o criterio central
// da Fase 2f: remote indisponivel → a fase conclui, registra o alerta "commits
// nao publicados (N)" e o push e retentado (com sucesso) no proximo commit.
func TestRodarFasePushFalhaNaoBloqueiaERetentaNoProximoCommit(t *testing.T) {
	repo, origin := repoComOrigin(t)
	r, dem, fase1 := runnerPush(t, repo, db.ModoIntegracaoMergeRequest)
	r.Selecionar = seletorStub(motorPorFase())
	ctx := context.Background()

	// acelera as esperas do retry interno do gitops.Push.
	origEspera := gitops.EsperaEntreTentativas
	gitops.EsperaEntreTentativas = time.Millisecond
	t.Cleanup(func() { gitops.EsperaEntreTentativas = origEspera })

	dem, err := r.Preparar(ctx, dem)
	if err != nil {
		t.Fatalf("Preparar: %v", err)
	}

	// quebra o remote: origin passa a apontar para um caminho inexistente.
	semRemote := filepath.Join(t.TempDir(), "nao-existe.git")
	gitCmd(t, repo, "remote", "set-url", "origin", semRemote)

	// fase 1: commita mas o push falha — a fase conclui mesmo assim.
	res1, err := r.RodarFase(ctx, dem, fase1, configTeste(), nil)
	if err != nil {
		t.Fatalf("RodarFase 1: %v", err)
	}
	if res1.Situacao != SituacaoConcluida || !res1.CommitFeito {
		t.Fatalf("fase 1 deveria concluir com commit apesar da falha de push: %+v", res1)
	}
	if res1.Publicado {
		t.Fatal("push deveria ter falhado (Publicado=false)")
	}
	if res1.CommitsNaoPublicados != 1 {
		t.Fatalf("fase 1: CommitsNaoPublicados = %d, esperava 1", res1.CommitsNaoPublicados)
	}
	ok, titulo := temEventoTipo(t, r, dem.ID, "push_falhou")
	if !ok {
		t.Fatal("esperava evento push_falhou (alerta de commits nao publicados)")
	}
	if !strings.Contains(titulo, "commits nao publicados (1)") {
		t.Fatalf("titulo do alerta = %q, esperava conter 'commits nao publicados (1)'", titulo)
	}
	if branchNoOrigin(origin, dem.Branch) {
		t.Fatal("branch nao deveria estar no origin apos falha de push")
	}

	// restaura o remote e roda a fase 2: o push do proximo commit reenvia TODOS os
	// commits acumulados (retry no proximo commit).
	gitCmd(t, repo, "remote", "set-url", "origin", origin)
	fase2, err := r.Store.CriarFase(ctx, db.Fase{DemandID: dem.ID, Codigo: "2", Titulo: "Fase dois", Status: db.StatusFasePendente})
	if err != nil {
		t.Fatalf("CriarFase 2: %v", err)
	}
	res2, err := r.RodarFase(ctx, dem, fase2, configTeste(), nil)
	if err != nil {
		t.Fatalf("RodarFase 2: %v", err)
	}
	if !res2.Publicado {
		t.Fatalf("fase 2 deveria publicar apos remote restaurado: %+v", res2)
	}
	if res2.CommitsNaoPublicados != 0 {
		t.Fatalf("fase 2: CommitsNaoPublicados = %d, esperava 0", res2.CommitsNaoPublicados)
	}
	if !branchNoOrigin(origin, dem.Branch) {
		t.Fatal("branch deveria estar publicada no origin apos o retry")
	}
	// o origin recebeu os dois commits de fase (retry empurrou o atrasado junto).
	out := gitCmd(t, origin, "log", "--oneline", dem.Branch)
	if !strings.Contains(out, "Fase 1:") || !strings.Contains(out, "Fase 2:") {
		t.Fatalf("origin deveria conter os commits das fases 1 e 2:\n%s", out)
	}
}

// TestMergeLocalNaoPublicaAutomaticamenteMasPublicaBranchManual: no modo
// merge_local nao ha push automatico, mas a acao manual publicar_branch publica
// mesmo assim (retry manual, agnostico ao modo).
func TestMergeLocalNaoPublicaAutomaticamenteMasPublicaBranchManual(t *testing.T) {
	repo, origin := repoComOrigin(t)
	r, dem, fase := runnerPush(t, repo, db.ModoIntegracaoMergeLocal)
	ctx := context.Background()

	dem, err := r.Preparar(ctx, dem)
	if err != nil {
		t.Fatalf("Preparar: %v", err)
	}
	res, err := r.RodarFase(ctx, dem, fase, configTeste(), nil)
	if err != nil {
		t.Fatalf("RodarFase: %v", err)
	}
	if !res.CommitFeito {
		t.Fatal("esperava commit da fase")
	}
	if res.Publicado {
		t.Fatal("merge_local NAO deve publicar automaticamente")
	}
	if branchNoOrigin(origin, dem.Branch) {
		t.Fatal("merge_local nao deveria ter publicado a branch")
	}
	if ok, _ := temEventoTipo(t, r, dem.ID, "branch_publicada"); ok {
		t.Fatal("merge_local nao deveria registrar branch_publicada")
	}

	// acao manual publica a branch mesmo no modo merge_local.
	rp, err := r.PublicarBranch(ctx, dem)
	if err != nil {
		t.Fatalf("PublicarBranch: %v", err)
	}
	if !rp.Publicado || rp.CommitsNaoPublicados != 0 {
		t.Fatalf("PublicarBranch: %+v, esperava Publicado=true e 0 pendentes", rp)
	}
	if !branchNoOrigin(origin, dem.Branch) {
		t.Fatal("PublicarBranch nao publicou a branch no origin")
	}
}

// TestPushPuladoSemRemote: projeto merge_request SEM remote — o push e pulado
// (nao e falha), a fase conclui e nenhum alerta e emitido.
func TestPushPuladoSemRemote(t *testing.T) {
	repo := repoMain(t) // sem origin
	r, dem, fase := runnerPush(t, repo, db.ModoIntegracaoMergeRequest)
	ctx := context.Background()

	dem, err := r.Preparar(ctx, dem)
	if err != nil {
		t.Fatalf("Preparar: %v", err)
	}
	res, err := r.RodarFase(ctx, dem, fase, configTeste(), nil)
	if err != nil {
		t.Fatalf("RodarFase: %v", err)
	}
	if !res.CommitFeito || res.Publicado {
		t.Fatalf("sem remote: esperava commit sem publicacao (%+v)", res)
	}
	if ok, _ := temEventoTipo(t, r, dem.ID, "push_falhou"); ok {
		t.Fatal("sem remote nao deveria emitir alerta de falha de push")
	}

	// a acao manual tambem e no-op tolerante (Pulado).
	rp, err := r.PublicarBranch(ctx, dem)
	if err != nil {
		t.Fatalf("PublicarBranch: %v", err)
	}
	if !rp.Pulado || rp.Publicado {
		t.Fatalf("PublicarBranch sem remote deveria ser Pulado: %+v", rp)
	}
}

// TestPublicarBranchSemPreparar: a acao manual sem branch/worktree preparados e
// erro de pre-condicao (nao um push tolerante).
func TestPublicarBranchSemPreparar(t *testing.T) {
	repo, _ := repoComOrigin(t)
	r, dem, _ := runnerPush(t, repo, db.ModoIntegracaoMergeRequest)
	if _, err := r.PublicarBranch(context.Background(), dem); err == nil {
		t.Fatal("esperava erro ao publicar demanda sem branch/worktree preparados")
	}
}
