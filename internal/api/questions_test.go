package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"sync"
	"testing"

	"github.com/marcos14/praxis-autonomous/internal/db"
)

// analisadorStub registra as demandas cuja análise foi disparada (Fase 3b).
type analisadorStub struct {
	mu  sync.Mutex
	ids []int64
}

func (a *analisadorStub) Disparar(demandaID int64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.ids = append(a.ids, demandaID)
}

func (a *analisadorStub) capturados() []int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]int64(nil), a.ids...)
}

func TestCriarDemandaChatDisparaAnalista(t *testing.T) {
	banco := abrirBancoTemp(t)
	stub := &analisadorStub{}
	srv := Novo(Opcoes{Banco: banco, Intake: stub})
	proj := criarProjetoTeste(t, srv)

	d := criarDemandaChatTeste(t, srv, proj, "Split", "Dividir recebimento entre filiais")

	got := stub.capturados()
	if len(got) != 1 || got[0] != d.ID {
		t.Fatalf("análise disparada = %v, quero [%d]", got, d.ID)
	}
}

func TestCriarDemandaManualNaoDisparaAnalista(t *testing.T) {
	banco := abrirBancoTemp(t)
	stub := &analisadorStub{}
	srv := Novo(Opcoes{Banco: banco, Intake: stub})
	proj := criarProjetoTeste(t, srv)

	// demanda manual (com fases) não passa pelo analista.
	rec := fazerReq(t, srv, http.MethodPost, "/api/v1/projects/"+strconv.FormatInt(proj, 10)+"/demands",
		map[string]any{"titulo": "manual", "fases": []map[string]any{{"codigo": "1", "titulo": "x"}}})
	if rec.Code != http.StatusCreated {
		t.Fatalf("criar demanda manual: status %d", rec.Code)
	}
	if got := stub.capturados(); len(got) != 0 {
		t.Fatalf("análise não deveria ser disparada para demanda manual: %v", got)
	}
}

// seedDemandaAguardandoRespostas cria uma demanda em aguardando_respostas com
// perguntas, direto no banco, para exercitar os endpoints de perguntas/respostas.
func seedDemandaAguardandoRespostas(t *testing.T, banco *db.DB) (int64, []db.Pergunta) {
	t.Helper()
	ctx := context.Background()
	proj, err := banco.CriarProjeto(ctx, db.Projeto{Nome: "P", Slug: "p-perg", Pasta: `C:\repo`,
		BranchPrincipal: "main", ModoIntegracao: "merge_request", Ativo: true})
	if err != nil {
		t.Fatalf("criar projeto: %v", err)
	}
	dem, err := banco.CriarDemanda(ctx, db.Demanda{ProjectID: proj.ID, Titulo: "D", Status: db.StatusDemandaAguardandoRespostas})
	if err != nil {
		t.Fatalf("criar demanda: %v", err)
	}
	perg, err := banco.SubstituirPerguntas(ctx, dem.ID, []db.Pergunta{
		{Pergunta: "P1", Tipo: "escolha", Opcoes: []string{"a", "b"}, Sugestao: "a", Impacto: db.ImpactoAlto},
		{Pergunta: "P2", Tipo: "texto"},
	})
	if err != nil {
		t.Fatalf("substituir perguntas: %v", err)
	}
	return dem.ID, perg
}

func TestListarPerguntas(t *testing.T) {
	banco := abrirBancoTemp(t)
	srv := Novo(Opcoes{Banco: banco})
	dem, _ := seedDemandaAguardandoRespostas(t, banco)

	rec := fazerReq(t, srv, http.MethodGet, "/api/v1/demands/"+strconv.FormatInt(dem, 10)+"/questions", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (corpo=%q)", rec.Code, rec.Body.String())
	}
	var perguntas []db.Pergunta
	if err := json.Unmarshal(rec.Body.Bytes(), &perguntas); err != nil {
		t.Fatalf("decodificar: %v", err)
	}
	if len(perguntas) != 2 {
		t.Fatalf("len = %d, quero 2", len(perguntas))
	}
	if perguntas[0].Impacto != db.ImpactoAlto || len(perguntas[0].Opcoes) != 2 {
		t.Fatalf("pergunta 1 mal serializada: %+v", perguntas[0])
	}
}

func TestListarPerguntasDemandaInexistente(t *testing.T) {
	srv := Novo(Opcoes{Banco: abrirBancoTemp(t)})
	rec := fazerReq(t, srv, http.MethodGet, "/api/v1/demands/9999/questions", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, quero 404", rec.Code)
	}
}

func TestResponderPerguntasPersisteEMudaStatus(t *testing.T) {
	banco := abrirBancoTemp(t)
	srv := Novo(Opcoes{Banco: banco})
	dem, perg := seedDemandaAguardandoRespostas(t, banco)

	rec := fazerReq(t, srv, http.MethodPost, "/api/v1/demands/"+strconv.FormatInt(dem, 10)+"/answers",
		map[string]any{"respostas": []map[string]any{
			{"id": perg[0].ID, "resposta": "a"},
			{"id": perg[1].ID, "resposta": "sem migração"},
		}})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, quero 200 (corpo=%q)", rec.Code, rec.Body.String())
	}
	d := decodDemanda(t, rec)
	if d.Status != db.StatusDemandaPlanejando {
		t.Fatalf("status = %q, quero planejando", d.Status)
	}

	// respostas persistidas.
	got, _ := banco.ListarPerguntas(context.Background(), dem)
	if got[0].Resposta != "a" || got[0].RespondidaEm == "" {
		t.Fatalf("P1 não respondida: %+v", got[0])
	}
	if got[1].Resposta != "sem migração" {
		t.Fatalf("P2 resposta = %q", got[1].Resposta)
	}

	// evento registrado.
	evs, _ := banco.ListarEventos(context.Background(), db.FiltroEventos{DemandID: &dem})
	achou := false
	for _, e := range evs {
		if e.Tipo == "respostas_recebidas" {
			achou = true
		}
	}
	if !achou {
		t.Fatalf("evento respostas_recebidas não registrado: %+v", evs)
	}
}

func TestResponderPerguntasStatusInvalido(t *testing.T) {
	banco := abrirBancoTemp(t)
	srv := Novo(Opcoes{Banco: banco})
	ctx := context.Background()
	proj, _ := banco.CriarProjeto(ctx, db.Projeto{Nome: "P", Slug: "p-inv", Pasta: `C:\r`,
		BranchPrincipal: "main", ModoIntegracao: "merge_request", Ativo: true})
	// demanda em 'recebida' (ainda não aguardando respostas).
	dem, _ := banco.CriarDemanda(ctx, db.Demanda{ProjectID: proj.ID, Titulo: "D", Status: db.StatusDemandaRecebida})

	rec := fazerReq(t, srv, http.MethodPost, "/api/v1/demands/"+strconv.FormatInt(dem.ID, 10)+"/answers",
		map[string]any{"respostas": []map[string]any{}})
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, quero 409 (corpo=%q)", rec.Code, rec.Body.String())
	}
}

func TestResponderPerguntasSemRespostasAvanca(t *testing.T) {
	banco := abrirBancoTemp(t)
	srv := Novo(Opcoes{Banco: banco})
	dem, _ := seedDemandaAguardandoRespostas(t, banco)

	// "gerar plano" mesmo sem responder (análise sem perguntas obrigatórias).
	rec := fazerReq(t, srv, http.MethodPost, "/api/v1/demands/"+strconv.FormatInt(dem, 10)+"/answers",
		map[string]any{"respostas": []map[string]any{}})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, quero 200", rec.Code)
	}
	d := decodDemanda(t, rec)
	if d.Status != db.StatusDemandaPlanejando {
		t.Fatalf("status = %q, quero planejando", d.Status)
	}
}
