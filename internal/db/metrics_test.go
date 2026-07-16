package db

import (
	"context"
	"errors"
	"testing"
)

func TestAcumularMetricaDiaIncrementa(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()
	proj := criarProjetoTeste(t, d, "a")

	m := MetricaDia{Dia: "2026-07-16", ProjectID: proj, Engine: "claude", CustoUSD: 1.0, FasesConcluidas: 1}
	if err := d.AcumularMetricaDia(ctx, m); err != nil {
		t.Fatalf("AcumularMetricaDia (1): %v", err)
	}
	// Segunda chamada na mesma chave soma (não substitui).
	m.CustoUSD = 2.5
	m.FasesConcluidas = 2
	if err := d.AcumularMetricaDia(ctx, m); err != nil {
		t.Fatalf("AcumularMetricaDia (2): %v", err)
	}

	linhas, err := d.ListarMetricasDia(ctx, FiltroMetricas{ProjectID: &proj})
	if err != nil {
		t.Fatalf("ListarMetricasDia: %v", err)
	}
	if len(linhas) != 1 {
		t.Fatalf("len = %d, quero 1 (mesma chave)", len(linhas))
	}
	if linhas[0].CustoUSD != 3.5 || linhas[0].FasesConcluidas != 3 {
		t.Fatalf("agregado = %+v, quero custo 3.5 / fases 3", linhas[0])
	}
}

func TestAcumularMetricaDiaChavesDistintas(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()
	proj := criarProjetoTeste(t, d, "a")

	linhas := []MetricaDia{
		{Dia: "2026-07-16", ProjectID: proj, Engine: "claude", CustoUSD: 1},
		{Dia: "2026-07-16", ProjectID: proj, Engine: "codex", CustoUSD: 2},
		{Dia: "2026-07-17", ProjectID: proj, Engine: "claude", CustoUSD: 3},
	}
	for _, m := range linhas {
		if err := d.AcumularMetricaDia(ctx, m); err != nil {
			t.Fatalf("AcumularMetricaDia: %v", err)
		}
	}
	todas, err := d.ListarMetricasDia(ctx, FiltroMetricas{ProjectID: &proj})
	if err != nil {
		t.Fatalf("ListarMetricasDia: %v", err)
	}
	if len(todas) != 3 {
		t.Fatalf("len = %d, quero 3 (chaves distintas)", len(todas))
	}
	// Filtro por dia.
	so16, err := d.ListarMetricasDia(ctx, FiltroMetricas{ProjectID: &proj, DiaDe: "2026-07-16", DiaAte: "2026-07-16"})
	if err != nil {
		t.Fatalf("ListarMetricasDia filtro dia: %v", err)
	}
	if len(so16) != 2 {
		t.Fatalf("len (dia 16) = %d, quero 2", len(so16))
	}
}

func TestAcumularMetricaDiaProjetoInexistente(t *testing.T) {
	d := abrirTemp(t)
	err := d.AcumularMetricaDia(context.Background(),
		MetricaDia{Dia: "2026-07-16", ProjectID: 999, Engine: "claude"})
	if !errors.Is(err, ErrNaoEncontrado) {
		t.Fatalf("erro = %v, quero ErrNaoEncontrado (FK)", err)
	}
}

func TestListarMetricasDiaVazioNaoNil(t *testing.T) {
	d := abrirTemp(t)
	lista, err := d.ListarMetricasDia(context.Background(), FiltroMetricas{})
	if err != nil {
		t.Fatalf("ListarMetricasDia: %v", err)
	}
	if lista == nil {
		t.Fatal("lista nil, quero slice vazio")
	}
}
