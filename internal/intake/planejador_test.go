package intake

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/marcos14/praxis-autonomous/internal/db"
	"github.com/marcos14/praxis-autonomous/internal/motor"
)

// demandaPlanejando cria uma demanda com PRD no chat, perguntas respondidas e
// status `planejando` — o ponto de partida do planejador.
func demandaPlanejando(t *testing.T, d *db.DB, prd string) db.Demanda {
	t.Helper()
	ctx := context.Background()
	dem := demandaComPRD(t, d, prd)
	if _, err := d.SubstituirPerguntas(ctx, dem.ID, []db.Pergunta{
		{Pergunta: "Rateio por percentual ou item?", Sugestao: "percentual", Impacto: db.ImpactoAlto},
		{Pergunta: "Split na emissão ou na baixa?", Sugestao: "baixa"},
	}); err != nil {
		t.Fatalf("substituir perguntas: %v", err)
	}
	perg, _ := d.ListarPerguntas(ctx, dem.ID)
	if _, err := d.ResponderPerguntas(ctx, dem.ID, []db.RespostaPergunta{{ID: perg[0].ID, Resposta: "percentual"}}); err != nil {
		t.Fatalf("responder: %v", err)
	}
	dem.Status = db.StatusDemandaPlanejando
	atual, err := d.AtualizarDemanda(ctx, dem)
	if err != nil {
		t.Fatalf("atualizar status: %v", err)
	}
	return atual
}

func TestPlanejadorGeraPlanoEFases(t *testing.T) {
	d := abrirDB(t)
	ctx := context.Background()
	dem := demandaPlanejando(t, d, "Permitir split de recebimento entre filiais.")

	saida := `{"plano_md":"# Plano\n\n## Fase 1 — Schema\n- [ ] criar tabela",
		"fases":[
		  {"codigo":"1","titulo":"Schema do split","requer_humano":false},
		  {"codigo":"2","titulo":"Regra de rateio","depende_de":["1"],"observacao":"usar percentual"},
		  {"codigo":"3","titulo":"UI de configuração","depende_de":["2"],"requer_humano":true}
		]}`

	var promptRecebido string
	var leituraSomente bool
	p := &Planejador{
		Store:  d,
		Motor:  "claude",
		Modelo: "sonnet",
		Prompt: func(_ context.Context, nome string) (string, error) {
			if nome != PromptPlanejador {
				t.Fatalf("prompt pedido = %q, quero %q", nome, PromptPlanejador)
			}
			return "PRD: {PRD}\nQA: {QA}", nil
		},
		Selecionar: seletorStub(stubMotor{nome: "claude", caps: motor.Capacidades{SchemaNativo: true},
			fn: func(op motor.OpcoesRun) (*motor.ResultadoRun, error) {
				promptRecebido = op.Prompt
				leituraSomente = op.SomenteLeitura
				return &motor.ResultadoRun{Estruturado: json.RawMessage(saida), CustoUSD: 0.55, LogPath: "planejador.jsonl"}, nil
			}}),
	}

	if err := p.Planejar(ctx, dem.ID); err != nil {
		t.Fatalf("Planejar: %v", err)
	}

	// PRD e respostas foram injetados no prompt.
	if !strings.Contains(promptRecebido, "Permitir split") {
		t.Fatalf("PRD não injetado: %q", promptRecebido)
	}
	if !strings.Contains(promptRecebido, "percentual") {
		t.Fatalf("resposta do usuário não injetada no QA: %q", promptRecebido)
	}
	if !leituraSomente {
		t.Fatal("o planejador deve rodar em modo SomenteLeitura")
	}

	got, err := d.ObterDemanda(ctx, dem.ID)
	if err != nil {
		t.Fatalf("ObterDemanda: %v", err)
	}
	if got.Status != db.StatusDemandaAguardandoAprovacao {
		t.Fatalf("status = %q, quero aguardando_aprovacao", got.Status)
	}
	if !strings.Contains(got.PlanoMD, "# Plano") {
		t.Fatalf("plano_md não persistido: %q", got.PlanoMD)
	}

	fases, err := d.ListarFases(ctx, dem.ID)
	if err != nil {
		t.Fatalf("ListarFases: %v", err)
	}
	if len(fases) != 3 {
		t.Fatalf("len fases = %d, quero 3", len(fases))
	}
	if fases[0].Codigo != "1" || fases[0].Ordem != 1 {
		t.Fatalf("fase 1 mal persistida: %+v", fases[0])
	}
	if len(fases[1].DependeDe) != 1 || fases[1].DependeDe[0] != "1" {
		t.Fatalf("depende_de da fase 2: %+v", fases[1].DependeDe)
	}
	if !fases[2].RequerHumano {
		t.Fatal("requer_humano da fase 3 não persistido")
	}

	// fala do planejador no chat.
	msgs, _ := d.ListarMensagensChat(ctx, dem.ID)
	achou := false
	for _, m := range msgs {
		if m.Papel == db.PapelPlanejador {
			achou = true
		}
	}
	if !achou {
		t.Fatal("nenhuma fala do planejador no chat")
	}

	// execução registrada (operacao=planejador) e eventos.
	execs, _ := d.ListarExecucoes(ctx, dem.ID)
	if len(execs) != 1 || execs[0].Operacao != db.OperacaoPlanejador {
		t.Fatalf("execução do planejador não registrada: %+v", execs)
	}
	evs, _ := d.ListarEventos(ctx, db.FiltroEventos{DemandID: &dem.ID})
	if !contemEvento(evs, "planejamento_iniciado") || !contemEvento(evs, "planejamento_concluido") {
		t.Fatalf("faltam eventos de planejamento: %+v", evs)
	}
}

func TestPlanejadorSemFasesMarcaFalhou(t *testing.T) {
	d := abrirDB(t)
	ctx := context.Background()
	dem := demandaPlanejando(t, d, "PRD qualquer.")

	p := &Planejador{
		Store: d, Motor: "claude",
		Prompt: func(context.Context, string) (string, error) { return "x {PRD} {QA}", nil },
		Selecionar: seletorStub(stubMotor{nome: "claude", fn: func(motor.OpcoesRun) (*motor.ResultadoRun, error) {
			return &motor.ResultadoRun{Resultado: `{"plano_md":"vazio","fases":[]}`, LogPath: "l"}, nil
		}}),
	}
	if err := p.Planejar(ctx, dem.ID); err != nil {
		t.Fatalf("Planejar não deveria devolver erro de infra: %v", err)
	}
	got, _ := d.ObterDemanda(ctx, dem.ID)
	if got.Status != db.StatusDemandaFalhou {
		t.Fatalf("status = %q, quero falhou (sem fases)", got.Status)
	}
}

func TestPlanejadorDependenciaInexistenteMarcaFalhou(t *testing.T) {
	d := abrirDB(t)
	ctx := context.Background()
	dem := demandaPlanejando(t, d, "PRD.")

	p := &Planejador{
		Store: d, Motor: "claude",
		Prompt: func(context.Context, string) (string, error) { return "x {PRD} {QA}", nil },
		Selecionar: seletorStub(stubMotor{nome: "claude", fn: func(motor.OpcoesRun) (*motor.ResultadoRun, error) {
			return &motor.ResultadoRun{Resultado: `{"plano_md":"p","fases":[{"codigo":"1","titulo":"x","depende_de":["9"]}]}`}, nil
		}}),
	}
	if err := p.Planejar(ctx, dem.ID); err != nil {
		t.Fatalf("Planejar: %v", err)
	}
	got, _ := d.ObterDemanda(ctx, dem.ID)
	if got.Status != db.StatusDemandaFalhou {
		t.Fatalf("status = %q, quero falhou (dependência dangling)", got.Status)
	}
}

func TestPlanejadorJSONInvalidoMarcaFalhou(t *testing.T) {
	d := abrirDB(t)
	ctx := context.Background()
	dem := demandaPlanejando(t, d, "PRD.")

	p := &Planejador{
		Store: d, Motor: "claude",
		Prompt: func(context.Context, string) (string, error) { return "x {PRD} {QA}", nil },
		Selecionar: seletorStub(stubMotor{nome: "claude", fn: func(motor.OpcoesRun) (*motor.ResultadoRun, error) {
			return &motor.ResultadoRun{Resultado: "isto não é JSON", LogPath: "l"}, nil
		}}),
	}
	if err := p.Planejar(ctx, dem.ID); err != nil {
		t.Fatalf("Planejar: %v", err)
	}
	got, _ := d.ObterDemanda(ctx, dem.ID)
	if got.Status != db.StatusDemandaFalhou || got.Erro == "" {
		t.Fatalf("status = %q, erro = %q; quero falhou com motivo", got.Status, got.Erro)
	}
}

func TestPlanejadorRecusaStatusInvalido(t *testing.T) {
	d := abrirDB(t)
	ctx := context.Background()
	dem := demandaComPRD(t, d, "PRD.") // fica em recebida (não planejando)

	p := &Planejador{Store: d, Motor: "claude",
		Prompt:     func(context.Context, string) (string, error) { return "x", nil },
		Selecionar: seletorStub(stubMotor{nome: "claude", fn: func(motor.OpcoesRun) (*motor.ResultadoRun, error) { return &motor.ResultadoRun{}, nil }}),
	}
	if err := p.Planejar(ctx, dem.ID); err == nil {
		t.Fatal("planejamento de demanda em status inválido deveria falhar")
	}
}
