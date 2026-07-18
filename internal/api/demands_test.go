package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/marcos14/praxis-autonomous/internal/db"
)

// criarProjetoTeste cria um projeto (repo git temporário) via API e devolve seu id.
func criarProjetoTeste(t *testing.T, srv *Servidor) int64 {
	t.Helper()
	rec := fazerReq(t, srv, http.MethodPost, "/api/v1/projects", map[string]any{
		"nome":  "Proj Demandas",
		"pasta": repoGitTemp(t),
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("criar projeto: status %d (corpo=%q)", rec.Code, rec.Body.String())
	}
	return decodProjeto(t, rec).ID
}

func decodDemanda(t *testing.T, rec *httptest.ResponseRecorder) respDemanda {
	t.Helper()
	var d respDemanda
	if err := json.Unmarshal(rec.Body.Bytes(), &d); err != nil {
		t.Fatalf("decodificar demanda: %v (corpo=%q)", err, rec.Body.String())
	}
	return d
}

func TestCriarDemandaComFasesOK(t *testing.T) {
	srv := Novo(Opcoes{Banco: abrirBancoTemp(t)})
	proj := criarProjetoTeste(t, srv)

	rec := fazerReq(t, srv, http.MethodPost, "/api/v1/projects/"+strconv.FormatInt(proj, 10)+"/demands",
		map[string]any{
			"titulo":   "Refatorar login",
			"plano_md": "# plano",
			"fases": []map[string]any{
				{"codigo": "1", "titulo": "Preparar"},
				{"codigo": "2", "titulo": "Implementar", "depende_de": []string{"1"}, "requer_humano": true},
			},
		})
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, quero 201 (corpo=%q)", rec.Code, rec.Body.String())
	}
	d := decodDemanda(t, rec)
	if d.ID == 0 {
		t.Fatal("demanda sem id")
	}
	if d.ProjectID != proj {
		t.Fatalf("project_id = %d, quero %d", d.ProjectID, proj)
	}
	if d.Status != db.StatusDemandaPronta {
		t.Fatalf("status = %q, quero pronta", d.Status)
	}
	if d.Origem != db.OrigemAPI {
		t.Fatalf("origem = %q, quero api", d.Origem)
	}
	if len(d.Fases) != 2 {
		t.Fatalf("fases = %d, quero 2", len(d.Fases))
	}
	if d.Fases[0].ID == 0 || d.Fases[1].ID == 0 {
		t.Fatal("fases sem id")
	}
	if d.Fases[0].Ordem != 1 || d.Fases[1].Ordem != 2 {
		t.Fatalf("ordem das fases = %d,%d, quero 1,2", d.Fases[0].Ordem, d.Fases[1].Ordem)
	}
	if !d.Fases[1].RequerHumano {
		t.Fatal("fase 2 deveria exigir humano")
	}
	if len(d.Fases[1].DependeDe) != 1 || d.Fases[1].DependeDe[0] != "1" {
		t.Fatalf("depende_de da fase 2 = %v, quero [1]", d.Fases[1].DependeDe)
	}
}

// criarDemandaTeste cria uma demanda com fases via API e devolve a resposta.
func criarDemandaTeste(t *testing.T, srv *Servidor, projID int64) respDemanda {
	t.Helper()
	rec := fazerReq(t, srv, http.MethodPost, "/api/v1/projects/"+strconv.FormatInt(projID, 10)+"/demands",
		map[string]any{
			"titulo":   "Demanda X",
			"plano_md": "# plano da demanda",
			"fases": []map[string]any{
				{"codigo": "1", "titulo": "Preparar"},
				{"codigo": "2", "titulo": "Implementar", "depende_de": []string{"1"}},
			},
		})
	if rec.Code != http.StatusCreated {
		t.Fatalf("criar demanda: status %d (corpo=%q)", rec.Code, rec.Body.String())
	}
	return decodDemanda(t, rec)
}

func TestObterDemandaComFasesRefleteStatus(t *testing.T) {
	banco := abrirBancoTemp(t)
	srv := Novo(Opcoes{Banco: banco})
	proj := criarProjetoTeste(t, srv)
	criada := criarDemandaTeste(t, srv, proj)

	rec := fazerReq(t, srv, http.MethodGet, "/api/v1/demands/"+strconv.FormatInt(criada.ID, 10), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET demanda: status %d (corpo=%q)", rec.Code, rec.Body.String())
	}
	d := decodDemanda(t, rec)
	if d.PlanoMD != "# plano da demanda" {
		t.Fatalf("plano_md = %q", d.PlanoMD)
	}
	if len(d.Fases) != 2 || d.Fases[0].Status != db.StatusFasePendente {
		t.Fatalf("fases = %+v, quero 2 pendentes", d.Fases)
	}

	// simula o avanço da execução: a fase 1 passa a executando no banco.
	f := d.Fases[0]
	f.Status = db.StatusFaseExecutando
	if _, err := banco.AtualizarFase(context.Background(), f); err != nil {
		t.Fatalf("atualizar fase: %v", err)
	}
	rec = fazerReq(t, srv, http.MethodGet, "/api/v1/demands/"+strconv.FormatInt(criada.ID, 10), nil)
	d = decodDemanda(t, rec)
	if d.Fases[0].Status != db.StatusFaseExecutando {
		t.Fatalf("fase 1 status = %q, quero executando (deve refletir o banco)", d.Fases[0].Status)
	}
}

func TestObterDemandaInexistente(t *testing.T) {
	srv := Novo(Opcoes{Banco: abrirBancoTemp(t)})
	rec := fazerReq(t, srv, http.MethodGet, "/api/v1/demands/999", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, quero 404 (corpo=%q)", rec.Code, rec.Body.String())
	}
	if c := decodErro(t, rec).Erro.Codigo; c != "nao_encontrado" {
		t.Fatalf("codigo = %q, quero nao_encontrado", c)
	}
}

func TestListarDemandasEFiltros(t *testing.T) {
	srv := Novo(Opcoes{Banco: abrirBancoTemp(t)})
	proj := criarProjetoTeste(t, srv)
	criarDemandaTeste(t, srv, proj)
	criarDemandaTeste(t, srv, proj)

	rec := fazerReq(t, srv, http.MethodGet, "/api/v1/demands", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("listar: status %d", rec.Code)
	}
	var todas []db.Demanda
	if err := json.Unmarshal(rec.Body.Bytes(), &todas); err != nil {
		t.Fatalf("decodificar lista: %v (corpo=%q)", err, rec.Body.String())
	}
	if len(todas) != 2 {
		t.Fatalf("len = %d, quero 2", len(todas))
	}

	// filtro por projeto casa; por status inexistente devolve vazio.
	rec = fazerReq(t, srv, http.MethodGet, "/api/v1/demands?project="+strconv.FormatInt(proj, 10), nil)
	if err := json.Unmarshal(rec.Body.Bytes(), &todas); err != nil {
		t.Fatalf("decodificar: %v", err)
	}
	if len(todas) != 2 {
		t.Fatalf("por projeto: len = %d, quero 2", len(todas))
	}
	rec = fazerReq(t, srv, http.MethodGet, "/api/v1/demands?status=integrada", nil)
	if err := json.Unmarshal(rec.Body.Bytes(), &todas); err != nil {
		t.Fatalf("decodificar: %v", err)
	}
	if len(todas) != 0 {
		t.Fatalf("status integrada: len = %d, quero 0", len(todas))
	}
}

func TestEventosDemanda(t *testing.T) {
	banco := abrirBancoTemp(t)
	srv := Novo(Opcoes{Banco: banco})
	proj := criarProjetoTeste(t, srv)
	criada := criarDemandaTeste(t, srv, proj)

	if _, err := banco.RegistrarEvento(context.Background(), db.Evento{
		DemandID: &criada.ID, Tipo: "fase_iniciada", Titulo: "Fase 1 iniciada", Detalhe: "executor",
	}); err != nil {
		t.Fatalf("registrar evento: %v", err)
	}
	rec := fazerReq(t, srv, http.MethodGet, "/api/v1/demands/"+strconv.FormatInt(criada.ID, 10)+"/events", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET eventos: status %d (corpo=%q)", rec.Code, rec.Body.String())
	}
	var eventos []db.Evento
	if err := json.Unmarshal(rec.Body.Bytes(), &eventos); err != nil {
		t.Fatalf("decodificar eventos: %v (corpo=%q)", err, rec.Body.String())
	}
	if len(eventos) != 1 || eventos[0].Titulo != "Fase 1 iniciada" {
		t.Fatalf("eventos = %+v, quero 1 evento da fase", eventos)
	}
}

func TestCriarDemandaProjetoInexistente(t *testing.T) {
	srv := Novo(Opcoes{Banco: abrirBancoTemp(t)})
	rec := fazerReq(t, srv, http.MethodPost, "/api/v1/projects/999/demands", map[string]any{
		"titulo": "x",
		"fases":  []map[string]any{{"codigo": "1", "titulo": "a"}},
	})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, quero 404 (corpo=%q)", rec.Code, rec.Body.String())
	}
	if c := decodErro(t, rec).Erro.Codigo; c != "nao_encontrado" {
		t.Fatalf("codigo = %q, quero nao_encontrado", c)
	}
}

func TestCriarDemandaSemFases(t *testing.T) {
	srv := Novo(Opcoes{Banco: abrirBancoTemp(t)})
	proj := criarProjetoTeste(t, srv)
	rec := fazerReq(t, srv, http.MethodPost, "/api/v1/projects/"+strconv.FormatInt(proj, 10)+"/demands",
		map[string]any{"titulo": "sem fases"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, quero 400 (corpo=%q)", rec.Code, rec.Body.String())
	}
}

func TestCriarDemandaTituloObrigatorio(t *testing.T) {
	srv := Novo(Opcoes{Banco: abrirBancoTemp(t)})
	proj := criarProjetoTeste(t, srv)
	rec := fazerReq(t, srv, http.MethodPost, "/api/v1/projects/"+strconv.FormatInt(proj, 10)+"/demands",
		map[string]any{"fases": []map[string]any{{"codigo": "1", "titulo": "a"}}})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, quero 400", rec.Code)
	}
}

func TestCriarDemandaCodigoRepetido(t *testing.T) {
	srv := Novo(Opcoes{Banco: abrirBancoTemp(t)})
	proj := criarProjetoTeste(t, srv)
	rec := fazerReq(t, srv, http.MethodPost, "/api/v1/projects/"+strconv.FormatInt(proj, 10)+"/demands",
		map[string]any{
			"titulo": "dup",
			"fases": []map[string]any{
				{"codigo": "1", "titulo": "a"},
				{"codigo": "1", "titulo": "b"},
			},
		})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, quero 400 (corpo=%q)", rec.Code, rec.Body.String())
	}
}

func TestCriarDemandaDependenciaInexistente(t *testing.T) {
	srv := Novo(Opcoes{Banco: abrirBancoTemp(t)})
	proj := criarProjetoTeste(t, srv)
	rec := fazerReq(t, srv, http.MethodPost, "/api/v1/projects/"+strconv.FormatInt(proj, 10)+"/demands",
		map[string]any{
			"titulo": "dep",
			"fases": []map[string]any{
				{"codigo": "1", "titulo": "a", "depende_de": []string{"9"}},
			},
		})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, quero 400 (corpo=%q)", rec.Code, rec.Body.String())
	}
}

func TestCriarDemandaChatComBranch(t *testing.T) {
	srv := Novo(Opcoes{Banco: abrirBancoTemp(t)})
	proj := criarProjetoTeste(t, srv)
	rec := fazerReq(t, srv, http.MethodPost, "/api/v1/projects/"+strconv.FormatInt(proj, 10)+"/demands",
		map[string]any{"prd": "Criar um painel na home", "branch": "painel-home"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, quero 201 (corpo=%q)", rec.Code, rec.Body.String())
	}
	if b := decodDemanda(t, rec).Branch; b != "praxis/painel-home" {
		t.Fatalf("branch = %q, quero praxis/painel-home", b)
	}
}

func TestCriarDemandaChatBranchInvalida(t *testing.T) {
	srv := Novo(Opcoes{Banco: abrirBancoTemp(t)})
	proj := criarProjetoTeste(t, srv)
	rec := fazerReq(t, srv, http.MethodPost, "/api/v1/projects/"+strconv.FormatInt(proj, 10)+"/demands",
		map[string]any{"prd": "x", "branch": "com espaco"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, quero 400 (corpo=%q)", rec.Code, rec.Body.String())
	}
}
