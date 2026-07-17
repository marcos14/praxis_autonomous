package intake

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marcos14/praxis-autonomous/internal/db"
	"github.com/marcos14/praxis-autonomous/internal/motor"
)

// stubMotor é um Motor programável para os testes (não chama nenhum CLI).
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

func seletorStub(m stubMotor) func(string) (motor.Motor, error) {
	return func(nome string) (motor.Motor, error) {
		if strings.EqualFold(nome, m.nome) || nome == "" {
			return m, nil
		}
		return nil, errors.New("motor desconhecido nos testes: " + nome)
	}
}

func abrirDB(t *testing.T) *db.DB {
	t.Helper()
	d, err := db.Abrir(filepath.Join(t.TempDir(), "praxis.db"))
	if err != nil {
		t.Fatalf("abrir db: %v", err)
	}
	t.Cleanup(func() { _ = d.Fechar() })
	return d
}

// demandaComPRD cria um projeto e uma demanda (recebida) com o PRD no chat.
func demandaComPRD(t *testing.T, d *db.DB, prd string) db.Demanda {
	t.Helper()
	ctx := context.Background()
	proj, err := d.CriarProjeto(ctx, db.Projeto{
		Nome: "ERP", Slug: "erp", Pasta: t.TempDir(), BranchPrincipal: "main",
		ModoIntegracao: "merge_request", Ativo: true,
	})
	if err != nil {
		t.Fatalf("criar projeto: %v", err)
	}
	dem, _, err := d.CriarDemandaComChat(ctx,
		db.Demanda{ProjectID: proj.ID, Titulo: "Split de pagamento", Status: db.StatusDemandaRecebida},
		db.MensagemChat{Papel: db.PapelUser, Conteudo: prd})
	if err != nil {
		t.Fatalf("criar demanda com chat: %v", err)
	}
	return dem
}

func TestAnalistaGeraPerguntasEMudaStatus(t *testing.T) {
	d := abrirDB(t)
	ctx := context.Background()
	dem := demandaComPRD(t, d, "Permitir split de recebimento entre filiais.")

	saida := `{"resumo":"Hoje o recebimento é 100% na filial emissora (financeiro/baixa.go).",
		"arquivos_provaveis":["financeiro/baixa.go","financeiro/titulo.go"],
		"perguntas":[
		  {"pergunta":"Rateio por percentual ou item?","contexto":"título único por nota","tipo":"escolha","opcoes":["percentual","item"],"sugestao":"percentual","impacto":"alto"},
		  {"pergunta":"Split na emissão ou na baixa?","tipo":"escolha","opcoes":["emissão","baixa"],"sugestao":"baixa","impacto":"alto"}
		]}`

	var promptRecebido string
	var leituraSomente bool
	a := &Analista{
		Store:  d,
		Motor:  "claude",
		Modelo: "sonnet",
		Prompt: func(_ context.Context, nome string) (string, error) {
			if nome != PromptAnalista {
				t.Fatalf("prompt pedido = %q, quero %q", nome, PromptAnalista)
			}
			return "analise: {PRD}", nil
		},
		Selecionar: seletorStub(stubMotor{nome: "claude", caps: motor.Capacidades{SchemaNativo: true},
			fn: func(op motor.OpcoesRun) (*motor.ResultadoRun, error) {
				promptRecebido = op.Prompt
				leituraSomente = op.SomenteLeitura
				return &motor.ResultadoRun{Estruturado: json.RawMessage(saida), CustoUSD: 0.41, LogPath: "analista.jsonl"}, nil
			}}),
	}

	if err := a.Analisar(ctx, dem.ID); err != nil {
		t.Fatalf("Analisar: %v", err)
	}

	if !strings.Contains(promptRecebido, "Permitir split") {
		t.Fatalf("PRD não foi injetado no prompt: %q", promptRecebido)
	}
	if !leituraSomente {
		t.Fatal("o analista deve rodar em modo SomenteLeitura")
	}

	got, err := d.ObterDemanda(ctx, dem.ID)
	if err != nil {
		t.Fatalf("ObterDemanda: %v", err)
	}
	if got.Status != db.StatusDemandaAguardandoRespostas {
		t.Fatalf("status = %q, quero %q", got.Status, db.StatusDemandaAguardandoRespostas)
	}

	perguntas, err := d.ListarPerguntas(ctx, dem.ID)
	if err != nil {
		t.Fatalf("ListarPerguntas: %v", err)
	}
	if len(perguntas) != 2 {
		t.Fatalf("len perguntas = %d, quero 2", len(perguntas))
	}
	if perguntas[0].Impacto != "alto" || perguntas[0].Sugestao != "percentual" {
		t.Fatalf("pergunta 1 mal persistida: %+v", perguntas[0])
	}
	if len(perguntas[0].Opcoes) != 2 {
		t.Fatalf("opcoes da pergunta 1: %+v", perguntas[0].Opcoes)
	}

	// fala do analista no chat, com o resumo e meta (arquivos_provaveis/custo).
	msgs, _ := d.ListarMensagensChat(ctx, dem.ID)
	var fala *db.MensagemChat
	for i := range msgs {
		if msgs[i].Papel == db.PapelAnalista {
			fala = &msgs[i]
		}
	}
	if fala == nil {
		t.Fatal("nenhuma fala do analista no chat")
	}
	if !strings.Contains(fala.Conteudo, "recebimento") {
		t.Fatalf("resumo não está na fala: %q", fala.Conteudo)
	}
	var meta struct {
		Arquivos []string `json:"arquivos_provaveis"`
		Custo    float64  `json:"custo_usd"`
	}
	if err := json.Unmarshal(fala.Meta, &meta); err != nil {
		t.Fatalf("meta inválida: %v (%s)", err, fala.Meta)
	}
	if len(meta.Arquivos) != 2 || meta.Custo != 0.41 {
		t.Fatalf("meta mal preenchida: %+v", meta)
	}

	// execução registrada (operacao=analista).
	execs, _ := d.ListarExecucoes(ctx, dem.ID)
	if len(execs) != 1 || execs[0].Operacao != db.OperacaoAnalista {
		t.Fatalf("execução do analista não registrada: %+v", execs)
	}

	// eventos de início e conclusão.
	evs, _ := d.ListarEventos(ctx, db.FiltroEventos{DemandID: &dem.ID})
	if !contemEvento(evs, "analise_iniciada") || !contemEvento(evs, "analise_concluida") {
		t.Fatalf("faltam eventos de análise: %+v", evs)
	}
}

func TestAnalistaSemPerguntas(t *testing.T) {
	d := abrirDB(t)
	ctx := context.Background()
	dem := demandaComPRD(t, d, "Ajuste trivial de label.")

	a := &Analista{
		Store: d, Motor: "claude",
		Prompt: func(context.Context, string) (string, error) { return "x {PRD}", nil },
		Selecionar: seletorStub(stubMotor{nome: "claude", fn: func(motor.OpcoesRun) (*motor.ResultadoRun, error) {
			return &motor.ResultadoRun{Resultado: `{"resumo":"claro","perguntas":[]}`}, nil
		}}),
	}
	if err := a.Analisar(ctx, dem.ID); err != nil {
		t.Fatalf("Analisar: %v", err)
	}
	got, _ := d.ObterDemanda(ctx, dem.ID)
	if got.Status != db.StatusDemandaAguardandoRespostas {
		t.Fatalf("status = %q, quero aguardando_respostas mesmo sem perguntas", got.Status)
	}
	perguntas, _ := d.ListarPerguntas(ctx, dem.ID)
	if len(perguntas) != 0 {
		t.Fatalf("len perguntas = %d, quero 0", len(perguntas))
	}
}

func TestAnalistaJSONInvalidoMarcaFalhou(t *testing.T) {
	d := abrirDB(t)
	ctx := context.Background()
	dem := demandaComPRD(t, d, "PRD qualquer.")

	a := &Analista{
		Store: d, Motor: "claude",
		Prompt: func(context.Context, string) (string, error) { return "x {PRD}", nil },
		Selecionar: seletorStub(stubMotor{nome: "claude", fn: func(motor.OpcoesRun) (*motor.ResultadoRun, error) {
			return &motor.ResultadoRun{Resultado: "isto não é JSON", LogPath: "l.jsonl"}, nil
		}}),
	}
	// desfecho lógico (JSON inválido) não é erro de infraestrutura → nil.
	if err := a.Analisar(ctx, dem.ID); err != nil {
		t.Fatalf("Analisar deveria tratar JSON inválido sem erro de infra: %v", err)
	}
	got, _ := d.ObterDemanda(ctx, dem.ID)
	if got.Status != db.StatusDemandaFalhou {
		t.Fatalf("status = %q, quero falhou", got.Status)
	}
	if got.Erro == "" {
		t.Fatal("erro da demanda não preenchido")
	}
}

func TestAnalistaResultadoErroMarcaFalhou(t *testing.T) {
	d := abrirDB(t)
	ctx := context.Background()
	dem := demandaComPRD(t, d, "PRD.")
	a := &Analista{
		Store: d, Motor: "claude",
		Prompt: func(context.Context, string) (string, error) { return "x", nil },
		Selecionar: seletorStub(stubMotor{nome: "claude", fn: func(motor.OpcoesRun) (*motor.ResultadoRun, error) {
			return &motor.ResultadoRun{IsError: true, Subtipo: "limite", LogPath: "l"}, nil
		}}),
	}
	if err := a.Analisar(ctx, dem.ID); err != nil {
		t.Fatalf("Analisar: %v", err)
	}
	got, _ := d.ObterDemanda(ctx, dem.ID)
	if got.Status != db.StatusDemandaFalhou {
		t.Fatalf("status = %q, quero falhou", got.Status)
	}
}

func TestAnalistaRecusaStatusInvalido(t *testing.T) {
	d := abrirDB(t)
	ctx := context.Background()
	dem := demandaComPRD(t, d, "PRD.")
	// força um status não-analisável.
	dem.Status = db.StatusDemandaExecutando
	if _, err := d.AtualizarDemanda(ctx, dem); err != nil {
		t.Fatalf("AtualizarDemanda: %v", err)
	}
	a := &Analista{Store: d, Motor: "claude",
		Prompt:     func(context.Context, string) (string, error) { return "x", nil },
		Selecionar: seletorStub(stubMotor{nome: "claude", fn: func(motor.OpcoesRun) (*motor.ResultadoRun, error) { return &motor.ResultadoRun{}, nil }}),
	}
	if err := a.Analisar(ctx, dem.ID); err == nil {
		t.Fatal("análise de demanda em status inválido deveria falhar")
	}
}

func contemEvento(evs []db.Evento, tipo string) bool {
	for _, e := range evs {
		if e.Tipo == tipo {
			return true
		}
	}
	return false
}
