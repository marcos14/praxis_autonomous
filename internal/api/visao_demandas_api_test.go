package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"testing"

	"github.com/marcos14/praxis-autonomous/internal/db"
)

func TestVisibilidadeDeDemandasNaAPI(t *testing.T) {
	c := montarCenarioVisaoAPI(t)
	rotaCriar := "/api/v1/projects/" + strconv.FormatInt(c.proj, 10) + "/demands"
	criar := func(token, vis, prd string) db.Demanda {
		t.Helper()
		corpo := map[string]any{"prd": prd, "origem": "api"}
		if vis != "" {
			corpo["visibilidade"] = vis
		}
		rec := fazerReqToken(t, c.srv, http.MethodPost, rotaCriar, token, corpo)
		if rec.Code != http.StatusCreated {
			t.Fatalf("criar demanda %q: status %d (corpo=%q)", prd, rec.Code, rec.Body.String())
		}
		return decodDemanda(t, rec).Demanda
	}
	privada := criar(c.ana, "", "demanda privada")
	publica := criar(c.ana, "publica", "demanda pública")
	if privada.Visibilidade != "privada" || publica.Visibilidade != "publica" {
		t.Fatalf("visibilidades = %q/%q", privada.Visibilidade, publica.Visibilidade)
	}

	contar := func(token, rota string) int {
		t.Helper()
		rec := fazerReqToken(t, c.srv, http.MethodGet, rota, token, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s: status %d (corpo=%q)", rota, rec.Code, rec.Body.String())
		}
		return contarLista(t, rec)
	}
	for _, tc := range []struct {
		nome  string
		token string
		rota  string
		quero int
	}{
		{"ana /demands", c.ana, "/api/v1/demands", 2},
		{"bia /demands", c.bia, "/api/v1/demands", 1},
		{"caio /board", c.caio, "/api/v1/board", 1},
		{"admin /board", c.admin, "/api/v1/board", 2},
		{"bia escopo=meus", c.bia, "/api/v1/demands?escopo=meus", 0},
		{"ana escopo=meus", c.ana, "/api/v1/board?escopo=meus", 2},
	} {
		if n := contar(tc.token, tc.rota); n != tc.quero {
			t.Errorf("%s = %d, quero %d", tc.nome, n, tc.quero)
		}
	}
	if rec := fazerReqToken(t, c.srv, http.MethodGet, "/api/v1/board?escopo=nada", c.bia, nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("escopo inválido no board: status %d, quero 400", rec.Code)
	}

	// Autor nas listagens.
	rec := fazerReqToken(t, c.srv, http.MethodGet, "/api/v1/demands", c.bia, nil)
	var lista []db.Demanda
	_ = json.Unmarshal(rec.Body.Bytes(), &lista)
	if len(lista) != 1 || lista[0].CriadoPorNome != "Ana" {
		t.Fatalf("listagem de bia: %+v", lista)
	}

	// Por id: privada alheia é 404 em qualquer sub-rota.
	rotaPriv := "/api/v1/demands/" + strconv.FormatInt(privada.ID, 10)
	for _, sub := range []string{"", "/chat", "/events"} {
		if rec := fazerReqToken(t, c.srv, http.MethodGet, rotaPriv+sub, c.bia, nil); rec.Code != http.StatusNotFound {
			t.Fatalf("bia GET %s: status %d, quero 404", rotaPriv+sub, rec.Code)
		}
	}
	if rec := fazerReqToken(t, c.srv, http.MethodGet, rotaPriv, c.ana, nil); rec.Code != http.StatusOK {
		t.Fatalf("ana GET própria: status %d", rec.Code)
	}

	// Reordenar com um id que não enxerga → 404; só com o que enxerga → ok.
	// (bia não tem demandas.operar: dá ao admin a checagem de visão via caio com papel? Usa o admin
	// para o caso feliz e um usuário comum para o 404 antes da permissão? A permissão
	// vem antes no middleware, então o 404 de visão é testado com um operador.)
	recPapel := fazerReqToken(t, c.srv, http.MethodPost, "/api/v1/roles", c.admin, map[string]any{
		"nome": "opera", "permissoes": []string{"demandas.operar"},
	})
	var papel struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal(recPapel.Body.Bytes(), &papel)
	recUser := fazerReqToken(t, c.srv, http.MethodPost, "/api/v1/users", c.admin, map[string]any{
		"nome": "Op", "email": "op@x.com", "senha": "senha-forte-123", "ativo": true, "papeis": []int64{papel.ID},
	})
	if recUser.Code != http.StatusCreated {
		t.Fatalf("criar operador: status %d (corpo=%q)", recUser.Code, recUser.Body.String())
	}
	op := loginToken(t, c.srv, "op@x.com", "senha-forte-123")
	if rec := fazerReqToken(t, c.srv, http.MethodPut, "/api/v1/demands/ordem", op, map[string]any{"ids": []int64{privada.ID, publica.ID}}); rec.Code != http.StatusNotFound {
		t.Fatalf("reordenar com demanda invisível: status %d, quero 404 (corpo=%q)", rec.Code, rec.Body.String())
	}
	if rec := fazerReqToken(t, c.srv, http.MethodPut, "/api/v1/demands/ordem", op, map[string]any{"ids": []int64{publica.ID}}); rec.Code != http.StatusOK {
		t.Fatalf("reordenar só com a pública: status %d (corpo=%q)", rec.Code, rec.Body.String())
	}

	// Atividade recente: evento da demanda privada não chega a quem não a vê.
	atividade := func(token string) []db.Evento {
		t.Helper()
		rec := fazerReqToken(t, c.srv, http.MethodGet, "/api/v1/activity", token, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("activity: status %d (corpo=%q)", rec.Code, rec.Body.String())
		}
		var evs []db.Evento
		_ = json.Unmarshal(rec.Body.Bytes(), &evs)
		return evs
	}
	temDaPrivada := func(evs []db.Evento) bool {
		for _, e := range evs {
			if e.DemandID != nil && *e.DemandID == privada.ID {
				return true
			}
		}
		return false
	}
	if !temDaPrivada(atividade(c.ana)) {
		t.Fatal("ana deveria ver o evento de criação da própria demanda")
	}
	if temDaPrivada(atividade(c.caio)) {
		t.Fatal("evento da demanda privada vazou na atividade de caio")
	}

	// Mudar a visibilidade: quem não vê recebe 404; dono → 204; não-dono que vê → 403; admin → 204.
	rotaVis := rotaPriv + "/visibilidade"
	if rec := fazerReqToken(t, c.srv, http.MethodPut, rotaVis, c.bia, map[string]any{"visibilidade": "publica"}); rec.Code != http.StatusNotFound {
		t.Fatalf("bia alterando demanda que não vê: status %d, quero 404", rec.Code)
	}
	if rec := fazerReqToken(t, c.srv, http.MethodPut, rotaVis, c.ana, map[string]any{"visibilidade": "grupo"}); rec.Code != http.StatusNoContent {
		t.Fatalf("ana → grupo: status %d (corpo=%q)", rec.Code, rec.Body.String())
	}
	if n := contar(c.bia, "/api/v1/demands"); n != 2 {
		t.Fatalf("bia após grupo vê %d, quero 2", n)
	}
	if n := contar(c.caio, "/api/v1/demands"); n != 1 {
		t.Fatalf("caio após grupo vê %d, quero 1", n)
	}
	if rec := fazerReqToken(t, c.srv, http.MethodPut, rotaVis, c.bia, map[string]any{"visibilidade": "publica"}); rec.Code != http.StatusForbidden {
		t.Fatalf("bia (vê, não é dona) alterando: status %d, quero 403", rec.Code)
	}
	if rec := fazerReqToken(t, c.srv, http.MethodPut, rotaVis, c.admin, map[string]any{"visibilidade": "publica"}); rec.Code != http.StatusNoContent {
		t.Fatalf("admin alterando: status %d", rec.Code)
	}
	if temDaPrivada(atividade(c.caio)) == false {
		t.Fatal("tornada pública, o evento da demanda deveria aparecer para caio")
	}
	// Criação com valor inválido.
	if rec := fazerReqToken(t, c.srv, http.MethodPost, rotaCriar, c.ana, map[string]any{"prd": "x", "visibilidade": "oculta"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("criar com visibilidade inválida: status %d, quero 400", rec.Code)
	}
}

func TestDemandaHerdaVisibilidadeDoPlanejamento(t *testing.T) {
	banco := abrirBancoTemp(t)
	srv := Novo(Opcoes{Banco: banco, Planejamentos: &estrategistaFake{raiz: t.TempDir()}, Intake: &analisadorFake{banco: banco}})
	proj := criarProjetoNomeado(t, srv, "Herança")
	admin := setupAdmin(t, srv)

	rec := fazerReqToken(t, srv, http.MethodPost, "/api/v1/planejamentos", admin,
		map[string]any{"project_id": proj, "mensagem": "portal", "visibilidade": "publica"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("criar planejamento: status %d (corpo=%q)", rec.Code, rec.Body.String())
	}
	plan := decodPlanejamento(t, rec)
	ctx := context.Background()
	if _, err := banco.SalvarRevisaoDocumento(ctx, plan.ID, "prd.md", "# PRD\n\nRequisitos…"); err != nil {
		t.Fatal(err)
	}
	// o planejamento fica ocioso para aceitar o handoff.
	plan.Status = db.StatusPlanejamentoOcioso
	if _, err := banco.AtualizarPlanejamento(ctx, plan); err != nil {
		t.Fatal(err)
	}
	rec = fazerReqToken(t, srv, http.MethodPost, fmt.Sprintf("/api/v1/planejamentos/%d/criar-demanda", plan.ID), admin, map[string]any{})
	if rec.Code != http.StatusCreated {
		t.Fatalf("criar demanda do planejamento: status %d (corpo=%q)", rec.Code, rec.Body.String())
	}
	dem := decodDemanda(t, rec)
	if dem.Visibilidade != "publica" {
		t.Fatalf("demanda herdada = %q, quero publica (a do planejamento)", dem.Visibilidade)
	}
}
