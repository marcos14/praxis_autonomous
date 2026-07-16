package api

import (
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
