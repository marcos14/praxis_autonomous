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

// stubMotor e um Motor programavel para os testes (nao chama nenhum CLI).
type stubMotor struct {
	nome string
	caps motor.Capacidades
	fn   func(op motor.OpcoesRun) (*motor.ResultadoRun, error)
}

func (s stubMotor) Nome() string                   { return s.nome }
func (s stubMotor) Capacidades() motor.Capacidades { return s.caps }
func (s stubMotor) Rodar(op motor.OpcoesRun) (*motor.ResultadoRun, error) {
	return s.fn(op)
}

// seletorStub devolve um seletor que resolve os motores dados por nome.
func seletorStub(ms ...stubMotor) func(string) (motor.Motor, error) {
	byNome := map[string]stubMotor{}
	for _, m := range ms {
		byNome[m.nome] = m
	}
	return func(nome string) (motor.Motor, error) {
		if m, ok := byNome[normalizarMotor(nome)]; ok {
			return m, nil
		}
		return nil, errMotorDesconhecido(nome)
	}
}

type errMotorDesconhecido string

func (e errMotorDesconhecido) Error() string { return "motor desconhecido nos testes: " + string(e) }

// gitInit cria um repo git com um commit inicial (arvore limpa) e devolve o dir.
func gitInit(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v — %s", strings.Join(args, " "), err, out)
		}
	}
	run("init", "-q")
	run("config", "user.email", "praxis@test.local")
	run("config", "user.name", "Praxis Teste")
	run("config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("inicial\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "-A")
	run("commit", "-q", "-m", "inicial")
	return dir
}

// contexto monta um ContextoExec de teste ligado a um banco temporario, com uma
// demanda e uma fase pendente. Devolve o contexto, o worktree (repo git) e a fase.
func contexto(t *testing.T, sel func(string) (motor.Motor, error)) (*ContextoExec, db.Fase) {
	t.Helper()
	caminho := filepath.Join(t.TempDir(), "praxis.db")
	d, err := db.Abrir(caminho)
	if err != nil {
		t.Fatalf("Abrir db: %v", err)
	}
	t.Cleanup(func() { d.Fechar() })

	ctx := context.Background()
	proj, err := d.CriarProjeto(ctx, db.Projeto{Nome: "Proj", Slug: "proj", Pasta: t.TempDir(), ModoIntegracao: "merge_request"})
	if err != nil {
		t.Fatalf("CriarProjeto: %v", err)
	}
	dem, err := d.CriarDemanda(ctx, db.Demanda{ProjectID: proj.ID, Titulo: "Demanda", PlanoMD: "# plano"})
	if err != nil {
		t.Fatalf("CriarDemanda: %v", err)
	}
	fase, err := d.CriarFase(ctx, db.Fase{DemandID: dem.ID, Codigo: "1", Titulo: "Fase um", Status: db.StatusFasePendente})
	if err != nil {
		t.Fatalf("CriarFase: %v", err)
	}
	wt := gitInit(t)
	c := &ContextoExec{
		Demanda:    dem,
		Fase:       fase,
		Worktree:   wt,
		DirLogs:    t.TempDir(),
		Config:     Config{MaxCorrecoes: 2, MaxCiclosRevisao: 2},
		Store:      d,
		Git:        gitops.Novo(),
		Prompt:     func(nome string) (string, error) { return "prompt {FASE} {TITULO}", nil },
		Ctx:        ctx,
		Selecionar: sel,
	}
	return c, fase
}

// motorHappy escreve um arquivo no worktree para executor/corretor e aprova no
// revisor.
func motorHappy(nome string) stubMotor {
	return stubMotor{nome: nome, fn: func(op motor.OpcoesRun) (*motor.ResultadoRun, error) {
		if strings.Contains(op.RotuloLog, "revisor") {
			return &motor.ResultadoRun{Resultado: `{"veredito":"APROVADO","problemas":[]}`, LogPath: "rev.jsonl"}, nil
		}
		if err := os.WriteFile(filepath.Join(op.Dir, "entrega.txt"), []byte("trabalho da fase\n"), 0o644); err != nil {
			return nil, err
		}
		return &motor.ResultadoRun{Resultado: "implementei a fase", CustoUSD: 0.10, LogPath: "exec.jsonl"}, nil
	}}
}

// TestExecutarFaseAteOCommit: pipeline de uma fase com motor stub roda ate o
// commit (criterio da Fase 2b).
func TestExecutarFaseAteOCommit(t *testing.T) {
	c, _ := contexto(t, seletorStub(motorHappy("claude")))
	c.Config.Contas = map[string]string{"claude": "principal"}
	res, err := c.ExecutarFase()
	if err != nil {
		t.Fatalf("ExecutarFase: %v", err)
	}
	if res.Situacao != SituacaoConcluida {
		t.Fatalf("situacao = %q, esperava concluida (erro: %s)", res.Situacao, res.Erro)
	}
	if !res.CommitFeito {
		t.Fatal("esperava commit feito")
	}
	if res.CustoUSD < 0.10 {
		t.Fatalf("custo = %v, esperava >= 0.10", res.CustoUSD)
	}

	// commit criado no worktree
	out, err := exec.Command("git", "-C", c.Worktree, "log", "--oneline").CombinedOutput()
	if err != nil {
		t.Fatalf("git log: %v — %s", err, out)
	}
	if strings.Count(string(out), "\n") < 2 { // inicial + fase
		t.Fatalf("esperava 2 commits, log:\n%s", out)
	}
	if !strings.Contains(string(out), "Fase 1: Fase um [praxis]") {
		t.Fatalf("mensagem de commit da fase ausente:\n%s", out)
	}

	// fase concluida no banco
	fase, err := c.Store.ObterFase(c.Ctx, c.Fase.ID)
	if err != nil {
		t.Fatalf("ObterFase: %v", err)
	}
	if fase.Status != db.StatusFaseConcluida {
		t.Fatalf("status da fase = %q", fase.Status)
	}
	if fase.Tentativas != 1 {
		t.Fatalf("tentativas = %d, esperava 1", fase.Tentativas)
	}

	// execucoes registradas (executor + revisor)
	execs, err := c.Store.ListarExecucoes(c.Ctx, c.Demanda.ID)
	if err != nil {
		t.Fatalf("ListarExecucoes: %v", err)
	}
	if len(execs) != 2 {
		t.Fatalf("execucoes = %d, esperava 2 (%+v)", len(execs), execs)
	}
	for _, e := range execs {
		if e.Conta != "principal" {
			t.Fatalf("execucao %s com conta %q, esperava principal", e.Operacao, e.Conta)
		}
	}
}

// TestExecutarFaseRevisorReprovaEntaoAprova exercita o ciclo de corretor apos
// reprovacao do revisor.
func TestExecutarFaseRevisorReprovaEntaoAprova(t *testing.T) {
	revisor := 0
	m := stubMotor{nome: "claude", fn: func(op motor.OpcoesRun) (*motor.ResultadoRun, error) {
		if strings.Contains(op.RotuloLog, "revisor") {
			revisor++
			if revisor == 1 {
				return &motor.ResultadoRun{Resultado: `{"veredito":"REPROVADO","problemas":["faltou X"]}`, LogPath: "r1"}, nil
			}
			return &motor.ResultadoRun{Resultado: `{"veredito":"APROVADO","problemas":[]}`, LogPath: "r2"}, nil
		}
		if err := os.WriteFile(filepath.Join(op.Dir, "entrega.txt"), []byte("v"+op.RotuloLog+"\n"), 0o644); err != nil {
			return nil, err
		}
		return &motor.ResultadoRun{Resultado: "ok", CustoUSD: 0.05, LogPath: "e"}, nil
	}}
	c, _ := contexto(t, seletorStub(m))
	res, err := c.ExecutarFase()
	if err != nil {
		t.Fatalf("ExecutarFase: %v", err)
	}
	if res.Situacao != SituacaoConcluida {
		t.Fatalf("situacao = %q (erro %s)", res.Situacao, res.Erro)
	}
	if revisor != 2 {
		t.Fatalf("revisor chamado %d vezes, esperava 2", revisor)
	}
	// executor + revisor1 + corretor-rev1 + revisor2 = 4 execucoes
	execs, _ := c.Store.ListarExecucoes(c.Ctx, c.Demanda.ID)
	if len(execs) != 4 {
		t.Fatalf("execucoes = %d, esperava 4", len(execs))
	}
}

// TestExecutarFaseFranquiaNaoBloqueia: sem fallback, franquia esgota → a fase
// vira aguardando_franquia com horario de retomada, sem bloquear.
func TestExecutarFaseFranquiaNaoBloqueia(t *testing.T) {
	base := time.Date(2026, 7, 16, 9, 0, 0, 0, time.UTC)
	m := stubMotor{nome: "claude", fn: func(motor.OpcoesRun) (*motor.ResultadoRun, error) {
		return &motor.ResultadoRun{LimiteSessao: true, DetalheLimite: "usage limit reached; reset 10:00"}, nil
	}}
	c, _ := contexto(t, seletorStub(m))
	c.Agora = func() time.Time { return base }

	res, err := c.ExecutarFase()
	if err != nil {
		t.Fatalf("ExecutarFase (nao deveria devolver erro de infra): %v", err)
	}
	if res.Situacao != SituacaoAguardandoFranquia {
		t.Fatalf("situacao = %q, esperava aguardando_franquia", res.Situacao)
	}
	if !res.RetomarEm.Equal(base.Add(EsperaResetFranquia)) {
		t.Fatalf("RetomarEm = %s, esperava %s", res.RetomarEm, base.Add(EsperaResetFranquia))
	}
	fase, _ := c.Store.ObterFase(c.Ctx, c.Fase.ID)
	if fase.Status != db.StatusFasePausada {
		t.Fatalf("fase deveria estar pausada, veio %q", fase.Status)
	}
}

// TestExecutarFaseRetomaExecutandoOrfaComWorktreeSujo: uma fase presa em
// `executando` (o serviço caiu no meio do run, sem persistir o desfecho) é
// retomada como uma pausada: o trabalho não commitado no worktree pertence a
// ela mesma, então a pré-checagem de árvore limpa não se aplica e a fase
// conclui normalmente (a sobra entra no commit da fase).
func TestExecutarFaseRetomaExecutandoOrfaComWorktreeSujo(t *testing.T) {
	c, fase := contexto(t, seletorStub(motorHappy("claude")))
	fase.Status = db.StatusFaseExecutando
	fase, err := c.Store.AtualizarFase(c.Ctx, fase)
	if err != nil {
		t.Fatalf("AtualizarFase: %v", err)
	}
	c.Fase = fase
	// sobra não commitada do run interrompido.
	if err := os.WriteFile(filepath.Join(c.Worktree, "sobra.txt"), []byte("parcial\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := c.ExecutarFase()
	if err != nil {
		t.Fatalf("ExecutarFase: %v", err)
	}
	if res.Situacao != SituacaoConcluida {
		t.Fatalf("situacao = %q (erro %s), esperava concluida", res.Situacao, res.Erro)
	}
	relida, _ := c.Store.ObterFase(c.Ctx, fase.ID)
	if relida.Status != db.StatusFaseConcluida {
		t.Fatalf("fase = %q, esperava concluida", relida.Status)
	}
}

// TestExecutarFaseExecutorErro: executor com is_error → fase falhou.
func TestExecutarFaseExecutorErro(t *testing.T) {
	m := stubMotor{nome: "claude", fn: func(motor.OpcoesRun) (*motor.ResultadoRun, error) {
		return &motor.ResultadoRun{IsError: true, Subtipo: "erro_execucao", LogPath: "e"}, nil
	}}
	c, _ := contexto(t, seletorStub(m))
	res, err := c.ExecutarFase()
	if err != nil {
		t.Fatalf("erro de infra inesperado: %v", err)
	}
	if res.Situacao != SituacaoFalhou {
		t.Fatalf("situacao = %q, esperava falhou", res.Situacao)
	}
	fase, _ := c.Store.ObterFase(c.Ctx, c.Fase.ID)
	if fase.Status != db.StatusFaseFalhou {
		t.Fatalf("fase = %q", fase.Status)
	}
}
