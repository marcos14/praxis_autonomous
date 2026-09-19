package intake

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/marcos14/praxis-autonomous/internal/db"
	"github.com/marcos14/praxis-autonomous/internal/motor"
)

func TestResolverMotorPrioridadeEConta(t *testing.T) {
	d := abrirDB(t)
	ctx := context.Background()

	// motor inativo (não deve ser escolhido, mesmo com prioridade menor).
	if _, err := d.CriarMotor(ctx, db.Motor{Nome: "opencode", Prioridade: 0, Ativo: false}); err != nil {
		t.Fatalf("criar motor: %v", err)
	}
	// motor ativo com modelo de análise e uma conta ativa.
	m, err := d.CriarMotor(ctx, db.Motor{Nome: "claude", Prioridade: 1, Ativo: true, Fallback: true,
		ModeloAnalise: "sonnet", BudgetFaseUSD: 2.5, TimeoutMin: 30})
	if err != nil {
		t.Fatalf("criar motor: %v", err)
	}
	if _, err := d.CriarConta(ctx, db.Conta{EngineID: m.ID, Alias: "principal", ConfigDir: `C:\cfg`, Ativo: true}); err != nil {
		t.Fatalf("criar conta: %v", err)
	}

	svc := NovoServico(OpcoesServico{Store: d})
	nome, modelo, _, conta, cfg, budget, timeout := svc.resolverMotor(ctx)
	if nome != "claude" || modelo != "sonnet" {
		t.Fatalf("motor/modelo = %q/%q, quero claude/sonnet", nome, modelo)
	}
	if conta != "principal" {
		t.Fatalf("conta = %q, quero o alias da conta ativa", conta)
	}
	if cfg != `C:\cfg` {
		t.Fatalf("config_dir = %q, quero da conta ativa", cfg)
	}
	if budget != 2.5 || timeout != 30 {
		t.Fatalf("budget/timeout = %v/%v", budget, timeout)
	}
}

// TestResolverMotorPulaMotorForaDoFallback: um motor de uso manual (fallback
// desligado) não é escolhido automaticamente pelo intake, mesmo com prioridade
// menor — a escolha cai no primeiro motor ativo QUE participa do fallback.
func TestResolverMotorPulaMotorForaDoFallback(t *testing.T) {
	d := abrirDB(t)
	ctx := context.Background()

	if _, err := d.CriarMotor(ctx, db.Motor{Nome: "codex", Prioridade: 0, Ativo: true, Fallback: false,
		ModeloAnalise: "gpt-manual"}); err != nil {
		t.Fatalf("criar motor manual: %v", err)
	}
	if _, err := d.CriarMotor(ctx, db.Motor{Nome: "claude", Prioridade: 1, Ativo: true, Fallback: true,
		ModeloAnalise: "sonnet"}); err != nil {
		t.Fatalf("criar motor: %v", err)
	}

	svc := NovoServico(OpcoesServico{Store: d})
	nome, modelo, _, _, _, _, _ := svc.resolverMotor(ctx)
	if nome != "claude" || modelo != "sonnet" {
		t.Fatalf("motor/modelo = %q/%q, quero claude/sonnet (o manual fica de fora)", nome, modelo)
	}
}

func TestResolverMotorSemMotorCaiNoDefault(t *testing.T) {
	d := abrirDB(t)
	svc := NovoServico(OpcoesServico{Store: d})
	nome, modelo, _, conta, cfg, _, _ := svc.resolverMotor(context.Background())
	if nome != "claude" || modelo != "" || conta != "" || cfg != "" {
		t.Fatalf("default = %q/%q/%q/%q, quero claude/''/''/''", nome, modelo, conta, cfg)
	}
}

func TestServicoDispararAnalisaEmBackground(t *testing.T) {
	d := abrirDB(t)
	ctx := context.Background()
	dem := demandaComPRD(t, d, "Emitir boleto híbrido com PIX.")

	saida := `{"resumo":"resumo do código","arquivos_provaveis":["boleto.go"],
		"perguntas":[{"pergunta":"PIX por filial?","tipo":"escolha","opcoes":["sim","não"],"sugestao":"sim","impacto":"alto"}]}`

	svc := NovoServico(OpcoesServico{
		Store: d,
		Selecionar: seletorStub(stubMotor{nome: "claude", caps: motor.Capacidades{SchemaNativo: true},
			fn: func(motor.OpcoesRun) (*motor.ResultadoRun, error) {
				return &motor.ResultadoRun{Estruturado: json.RawMessage(saida), CustoUSD: 0.2}, nil
			}}),
	})

	svc.Disparar(dem.ID)
	svc.Aguardar()

	got, _ := d.ObterDemanda(ctx, dem.ID)
	if got.Status != db.StatusDemandaAguardandoRespostas {
		t.Fatalf("status = %q, quero aguardando_respostas", got.Status)
	}
	perguntas, _ := d.ListarPerguntas(ctx, dem.ID)
	if len(perguntas) != 1 {
		t.Fatalf("len perguntas = %d, quero 1", len(perguntas))
	}
}

func TestServicoDispararPlanejamentoEmBackground(t *testing.T) {
	d := abrirDB(t)
	ctx := context.Background()
	dem := demandaPlanejando(t, d, "Emitir boleto híbrido com PIX.")

	saida := `{"plano_md":"# Plano","fases":[{"codigo":"1","titulo":"Schema"}]}`

	svc := NovoServico(OpcoesServico{
		Store: d,
		Selecionar: seletorStub(stubMotor{nome: "claude", caps: motor.Capacidades{SchemaNativo: true},
			fn: func(motor.OpcoesRun) (*motor.ResultadoRun, error) {
				return &motor.ResultadoRun{Estruturado: json.RawMessage(saida), CustoUSD: 0.3}, nil
			}}),
	})

	svc.DispararPlanejamento(dem.ID)
	svc.Aguardar()

	got, _ := d.ObterDemanda(ctx, dem.ID)
	if got.Status != db.StatusDemandaAguardandoAprovacao {
		t.Fatalf("status = %q, quero aguardando_aprovacao", got.Status)
	}
	fases, _ := d.ListarFases(ctx, dem.ID)
	if len(fases) != 1 {
		t.Fatalf("len fases = %d, quero 1", len(fases))
	}
}

// TestServicoAtualizaRepoAntesDaAnalise garante que o serviço posiciona o repo
// do projeto na branch principal (pull) ANTES de o analista ler o código — no
// servidor, o clone pode estar defasado ou em outra branch.
func TestServicoAtualizaRepoAntesDaAnalise(t *testing.T) {
	d := abrirDB(t)
	ctx := context.Background()
	dem := demandaComPRD(t, d, "Permitir split de recebimento entre filiais.")
	proj, err := d.ObterProjeto(ctx, dem.ProjectID)
	if err != nil {
		t.Fatalf("obter projeto: %v", err)
	}

	saida := `{"resumo":"ok","perguntas":[]}`
	var ordem []string
	var chamada string
	svc := NovoServico(OpcoesServico{
		Store: d,
		Ctx:   ctx,
		Selecionar: seletorStub(stubMotor{nome: "claude", caps: motor.Capacidades{SchemaNativo: true},
			fn: func(op motor.OpcoesRun) (*motor.ResultadoRun, error) {
				ordem = append(ordem, "motor")
				return &motor.ResultadoRun{Estruturado: json.RawMessage(saida)}, nil
			}}),
		AtualizarRepo: func(pasta, branch string) (string, error) {
			ordem = append(ordem, "pull")
			chamada = pasta + "@" + branch
			return "", nil
		},
	})

	if err := svc.Analisar(ctx, dem.ID); err != nil {
		t.Fatalf("Analisar: %v", err)
	}
	if chamada != proj.Pasta+"@"+proj.BranchPrincipal {
		t.Fatalf("atualizarRepo = %q, quero %q", chamada, proj.Pasta+"@"+proj.BranchPrincipal)
	}
	if len(ordem) != 2 || ordem[0] != "pull" || ordem[1] != "motor" {
		t.Fatalf("ordem = %v, quero o pull ANTES do motor", ordem)
	}
}

// TestServicoAvisoDeRepoViraEvento garante que um repo que não pôde ser
// atualizado gera um evento visível da demanda (e a análise segue mesmo assim).
func TestServicoAvisoDeRepoViraEvento(t *testing.T) {
	d := abrirDB(t)
	ctx := context.Background()
	dem := demandaComPRD(t, d, "PRD qualquer.")

	saida := `{"resumo":"ok","perguntas":[]}`
	svc := NovoServico(OpcoesServico{
		Store: d,
		Ctx:   ctx,
		Selecionar: seletorStub(stubMotor{nome: "claude", caps: motor.Capacidades{SchemaNativo: true},
			fn: func(op motor.OpcoesRun) (*motor.ResultadoRun, error) {
				return &motor.ResultadoRun{Estruturado: json.RawMessage(saida)}, nil
			}}),
		AtualizarRepo: func(pasta, branch string) (string, error) {
			return "a branch local divergiu de origin/main; a análise usará o estado local", nil
		},
	})

	if err := svc.Analisar(ctx, dem.ID); err != nil {
		t.Fatalf("Analisar: %v", err)
	}
	// A análise concluiu mesmo com o aviso…
	got, _ := d.ObterDemanda(ctx, dem.ID)
	if got.Status != db.StatusDemandaAguardandoRespostas {
		t.Fatalf("status = %q, quero aguardando_respostas", got.Status)
	}
	// …e o aviso virou evento da demanda.
	eventos, err := d.EventosApos(ctx, 0, 100, db.Visao{})
	if err != nil {
		t.Fatalf("EventosApos: %v", err)
	}
	temAviso := false
	for _, ev := range eventos {
		if ev.Tipo == "aviso" && strings.Contains(ev.Detalhe, "divergiu") &&
			ev.DemandID != nil && *ev.DemandID == dem.ID {
			temAviso = true
		}
	}
	if !temAviso {
		t.Fatalf("aviso do repo não virou evento da demanda: %+v", eventos)
	}
}
