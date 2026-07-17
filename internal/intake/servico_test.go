package intake

import (
	"context"
	"encoding/json"
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
	m, err := d.CriarMotor(ctx, db.Motor{Nome: "claude", Prioridade: 1, Ativo: true,
		ModeloAnalise: "sonnet", BudgetFaseUSD: 2.5, TimeoutMin: 30})
	if err != nil {
		t.Fatalf("criar motor: %v", err)
	}
	if _, err := d.CriarConta(ctx, db.Conta{EngineID: m.ID, Alias: "principal", ConfigDir: `C:\cfg`, Ativo: true}); err != nil {
		t.Fatalf("criar conta: %v", err)
	}

	svc := NovoServico(OpcoesServico{Store: d})
	nome, modelo, _, cfg, budget, timeout := svc.resolverMotor(ctx)
	if nome != "claude" || modelo != "sonnet" {
		t.Fatalf("motor/modelo = %q/%q, quero claude/sonnet", nome, modelo)
	}
	if cfg != `C:\cfg` {
		t.Fatalf("config_dir = %q, quero da conta ativa", cfg)
	}
	if budget != 2.5 || timeout != 30 {
		t.Fatalf("budget/timeout = %v/%v", budget, timeout)
	}
}

func TestResolverMotorSemMotorCaiNoDefault(t *testing.T) {
	d := abrirDB(t)
	svc := NovoServico(OpcoesServico{Store: d})
	nome, modelo, _, cfg, _, _ := svc.resolverMotor(context.Background())
	if nome != "claude" || modelo != "" || cfg != "" {
		t.Fatalf("default = %q/%q/%q, quero claude/''/''", nome, modelo, cfg)
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
