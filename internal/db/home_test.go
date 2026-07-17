package db

import (
	"context"
	"testing"
)

// TestResumoHomeAgregados monta um cenário com custos, fases concluídas e
// demandas em vários status e confere os agregados da Home. As datas de corte
// são bem no passado para incluir todo o dado de teste (runs.iniciado_em e
// demands.atualizado_em são carimbados pelo banco com o horário atual).
func TestResumoHomeAgregados(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()
	proj := criarProjetoTeste(t, d, "a")

	// Demanda 1: executando, com dois runs (custo 1.5 + 0.5) e uma fase concluída.
	dem1, err := d.CriarDemanda(ctx, Demanda{ProjectID: proj, Titulo: "d1",
		Status: StatusDemandaExecutando, CustoUSD: 2.0})
	if err != nil {
		t.Fatalf("criar d1: %v", err)
	}
	if _, err := d.CriarExecucao(ctx, Execucao{DemandID: dem1.ID, Operacao: OperacaoExecutor, Engine: "claude", CustoUSD: 1.5}); err != nil {
		t.Fatalf("run1: %v", err)
	}
	if _, err := d.CriarExecucao(ctx, Execucao{DemandID: dem1.ID, Operacao: OperacaoCorretor, Engine: "claude", CustoUSD: 0.5}); err != nil {
		t.Fatalf("run2: %v", err)
	}
	f, err := d.CriarFase(ctx, Fase{DemandID: dem1.ID, Codigo: "1", Titulo: "x", Status: StatusFaseConcluida})
	if err != nil {
		t.Fatalf("fase: %v", err)
	}
	f.ConcluidoEm = "2026-07-17T10:00:00.000Z"
	if _, err := d.AtualizarFase(ctx, f); err != nil {
		t.Fatalf("atualizar fase: %v", err)
	}

	// Demanda 2: integrada (custo 4.0), não conta como ativa mas conta em integradas.
	if _, err := d.CriarDemanda(ctx, Demanda{ProjectID: proj, Titulo: "d2",
		Status: StatusDemandaIntegrada, CustoUSD: 4.0}); err != nil {
		t.Fatalf("criar d2: %v", err)
	}
	// Demanda 3: aguardando franquia.
	if _, err := d.CriarDemanda(ctx, Demanda{ProjectID: proj, Titulo: "d3",
		Status: StatusDemandaAguardandoFranquia}); err != nil {
		t.Fatalf("criar d3: %v", err)
	}
	// Demanda 4: cancelada (não conta como ativa).
	if _, err := d.CriarDemanda(ctx, Demanda{ProjectID: proj, Titulo: "d4",
		Status: StatusDemandaCancelada}); err != nil {
		t.Fatalf("criar d4: %v", err)
	}

	r, err := d.ResumoHome(ctx, "2000-01-01", "2000-01-01", "2000-01-01")
	if err != nil {
		t.Fatalf("ResumoHome: %v", err)
	}
	if r.GastoMes != 2.0 {
		t.Fatalf("gasto_mes = %v, quero 2.0 (soma dos runs)", r.GastoMes)
	}
	// Ativas: d1 (executando), d3 (aguardando_franquia). d2 integrada e d4 cancelada não contam.
	if r.DemandasAtivas != 2 {
		t.Fatalf("demandas_ativas = %d, quero 2", r.DemandasAtivas)
	}
	if r.FasesConcluidas7d != 1 {
		t.Fatalf("fases_concluidas_7d = %d, quero 1", r.FasesConcluidas7d)
	}
	if r.IntegradasMes != 1 {
		t.Fatalf("integradas_mes = %d, quero 1", r.IntegradasMes)
	}
	if r.AguardandoFranquia != 1 {
		t.Fatalf("aguardando_franquia = %d, quero 1", r.AguardandoFranquia)
	}
	if len(r.GastosPorDia) != 1 || r.GastosPorDia[0].CustoUSD != 2.0 {
		t.Fatalf("gastos_por_dia = %+v, quero 1 dia com 2.0", r.GastosPorDia)
	}
	if len(r.PorProjeto) != 1 {
		t.Fatalf("por_projeto = %d linhas, quero 1", len(r.PorProjeto))
	}
	// Custo total do projeto = 2 + 4 = 6; ativas no projeto = 2.
	if r.PorProjeto[0].CustoUSD != 6.0 || r.PorProjeto[0].DemandasAtivas != 2 {
		t.Fatalf("por_projeto[0] = %+v, quero custo 6.0 / ativas 2", r.PorProjeto[0])
	}
}

func TestListarDemandasPorStatus(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()
	proj := criarProjetoTeste(t, d, "a")

	mk := func(st string) {
		if _, err := d.CriarDemanda(ctx, Demanda{ProjectID: proj, Titulo: st, Status: st}); err != nil {
			t.Fatalf("criar %s: %v", st, err)
		}
	}
	mk(StatusDemandaAguardandoRespostas)
	mk(StatusDemandaAguardandoAprovacao)
	mk(StatusDemandaConflito)
	mk(StatusDemandaExecutando) // não deve entrar

	pend, err := d.ListarDemandasPorStatus(ctx, []string{
		StatusDemandaAguardandoRespostas, StatusDemandaAguardandoAprovacao, StatusDemandaConflito})
	if err != nil {
		t.Fatalf("ListarDemandasPorStatus: %v", err)
	}
	if len(pend) != 3 {
		t.Fatalf("pendências = %d, quero 3", len(pend))
	}

	vazio, err := d.ListarDemandasPorStatus(ctx, nil)
	if err != nil {
		t.Fatalf("lista vazia: %v", err)
	}
	if vazio == nil || len(vazio) != 0 {
		t.Fatalf("lista de statuses vazia deveria devolver slice vazio, got %v", vazio)
	}
}
