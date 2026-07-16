package db

import (
	"context"
	"errors"
	"testing"
)

// criarProjetoTeste cria um projeto mínimo e devolve seu id, para servir de pai
// (FK) nos testes das tabelas do ciclo de execução. Slug único por sufixo.
func criarProjetoTeste(t *testing.T, d *DB, sufixo string) int64 {
	t.Helper()
	p, err := d.CriarProjeto(context.Background(), Projeto{
		Nome:            "Proj " + sufixo,
		Slug:            "proj-" + sufixo,
		Pasta:           `C:\repo`,
		BranchPrincipal: "main",
		ModoIntegracao:  "merge_request",
		Ativo:           true,
	})
	if err != nil {
		t.Fatalf("criar projeto de teste: %v", err)
	}
	return p.ID
}

func TestCriarDemandaPreencheDefaults(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()
	proj := criarProjetoTeste(t, d, "a")

	dem, err := d.CriarDemanda(ctx, Demanda{ProjectID: proj, Titulo: "Ajustar login"})
	if err != nil {
		t.Fatalf("CriarDemanda: %v", err)
	}
	if dem.ID == 0 {
		t.Fatal("ID não preenchido")
	}
	if dem.CriadoEm == "" || dem.AtualizadoEm == "" {
		t.Fatalf("timestamps não preenchidos: %+v", dem)
	}
	if dem.Origem != OrigemUI {
		t.Fatalf("Origem = %q, quero default %q", dem.Origem, OrigemUI)
	}
	if dem.Status != StatusDemandaRecebida {
		t.Fatalf("Status = %q, quero default %q", dem.Status, StatusDemandaRecebida)
	}
}

func TestCriarDemandaProjetoInexistente(t *testing.T) {
	d := abrirTemp(t)
	_, err := d.CriarDemanda(context.Background(), Demanda{ProjectID: 999, Titulo: "x"})
	if !errors.Is(err, ErrNaoEncontrado) {
		t.Fatalf("erro = %v, quero ErrNaoEncontrado (FK)", err)
	}
}

func TestCriarDemandaComFases(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()
	proj := criarProjetoTeste(t, d, "cf")

	dem, fases, err := d.CriarDemandaComFases(ctx,
		Demanda{ProjectID: proj, Titulo: "Nova", Status: StatusDemandaPronta},
		[]Fase{
			{Codigo: "1", Titulo: "Primeira"},
			{Codigo: "2", Titulo: "Segunda", DependeDe: []string{"1"}},
		},
	)
	if err != nil {
		t.Fatalf("CriarDemandaComFases: %v", err)
	}
	if dem.ID == 0 || dem.CriadoEm == "" {
		t.Fatalf("demanda não persistida: %+v", dem)
	}
	if len(fases) != 2 || fases[0].ID == 0 || fases[1].ID == 0 {
		t.Fatalf("fases não persistidas: %+v", fases)
	}
	if fases[0].Status != StatusFasePendente {
		t.Fatalf("status default da fase = %q, quero pendente", fases[0].Status)
	}
	// as fases estão no banco, ligadas à demanda.
	lidas, err := d.ListarFases(ctx, dem.ID)
	if err != nil || len(lidas) != 2 {
		t.Fatalf("ListarFases = %d fases (err=%v), quero 2", len(lidas), err)
	}
}

func TestCriarDemandaComFasesCodigoDuplicadoFazRollback(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()
	proj := criarProjetoTeste(t, d, "rb")

	_, _, err := d.CriarDemandaComFases(ctx,
		Demanda{ProjectID: proj, Titulo: "Dup"},
		[]Fase{{Codigo: "1", Titulo: "a"}, {Codigo: "1", Titulo: "b"}},
	)
	if !errors.Is(err, ErrCodigoFaseDuplicado) {
		t.Fatalf("erro = %v, quero ErrCodigoFaseDuplicado", err)
	}
	// rollback: nenhuma demanda deve ter sido criada.
	dems, err := d.ListarDemandas(ctx, FiltroDemandas{})
	if err != nil {
		t.Fatalf("ListarDemandas: %v", err)
	}
	if len(dems) != 0 {
		t.Fatalf("esperava 0 demandas após rollback, veio %d", len(dems))
	}
}

func TestCriarDemandaComFasesProjetoInexistente(t *testing.T) {
	d := abrirTemp(t)
	_, _, err := d.CriarDemandaComFases(context.Background(),
		Demanda{ProjectID: 999, Titulo: "x"}, []Fase{{Codigo: "1", Titulo: "a"}})
	if !errors.Is(err, ErrNaoEncontrado) {
		t.Fatalf("erro = %v, quero ErrNaoEncontrado (FK)", err)
	}
}

func TestCriarDemandaOrigemInvalida(t *testing.T) {
	d := abrirTemp(t)
	proj := criarProjetoTeste(t, d, "a")
	_, err := d.CriarDemanda(context.Background(),
		Demanda{ProjectID: proj, Titulo: "x", Origem: "telepatia"})
	if err == nil {
		t.Fatal("CHECK de origem deveria rejeitar valor inválido")
	}
}

func TestListarDemandasFiltraProjetoEStatus(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()
	projA := criarProjetoTeste(t, d, "a")
	projB := criarProjetoTeste(t, d, "b")

	mustDem := func(proj int64, status string) {
		if _, err := d.CriarDemanda(ctx, Demanda{ProjectID: proj, Titulo: "t", Status: status}); err != nil {
			t.Fatalf("CriarDemanda: %v", err)
		}
	}
	mustDem(projA, StatusDemandaExecutando)
	mustDem(projA, StatusDemandaConcluida)
	mustDem(projB, StatusDemandaExecutando)

	soA, err := d.ListarDemandas(ctx, FiltroDemandas{ProjectID: &projA})
	if err != nil {
		t.Fatalf("ListarDemandas projA: %v", err)
	}
	if len(soA) != 2 {
		t.Fatalf("demandas de projA = %d, quero 2", len(soA))
	}
	exec := StatusDemandaExecutando
	execA, err := d.ListarDemandas(ctx, FiltroDemandas{ProjectID: &projA, Status: exec})
	if err != nil {
		t.Fatalf("ListarDemandas projA executando: %v", err)
	}
	if len(execA) != 1 {
		t.Fatalf("demandas executando de projA = %d, quero 1", len(execA))
	}
	todas, err := d.ListarDemandas(ctx, FiltroDemandas{})
	if err != nil {
		t.Fatalf("ListarDemandas todas: %v", err)
	}
	if len(todas) != 3 {
		t.Fatalf("total = %d, quero 3", len(todas))
	}
}

func TestListarDemandasVazioNaoNil(t *testing.T) {
	d := abrirTemp(t)
	lista, err := d.ListarDemandas(context.Background(), FiltroDemandas{})
	if err != nil {
		t.Fatalf("ListarDemandas: %v", err)
	}
	if lista == nil {
		t.Fatal("lista nil, quero slice vazio")
	}
}

func TestObterDemandaInexistente(t *testing.T) {
	d := abrirTemp(t)
	_, err := d.ObterDemanda(context.Background(), 999)
	if !errors.Is(err, ErrNaoEncontrado) {
		t.Fatalf("erro = %v, quero ErrNaoEncontrado", err)
	}
}

func TestAtualizarDemanda(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()
	proj := criarProjetoTeste(t, d, "a")

	dem, err := d.CriarDemanda(ctx, Demanda{ProjectID: proj, Titulo: "Antes"})
	if err != nil {
		t.Fatalf("CriarDemanda: %v", err)
	}
	dem.Titulo = "Depois"
	dem.Status = StatusDemandaExecutando
	dem.Branch = "praxis/d1-antes"
	dem.CustoUSD = 1.5
	atualizada, err := d.AtualizarDemanda(ctx, dem)
	if err != nil {
		t.Fatalf("AtualizarDemanda: %v", err)
	}
	if atualizada.Titulo != "Depois" || atualizada.Status != StatusDemandaExecutando {
		t.Fatalf("atualização não refletiu: %+v", atualizada)
	}
	if atualizada.Branch != "praxis/d1-antes" || atualizada.CustoUSD != 1.5 {
		t.Fatalf("campos não persistiram: %+v", atualizada)
	}
	if atualizada.CriadoEm != dem.CriadoEm {
		t.Fatalf("CriadoEm mudou: %q -> %q", dem.CriadoEm, atualizada.CriadoEm)
	}
	if atualizada.AtualizadoEm < dem.CriadoEm {
		t.Fatalf("AtualizadoEm %q anterior a CriadoEm %q", atualizada.AtualizadoEm, dem.CriadoEm)
	}
}

func TestAtualizarDemandaInexistente(t *testing.T) {
	d := abrirTemp(t)
	_, err := d.AtualizarDemanda(context.Background(), Demanda{ID: 999, Titulo: "x", Origem: OrigemUI, Status: StatusDemandaRecebida})
	if !errors.Is(err, ErrNaoEncontrado) {
		t.Fatalf("erro = %v, quero ErrNaoEncontrado", err)
	}
}

func TestRemoverDemandaCascata(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()
	proj := criarProjetoTeste(t, d, "a")

	dem, err := d.CriarDemanda(ctx, Demanda{ProjectID: proj, Titulo: "t"})
	if err != nil {
		t.Fatalf("CriarDemanda: %v", err)
	}
	if _, err := d.CriarFase(ctx, Fase{DemandID: dem.ID, Codigo: "1", Titulo: "f"}); err != nil {
		t.Fatalf("CriarFase: %v", err)
	}
	if _, err := d.CriarExecucao(ctx, Execucao{DemandID: dem.ID, Operacao: OperacaoExecutor}); err != nil {
		t.Fatalf("CriarExecucao: %v", err)
	}

	if err := d.RemoverDemanda(ctx, dem.ID); err != nil {
		t.Fatalf("RemoverDemanda: %v", err)
	}
	if _, err := d.ObterDemanda(ctx, dem.ID); !errors.Is(err, ErrNaoEncontrado) {
		t.Fatalf("demanda ainda existe: %v", err)
	}
	fases, err := d.ListarFases(ctx, dem.ID)
	if err != nil {
		t.Fatalf("ListarFases: %v", err)
	}
	if len(fases) != 0 {
		t.Fatalf("fases não removidas em cascata: %d", len(fases))
	}
	execs, err := d.ListarExecucoes(ctx, dem.ID)
	if err != nil {
		t.Fatalf("ListarExecucoes: %v", err)
	}
	if len(execs) != 0 {
		t.Fatalf("execuções não removidas em cascata: %d", len(execs))
	}
}

func TestRemoverDemandaInexistente(t *testing.T) {
	d := abrirTemp(t)
	if err := d.RemoverDemanda(context.Background(), 999); !errors.Is(err, ErrNaoEncontrado) {
		t.Fatalf("erro = %v, quero ErrNaoEncontrado", err)
	}
}
