package db

import (
	"context"
	"errors"
	"testing"
)

func TestCriarExecucaoSemFase(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()
	dem := demandaTeste(t, d, "a")

	e, err := d.CriarExecucao(ctx, Execucao{DemandID: dem, Operacao: OperacaoAnalista})
	if err != nil {
		t.Fatalf("CriarExecucao: %v", err)
	}
	if e.ID == 0 || e.IniciadoEm == "" {
		t.Fatalf("id/iniciado_em não preenchidos: %+v", e)
	}
	if e.PhaseID != nil {
		t.Fatalf("PhaseID = %v, quero nil", *e.PhaseID)
	}
}

func TestCriarExecucaoComFase(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()
	dem := demandaTeste(t, d, "a")
	f, err := d.CriarFase(ctx, Fase{DemandID: dem, Codigo: "1a", Titulo: "x"})
	if err != nil {
		t.Fatalf("CriarFase: %v", err)
	}
	e, err := d.CriarExecucao(ctx, Execucao{DemandID: dem, PhaseID: &f.ID, Operacao: OperacaoExecutor})
	if err != nil {
		t.Fatalf("CriarExecucao: %v", err)
	}
	if e.PhaseID == nil || *e.PhaseID != f.ID {
		t.Fatalf("PhaseID = %v, quero %d", e.PhaseID, f.ID)
	}
}

func TestCriarExecucaoDemandaInexistente(t *testing.T) {
	d := abrirTemp(t)
	_, err := d.CriarExecucao(context.Background(), Execucao{DemandID: 999, Operacao: OperacaoExecutor})
	if !errors.Is(err, ErrNaoEncontrado) {
		t.Fatalf("erro = %v, quero ErrNaoEncontrado (FK)", err)
	}
}

func TestAtualizarExecucaoFinaliza(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()
	dem := demandaTeste(t, d, "a")

	e, err := d.CriarExecucao(ctx, Execucao{DemandID: dem, Operacao: OperacaoExecutor})
	if err != nil {
		t.Fatalf("CriarExecucao: %v", err)
	}
	e.Engine = "claude"
	e.Modelo = "opus"
	e.CustoUSD = 0.42
	e.TokensIn = 100
	e.TokensOut = 250
	e.IsError = true
	e.LogRef = "logs/run-1.jsonl"
	e.TerminadoEm = "2026-07-16T10:00:00.000Z"
	atualizada, err := d.AtualizarExecucao(ctx, e)
	if err != nil {
		t.Fatalf("AtualizarExecucao: %v", err)
	}
	if atualizada.Engine != "claude" || atualizada.CustoUSD != 0.42 {
		t.Fatalf("campos não persistiram: %+v", atualizada)
	}
	if atualizada.TokensIn != 100 || atualizada.TokensOut != 250 {
		t.Fatalf("tokens não persistiram: %+v", atualizada)
	}
	if !atualizada.IsError || atualizada.LogRef != "logs/run-1.jsonl" {
		t.Fatalf("is_error/log_ref não persistiram: %+v", atualizada)
	}
	if atualizada.IniciadoEm != e.IniciadoEm {
		t.Fatalf("IniciadoEm mudou: %q -> %q", e.IniciadoEm, atualizada.IniciadoEm)
	}
}

func TestAtualizarExecucaoInexistente(t *testing.T) {
	d := abrirTemp(t)
	_, err := d.AtualizarExecucao(context.Background(), Execucao{ID: 999})
	if !errors.Is(err, ErrNaoEncontrado) {
		t.Fatalf("erro = %v, quero ErrNaoEncontrado", err)
	}
}

func TestListarExecucoesOrdenadoPorID(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()
	dem := demandaTeste(t, d, "a")

	for _, op := range []string{OperacaoExecutor, OperacaoGates, OperacaoRevisor} {
		if _, err := d.CriarExecucao(ctx, Execucao{DemandID: dem, Operacao: op}); err != nil {
			t.Fatalf("CriarExecucao %s: %v", op, err)
		}
	}
	execs, err := d.ListarExecucoes(ctx, dem)
	if err != nil {
		t.Fatalf("ListarExecucoes: %v", err)
	}
	if len(execs) != 3 {
		t.Fatalf("len = %d, quero 3", len(execs))
	}
	if execs[0].Operacao != OperacaoExecutor || execs[2].Operacao != OperacaoRevisor {
		t.Fatalf("ordem inesperada: %+v", execs)
	}
}

func TestListarExecucoesVazioNaoNil(t *testing.T) {
	d := abrirTemp(t)
	dem := demandaTeste(t, d, "a")
	execs, err := d.ListarExecucoes(context.Background(), dem)
	if err != nil {
		t.Fatalf("ListarExecucoes: %v", err)
	}
	if execs == nil {
		t.Fatal("execs nil, quero slice vazio")
	}
}
