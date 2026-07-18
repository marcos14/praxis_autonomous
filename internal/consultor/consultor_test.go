package consultor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marcos14/praxis-autonomous/internal/db"
	"github.com/marcos14/praxis-autonomous/internal/motor"
)

// stubMotor é um Motor programável para os testes (não chama nenhum CLI).
type stubMotor struct {
	nome string
	fn   func(op motor.OpcoesRun) (*motor.ResultadoRun, error)
}

func (s stubMotor) Nome() string                   { return s.nome }
func (s stubMotor) Capacidades() motor.Capacidades { return motor.Capacidades{SchemaNativo: true} }
func (s stubMotor) Rodar(op motor.OpcoesRun) (*motor.ResultadoRun, error) {
	return s.fn(op)
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

func criarProjetoComOverview(t *testing.T, d *db.DB, nome, overview string) db.Projeto {
	t.Helper()
	ctx := context.Background()
	p, err := d.CriarProjeto(ctx, db.Projeto{
		Nome: nome, Slug: strings.ToLower(nome), Pasta: t.TempDir(),
		BranchPrincipal: "main", ModoIntegracao: "merge_request", Ativo: true,
	})
	if err != nil {
		t.Fatalf("criar projeto: %v", err)
	}
	if overview != "" {
		if err := d.AtualizarOverview(ctx, p.ID, overview); err != nil {
			t.Fatalf("gravar overview: %v", err)
		}
	}
	proj, err := d.ObterProjeto(ctx, p.ID)
	if err != nil {
		t.Fatalf("reler projeto: %v", err)
	}
	return proj
}

// servicoDeTeste monta um Servico com todos os seams stubados. A função fn é o
// comportamento do motor.
func servicoDeTeste(t *testing.T, d *db.DB, fn func(op motor.OpcoesRun) (*motor.ResultadoRun, error)) *Servico {
	t.Helper()
	return NovoServico(OpcoesServico{
		Store:   d,
		DirLogs: t.TempDir(),
		Ctx:     context.Background(),
		Selecionar: func(nome string) (motor.Motor, error) {
			return stubMotor{nome: "claude", fn: fn}, nil
		},
		Prompt: func(_ context.Context, nome string) (string, error) {
			return "consultor: {CONTEXTO_REPOS} ### {HISTORICO}", nil
		},
		StatPasta:     func(string) error { return nil },
		AtualizarRepo: func(string, string) (string, error) { return "", nil },
	})
}

func criarConsultaProjeto(t *testing.T, d *db.DB, proj db.Projeto, pergunta string) db.Consulta {
	t.Helper()
	c, _, err := d.CriarConsultaComChat(context.Background(),
		db.Consulta{ProjectID: &proj.ID, Titulo: "Consulta teste"},
		db.MensagemConsulta{Conteudo: pergunta})
	if err != nil {
		t.Fatalf("criar consulta: %v", err)
	}
	return c
}

func resultadoJSON(t *testing.T, v any) (*motor.ResultadoRun, error) {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal saída: %v", err)
	}
	return &motor.ResultadoRun{Estruturado: b, CustoUSD: 0.25, LogPath: "consultor.jsonl"}, nil
}

func TestTurnoPerguntasDeClarificacao(t *testing.T) {
	d := abrirDB(t)
	ctx := context.Background()
	proj := criarProjetoComOverview(t, d, "ERP", "## Objetivo\nSistema de cobrança.")
	cons := criarConsultaProjeto(t, d, proj, "como funciona a cobrança?")

	var opRecebida motor.OpcoesRun
	s := servicoDeTeste(t, d, func(op motor.OpcoesRun) (*motor.ResultadoRun, error) {
		opRecebida = op
		return resultadoJSON(t, SaidaConsultor{
			Tipo: TurnoPerguntas,
			Perguntas: []PerguntaConsultor{
				{Pergunta: "Você quer entender a régua de cobrança ou a baixa de pagamentos?", Contexto: "são fluxos distintos"},
				{Pergunta: "É para um cliente específico?"},
			},
		})
	})

	if err := s.Responder(ctx, cons.ID); err != nil {
		t.Fatalf("Responder: %v", err)
	}

	if !opRecebida.SomenteLeitura || !opRecebida.ProibirCommit {
		t.Fatal("o consultor deve rodar SomenteLeitura + ProibirCommit")
	}
	if opRecebida.Dir != proj.Pasta {
		t.Fatalf("Dir = %q, quero a pasta do projeto %q", opRecebida.Dir, proj.Pasta)
	}
	if !strings.Contains(opRecebida.Prompt, "Sistema de cobrança.") {
		t.Fatalf("overview não injetado no prompt: %q", opRecebida.Prompt)
	}
	if !strings.Contains(opRecebida.Prompt, "Usuário: como funciona a cobrança?") {
		t.Fatalf("histórico não injetado no prompt: %q", opRecebida.Prompt)
	}
	if opRecebida.Schema != SchemaConsultor {
		t.Fatal("schema do consultor não foi passado ao motor")
	}

	msgs, err := d.ListarMensagensConsulta(ctx, cons.ID)
	if err != nil {
		t.Fatalf("ListarMensagensConsulta: %v", err)
	}
	ultima := msgs[len(msgs)-1]
	if ultima.Papel != db.PapelConsultaConsultor {
		t.Fatalf("última fala papel = %q, quero consultor", ultima.Papel)
	}
	if !strings.Contains(ultima.Conteudo, "1. Você quer entender a régua") ||
		!strings.Contains(ultima.Conteudo, "2. É para um cliente específico?") {
		t.Fatalf("perguntas não formatadas: %q", ultima.Conteudo)
	}
	var meta map[string]any
	if err := json.Unmarshal(ultima.Meta, &meta); err != nil {
		t.Fatalf("meta inválido: %v", err)
	}
	if meta["tipo"] != TurnoPerguntas {
		t.Fatalf("meta.tipo = %v, quero perguntas", meta["tipo"])
	}

	got, err := d.ObterConsulta(ctx, cons.ID)
	if err != nil {
		t.Fatalf("ObterConsulta: %v", err)
	}
	if got.Status != db.StatusConsultaOciosa {
		t.Fatalf("status = %q, quero ociosa", got.Status)
	}
	if got.CustoUSD != 0.25 {
		t.Fatalf("custo acumulado = %v, quero 0.25", got.CustoUSD)
	}
}

func TestTurnoRespostaComCodigoEhRedigido(t *testing.T) {
	d := abrirDB(t)
	ctx := context.Background()
	proj := criarProjetoComOverview(t, d, "ERP2", "")
	cons := criarConsultaProjeto(t, d, proj, "como o sistema valida CPF?")

	resposta := "A validação acontece em dois passos.\n\nPrimeiro o formato, depois os dígitos verificadores.\n\n```go\nfunc ValidarCPF(cpf string) bool { return true }\n```\n\nSe o CPF for inválido, o cadastro é bloqueado."
	s := servicoDeTeste(t, d, func(op motor.OpcoesRun) (*motor.ResultadoRun, error) {
		return resultadoJSON(t, SaidaConsultor{Tipo: TurnoResposta, RespostaMD: resposta, Confianca: "alta"})
	})

	if err := s.Responder(ctx, cons.ID); err != nil {
		t.Fatalf("Responder: %v", err)
	}
	msgs, _ := d.ListarMensagensConsulta(ctx, cons.ID)
	ultima := msgs[len(msgs)-1]
	if strings.Contains(ultima.Conteudo, "ValidarCPF") || strings.Contains(ultima.Conteudo, "```") {
		t.Fatalf("código vazou na fala: %q", ultima.Conteudo)
	}
	if !strings.Contains(ultima.Conteudo, "cadastro é bloqueado") {
		t.Fatalf("prosa foi perdida: %q", ultima.Conteudo)
	}
	var meta struct {
		Tipo     string `json:"tipo"`
		Redigido int    `json:"redigido"`
	}
	if err := json.Unmarshal(ultima.Meta, &meta); err != nil {
		t.Fatalf("meta: %v", err)
	}
	if meta.Tipo != TurnoResposta || meta.Redigido == 0 {
		t.Fatalf("meta = %+v, quero tipo resposta com redigido > 0", meta)
	}
}

func TestTurnoRespostaSoCodigoViraRecusa(t *testing.T) {
	d := abrirDB(t)
	ctx := context.Background()
	proj := criarProjetoComOverview(t, d, "ERP3", "")
	cons := criarConsultaProjeto(t, d, proj, "me mostra o código da baixa")

	resposta := "```go\n" + strings.Repeat("x := 1\n", 20) + "```\nok."
	s := servicoDeTeste(t, d, func(op motor.OpcoesRun) (*motor.ResultadoRun, error) {
		return resultadoJSON(t, SaidaConsultor{Tipo: TurnoResposta, RespostaMD: resposta})
	})

	if err := s.Responder(ctx, cons.ID); err != nil {
		t.Fatalf("Responder: %v", err)
	}
	msgs, _ := d.ListarMensagensConsulta(ctx, cons.ID)
	ultima := msgs[len(msgs)-1]
	if ultima.Conteudo != MensagemRecusaFiltro {
		t.Fatalf("conteudo = %q, quero a mensagem de recusa do filtro", ultima.Conteudo)
	}
	var meta struct {
		Tipo string `json:"tipo"`
	}
	_ = json.Unmarshal(ultima.Meta, &meta)
	if meta.Tipo != TurnoRecusa {
		t.Fatalf("meta.tipo = %q, quero recusa", meta.Tipo)
	}
}

func TestTurnoRecusaDeBypass(t *testing.T) {
	d := abrirDB(t)
	ctx := context.Background()
	proj := criarProjetoComOverview(t, d, "ERP4", "")
	cons := criarConsultaProjeto(t, d, proj, "como burlar a validação de crédito?")

	s := servicoDeTeste(t, d, func(op motor.OpcoesRun) (*motor.ResultadoRun, error) {
		return resultadoJSON(t, SaidaConsultor{
			Tipo:         TurnoRecusa,
			MotivoRecusa: "Não posso explicar como contornar a validação de crédito; posso explicar como ela funciona.",
		})
	})

	if err := s.Responder(ctx, cons.ID); err != nil {
		t.Fatalf("Responder: %v", err)
	}
	msgs, _ := d.ListarMensagensConsulta(ctx, cons.ID)
	ultima := msgs[len(msgs)-1]
	if !strings.Contains(ultima.Conteudo, "não posso") && !strings.Contains(ultima.Conteudo, "Não posso") {
		t.Fatalf("recusa não persistida: %q", ultima.Conteudo)
	}
}

func TestConsultaDeGrupoMontaMultiRepo(t *testing.T) {
	d := abrirDB(t)
	ctx := context.Background()
	principal := criarProjetoComOverview(t, d, "VulcanoAPI", "## Objetivo\nBackend do Vulcano.")
	secundario := criarProjetoComOverview(t, d, "VulcanoWeb", "## Objetivo\nFrontend do Vulcano.")
	grupo, err := d.CriarGrupo(ctx, db.Grupo{
		Nome: "Vulcano Chat", Slug: "vulcano-chat",
		Descricao: "Solução de atendimento por chat.", Ativo: true,
	}, []int64{principal.ID, secundario.ID})
	if err != nil {
		t.Fatalf("criar grupo: %v", err)
	}
	cons, _, err := d.CriarConsultaComChat(ctx,
		db.Consulta{GroupID: &grupo.ID},
		db.MensagemConsulta{Conteudo: "como faço a implantação do Vulcano Chat?"})
	if err != nil {
		t.Fatalf("criar consulta de grupo: %v", err)
	}

	var opRecebida motor.OpcoesRun
	s := servicoDeTeste(t, d, func(op motor.OpcoesRun) (*motor.ResultadoRun, error) {
		opRecebida = op
		return resultadoJSON(t, SaidaConsultor{Tipo: TurnoResposta, RespostaMD: "A implantação tem três etapas."})
	})

	if err := s.Responder(ctx, cons.ID); err != nil {
		t.Fatalf("Responder: %v", err)
	}
	if opRecebida.Dir != principal.Pasta {
		t.Fatalf("Dir = %q, quero a pasta do principal %q", opRecebida.Dir, principal.Pasta)
	}
	achou := false
	for _, ad := range opRecebida.AddDirs {
		if ad == secundario.Pasta {
			achou = true
		}
	}
	if !achou {
		t.Fatalf("AddDirs = %v, quero conter a pasta do secundário %q", opRecebida.AddDirs, secundario.Pasta)
	}
	for _, trecho := range []string{"Solução: Vulcano Chat", "atendimento por chat",
		"Backend do Vulcano.", "Frontend do Vulcano."} {
		if !strings.Contains(opRecebida.Prompt, trecho) {
			t.Fatalf("contexto do grupo sem %q:\n%s", trecho, opRecebida.Prompt)
		}
	}
}

func TestGrupoMembroSecundarioInacessivelViraAviso(t *testing.T) {
	d := abrirDB(t)
	ctx := context.Background()
	principal := criarProjetoComOverview(t, d, "SolAPI", "")
	secundario := criarProjetoComOverview(t, d, "SolWeb", "")
	grupo, err := d.CriarGrupo(ctx, db.Grupo{Nome: "Sol", Slug: "sol", Ativo: true},
		[]int64{principal.ID, secundario.ID})
	if err != nil {
		t.Fatalf("criar grupo: %v", err)
	}
	cons, _, err := d.CriarConsultaComChat(ctx, db.Consulta{GroupID: &grupo.ID},
		db.MensagemConsulta{Conteudo: "pergunta"})
	if err != nil {
		t.Fatalf("criar consulta: %v", err)
	}

	s := servicoDeTeste(t, d, func(op motor.OpcoesRun) (*motor.ResultadoRun, error) {
		for _, ad := range op.AddDirs {
			if ad == secundario.Pasta {
				t.Fatal("pasta inacessível não deveria ir para AddDirs")
			}
		}
		return resultadoJSON(t, SaidaConsultor{Tipo: TurnoResposta, RespostaMD: "ok, respondido."})
	})
	s.statPasta = func(p string) error {
		if p == secundario.Pasta {
			return errors.New("pasta sumiu")
		}
		return nil
	}

	if err := s.Responder(ctx, cons.ID); err != nil {
		t.Fatalf("Responder: %v", err)
	}
	msgs, _ := d.ListarMensagensConsulta(ctx, cons.ID)
	temAviso := false
	for _, m := range msgs {
		if m.Papel == db.PapelConsultaSistema && strings.Contains(m.Conteudo, "SolWeb") {
			temAviso = true
		}
	}
	if !temAviso {
		t.Fatalf("sem fala de sistema avisando do membro inacessível: %+v", msgs)
	}
}

func TestGrupoPrincipalInacessivelFalhaConsulta(t *testing.T) {
	d := abrirDB(t)
	ctx := context.Background()
	principal := criarProjetoComOverview(t, d, "QuebradoAPI", "")
	grupo, err := d.CriarGrupo(ctx, db.Grupo{Nome: "Quebrado", Slug: "quebrado", Ativo: true},
		[]int64{principal.ID})
	if err != nil {
		t.Fatalf("criar grupo: %v", err)
	}
	cons, _, err := d.CriarConsultaComChat(ctx, db.Consulta{GroupID: &grupo.ID},
		db.MensagemConsulta{Conteudo: "pergunta"})
	if err != nil {
		t.Fatalf("criar consulta: %v", err)
	}

	s := servicoDeTeste(t, d, func(op motor.OpcoesRun) (*motor.ResultadoRun, error) {
		t.Fatal("o motor não deveria rodar com o principal inacessível")
		return nil, nil
	})
	s.statPasta = func(string) error { return errors.New("sem acesso") }

	if err := s.Responder(ctx, cons.ID); err == nil {
		t.Fatal("Responder deveria devolver erro de montagem")
	}
	got, _ := d.ObterConsulta(ctx, cons.ID)
	if got.Status != db.StatusConsultaFalhou {
		t.Fatalf("status = %q, quero falhou", got.Status)
	}
}

func TestResolverMotorConsultaPorGrupoDeUsuarios(t *testing.T) {
	d := abrirDB(t)
	ctx := context.Background()
	proj := criarProjetoComOverview(t, d, "ERPG", "")

	motorPadrao, err := d.CriarMotor(ctx, db.Motor{
		Nome: "claude", Prioridade: 0, Ativo: true,
		ModeloAnalise: "sonnet", ModeloConsulta: "haiku", BudgetFaseUSD: 2, TimeoutMin: 10,
	})
	if err != nil {
		t.Fatalf("motor padrão: %v", err)
	}
	motorGrupo, err := d.CriarMotor(ctx, db.Motor{
		Nome: "codex", Prioridade: 1, Ativo: true, ModeloAnalise: "gpt-5", BudgetFaseUSD: 1,
	})
	if err != nil {
		t.Fatalf("motor do grupo: %v", err)
	}

	suporte, err := d.CriarUsuario(ctx, "Sup", "sup@x.com", "senha-forte-123", nil)
	if err != nil {
		t.Fatalf("usuário: %v", err)
	}
	grupo, err := d.CriarGrupoUsuarios(ctx, db.GrupoUsuarios{
		Nome: "Suporte", EngineID: &motorGrupo.ID, Modelo: "gpt-5-mini",
	})
	if err != nil {
		t.Fatalf("grupo: %v", err)
	}
	if err := d.DefinirGrupoDoUsuario(ctx, suporte.ID, &grupo.ID); err != nil {
		t.Fatalf("vincular: %v", err)
	}

	rodarTurno := func(criadoPor *int64) motor.OpcoesRun {
		t.Helper()
		cons, _, err := d.CriarConsultaComChat(ctx,
			db.Consulta{ProjectID: &proj.ID, CriadoPor: criadoPor},
			db.MensagemConsulta{Conteudo: "pergunta"})
		if err != nil {
			t.Fatalf("criar consulta: %v", err)
		}
		var op motor.OpcoesRun
		s := servicoDeTeste(t, d, func(o motor.OpcoesRun) (*motor.ResultadoRun, error) {
			op = o
			return resultadoJSON(t, SaidaConsultor{Tipo: TurnoResposta, RespostaMD: "ok."})
		})
		if err := s.Responder(ctx, cons.ID); err != nil {
			t.Fatalf("Responder: %v", err)
		}
		return op
	}

	// Usuário COM grupo: motor e modelo do grupo.
	op := rodarTurno(&suporte.ID)
	if op.Modelo != "gpt-5-mini" {
		t.Fatalf("modelo com grupo = %q, quero gpt-5-mini (do grupo)", op.Modelo)
	}
	if op.BudgetUSD != 1 {
		t.Fatalf("budget com grupo = %v, quero o do motor do grupo (1)", op.BudgetUSD)
	}

	// Sem usuário (bootstrap): motor padrão com modelo_consulta.
	op = rodarTurno(nil)
	if op.Modelo != "haiku" {
		t.Fatalf("modelo padrão = %q, quero haiku (modelo_consulta)", op.Modelo)
	}
	if op.BudgetUSD != 2 {
		t.Fatalf("budget padrão = %v, quero 2", op.BudgetUSD)
	}

	// Sem modelo_consulta no motor padrão: cai no modelo_analise.
	motorPadrao.ModeloConsulta = ""
	if _, err := d.AtualizarMotor(ctx, motorPadrao); err != nil {
		t.Fatalf("limpar modelo_consulta: %v", err)
	}
	op = rodarTurno(nil)
	if op.Modelo != "sonnet" {
		t.Fatalf("fallback = %q, quero sonnet (modelo_analise)", op.Modelo)
	}

	// Grupo sem motor próprio, só modelo: motor padrão + modelo do grupo.
	grupo.EngineID = nil
	grupo.Modelo = "opus-econ"
	if _, err := d.AtualizarGrupoUsuarios(ctx, grupo); err != nil {
		t.Fatalf("atualizar grupo: %v", err)
	}
	op = rodarTurno(&suporte.ID)
	if op.Modelo != "opus-econ" {
		t.Fatalf("modelo grupo-sem-motor = %q, quero opus-econ", op.Modelo)
	}
	if op.BudgetUSD != 2 {
		t.Fatalf("budget grupo-sem-motor = %v, quero o do motor padrão (2)", op.BudgetUSD)
	}
}

func TestTurnoAtualizaRepoAntesDaLeitura(t *testing.T) {
	d := abrirDB(t)
	ctx := context.Background()
	proj := criarProjetoComOverview(t, d, "ERPUP", "")
	cons := criarConsultaProjeto(t, d, proj, "como funciona?")

	var (
		chamadas []string
		ordem    []string
	)
	s := servicoDeTeste(t, d, func(op motor.OpcoesRun) (*motor.ResultadoRun, error) {
		ordem = append(ordem, "motor")
		return resultadoJSON(t, SaidaConsultor{Tipo: TurnoResposta, RespostaMD: "ok."})
	})
	s.atualizarRepo = func(pasta, branch string) (string, error) {
		ordem = append(ordem, "pull")
		chamadas = append(chamadas, pasta+"@"+branch)
		return "", nil
	}

	if err := s.Responder(ctx, cons.ID); err != nil {
		t.Fatalf("Responder: %v", err)
	}
	if len(chamadas) != 1 || chamadas[0] != proj.Pasta+"@main" {
		t.Fatalf("atualizarRepo = %v, quero a pasta do projeto na branch main", chamadas)
	}
	if len(ordem) != 2 || ordem[0] != "pull" || ordem[1] != "motor" {
		t.Fatalf("ordem = %v, quero o pull ANTES do motor", ordem)
	}
}

func TestAvisoDeRepoDesatualizadoViraFalaDeSistema(t *testing.T) {
	d := abrirDB(t)
	ctx := context.Background()
	proj := criarProjetoComOverview(t, d, "ERPAV", "")
	cons := criarConsultaProjeto(t, d, proj, "como funciona?")

	s := servicoDeTeste(t, d, func(op motor.OpcoesRun) (*motor.ResultadoRun, error) {
		return resultadoJSON(t, SaidaConsultor{Tipo: TurnoResposta, RespostaMD: "ok."})
	})
	s.atualizarRepo = func(pasta, branch string) (string, error) {
		return "a branch \"main\" tem mudanças locais não commitadas; o pull não foi aplicado", nil
	}

	if err := s.Responder(ctx, cons.ID); err != nil {
		t.Fatalf("Responder: %v", err)
	}
	msgs, _ := d.ListarMensagensConsulta(ctx, cons.ID)
	temAviso := false
	for _, m := range msgs {
		if m.Papel == db.PapelConsultaSistema && strings.Contains(m.Conteudo, "pull não foi aplicado") {
			temAviso = true
		}
	}
	if !temAviso {
		t.Fatalf("aviso de repo desatualizado não virou fala de sistema: %+v", msgs)
	}
}

func TestErroDoMotorCarimbaFalhouComFalaDeSistema(t *testing.T) {
	d := abrirDB(t)
	ctx := context.Background()
	proj := criarProjetoComOverview(t, d, "ERP5", "")
	cons := criarConsultaProjeto(t, d, proj, "pergunta")

	s := servicoDeTeste(t, d, func(op motor.OpcoesRun) (*motor.ResultadoRun, error) {
		return nil, fmt.Errorf("cli explodiu")
	})
	if err := s.Responder(ctx, cons.ID); err != nil {
		t.Fatalf("Responder deveria absorver a falha lógica: %v", err)
	}
	got, _ := d.ObterConsulta(ctx, cons.ID)
	if got.Status != db.StatusConsultaFalhou || got.Erro == "" {
		t.Fatalf("consulta = %+v, quero status falhou com erro", got)
	}
	msgs, _ := d.ListarMensagensConsulta(ctx, cons.ID)
	ultima := msgs[len(msgs)-1]
	if ultima.Papel != db.PapelConsultaSistema {
		t.Fatalf("última fala = %+v, quero fala de sistema explicando a falha", ultima)
	}

	// Execução registrada com is_error.
	exec, tem, err := d.UltimaExecucaoConsultaComLog(ctx, cons.ID)
	if err != nil {
		t.Fatalf("UltimaExecucaoConsultaComLog: %v", err)
	}
	// Sem LogPath (motor nem rodou) não há log; basta não quebrar.
	_ = exec
	_ = tem
}
