package api

import (
	"context"
	"net/http"
	"strconv"
	"testing"

	"github.com/marcos14/praxis-autonomous/internal/db"
)

// ctlFake registra as chamadas de Interromper (ação cancelar/pausar).
type ctlFake struct {
	interrompidas []int64
	rodando       map[int64]bool
}

func (c *ctlFake) Interromper(id int64) bool {
	c.interrompidas = append(c.interrompidas, id)
	return c.rodando[id]
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

func actionsURL(id int64) string {
	return "/api/v1/demands/" + strconv.FormatInt(id, 10) + "/actions"
}
