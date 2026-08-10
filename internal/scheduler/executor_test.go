package scheduler

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/marcos14/praxis-autonomous/internal/db"
	"github.com/marcos14/praxis-autonomous/internal/gitops"
	"github.com/marcos14/praxis-autonomous/internal/motor"
	"github.com/marcos14/praxis-autonomous/internal/pipeline"
)

// --- proximaFase (fila respeitando depende_de e requer_humano) ----------------

func fz(codigo, status string, requerHumano bool, deps ...string) db.Fase {
	return db.Fase{Codigo: codigo, Status: status, RequerHumano: requerHumano, DependeDe: deps}
}

func TestProximaFase(t *testing.T) {
	casos := []struct {
		nome    string
		fases   []db.Fase
		wantSit situacaoFila
		wantCod string // código esperado quando filaProntaParaRodar
	}{
		{"vazia", nil, filaVazia, ""},
		{"tudo concluido",
			[]db.Fase{fz("1", db.StatusFaseConcluida, false)}, filaConcluida, ""},
		{"uma falhou bloqueia",
			[]db.Fase{fz("1", db.StatusFaseConcluida, false), fz("2", db.StatusFaseFalhou, false)}, filaFalhou, ""},
		{"primeira pendente sem deps",
			[]db.Fase{fz("1", db.StatusFasePendente, false), fz("2", db.StatusFasePendente, false, "1")}, filaProntaParaRodar, "1"},
		{"pula requer_humano e roda a proxima elegivel",
			[]db.Fase{fz("1", db.StatusFasePendente, true), fz("2", db.StatusFasePendente, false)}, filaProntaParaRodar, "2"},
		{"dependente espera a dependencia concluir",
			[]db.Fase{fz("1", db.StatusFaseConcluida, false), fz("2", db.StatusFasePendente, false, "1")}, filaProntaParaRodar, "2"},
		{"so restam requer_humano → bloqueada",
			[]db.Fase{fz("1", db.StatusFaseConcluida, false), fz("2", db.StatusFasePendente, true, "1")}, filaBloqueada, ""},
		{"dependente preso porque a dep tambem e humana → bloqueada",
			[]db.Fase{fz("1", db.StatusFasePendente, true), fz("2", db.StatusFasePendente, false, "1")}, filaBloqueada, ""},
		{"pausada (franquia) e retomavel",
			[]db.Fase{fz("1", db.StatusFasePausada, false)}, filaProntaParaRodar, "1"},
		{"executando orfa (queda do servico) e retomavel",
			[]db.Fase{fz("1", db.StatusFaseConcluida, false), fz("2", db.StatusFaseExecutando, false, "1")}, filaProntaParaRodar, "2"},
		// regressão: fase presa em executando + dependente + requer_humano no fim
		// NÃO pode virar filaBloqueada ("aguardando humano") — a órfã é retomada.
		{"executando orfa antes de requer_humano nao bloqueia",
			[]db.Fase{fz("6", db.StatusFaseExecutando, false), fz("7", db.StatusFasePendente, false, "6"), fz("8", db.StatusFasePendente, true, "7")}, filaProntaParaRodar, "6"},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			f, sit := proximaFase(c.fases)
			if sit != c.wantSit {
				t.Fatalf("situacao = %d, quero %d", sit, c.wantSit)
			}
			if sit == filaProntaParaRodar && f.Codigo != c.wantCod {
				t.Fatalf("fase escolhida = %q, quero %q", f.Codigo, c.wantCod)
			}
		})
	}
}

// --- integração: 2 demandas paralelas → 2 branches (critério da Fase 2g) ------

// gitEx roda um comando git em dir, falhando o teste em erro.
func gitEx(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %s em %s: %v — %s", strings.Join(args, " "), dir, err, out)
	}
	return string(out)
}

// repoComOrigin cria um repo git (branch main) com um remote bare `origin` já
// com a main publicada. Devolve (repo, origin).
func repoComOrigin(t *testing.T) (string, string) {
	t.Helper()
	origin := t.TempDir()
	gitEx(t, origin, "init", "-q", "--bare", "-b", "main")
	repo := t.TempDir()
	gitEx(t, repo, "init", "-q", "-b", "main")
	gitEx(t, repo, "config", "user.email", "praxis@test.local")
	gitEx(t, repo, "config", "user.name", "Praxis Teste")
	gitEx(t, repo, "config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("inicial\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitEx(t, repo, "add", "-A")
	gitEx(t, repo, "commit", "-q", "-m", "inicial")
	gitEx(t, repo, "remote", "add", "origin", origin)
	gitEx(t, repo, "push", "-q", "-u", "origin", "main")
	return repo, origin
}

// motorStub é um motor.Motor de teste: escreve um arquivo no worktree para o
// executor/corretor e aprova no revisor. Não chama nenhum CLI.
type motorStub struct{ nome string }

func (m motorStub) Nome() string                   { return m.nome }
func (m motorStub) Capacidades() motor.Capacidades { return motor.Capacidades{} }
func (m motorStub) Rodar(op motor.OpcoesRun) (*motor.ResultadoRun, error) {
	if strings.Contains(op.RotuloLog, "revisor") {
		return &motor.ResultadoRun{Resultado: `{"veredito":"APROVADO","problemas":[]}`, LogPath: "rev"}, nil
	}
	// conteúdo único por rodada (fase-<codigo>-<etapa>) → cada fase produz uma
	// mudança real na árvore, logo um commit próprio.
	if err := os.WriteFile(filepath.Join(op.Dir, "entrega.txt"), []byte(op.RotuloLog+"\n"), 0o644); err != nil {
		return nil, err
	}
	return &motor.ResultadoRun{Resultado: "implementei a fase", CustoUSD: 0.05, LogPath: "exec"}, nil
}

func TestDuasDemandasParalelasGeramBranchesIndependentes(t *testing.T) {
	repo, origin := repoComOrigin(t)
	d := abrirTempDB(t)
	ctx := context.Background()

	proj, err := d.CriarProjeto(ctx, db.Projeto{
		Nome: "Paralela", Slug: "paralela", Pasta: repo,
		BranchPrincipal: "main", ModoIntegracao: db.ModoIntegracaoMergeRequest, Ativo: true,
	})
	if err != nil {
		t.Fatalf("criar projeto: %v", err)
	}

	criarDem := func(titulo string) db.Demanda {
		dem, _, err := d.CriarDemandaComFases(ctx,
			db.Demanda{ProjectID: proj.ID, Titulo: titulo, Status: db.StatusDemandaPronta, PlanoMD: "# plano"},
			[]db.Fase{{Codigo: "1", Titulo: "Única fase", Status: db.StatusFasePendente}},
		)
		if err != nil {
			t.Fatalf("criar demanda %q: %v", titulo, err)
		}
		return dem
	}
	dem1 := criarDem("Demanda Um")
	dem2 := criarDem("Demanda Dois")

	runner := &pipeline.Runner{
		Store:      d,
		Git:        gitops.Novo(),
		Home:       t.TempDir(),
		Prompt:     func(string) (string, error) { return "prompt {FASE} {TITULO}", nil },
		Selecionar: func(string) (motor.Motor, error) { return motorStub{nome: "claude"}, nil },
	}
	exec := &ExecutorDemanda{Store: d, Runner: runner}

	s := Novo(Opcoes{
		Fonte:         NovaFonteBanco(d),
		Executor:      exec,
		MaxGlobal:     2,
		MaxPorProjeto: 0, // sem limite por projeto: as duas rodam em paralelo
		Store:         d,
		Intervalo:     2 * time.Millisecond,
	})

	concluida := func(id int64) bool {
		dem, err := d.ObterDemanda(ctx, id)
		return err == nil && dem.Status == db.StatusDemandaConcluida
	}
	rodarPor(t, s, func() bool { return concluida(dem1.ID) && concluida(dem2.ID) }, 20*time.Second)

	if !concluida(dem1.ID) || !concluida(dem2.ID) {
		t.Fatalf("demandas não concluíram: d1=%v d2=%v", concluida(dem1.ID), concluida(dem2.ID))
	}

	// cada demanda tem branch dedicada com exatamente 1 commit novo, e a main
	// permaneceu intocada.
	for _, dem := range []db.Demanda{dem1, dem2} {
		atual, err := d.ObterDemanda(ctx, dem.ID)
		if err != nil {
			t.Fatalf("obter demanda %d: %v", dem.ID, err)
		}
		branch := "praxis/d" + strconv.FormatInt(dem.ID, 10) + "-" + slugEsperado(dem.Titulo)
		if atual.Branch != branch {
			t.Fatalf("branch da demanda %d = %q, quero %q", dem.ID, atual.Branch, branch)
		}
		// 1 commit novo na branch (além do inicial da main).
		log := gitEx(t, repo, "log", "--oneline", branch)
		if n := strings.Count(strings.TrimSpace(log), "\n"); n != 1 {
			t.Fatalf("branch %s: esperava 2 commits (1 novo), log:\n%s", branch, log)
		}
		if !strings.Contains(log, "Fase 1: Única fase [praxis]") {
			t.Fatalf("branch %s sem o commit da fase:\n%s", branch, log)
		}
		// push automático publicou a branch no origin.
		if pushed := gitEx(t, origin, "branch", "--list", branch); !strings.Contains(pushed, branch) {
			t.Fatalf("branch %s não foi publicada no origin:\n%s", branch, pushed)
		}
	}

	// a main não ganhou commits (só o inicial).
	if n := strings.Count(strings.TrimSpace(gitEx(t, repo, "log", "--oneline", "main")), "\n"); n != 0 {
		t.Fatalf("a main ganhou commits além do inicial")
	}
	// as duas branches são distintas (commits independentes).
	if dem1.ID == dem2.ID {
		t.Fatal("ids de demanda coincidiram (teste inválido)")
	}
}

// slugEsperado reproduz o slug do título usado pelo Runner (kebab ASCII).
func slugEsperado(titulo string) string {
	return strings.TrimPrefix(pipeline.NomeBranch(0, titulo), gitops.PrefixoBranch+"d0-")
}

// repoLocal cria um repo git (branch main, um commit) SEM remote — usado nos
// testes de modo merge_local (sem push).
func repoLocal(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	gitEx(t, repo, "init", "-q", "-b", "main")
	gitEx(t, repo, "config", "user.email", "praxis@test.local")
	gitEx(t, repo, "config", "user.name", "Praxis Teste")
	gitEx(t, repo, "config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("inicial\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitEx(t, repo, "add", "-A")
	gitEx(t, repo, "commit", "-q", "-m", "inicial")
	return repo
}

func novoRunnerStub(t *testing.T, d *db.DB) *pipeline.Runner {
	t.Helper()
	return &pipeline.Runner{
		Store:      d,
		Git:        gitops.Novo(),
		Home:       t.TempDir(),
		Prompt:     func(string) (string, error) { return "prompt {FASE} {TITULO}", nil },
		Selecionar: func(string) (motor.Motor, error) { return motorStub{nome: "claude"}, nil },
	}
}

// TestDemandaMultiFaseConduzTodasAsFases: uma demanda com 2 fases (a 2ª depende da
// 1ª) é conduzida do início ao fim pelo scheduler, na ordem das dependências,
// gerando 2 commits na branch da demanda.
func TestDemandaMultiFaseConduzTodasAsFases(t *testing.T) {
	repo := repoLocal(t)
	d := abrirTempDB(t)
	ctx := context.Background()

	proj, err := d.CriarProjeto(ctx, db.Projeto{
		Nome: "Multi", Slug: "multi", Pasta: repo,
		BranchPrincipal: "main", ModoIntegracao: db.ModoIntegracaoMergeLocal, Ativo: true,
	})
	if err != nil {
		t.Fatalf("criar projeto: %v", err)
	}
	dem, _, err := d.CriarDemandaComFases(ctx,
		db.Demanda{ProjectID: proj.ID, Titulo: "Multi Fase", Status: db.StatusDemandaPronta, PlanoMD: "# plano"},
		[]db.Fase{
			{Codigo: "1", Titulo: "Primeira", Status: db.StatusFasePendente},
			{Codigo: "2", Titulo: "Segunda", Status: db.StatusFasePendente, DependeDe: []string{"1"}},
		},
	)
	if err != nil {
		t.Fatalf("criar demanda: %v", err)
	}

	exec := &ExecutorDemanda{Store: d, Runner: novoRunnerStub(t, d)}
	s := Novo(Opcoes{Fonte: NovaFonteBanco(d), Executor: exec, MaxGlobal: 1, Store: d, Intervalo: 2 * time.Millisecond})
	rodarPor(t, s, func() bool {
		a, err := d.ObterDemanda(ctx, dem.ID)
		return err == nil && a.Status == db.StatusDemandaConcluida
	}, 20*time.Second)

	a, _ := d.ObterDemanda(ctx, dem.ID)
	if a.Status != db.StatusDemandaConcluida {
		t.Fatalf("status = %q, quero concluida", a.Status)
	}
	fases, _ := d.ListarFases(ctx, dem.ID)
	for _, f := range fases {
		if f.Status != db.StatusFaseConcluida {
			t.Fatalf("fase %s = %q, quero concluida", f.Codigo, f.Status)
		}
	}
	// 2 commits novos na branch (um por fase).
	log := gitEx(t, repo, "log", "--oneline", a.Branch)
	if n := strings.Count(strings.TrimSpace(log), "\n"); n != 2 {
		t.Fatalf("esperava 3 commits (inicial + 2 fases), log:\n%s", log)
	}
	// a fase 1 foi commitada antes da fase 2 (ordem das dependências).
	if strings.Index(log, "Fase 2:") > strings.Index(log, "Fase 1:") {
		t.Fatalf("ordem dos commits invertida (fase 2 antes da fase 1):\n%s", log)
	}
}

// TestDemandaRequerHumanoPausa: uma demanda cuja próxima fase exige humano é
// pausada (não executada automaticamente) e a fase permanece pendente.
func TestDemandaRequerHumanoPausa(t *testing.T) {
	repo := repoLocal(t)
	d := abrirTempDB(t)
	ctx := context.Background()

	proj, err := d.CriarProjeto(ctx, db.Projeto{
		Nome: "Humano", Slug: "humano", Pasta: repo,
		BranchPrincipal: "main", ModoIntegracao: db.ModoIntegracaoMergeLocal, Ativo: true,
	})
	if err != nil {
		t.Fatalf("criar projeto: %v", err)
	}
	dem, _, err := d.CriarDemandaComFases(ctx,
		db.Demanda{ProjectID: proj.ID, Titulo: "Precisa Humano", Status: db.StatusDemandaPronta},
		[]db.Fase{{Codigo: "1", Titulo: "Aprovação manual", Status: db.StatusFasePendente, RequerHumano: true}},
	)
	if err != nil {
		t.Fatalf("criar demanda: %v", err)
	}

	exec := &ExecutorDemanda{Store: d, Runner: novoRunnerStub(t, d)}
	s := Novo(Opcoes{Fonte: NovaFonteBanco(d), Executor: exec, MaxGlobal: 1, Store: d, Intervalo: 2 * time.Millisecond})
	rodarPor(t, s, func() bool {
		a, err := d.ObterDemanda(ctx, dem.ID)
		return err == nil && a.Status == db.StatusDemandaPausada
	}, 10*time.Second)

	a, _ := d.ObterDemanda(ctx, dem.ID)
	if a.Status != db.StatusDemandaPausada {
		t.Fatalf("status = %q, quero pausada (aguardando humano)", a.Status)
	}
	fases, _ := d.ListarFases(ctx, dem.ID)
	if fases[0].Status != db.StatusFasePendente {
		t.Fatalf("fase requer_humano = %q, quero pendente (não executada)", fases[0].Status)
	}
	if fases[0].Tentativas != 0 {
		t.Fatalf("fase requer_humano teve %d tentativas, quero 0", fases[0].Tentativas)
	}
}

// TestResolverConfigBancoGates confirma que a config "gates" (lista de comandos)
// do projeto vira os gates da pipeline.Config resolvida (wiring 2g.n1).
func TestResolverConfigBancoGates(t *testing.T) {
	repo := repoLocal(t)
	d := abrirTempDB(t)
	ctx := context.Background()
	proj, err := d.CriarProjeto(ctx, db.Projeto{
		Nome: "Gates", Slug: "gates", Pasta: repo,
		BranchPrincipal: "main", ModoIntegracao: db.ModoIntegracaoMergeLocal, Ativo: true,
	})
	if err != nil {
		t.Fatalf("criar projeto: %v", err)
	}
	if err := d.DefinirConfigProjeto(ctx, proj.ID, map[string]json.RawMessage{
		"gates": json.RawMessage(`["go build ./...","go test ./..."]`),
	}); err != nil {
		t.Fatalf("config: %v", err)
	}
	dem := db.Demanda{ProjectID: proj.ID, Titulo: "d", Status: db.StatusDemandaPronta}
	cfg, err := resolverConfigBanco(ctx, d, dem, "")
	if err != nil {
		t.Fatalf("resolverConfigBanco: %v", err)
	}
	if len(cfg.Gates) != 1 || len(cfg.Gates[0].Comandos) != 2 {
		t.Fatalf("gates resolvidos = %#v, quero 1 bloco com 2 comandos", cfg.Gates)
	}
	if cfg.Gates[0].Comandos[0] != "go build ./..." {
		t.Fatalf("comando[0] = %q", cfg.Gates[0].Comandos[0])
	}
}

func TestResolverConfigBancoSelecionaPerfilParaCadaMotor(t *testing.T) {
	repo := repoLocal(t)
	d := abrirTempDB(t)
	ctx := context.Background()
	proj, err := d.CriarProjeto(ctx, db.Projeto{
		Nome: "Perfis", Slug: "perfis", Pasta: repo,
		BranchPrincipal: "main", ModoIntegracao: db.ModoIntegracaoMergeLocal, Ativo: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	claude, err := d.CriarMotor(ctx, db.Motor{Nome: "claude", Ativo: true, Fallback: true, Prioridade: 0, Params: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	codex, err := d.CriarMotor(ctx, db.Motor{Nome: "codex", Ativo: true, Fallback: true, Prioridade: 1, Params: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	for _, conta := range []db.Conta{
		{EngineID: claude.ID, Alias: "c1", ConfigDir: "claude-1", Ativo: true},
		{EngineID: claude.ID, Alias: "c2", ConfigDir: "claude-2", Ativo: true},
		{EngineID: codex.ID, Alias: "x1", ConfigDir: "codex-1", Ativo: true},
		{EngineID: codex.ID, Alias: "x2", ConfigDir: "codex-2", Ativo: true},
	} {
		if _, err := d.CriarConta(ctx, conta); err != nil {
			t.Fatal(err)
		}
	}

	dem := db.Demanda{ID: 2, ProjectID: proj.ID}
	cfg, err := resolverConfigBanco(ctx, d, dem, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.PerfilDirDoMotor("claude"); got != "claude-2" {
		t.Fatalf("perfil Claude = %q, quero claude-2", got)
	}
	if got := cfg.PerfilDirDoMotor("codex"); got != "codex-2" {
		t.Fatalf("perfil Codex = %q, quero codex-2", got)
	}
	// o alias do perfil acompanha o diretório — é ele que fica registrado no run.
	if got := cfg.ContaDoMotor("claude"); got != "c2" {
		t.Fatalf("conta Claude = %q, quero c2", got)
	}
	if got := cfg.ContaDoMotor("codex"); got != "x2" {
		t.Fatalf("conta Codex = %q, quero x2", got)
	}
	// TODOS os perfis entram na lista, rotacionados a partir do da afinidade:
	// o fallback esgota c2 e depois c1 antes de trocar para o codex.
	perfis := cfg.PerfisDoMotor("claude")
	if len(perfis) != 2 || perfis[0].Conta != "c2" || perfis[1].Conta != "c1" {
		t.Fatalf("perfis Claude = %+v, quero [c2 c1]", perfis)
	}
}

// TestResolverConfigBancoMotorForaDoFallback: um motor com fallback desligado
// não entra na cadeia nem vira o motor padrão, mas mantém modelo e perfis
// resolvidos para quando o motor preferido do projeto apontar para ele.
func TestResolverConfigBancoMotorForaDoFallback(t *testing.T) {
	repo := repoLocal(t)
	d := abrirTempDB(t)
	ctx := context.Background()
	proj, err := d.CriarProjeto(ctx, db.Projeto{
		Nome: "Exclusivo", Slug: "exclusivo", Pasta: repo,
		BranchPrincipal: "main", ModoIntegracao: db.ModoIntegracaoMergeLocal, Ativo: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	// prioridade 0, mas fora do fallback: nunca é escolhido automaticamente.
	exclusivo, err := d.CriarMotor(ctx, db.Motor{Nome: "codex", Ativo: true, Fallback: false,
		Prioridade: 0, ModeloExec: "gpt-manual", Params: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.CriarConta(ctx, db.Conta{EngineID: exclusivo.ID, Alias: "manual", ConfigDir: "codex-manual", Ativo: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.CriarMotor(ctx, db.Motor{Nome: "claude", Ativo: true, Fallback: true,
		Prioridade: 1, Params: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}

	dem := db.Demanda{ID: 1, ProjectID: proj.ID}
	cfg, err := resolverConfigBanco(ctx, d, dem, "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MotorPadrao != "claude" {
		t.Fatalf("motor padrão = %q, quero claude (o exclusivo é só manual)", cfg.MotorPadrao)
	}
	if len(cfg.Fallback.Ordem) != 1 || cfg.Fallback.Ordem[0] != "claude" {
		t.Fatalf("ordem de fallback = %v, quero só [claude]", cfg.Fallback.Ordem)
	}
	if !cfg.Fallback.Ativo {
		t.Fatal("fallback deveria ficar ativo para o uso manual poder cair na cadeia")
	}
	// o motor manual continua utilizável: modelo e perfil resolvidos.
	if got := cfg.ContaDoMotor("codex"); got != "manual" {
		t.Fatalf("conta do motor manual = %q, quero manual", got)
	}
	if got := cfg.PerfilDirDoMotor("codex"); got != "codex-manual" {
		t.Fatalf("perfil do motor manual = %q, quero codex-manual", got)
	}
}

// TestErroAntigoLimpaAoProgredir: uma demanda que carrega o texto de uma falha
// anterior (badge "erro" na UI) tem o campo limpo ao voltar a progredir — pela
// fase concluída (limparErro) e pela pausa aguardando humano, que NÃO é erro
// (marcarDemanda grava erro vazio).
func TestErroAntigoLimpaAoProgredir(t *testing.T) {
	repo := repoLocal(t)
	d := abrirTempDB(t)
	ctx := context.Background()

	proj, err := d.CriarProjeto(ctx, db.Projeto{
		Nome: "ErroAntigo", Slug: "erro-antigo", Pasta: repo,
		BranchPrincipal: "main", ModoIntegracao: db.ModoIntegracaoMergeLocal, Ativo: true,
	})
	if err != nil {
		t.Fatalf("criar projeto: %v", err)
	}
	dem, _, err := d.CriarDemandaComFases(ctx,
		db.Demanda{ProjectID: proj.ID, Titulo: "Recuperada", Status: db.StatusDemandaPronta, PlanoMD: "# plano"},
		[]db.Fase{
			{Codigo: "1", Titulo: "Automática", Status: db.StatusFasePendente},
			{Codigo: "2", Titulo: "Homologação", Status: db.StatusFasePendente, RequerHumano: true, DependeDe: []string{"1"}},
		},
	)
	if err != nil {
		t.Fatalf("criar demanda: %v", err)
	}
	// falha antiga registrada (ex.: run que caiu antes da recuperação).
	dem.Erro = "executor terminou com erro (falha antiga)"
	if dem, err = d.AtualizarDemanda(ctx, dem); err != nil {
		t.Fatalf("gravar erro antigo: %v", err)
	}

	exec := &ExecutorDemanda{Store: d, Runner: novoRunnerStub(t, d)}
	s := Novo(Opcoes{Fonte: NovaFonteBanco(d), Executor: exec, MaxGlobal: 1, Store: d, Intervalo: 2 * time.Millisecond})
	// roda até pausar aguardando humano (a fase 1 conclui no caminho).
	rodarPor(t, s, func() bool {
		a, err := d.ObterDemanda(ctx, dem.ID)
		return err == nil && a.Status == db.StatusDemandaPausada
	}, 20*time.Second)

	a, _ := d.ObterDemanda(ctx, dem.ID)
	if a.Status != db.StatusDemandaPausada {
		t.Fatalf("status = %q, quero pausada (aguardando humano)", a.Status)
	}
	if a.Erro != "" {
		t.Fatalf("erro antigo deveria ter sido limpo ao progredir: %q", a.Erro)
	}
	fases, _ := d.ListarFases(ctx, dem.ID)
	for _, f := range fases {
		if f.Codigo == "1" && f.Status != db.StatusFaseConcluida {
			t.Fatalf("fase 1 = %q, quero concluida", f.Status)
		}
	}
}

// TestResolverConfigBancoFiltraMotoresPorCriador cobre a Fase A (donos e
// visibilidade): a cadeia de fallback de uma demanda só contém motores VISÍVEIS
// ao criador dela; demanda sem criador (token de API) usa só os públicos; e um
// motor_preferido apontando para um motor escondido pela ACL é ignorado.
func TestResolverConfigBancoFiltraMotoresPorCriador(t *testing.T) {
	repo := repoLocal(t)
	d := abrirTempDB(t)
	ctx := context.Background()
	proj, err := d.CriarProjeto(ctx, db.Projeto{
		Nome: "ACL", Slug: "acl", Pasta: repo,
		BranchPrincipal: "main", ModoIntegracao: db.ModoIntegracaoMergeLocal, Ativo: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	dona, err := d.CriarUsuario(ctx, "Dona", "dona@x.com", "senha-123", nil)
	if err != nil {
		t.Fatal(err)
	}

	// "restrito" tem a MAIOR prioridade, mas só a dona o vê; "publico" é aberto.
	restrito, err := d.CriarMotor(ctx, db.Motor{Nome: "restrito", Ativo: true, Fallback: true,
		Prioridade: 0, ModeloExec: "modelo-r", Params: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.CriarMotor(ctx, db.Motor{Nome: "publico", Ativo: true, Fallback: true,
		Prioridade: 1, ModeloExec: "modelo-p", Params: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
	if err := d.DefinirAcessoMotor(ctx, restrito.ID, []int64{dona.ID}, nil); err != nil {
		t.Fatal(err)
	}

	// Demanda SEM criador (token de API): só o público entra.
	dem := db.Demanda{ProjectID: proj.ID, Titulo: "d", Status: db.StatusDemandaPronta}
	cfg, err := resolverConfigBanco(ctx, d, dem, "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MotorPadrao != "publico" {
		t.Fatalf("sem criador: motor padrão = %q, quero publico", cfg.MotorPadrao)
	}
	for _, m := range cfg.Fallback.Ordem {
		if m == "restrito" {
			t.Fatal("motor restrito não pode entrar na cadeia de uma demanda sem criador")
		}
	}
	if _, tem := cfg.Modelos["restrito"]; tem {
		t.Fatal("modelo do motor restrito vazou para a config")
	}

	// Demanda DA DONA: o restrito (prioridade maior) vira o padrão.
	demDona := db.Demanda{ProjectID: proj.ID, Titulo: "d2", Status: db.StatusDemandaPronta, CriadoPor: &dona.ID}
	cfg, err = resolverConfigBanco(ctx, d, demDona, "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MotorPadrao != "restrito" {
		t.Fatalf("criadora dona: motor padrão = %q, quero restrito", cfg.MotorPadrao)
	}

	// motor_preferido apontando para o restrito: vale para a dona, é ignorado
	// para quem não o vê (fica o padrão da cadeia).
	if err := d.DefinirConfigProjeto(ctx, proj.ID, map[string]json.RawMessage{
		"motor_preferido": json.RawMessage(`"restrito"`),
	}); err != nil {
		t.Fatal(err)
	}
	cfg, err = resolverConfigBanco(ctx, d, dem, "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MotorPadrao != "publico" {
		t.Fatalf("preferido escondido: motor padrão = %q, quero publico", cfg.MotorPadrao)
	}
	cfg, err = resolverConfigBanco(ctx, d, demDona, "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MotorPadrao != "restrito" {
		t.Fatalf("preferido visível à dona: motor padrão = %q, quero restrito", cfg.MotorPadrao)
	}
}
