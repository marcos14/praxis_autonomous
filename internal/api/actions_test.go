package api

import (
	"context"
	"net/http"
	"strconv"
	"testing"

	"github.com/marcos14/praxis-autonomous/internal/db"
)

// ctlFake registra as chamadas de Interromper (ação cancelar/pausar) e de
// Reenfileirar (ação tentar_novamente).
type ctlFake struct {
	interrompidas  []int64
	reenfileiradas []int64
	rodando        map[int64]bool
}

func (c *ctlFake) Interromper(id int64) bool {
	c.interrompidas = append(c.interrompidas, id)
	return c.rodando[id]
}

func (c *ctlFake) Reenfileirar(id int64) {
	c.reenfileiradas = append(c.reenfileiradas, id)
}

func TestAcaoPausarRetomarCancelar(t *testing.T) {
	ctl := &ctlFake{rodando: map[int64]bool{}}
	srv := Novo(Opcoes{Banco: abrirBancoTemp(t), Exec: ctl})
	proj := criarProjetoTeste(t, srv)
	dem := criarDemandaTeste(t, srv, proj)
	ctl.rodando[dem.ID] = true // simula demanda em execução

	// pausar (pronta → pausada).
	rec := fazerReq(t, srv, http.MethodPost, actionsURL(dem.ID), map[string]any{"acao": "pausar"})
	if rec.Code != http.StatusOK {
		t.Fatalf("pausar: status %d (corpo=%q)", rec.Code, rec.Body.String())
	}
	if got := decodDemanda(t, rec); got.Status != db.StatusDemandaPausada {
		t.Fatalf("pausar: status = %q, quero pausada", got.Status)
	}
	if len(ctl.interrompidas) != 1 || ctl.interrompidas[0] != dem.ID {
		t.Fatalf("pausar deveria chamar Interromper(%d): %v", dem.ID, ctl.interrompidas)
	}

	// retomar (pausada → pronta).
	rec = fazerReq(t, srv, http.MethodPost, actionsURL(dem.ID), map[string]any{"acao": "retomar"})
	if rec.Code != http.StatusOK {
		t.Fatalf("retomar: status %d", rec.Code)
	}
	if got := decodDemanda(t, rec); got.Status != db.StatusDemandaPronta {
		t.Fatalf("retomar: status = %q, quero pronta", got.Status)
	}

	// cancelar (pronta → cancelada) + Interromper.
	rec = fazerReq(t, srv, http.MethodPost, actionsURL(dem.ID), map[string]any{"acao": "cancelar"})
	if rec.Code != http.StatusOK {
		t.Fatalf("cancelar: status %d", rec.Code)
	}
	if got := decodDemanda(t, rec); got.Status != db.StatusDemandaCancelada {
		t.Fatalf("cancelar: status = %q, quero cancelada", got.Status)
	}
	if len(ctl.interrompidas) != 2 {
		t.Fatalf("cancelar deveria chamar Interromper de novo: %v", ctl.interrompidas)
	}

	// eventos registrados (pausada/retomada/cancelada).
	evs, err := srv.banco.ListarEventos(context.Background(), db.FiltroEventos{DemandID: &dem.ID})
	if err != nil {
		t.Fatal(err)
	}
	tipos := map[string]bool{}
	for _, e := range evs {
		tipos[e.Tipo] = true
	}
	for _, esperado := range []string{"demanda_pausada", "demanda_retomada", "demanda_cancelada"} {
		if !tipos[esperado] {
			t.Fatalf("evento %q não registrado (vistos: %v)", esperado, tipos)
		}
	}
}

func TestAcaoTransicaoInvalida(t *testing.T) {
	srv := Novo(Opcoes{Banco: abrirBancoTemp(t)})
	proj := criarProjetoTeste(t, srv)
	dem := criarDemandaTeste(t, srv, proj)

	// cancelar → cancelada; cancelar de novo é no-op idempotente (200).
	if rec := fazerReq(t, srv, http.MethodPost, actionsURL(dem.ID), map[string]any{"acao": "cancelar"}); rec.Code != http.StatusOK {
		t.Fatalf("cancelar: status %d", rec.Code)
	}
	if rec := fazerReq(t, srv, http.MethodPost, actionsURL(dem.ID), map[string]any{"acao": "cancelar"}); rec.Code != http.StatusOK {
		t.Fatalf("cancelar idempotente: status %d, quero 200", rec.Code)
	}
	// retomar uma demanda cancelada (terminal) é inválido (409).
	if rec := fazerReq(t, srv, http.MethodPost, actionsURL(dem.ID), map[string]any{"acao": "retomar"}); rec.Code != http.StatusConflict {
		t.Fatalf("retomar cancelada: status %d, quero 409 (corpo=%q)", rec.Code, rec.Body.String())
	}
	// pausar uma demanda cancelada (terminal) é inválido (409).
	if rec := fazerReq(t, srv, http.MethodPost, actionsURL(dem.ID), map[string]any{"acao": "pausar"}); rec.Code != http.StatusConflict {
		t.Fatalf("pausar cancelada: status %d, quero 409", rec.Code)
	}
}

func TestAcaoDesconhecidaEInexistente(t *testing.T) {
	srv := Novo(Opcoes{Banco: abrirBancoTemp(t)})
	proj := criarProjetoTeste(t, srv)
	dem := criarDemandaTeste(t, srv, proj)

	if rec := fazerReq(t, srv, http.MethodPost, actionsURL(dem.ID), map[string]any{"acao": "explodir"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("ação desconhecida: status %d, quero 400", rec.Code)
	}
	if rec := fazerReq(t, srv, http.MethodPost, "/api/v1/demands/999999/actions", map[string]any{"acao": "pausar"}); rec.Code != http.StatusNotFound {
		t.Fatalf("demanda inexistente: status %d, quero 404", rec.Code)
	}
}

// TestAcaoSemControlador: sem Exec configurado, a ação ainda transita o status.
func TestAcaoSemControlador(t *testing.T) {
	srv := Novo(Opcoes{Banco: abrirBancoTemp(t)}) // Exec nil
	proj := criarProjetoTeste(t, srv)
	dem := criarDemandaTeste(t, srv, proj)

	rec := fazerReq(t, srv, http.MethodPost, actionsURL(dem.ID), map[string]any{"acao": "cancelar"})
	if rec.Code != http.StatusOK {
		t.Fatalf("cancelar sem controlador: status %d", rec.Code)
	}
	if got := decodDemanda(t, rec); got.Status != db.StatusDemandaCancelada {
		t.Fatalf("status = %q, quero cancelada", got.Status)
	}
}

// TestTentarNovamenteExecucao: demanda falhou com fase falhada → fase volta a
// pendente, demanda a pronta, scheduler reenfileirado e erro limpo.
func TestTentarNovamenteExecucao(t *testing.T) {
	ctx := context.Background()
	banco := abrirBancoTemp(t)
	ctl := &ctlFake{rodando: map[int64]bool{}}
	srv := Novo(Opcoes{Banco: banco, Exec: ctl})
	proj := criarProjetoTeste(t, srv)
	dem := criarDemandaTeste(t, srv, proj)

	// simula uma execução que falhou: fase 1 falhou, demanda falhou com erro.
	fases, _ := banco.ListarFases(ctx, dem.ID)
	fases[0].Status = db.StatusFaseFalhou
	if _, err := banco.AtualizarFase(ctx, fases[0]); err != nil {
		t.Fatalf("marcar fase falhou: %v", err)
	}
	demBanco, _ := banco.ObterDemanda(ctx, dem.ID)
	demBanco.Status = db.StatusDemandaFalhou
	demBanco.Erro = "análise terminou com erro (error_max_budget_usd)"
	if _, err := banco.AtualizarDemanda(ctx, demBanco); err != nil {
		t.Fatalf("marcar demanda falhou: %v", err)
	}

	rec := fazerReq(t, srv, http.MethodPost, actionsURL(dem.ID), map[string]any{"acao": "tentar_novamente"})
	if rec.Code != http.StatusOK {
		t.Fatalf("tentar_novamente: status %d (corpo=%q)", rec.Code, rec.Body.String())
	}
	got := decodDemanda(t, rec)
	if got.Status != db.StatusDemandaPronta {
		t.Fatalf("status = %q, quero pronta", got.Status)
	}
	if got.Erro != "" {
		t.Fatalf("erro deveria ter sido limpo, veio %q", got.Erro)
	}
	fases, _ = banco.ListarFases(ctx, dem.ID)
	if fases[0].Status != db.StatusFasePendente {
		t.Fatalf("fase falhada deveria voltar a pendente, veio %q", fases[0].Status)
	}
	if len(ctl.reenfileiradas) != 1 || ctl.reenfileiradas[0] != dem.ID {
		t.Fatalf("deveria reenfileirar a demanda %d no scheduler: %v", dem.ID, ctl.reenfileiradas)
	}
	evs, _ := banco.ListarEventos(ctx, db.FiltroEventos{DemandID: &dem.ID})
	if !temTipoEvento(evs, "demanda_reativada") {
		t.Fatalf("evento demanda_reativada não registrado: %+v", evs)
	}
}

// TestTentarNovamenteAnalise: demanda por chat que falhou na análise (sem fases,
// sem perguntas) → volta a recebida e o analista é redisparado.
func TestTentarNovamenteAnalise(t *testing.T) {
	ctx := context.Background()
	banco := abrirBancoTemp(t)
	stub := &analisadorStub{}
	srv := Novo(Opcoes{Banco: banco, Intake: stub})
	proj := criarProjetoTeste(t, srv)

	dem, _, err := banco.CriarDemandaComChat(ctx, db.Demanda{ProjectID: proj, Titulo: "D",
		Status: db.StatusDemandaFalhou, Erro: "análise terminou com erro (error_max_budget_usd)"},
		db.MensagemChat{Papel: db.PapelUser, Conteudo: "PRD grande"})
	if err != nil {
		t.Fatalf("criar demanda: %v", err)
	}

	rec := fazerReq(t, srv, http.MethodPost, actionsURL(dem.ID), map[string]any{"acao": "tentar_novamente"})
	if rec.Code != http.StatusOK {
		t.Fatalf("tentar_novamente: status %d (corpo=%q)", rec.Code, rec.Body.String())
	}
	got := decodDemanda(t, rec)
	if got.Status != db.StatusDemandaRecebida {
		t.Fatalf("status = %q, quero recebida", got.Status)
	}
	if ids := stub.capturados(); len(ids) != 1 || ids[0] != dem.ID {
		t.Fatalf("análise deveria ser redisparada para %d: %v", dem.ID, ids)
	}
}

// TestTentarNovamentePlanejamento: perguntas todas respondidas (o planejador já
// tinha rodado e falhou) → volta a planejando e o planejador é redisparado.
func TestTentarNovamentePlanejamento(t *testing.T) {
	ctx := context.Background()
	banco := abrirBancoTemp(t)
	stub := &planejadorStub{}
	srv := Novo(Opcoes{Banco: banco, Planejamento: stub})
	dem, perguntas := seedDemandaAguardandoRespostas(t, banco)

	respostas := make([]db.RespostaPergunta, 0, len(perguntas))
	for _, p := range perguntas {
		respostas = append(respostas, db.RespostaPergunta{ID: p.ID, Resposta: "ok"})
	}
	if _, err := banco.ResponderPerguntas(ctx, dem, respostas); err != nil {
		t.Fatalf("responder perguntas: %v", err)
	}
	demBanco, _ := banco.ObterDemanda(ctx, dem)
	demBanco.Status = db.StatusDemandaFalhou
	demBanco.Erro = "planejamento terminou com erro (error_max_budget_usd)"
	if _, err := banco.AtualizarDemanda(ctx, demBanco); err != nil {
		t.Fatalf("marcar demanda falhou: %v", err)
	}

	rec := fazerReq(t, srv, http.MethodPost, actionsURL(dem), map[string]any{"acao": "tentar_novamente"})
	if rec.Code != http.StatusOK {
		t.Fatalf("tentar_novamente: status %d (corpo=%q)", rec.Code, rec.Body.String())
	}
	got := decodDemanda(t, rec)
	if got.Status != db.StatusDemandaPlanejando {
		t.Fatalf("status = %q, quero planejando", got.Status)
	}
	if ids := stub.capturados(); len(ids) != 1 || ids[0] != dem {
		t.Fatalf("planejamento deveria ser redisparado para %d: %v", dem, ids)
	}
}

// TestTentarNovamenteExigeFalhou: em qualquer status que não `falhou`, a ação é
// uma transição inválida (409).
func TestTentarNovamenteExigeFalhou(t *testing.T) {
	srv := Novo(Opcoes{Banco: abrirBancoTemp(t)})
	proj := criarProjetoTeste(t, srv)
	dem := criarDemandaTeste(t, srv, proj) // nasce pronta

	rec := fazerReq(t, srv, http.MethodPost, actionsURL(dem.ID), map[string]any{"acao": "tentar_novamente"})
	if rec.Code != http.StatusConflict {
		t.Fatalf("tentar_novamente em pronta: status %d, quero 409 (corpo=%q)", rec.Code, rec.Body.String())
	}
}

// temTipoEvento informa se a lista contém um evento do tipo dado.
func temTipoEvento(evs []db.Evento, tipo string) bool {
	for _, e := range evs {
		if e.Tipo == tipo {
			return true
		}
	}
	return false
}

func actionsURL(id int64) string {
	return "/api/v1/demands/" + strconv.FormatInt(id, 10) + "/actions"
}
