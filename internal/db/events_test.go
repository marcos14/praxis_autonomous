package db

import (
	"context"
	"errors"
	"testing"
)

func TestRegistrarEventoGlobalDoProjeto(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()
	proj := criarProjetoTeste(t, d, "a")

	e, err := d.RegistrarEvento(ctx, Evento{ProjectID: &proj, Tipo: "projeto_criado", Titulo: "Proj"})
	if err != nil {
		t.Fatalf("RegistrarEvento: %v", err)
	}
	if e.ID == 0 || e.CriadoEm == "" {
		t.Fatalf("id/criado_em não preenchidos: %+v", e)
	}
	if e.ProjectID == nil || *e.ProjectID != proj {
		t.Fatalf("ProjectID = %v, quero %d", e.ProjectID, proj)
	}
	if e.DemandID != nil {
		t.Fatalf("DemandID = %v, quero nil", *e.DemandID)
	}
}

func TestRegistrarEventoSemVinculos(t *testing.T) {
	d := abrirTemp(t)
	e, err := d.RegistrarEvento(context.Background(), Evento{Tipo: "boot", Titulo: "subiu"})
	if err != nil {
		t.Fatalf("RegistrarEvento: %v", err)
	}
	if e.ProjectID != nil || e.DemandID != nil {
		t.Fatalf("vínculos deveriam ser nil: %+v", e)
	}
}

func TestRegistrarEventoDemandaInexistente(t *testing.T) {
	d := abrirTemp(t)
	inexistente := int64(999)
	_, err := d.RegistrarEvento(context.Background(), Evento{DemandID: &inexistente, Tipo: "x"})
	if !errors.Is(err, ErrNaoEncontrado) {
		t.Fatalf("erro = %v, quero ErrNaoEncontrado (FK)", err)
	}
}

func TestListarEventosFiltraEOrdenaDesc(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()
	proj := criarProjetoTeste(t, d, "a")
	dem, err := d.CriarDemanda(ctx, Demanda{ProjectID: proj, Titulo: "t"})
	if err != nil {
		t.Fatalf("CriarDemanda: %v", err)
	}

	// 3 eventos da demanda + 1 só do projeto.
	for _, tipo := range []string{"fase_iniciada", "fase_concluida", "commit"} {
		if _, err := d.RegistrarEvento(ctx, Evento{ProjectID: &proj, DemandID: &dem.ID, Tipo: tipo}); err != nil {
			t.Fatalf("RegistrarEvento %s: %v", tipo, err)
		}
	}
	if _, err := d.RegistrarEvento(ctx, Evento{ProjectID: &proj, Tipo: "config_alterada"}); err != nil {
		t.Fatalf("RegistrarEvento projeto: %v", err)
	}

	// consulta_id / planejamento_id (M4) fazem ida e volta e a FK vale.
	cons, _, err := d.CriarConsultaComChat(ctx, Consulta{ProjectID: &proj, Titulo: "c"}, MensagemConsulta{Conteudo: "?"})
	if err != nil {
		t.Fatalf("consulta: %v", err)
	}
	evC, err := d.RegistrarEvento(ctx, Evento{ConsultaID: &cons.ID, Tipo: "consulta_respondida", Titulo: "r"})
	if err != nil || evC.ConsultaID == nil || *evC.ConsultaID != cons.ID {
		t.Fatalf("evento de consulta: %+v %v", evC, err)
	}
	if lidos, _ := d.EventosApos(ctx, evC.ID-1, 1, Visao{}); len(lidos) != 1 || lidos[0].ConsultaID == nil || *lidos[0].ConsultaID != cons.ID || lidos[0].PlanejamentoID != nil {
		t.Fatalf("consulta_id não voltou do banco: %+v", lidos)
	}
	invalido := int64(9999)
	if _, err := d.RegistrarEvento(ctx, Evento{PlanejamentoID: &invalido, Tipo: "x", Titulo: "x"}); !errors.Is(err, ErrNaoEncontrado) {
		t.Fatalf("planejamento inexistente: %v", err)
	}

	daDemanda, err := d.ListarEventos(ctx, FiltroEventos{DemandID: &dem.ID})
	if err != nil {
		t.Fatalf("ListarEventos demanda: %v", err)
	}
	if len(daDemanda) != 3 {
		t.Fatalf("eventos da demanda = %d, quero 3", len(daDemanda))
	}
	// Ordem decrescente por id: o último inserido vem primeiro.
	if daDemanda[0].Tipo != "commit" {
		t.Fatalf("primeiro = %q, quero commit (mais recente)", daDemanda[0].Tipo)
	}

	doProjeto, err := d.ListarEventos(ctx, FiltroEventos{ProjectID: &proj})
	if err != nil {
		t.Fatalf("ListarEventos projeto: %v", err)
	}
	if len(doProjeto) != 4 {
		t.Fatalf("eventos do projeto = %d, quero 4", len(doProjeto))
	}
}

func TestListarEventosLimite(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		if _, err := d.RegistrarEvento(ctx, Evento{Tipo: "tick"}); err != nil {
			t.Fatalf("RegistrarEvento: %v", err)
		}
	}
	lim, err := d.ListarEventos(ctx, FiltroEventos{Limite: 2})
	if err != nil {
		t.Fatalf("ListarEventos: %v", err)
	}
	if len(lim) != 2 {
		t.Fatalf("len = %d, quero 2 (limite)", len(lim))
	}
}

func TestListarEventosVazioNaoNil(t *testing.T) {
	d := abrirTemp(t)
	lista, err := d.ListarEventos(context.Background(), FiltroEventos{})
	if err != nil {
		t.Fatalf("ListarEventos: %v", err)
	}
	if lista == nil {
		t.Fatal("lista nil, quero slice vazio")
	}
}
