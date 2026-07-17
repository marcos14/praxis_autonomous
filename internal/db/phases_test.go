package db

import (
	"context"
	"errors"
	"testing"
)

// demandaTeste cria projeto + demanda e devolve o id da demanda.
func demandaTeste(t *testing.T, d *DB, sufixo string) int64 {
	t.Helper()
	proj := criarProjetoTeste(t, d, sufixo)
	dem, err := d.CriarDemanda(context.Background(), Demanda{ProjectID: proj, Titulo: "demanda " + sufixo})
	if err != nil {
		t.Fatalf("criar demanda de teste: %v", err)
	}
	return dem.ID
}

func TestCriarFaseDefaultsEDependeDe(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()
	dem := demandaTeste(t, d, "a")

	f, err := d.CriarFase(ctx, Fase{
		DemandID:  dem,
		Codigo:    "2a",
		Titulo:    "Schema",
		DependeDe: []string{" 1a ", "", "1b"},
	})
	if err != nil {
		t.Fatalf("CriarFase: %v", err)
	}
	if f.ID == 0 {
		t.Fatal("ID não preenchido")
	}
	if f.Status != StatusFasePendente {
		t.Fatalf("Status = %q, quero default %q", f.Status, StatusFasePendente)
	}
	if len(f.DependeDe) != 2 || f.DependeDe[0] != "1a" || f.DependeDe[1] != "1b" {
		t.Fatalf("DependeDe = %v, quero [1a 1b]", f.DependeDe)
	}
	// Relê do banco e confere o round-trip do JSON.
	relida, err := d.ObterFase(ctx, f.ID)
	if err != nil {
		t.Fatalf("ObterFase: %v", err)
	}
	if len(relida.DependeDe) != 2 {
		t.Fatalf("DependeDe relido = %v", relida.DependeDe)
	}
}

func TestSubstituirFases(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()
	dem := demandaTeste(t, d, "sub")

	// conjunto inicial (3 fases).
	if _, err := d.SubstituirFases(ctx, dem, []Fase{
		{Codigo: "1", Titulo: "Fundação"},
		{Codigo: "2", Titulo: "API", DependeDe: []string{"1"}},
		{Codigo: "3", Titulo: "UI", DependeDe: []string{"2"}},
	}); err != nil {
		t.Fatalf("SubstituirFases inicial: %v", err)
	}

	// substitui por um conjunto novo (2 fases, ordem diferente, códigos novos).
	criadas, err := d.SubstituirFases(ctx, dem, []Fase{
		{Codigo: "a", Titulo: "Primeira", RequerHumano: true},
		{Codigo: "b", Titulo: "Segunda", DependeDe: []string{"a"}},
	})
	if err != nil {
		t.Fatalf("SubstituirFases: %v", err)
	}
	if len(criadas) != 2 {
		t.Fatalf("len criadas = %d, quero 2", len(criadas))
	}
	// ordem reatribuída 1..N na ordem do slice.
	if criadas[0].Ordem != 1 || criadas[1].Ordem != 2 {
		t.Fatalf("ordem = %d,%d, quero 1,2", criadas[0].Ordem, criadas[1].Ordem)
	}
	if !criadas[0].RequerHumano {
		t.Fatal("requer_humano não persistido")
	}

	// o conjunto antigo foi trocado por inteiro.
	todas, err := d.ListarFases(ctx, dem)
	if err != nil {
		t.Fatalf("ListarFases: %v", err)
	}
	if len(todas) != 2 || todas[0].Codigo != "a" || todas[1].Codigo != "b" {
		t.Fatalf("fases após substituir = %+v, quero [a b]", todas)
	}
	if todas[0].Status != StatusFasePendente {
		t.Fatalf("status default = %q", todas[0].Status)
	}
}

func TestSubstituirFasesCodigoDuplicado(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()
	dem := demandaTeste(t, d, "dup")

	_, err := d.SubstituirFases(ctx, dem, []Fase{
		{Codigo: "1", Titulo: "x"},
		{Codigo: "1", Titulo: "y"},
	})
	if !errors.Is(err, ErrCodigoFaseDuplicado) {
		t.Fatalf("erro = %v, quero ErrCodigoFaseDuplicado", err)
	}
	// transação: nada persistido no erro.
	todas, _ := d.ListarFases(ctx, dem)
	if len(todas) != 0 {
		t.Fatalf("fases = %d, quero 0 (rollback)", len(todas))
	}
}

func TestCriarFaseCodigoDuplicado(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()
	dem := demandaTeste(t, d, "a")

	if _, err := d.CriarFase(ctx, Fase{DemandID: dem, Codigo: "1a", Titulo: "x"}); err != nil {
		t.Fatalf("primeira fase: %v", err)
	}
	_, err := d.CriarFase(ctx, Fase{DemandID: dem, Codigo: "1a", Titulo: "y"})
	if !errors.Is(err, ErrCodigoFaseDuplicado) {
		t.Fatalf("erro = %v, quero ErrCodigoFaseDuplicado", err)
	}
}

func TestCriarFaseMesmoCodigoOutraDemanda(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()
	dem1 := demandaTeste(t, d, "a")
	dem2 := demandaTeste(t, d, "b")

	if _, err := d.CriarFase(ctx, Fase{DemandID: dem1, Codigo: "1a", Titulo: "x"}); err != nil {
		t.Fatalf("fase dem1: %v", err)
	}
	// Mesmo código em outra demanda é permitido (unicidade é por demanda).
	if _, err := d.CriarFase(ctx, Fase{DemandID: dem2, Codigo: "1a", Titulo: "x"}); err != nil {
		t.Fatalf("fase dem2 deveria ser permitida: %v", err)
	}
}

func TestCriarFaseDemandaInexistente(t *testing.T) {
	d := abrirTemp(t)
	_, err := d.CriarFase(context.Background(), Fase{DemandID: 999, Codigo: "1", Titulo: "x"})
	if !errors.Is(err, ErrNaoEncontrado) {
		t.Fatalf("erro = %v, quero ErrNaoEncontrado (FK)", err)
	}
}

func TestListarFasesOrdenadoPorOrdem(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()
	dem := demandaTeste(t, d, "a")

	criar := func(codigo string, ordem int) {
		if _, err := d.CriarFase(ctx, Fase{DemandID: dem, Codigo: codigo, Titulo: codigo, Ordem: ordem}); err != nil {
			t.Fatalf("CriarFase %s: %v", codigo, err)
		}
	}
	criar("c", 2)
	criar("a", 0)
	criar("b", 1)

	fases, err := d.ListarFases(ctx, dem)
	if err != nil {
		t.Fatalf("ListarFases: %v", err)
	}
	quero := []string{"a", "b", "c"}
	if len(fases) != 3 {
		t.Fatalf("len = %d, quero 3", len(fases))
	}
	for i, c := range quero {
		if fases[i].Codigo != c {
			t.Fatalf("posição %d = %q, quero %q", i, fases[i].Codigo, c)
		}
	}
}

func TestAtualizarFase(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()
	dem := demandaTeste(t, d, "a")

	f, err := d.CriarFase(ctx, Fase{DemandID: dem, Codigo: "1a", Titulo: "Antes"})
	if err != nil {
		t.Fatalf("CriarFase: %v", err)
	}
	f.Status = StatusFaseConcluida
	f.Tentativas = 2
	f.CustoUSD = 3.14
	f.RequerHumano = true
	f.Observacao = "ok"
	atualizada, err := d.AtualizarFase(ctx, f)
	if err != nil {
		t.Fatalf("AtualizarFase: %v", err)
	}
	if atualizada.Status != StatusFaseConcluida || atualizada.Tentativas != 2 {
		t.Fatalf("atualização não refletiu: %+v", atualizada)
	}
	if !atualizada.RequerHumano || atualizada.CustoUSD != 3.14 {
		t.Fatalf("campos não persistiram: %+v", atualizada)
	}
}

func TestAtualizarFaseInexistente(t *testing.T) {
	d := abrirTemp(t)
	_, err := d.AtualizarFase(context.Background(), Fase{ID: 999, Codigo: "1", Titulo: "x"})
	if !errors.Is(err, ErrNaoEncontrado) {
		t.Fatalf("erro = %v, quero ErrNaoEncontrado", err)
	}
}

func TestRemoverFaseZeraPhaseIDDaExecucao(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()
	dem := demandaTeste(t, d, "a")

	f, err := d.CriarFase(ctx, Fase{DemandID: dem, Codigo: "1a", Titulo: "x"})
	if err != nil {
		t.Fatalf("CriarFase: %v", err)
	}
	run, err := d.CriarExecucao(ctx, Execucao{DemandID: dem, PhaseID: &f.ID, Operacao: OperacaoExecutor})
	if err != nil {
		t.Fatalf("CriarExecucao: %v", err)
	}

	if err := d.RemoverFase(ctx, f.ID); err != nil {
		t.Fatalf("RemoverFase: %v", err)
	}
	relida, err := d.ObterExecucao(ctx, run.ID)
	if err != nil {
		t.Fatalf("ObterExecucao: %v", err)
	}
	if relida.PhaseID != nil {
		t.Fatalf("PhaseID = %v, quero nil (ON DELETE SET NULL)", *relida.PhaseID)
	}
}

func TestRemoverFaseInexistente(t *testing.T) {
	d := abrirTemp(t)
	if err := d.RemoverFase(context.Background(), 999); !errors.Is(err, ErrNaoEncontrado) {
		t.Fatalf("erro = %v, quero ErrNaoEncontrado", err)
	}
}
