package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/marcos14/praxis-autonomous/internal/db"
)

func TestMetricasHome(t *testing.T) {
	banco := abrirBancoTemp(t)
	srv := Novo(Opcoes{Banco: banco})
	proj := criarProjetoTeste(t, srv)
	dem := criarDemandaTeste(t, srv, proj) // status pronta, 2 fases

	if _, err := banco.CriarExecucao(context.Background(), db.Execucao{
		DemandID: dem.ID, Operacao: db.OperacaoExecutor, Engine: "claude", CustoUSD: 1.25,
	}); err != nil {
		t.Fatalf("criar execução: %v", err)
	}

	rec := fazerReq(t, srv, http.MethodGet, "/api/v1/metrics", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, quero 200 (corpo=%q)", rec.Code, rec.Body.String())
	}
	var m respMetricas
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("decodificar métricas: %v", err)
	}
	if m.GastoMes != 1.25 {
		t.Fatalf("gasto_mes = %v, quero 1.25", m.GastoMes)
	}
	if m.DemandasAtivas != 1 {
		t.Fatalf("demandas_ativas = %d, quero 1", m.DemandasAtivas)
	}
	if m.Hoje == "" {
		t.Fatal("hoje vazio")
	}
}

func TestPendenciasHome(t *testing.T) {
	banco := abrirBancoTemp(t)
	srv := Novo(Opcoes{Banco: banco})
	proj := criarProjetoTeste(t, srv)

	// Uma demanda aguardando aprovação (pendência) e uma pronta (não pendência).
	if _, err := banco.CriarDemanda(context.Background(), db.Demanda{
		ProjectID: proj, Titulo: "aprovar", Status: db.StatusDemandaAguardandoAprovacao}); err != nil {
		t.Fatalf("criar: %v", err)
	}
	if _, err := banco.CriarDemanda(context.Background(), db.Demanda{
		ProjectID: proj, Titulo: "pronta", Status: db.StatusDemandaPronta}); err != nil {
		t.Fatalf("criar: %v", err)
	}

	rec := fazerReq(t, srv, http.MethodGet, "/api/v1/pendencias", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, quero 200", rec.Code)
	}
	var pend []db.Demanda
	if err := json.Unmarshal(rec.Body.Bytes(), &pend); err != nil {
		t.Fatalf("decodificar: %v", err)
	}
	if len(pend) != 1 || pend[0].Status != db.StatusDemandaAguardandoAprovacao {
		t.Fatalf("pendências = %+v, quero 1 aguardando_aprovacao", pend)
	}
}

func TestAtividadeRecente(t *testing.T) {
	banco := abrirBancoTemp(t)
	srv := Novo(Opcoes{Banco: banco})
	proj := criarProjetoTeste(t, srv)
	pid := proj
	if _, err := banco.RegistrarEvento(context.Background(), db.Evento{
		ProjectID: &pid, Tipo: "demanda_criada", Titulo: "Praxis: criada"}); err != nil {
		t.Fatalf("evento: %v", err)
	}

	rec := fazerReq(t, srv, http.MethodGet, "/api/v1/activity?limite=5", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, quero 200", rec.Code)
	}
	var evs []db.Evento
	if err := json.Unmarshal(rec.Body.Bytes(), &evs); err != nil {
		t.Fatalf("decodificar: %v", err)
	}
	if len(evs) != 1 || evs[0].Titulo != "Praxis: criada" {
		t.Fatalf("atividade = %+v, quero 1 evento", evs)
	}
}
