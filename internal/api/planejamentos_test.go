package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marcos14/praxis-autonomous/internal/db"
)

// estrategistaFake registra os turnos disparados e resolve as pastas de
// trabalho numa raiz temporária (simula o estrategista sem harness).
type estrategistaFake struct {
	disparos []int64
	raiz     string
}

func (f *estrategistaFake) DispararResposta(id int64) { f.disparos = append(f.disparos, id) }
func (f *estrategistaFake) Pasta(id int64) string {
	return filepath.Join(f.raiz, fmt.Sprintf("p%d", id))
}

func decodPlanejamento(t *testing.T, rec *httptest.ResponseRecorder) db.Planejamento {
	t.Helper()
	var p db.Planejamento
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatalf("decodificar planejamento: %v (%s)", err, rec.Body.String())
	}
	return p
}

func TestPlanejamentosCRUDViaAPI(t *testing.T) {
	banco := abrirBancoTemp(t)
	fake := &estrategistaFake{raiz: t.TempDir()}
	srv := Novo(Opcoes{Banco: banco, Planejamentos: fake})
	projID := criarProjetoTeste(t, srv)

	// Alvo ambíguo é recusado.
	rec := fazerReq(t, srv, "POST", "/api/v1/planejamentos",
		map[string]any{"project_id": projID, "group_id": 1, "mensagem": "x"})
	if rec.Code != 400 {
		t.Fatalf("alvo ambíguo = %d, quero 400", rec.Code)
	}
	// Foco inválido é recusado.
	rec = fazerReq(t, srv, "POST", "/api/v1/planejamentos",
		map[string]any{"project_id": projID, "mensagem": "x", "foco": "roadmap"})
	if rec.Code != 400 {
		t.Fatalf("foco inválido = %d, quero 400", rec.Code)
	}

	// Criação dispara o primeiro turno e nasce pensando.
	rec = fazerReq(t, srv, "POST", "/api/v1/planejamentos", map[string]any{
		"project_id": projID, "mensagem": "quero um portal de boletos",
		"foco": "ambos", "nivel_visual": "prototipo",
	})
	if rec.Code != 201 {
		t.Fatalf("criar = %d (%s), quero 201", rec.Code, rec.Body.String())
	}
	plan := decodPlanejamento(t, rec)
	if plan.Status != db.StatusPlanejamentoPensando || plan.Foco != "ambos" || plan.NivelVisual != "prototipo" {
		t.Fatalf("criado = %+v", plan)
	}
	if len(fake.disparos) != 1 || fake.disparos[0] != plan.ID {
		t.Fatalf("disparos = %v, quero o turno do planejamento criado", fake.disparos)
	}

	// Lista com nome do projeto resolvido.
	rec = fazerReq(t, srv, "GET", "/api/v1/planejamentos", nil)
	if rec.Code != 200 {
		t.Fatalf("listar = %d", rec.Code)
	}
	var lista []db.Planejamento
	_ = json.Unmarshal(rec.Body.Bytes(), &lista)
	if len(lista) != 1 || lista[0].ProjetoNome == "" {
		t.Fatalf("lista = %+v, quero 1 com projeto_nome", lista)
	}

	// Chat com turno em voo é recusado (409).
	rec = fazerReq(t, srv, "POST", fmt.Sprintf("/api/v1/planejamentos/%d/chat", plan.ID),
		map[string]any{"conteudo": "mais uma coisa"})
	if rec.Code != 409 {
		t.Fatalf("chat pensando = %d, quero 409", rec.Code)
	}
	// PUT com turno em voo idem.
	rec = fazerReq(t, srv, "PUT", fmt.Sprintf("/api/v1/planejamentos/%d", plan.ID),
		map[string]any{"foco": "prd"})
	if rec.Code != 409 {
		t.Fatalf("editar pensando = %d, quero 409", rec.Code)
	}

	// Turno concluído (simulado): chat volta a funcionar e re-dispara.
	plan.Status = db.StatusPlanejamentoOcioso
	if _, err := banco.AtualizarPlanejamento(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	rec = fazerReq(t, srv, "POST", fmt.Sprintf("/api/v1/planejamentos/%d/chat", plan.ID),
		map[string]any{"conteudo": "inclui multa e juros"})
	if rec.Code != 202 {
		t.Fatalf("chat ocioso = %d (%s), quero 202", rec.Code, rec.Body.String())
	}
	if len(fake.disparos) != 2 {
		t.Fatalf("disparos = %v, quero 2 turnos", fake.disparos)
	}

	// Ajuste de preferências entre turnos.
	plan.Status = db.StatusPlanejamentoOcioso
	if _, err := banco.AtualizarPlanejamento(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	rec = fazerReq(t, srv, "PUT", fmt.Sprintf("/api/v1/planejamentos/%d", plan.ID),
		map[string]any{"foco": "prd", "nivel_visual": "documento"})
	if rec.Code != 200 {
		t.Fatalf("editar = %d (%s), quero 200", rec.Code, rec.Body.String())
	}
	editado := decodPlanejamento(t, rec)
	if editado.Foco != "prd" || editado.NivelVisual != "documento" {
		t.Fatalf("editado = %+v", editado)
	}

	// Exclusão remove o banco e a pasta de trabalho.
	pasta := fake.Pasta(plan.ID)
	if err := os.MkdirAll(pasta, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pasta, "prd.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec = fazerReq(t, srv, "DELETE", fmt.Sprintf("/api/v1/planejamentos/%d", plan.ID), nil)
	if rec.Code != 204 {
		t.Fatalf("excluir = %d, quero 204", rec.Code)
	}
	if _, err := os.Stat(pasta); !os.IsNotExist(err) {
		t.Fatalf("pasta do planejamento deveria ter sido removida: %v", err)
	}
	if _, err := banco.ObterPlanejamento(context.Background(), plan.ID); err == nil {
		t.Fatal("planejamento deveria ter sido excluído do banco")
	}
}

func TestArtefatoServidoComCSPSandbox(t *testing.T) {
	banco := abrirBancoTemp(t)
	fake := &estrategistaFake{raiz: t.TempDir()}
	srv := Novo(Opcoes{Banco: banco, Planejamentos: fake})
	projID := criarProjetoTeste(t, srv)

	plan, _, err := banco.CriarPlanejamentoComChat(context.Background(),
		db.Planejamento{ProjectID: &projID}, db.MensagemPlanejamento{Conteudo: "x"})
	if err != nil {
		t.Fatal(err)
	}
	pasta := fake.Pasta(plan.ID)
	if err := os.MkdirAll(pasta, 0o755); err != nil {
		t.Fatal(err)
	}
	html := "<h1>Apresentação</h1><script>alert(1)</script>"
	if err := os.WriteFile(filepath.Join(pasta, "apresentacao.html"), []byte(html), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := banco.UpsertArtefatoPlanejamento(context.Background(), db.ArtefatoPlanejamento{
		PlanejamentoID: plan.ID, Arquivo: "apresentacao.html", Titulo: "Plano", Hash: "h"}); err != nil {
		t.Fatal(err)
	}

	// Artefato indexado é servido com a jaula de sandbox.
	rec := fazerReq(t, srv, "GET", fmt.Sprintf("/api/v1/planejamentos/%d/artefatos/apresentacao.html", plan.ID), nil)
	if rec.Code != 200 {
		t.Fatalf("servir = %d (%s), quero 200", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Security-Policy"); got != "sandbox allow-scripts allow-popups" {
		t.Fatalf("CSP = %q, quero a jaula de sandbox sem allow-same-origin", got)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Fatalf("Content-Type = %q", ct)
	}
	if rec.Body.String() != html {
		t.Fatalf("conteúdo divergente: %q", rec.Body.String())
	}

	// Arquivo que existe no disco mas NÃO está indexado não é servido.
	if err := os.WriteFile(filepath.Join(pasta, "avulso.html"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec = fazerReq(t, srv, "GET", fmt.Sprintf("/api/v1/planejamentos/%d/artefatos/avulso.html", plan.ID), nil)
	if rec.Code != 404 {
		t.Fatalf("não indexado = %d, quero 404", rec.Code)
	}

	// Nome fora do padrão (extensão errada / traversal) é recusado.
	for _, nome := range []string{"evil.txt", "..%2Fprd.md", ".oculto.html"} {
		rec = fazerReq(t, srv, "GET", fmt.Sprintf("/api/v1/planejamentos/%d/artefatos/%s", plan.ID, nome), nil)
		if rec.Code != 400 && rec.Code != 404 {
			t.Fatalf("nome %q = %d, quero 400/404", nome, rec.Code)
		}
	}

	// A lista de artefatos devolve o índice.
	rec = fazerReq(t, srv, "GET", fmt.Sprintf("/api/v1/planejamentos/%d/artefatos", plan.ID), nil)
	if rec.Code != 200 {
		t.Fatalf("listar artefatos = %d", rec.Code)
	}
	var arts []db.ArtefatoPlanejamento
	_ = json.Unmarshal(rec.Body.Bytes(), &arts)
	if len(arts) != 1 || arts[0].Titulo != "Plano" {
		t.Fatalf("artefatos = %+v", arts)
	}
}

func TestCriarDemandaDePlanejamento(t *testing.T) {
	banco := abrirBancoTemp(t)
	fake := &estrategistaFake{raiz: t.TempDir()}
	analista := &analisadorFake{banco: banco}
	srv := Novo(Opcoes{Banco: banco, Planejamentos: fake, Intake: analista})
	projID := criarProjetoTeste(t, srv)

	plan, _, err := banco.CriarPlanejamentoComChat(context.Background(),
		db.Planejamento{ProjectID: &projID, Titulo: "Portal de boletos", Foco: db.FocoPlanejamentoAmbos},
		db.MensagemPlanejamento{Conteudo: "x"})
	if err != nil {
		t.Fatal(err)
	}

	// Sem documento ainda: 409 com código claro.
	rec := fazerReq(t, srv, "POST", fmt.Sprintf("/api/v1/planejamentos/%d/criar-demanda", plan.ID), map[string]any{})
	if rec.Code != 409 {
		t.Fatalf("sem documento = %d (%s), quero 409", rec.Code, rec.Body.String())
	}

	if _, err := banco.SalvarRevisaoDocumento(context.Background(), plan.ID, "prd.md", "# PRD\n\nRequisitos…"); err != nil {
		t.Fatal(err)
	}
	if _, err := banco.SalvarRevisaoDocumento(context.Background(), plan.ID, "adrs.md", "## ADR-001\n\nDecisão…"); err != nil {
		t.Fatal(err)
	}

	rec = fazerReq(t, srv, "POST", fmt.Sprintf("/api/v1/planejamentos/%d/criar-demanda", plan.ID), map[string]any{})
	if rec.Code != 201 {
		t.Fatalf("criar demanda = %d (%s), quero 201", rec.Code, rec.Body.String())
	}
	dem := decodDemanda(t, rec)
	if dem.ProjectID != projID || dem.OrigemRef != fmt.Sprintf("planejamento #%d", plan.ID) {
		t.Fatalf("demanda = %+v", dem)
	}
	if len(analista.chamadas) != 1 || analista.chamadas[0] != dem.ID {
		t.Fatalf("analista disparado = %v, quero a demanda criada", analista.chamadas)
	}

	// O PRD do chat leva o documento e os ADRs anexados.
	msgs, err := banco.ListarMensagensChat(context.Background(), dem.ID)
	if err != nil || len(msgs) == 0 {
		t.Fatalf("chat da demanda = %v (%v)", msgs, err)
	}
	prd := msgs[0].Conteudo
	for _, trecho := range []string{"# PRD", "Decisões arquiteturais", "ADR-001"} {
		if !strings.Contains(prd, trecho) {
			t.Fatalf("PRD do handoff sem %q:\n%s", trecho, prd)
		}
	}

	// O planejamento fica vinculado e um segundo handoff é recusado.
	got, _ := banco.ObterPlanejamento(context.Background(), plan.ID)
	if got.DemandID == nil || *got.DemandID != dem.ID {
		t.Fatalf("demand_id = %v, quero %d", got.DemandID, dem.ID)
	}
	rec = fazerReq(t, srv, "POST", fmt.Sprintf("/api/v1/planejamentos/%d/criar-demanda", plan.ID), map[string]any{})
	if rec.Code != 409 {
		t.Fatalf("segundo handoff = %d, quero 409", rec.Code)
	}
}

func TestCriarDemandaDePlanejamentoDeGrupoExigeMembro(t *testing.T) {
	banco := abrirBancoTemp(t)
	fake := &estrategistaFake{raiz: t.TempDir()}
	srv := Novo(Opcoes{Banco: banco, Planejamentos: fake})
	membroID := criarProjetoTeste(t, srv)
	// segundo projeto com nome próprio (o helper usa um slug fixo).
	rec0 := fazerReq(t, srv, "POST", "/api/v1/projects", map[string]any{
		"nome": "Proj Fora do Grupo", "pasta": repoGitTemp(t),
	})
	if rec0.Code != 201 {
		t.Fatalf("criar segundo projeto: %d (%s)", rec0.Code, rec0.Body.String())
	}
	foraID := decodProjeto(t, rec0).ID

	grupo, err := banco.CriarGrupo(context.Background(),
		db.Grupo{Nome: "Sol", Slug: "sol-plan", Ativo: true}, []int64{membroID})
	if err != nil {
		t.Fatal(err)
	}
	plan, _, err := banco.CriarPlanejamentoComChat(context.Background(),
		db.Planejamento{GroupID: &grupo.ID}, db.MensagemPlanejamento{Conteudo: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := banco.SalvarRevisaoDocumento(context.Background(), plan.ID, "prd.md", "# PRD"); err != nil {
		t.Fatal(err)
	}

	// Sem project_id: 400.
	rec := fazerReq(t, srv, "POST", fmt.Sprintf("/api/v1/planejamentos/%d/criar-demanda", plan.ID), map[string]any{})
	if rec.Code != 400 {
		t.Fatalf("grupo sem project_id = %d, quero 400", rec.Code)
	}
	// Projeto fora do grupo: 400.
	rec = fazerReq(t, srv, "POST", fmt.Sprintf("/api/v1/planejamentos/%d/criar-demanda", plan.ID),
		map[string]any{"project_id": foraID})
	if rec.Code != 400 {
		t.Fatalf("projeto fora do grupo = %d, quero 400", rec.Code)
	}
	// Membro do grupo: cria.
	rec = fazerReq(t, srv, "POST", fmt.Sprintf("/api/v1/planejamentos/%d/criar-demanda", plan.ID),
		map[string]any{"project_id": membroID})
	if rec.Code != 201 {
		t.Fatalf("membro do grupo = %d (%s), quero 201", rec.Code, rec.Body.String())
	}
}
