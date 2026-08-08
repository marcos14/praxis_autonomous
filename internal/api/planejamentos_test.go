package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
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

	// O vínculo registra a revisão entregue (PRD rev 1, ADRs rev 1).
	vinculos, err := banco.ListarDemandasDoPlanejamento(context.Background(), plan.ID)
	if err != nil || len(vinculos) != 1 {
		t.Fatalf("vínculos = %+v (%v), quero 1", vinculos, err)
	}
	if vinculos[0].DemandID != dem.ID || vinculos[0].Tipo != db.TipoDemandaPlanejamentoCompleta ||
		vinculos[0].PRDRev != 1 || vinculos[0].ADRsRev != 1 {
		t.Fatalf("vínculo = %+v, quero completa com PRD/ADRs rev 1", vinculos[0])
	}

	// Um segundo handoff é permitido (refazer / variante A/B) e o documento que
	// evoluiu entrega a revisão nova.
	if _, err := banco.SalvarRevisaoDocumento(context.Background(), plan.ID, "prd.md", "# PRD v2\n\nRequisitos revistos…"); err != nil {
		t.Fatal(err)
	}
	rec = fazerReq(t, srv, "POST", fmt.Sprintf("/api/v1/planejamentos/%d/criar-demanda", plan.ID), map[string]any{})
	if rec.Code != 201 {
		t.Fatalf("segundo handoff = %d (%s), quero 201", rec.Code, rec.Body.String())
	}
	dem2 := decodDemanda(t, rec)

	// GET /demandas devolve os dois vínculos e as revisões atuais (drift).
	rec = fazerReq(t, srv, "GET", fmt.Sprintf("/api/v1/planejamentos/%d/demandas", plan.ID), nil)
	if rec.Code != 200 {
		t.Fatalf("listar demandas = %d", rec.Code)
	}
	var resp struct {
		Demandas     []db.VinculoPlanejamentoDemanda `json:"demandas"`
		PRDRevAtual  int64                           `json:"prd_rev_atual"`
		ADRsRevAtual int64                           `json:"adrs_rev_atual"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decodificar: %v (%s)", err, rec.Body.String())
	}
	if len(resp.Demandas) != 2 || resp.PRDRevAtual != 2 || resp.ADRsRevAtual != 1 {
		t.Fatalf("resposta = %+v, quero 2 vínculos e PRD atual na rev 2", resp)
	}
	if resp.Demandas[1].DemandID != dem2.ID || resp.Demandas[1].PRDRev != 2 {
		t.Fatalf("segundo vínculo = %+v, quero a rev 2 entregue", resp.Demandas[1])
	}

	// E a contagem aparece no planejamento.
	got, _ := banco.ObterPlanejamento(context.Background(), plan.ID)
	if got.DemandasCriadas != 2 {
		t.Fatalf("demandas_criadas = %d, quero 2", got.DemandasCriadas)
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

// fazerUpload envia um arquivo multipart (campo "arquivo") para a rota.
func fazerUpload(t *testing.T, srv *Servidor, caminho, nomeArquivo string, conteudo []byte) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("arquivo", nomeArquivo)
	if err != nil {
		t.Fatalf("montar multipart: %v", err)
	}
	if _, err := fw.Write(conteudo); err != nil {
		t.Fatalf("escrever multipart: %v", err)
	}
	_ = mw.Close()
	req := httptest.NewRequest("POST", caminho, &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func TestReferenciasDoPlanejamento(t *testing.T) {
	banco := abrirBancoTemp(t)
	fake := &estrategistaFake{raiz: t.TempDir()}
	srv := Novo(Opcoes{Banco: banco, Planejamentos: fake})
	projID := criarProjetoTeste(t, srv)

	plan, _, err := banco.CriarPlanejamentoComChat(context.Background(),
		db.Planejamento{ProjectID: &projID}, db.MensagemPlanejamento{Conteudo: "x"})
	if err != nil {
		t.Fatal(err)
	}
	base := fmt.Sprintf("/api/v1/planejamentos/%d/referencias", plan.ID)

	// Upload válido (nome com acento e espaço, como arquivos reais).
	rec := fazerUpload(t, srv, base, "Transcrição da reunião.md", []byte("## Ata\n\nDecidimos X."))
	if rec.Code != 201 {
		t.Fatalf("upload = %d (%s), quero 201", rec.Code, rec.Body.String())
	}
	// Extensão proibida é recusada.
	rec = fazerUpload(t, srv, base, "virus.exe", []byte("x"))
	if rec.Code != 400 {
		t.Fatalf("extensão proibida = %d, quero 400", rec.Code)
	}
	// Nome com traversal é recusado.
	rec = fazerUpload(t, srv, base, "..\\..\\evil.md", []byte("x"))
	if rec.Code == 201 {
		// filepath.Base neutraliza o caminho; se entrou, tem de ter virado só o nome.
		if _, err := os.Stat(filepath.Join(fake.Pasta(plan.ID), "referencias", "evil.md")); err != nil {
			t.Fatalf("upload com traversal não foi neutralizado (%d)", rec.Code)
		}
	}

	// O anexo vira fala de sistema (o estrategista fica sabendo).
	msgs, _ := banco.ListarMensagensPlanejamento(context.Background(), plan.ID)
	temFala := false
	for _, m := range msgs {
		if m.Papel == db.PapelPlanejamentoSistema && strings.Contains(m.Conteudo, "Transcrição da reunião.md") {
			temFala = true
		}
	}
	if !temFala {
		t.Fatalf("anexo não virou fala de sistema: %+v", msgs)
	}

	// Listagem devolve o arquivo com tamanho.
	rec = fazerReq(t, srv, "GET", base, nil)
	if rec.Code != 200 {
		t.Fatalf("listar = %d", rec.Code)
	}
	var refs []respReferencia
	_ = json.Unmarshal(rec.Body.Bytes(), &refs)
	achou := false
	for _, ref := range refs {
		if ref.Arquivo == "Transcrição da reunião.md" && ref.Tamanho > 0 {
			achou = true
		}
	}
	if !achou {
		t.Fatalf("listagem = %+v, quero a transcrição com tamanho", refs)
	}

	// Download sempre como attachment (referência nunca é exibida no domínio).
	rec = fazerReq(t, srv, "GET", base+"/"+"Transcri%C3%A7%C3%A3o%20da%20reuni%C3%A3o.md", nil)
	if rec.Code != 200 {
		t.Fatalf("download = %d (%s)", rec.Code, rec.Body.String())
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.HasPrefix(cd, "attachment") {
		t.Fatalf("Content-Disposition = %q, quero attachment", cd)
	}
	if !strings.Contains(rec.Body.String(), "Decidimos X.") {
		t.Fatalf("conteúdo divergente: %q", rec.Body.String())
	}

	// Exclusão remove o arquivo e registra fala.
	rec = fazerReq(t, srv, "DELETE", base+"/"+"Transcri%C3%A7%C3%A3o%20da%20reuni%C3%A3o.md", nil)
	if rec.Code != 204 {
		t.Fatalf("excluir = %d, quero 204", rec.Code)
	}
	rec = fazerReq(t, srv, "GET", base+"/"+"Transcri%C3%A7%C3%A3o%20da%20reuni%C3%A3o.md", nil)
	if rec.Code != 404 {
		t.Fatalf("baixar excluída = %d, quero 404", rec.Code)
	}
}

func TestCriacaoComAnexosPendentesSeguraOTurno(t *testing.T) {
	banco := abrirBancoTemp(t)
	fake := &estrategistaFake{raiz: t.TempDir()}
	srv := Novo(Opcoes{Banco: banco, Planejamentos: fake})
	projID := criarProjetoTeste(t, srv)

	// Com anexos pendentes: nasce ocioso e NÃO dispara o estrategista.
	rec := fazerReq(t, srv, "POST", "/api/v1/planejamentos", map[string]any{
		"project_id": projID, "mensagem": "use a ata anexada", "anexos_pendentes": true,
	})
	if rec.Code != 201 {
		t.Fatalf("criar = %d (%s)", rec.Code, rec.Body.String())
	}
	plan := decodPlanejamento(t, rec)
	if plan.Status != db.StatusPlanejamentoOcioso {
		t.Fatalf("status = %q, quero ocioso (turno segurado)", plan.Status)
	}
	if len(fake.disparos) != 0 {
		t.Fatalf("disparos = %v, quero nenhum antes dos anexos", fake.disparos)
	}

	// Sobe a referência e dispara o turno explicitamente.
	rec = fazerUpload(t, srv, fmt.Sprintf("/api/v1/planejamentos/%d/referencias", plan.ID),
		"ata.md", []byte("## Ata"))
	if rec.Code != 201 {
		t.Fatalf("upload = %d (%s)", rec.Code, rec.Body.String())
	}
	rec = fazerReq(t, srv, "POST", fmt.Sprintf("/api/v1/planejamentos/%d/turno", plan.ID), map[string]any{})
	if rec.Code != 202 {
		t.Fatalf("disparar turno = %d (%s), quero 202", rec.Code, rec.Body.String())
	}
	if len(fake.disparos) != 1 || fake.disparos[0] != plan.ID {
		t.Fatalf("disparos = %v, quero o turno adiado", fake.disparos)
	}
	got, _ := banco.ObterPlanejamento(context.Background(), plan.ID)
	if got.Status != db.StatusPlanejamentoPensando {
		t.Fatalf("status pós-disparo = %q, quero pensando", got.Status)
	}

	// Turno em voo: novo disparo é recusado (tentar de novo só após o desfecho).
	rec = fazerReq(t, srv, "POST", fmt.Sprintf("/api/v1/planejamentos/%d/turno", plan.ID), map[string]any{})
	if rec.Code != 409 {
		t.Fatalf("disparo com turno em voo = %d, quero 409", rec.Code)
	}

	// Após uma falha, o mesmo endpoint é o "tentar novamente" (limpa o erro).
	got.Status = db.StatusPlanejamentoFalhou
	got.Erro = "explodiu"
	if _, err := banco.AtualizarPlanejamento(context.Background(), got); err != nil {
		t.Fatal(err)
	}
	rec = fazerReq(t, srv, "POST", fmt.Sprintf("/api/v1/planejamentos/%d/turno", plan.ID), map[string]any{})
	if rec.Code != 202 {
		t.Fatalf("retry = %d, quero 202", rec.Code)
	}
	retry := decodPlanejamento(t, rec)
	if retry.Status != db.StatusPlanejamentoPensando || retry.Erro != "" {
		t.Fatalf("retry = %+v, quero pensando com erro limpo", retry)
	}
	if len(fake.disparos) != 2 {
		t.Fatalf("disparos = %v, quero 2", fake.disparos)
	}
}

func TestDownloadDeArtefatoComDisposition(t *testing.T) {
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
	if err := os.WriteFile(filepath.Join(pasta, "apresentacao.html"), []byte("<p>x</p>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := banco.UpsertArtefatoPlanejamento(context.Background(), db.ArtefatoPlanejamento{
		PlanejamentoID: plan.ID, Arquivo: "apresentacao.html", Hash: "h"}); err != nil {
		t.Fatal(err)
	}

	url := fmt.Sprintf("/api/v1/planejamentos/%d/artefatos/apresentacao.html", plan.ID)
	// Sem download: exibição (sem Content-Disposition), com a jaula CSP.
	rec := fazerReq(t, srv, "GET", url, nil)
	if rec.Code != 200 || rec.Header().Get("Content-Disposition") != "" {
		t.Fatalf("exibição = %d disposition=%q", rec.Code, rec.Header().Get("Content-Disposition"))
	}
	// Com ?download=1: attachment (e a CSP continua — inofensiva no download).
	rec = fazerReq(t, srv, "GET", url+"?download=1", nil)
	if rec.Code != 200 {
		t.Fatalf("download = %d", rec.Code)
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.HasPrefix(cd, "attachment") {
		t.Fatalf("Content-Disposition = %q, quero attachment", cd)
	}
}

func TestFalasDoPlanejamentoCarregamAutor(t *testing.T) {
	banco := abrirBancoTemp(t)
	fake := &estrategistaFake{raiz: t.TempDir()}
	srv := Novo(Opcoes{Banco: banco, Planejamentos: fake})
	token := setupAdmin(t, srv) // usuário "Root" logado — fim do modo bootstrap

	rec := fazerReqToken(t, srv, "POST", "/api/v1/projects", token, map[string]any{
		"nome": "Proj Autor", "pasta": repoGitTemp(t),
	})
	if rec.Code != 201 {
		t.Fatalf("criar projeto: %d (%s)", rec.Code, rec.Body.String())
	}
	projID := decodProjeto(t, rec).ID

	rec = fazerReqToken(t, srv, "POST", "/api/v1/planejamentos", token, map[string]any{
		"project_id": projID, "mensagem": "primeira necessidade",
	})
	if rec.Code != 201 {
		t.Fatalf("criar planejamento: %d (%s)", rec.Code, rec.Body.String())
	}
	plan := decodPlanejamento(t, rec)

	msgs, err := banco.ListarMensagensPlanejamento(context.Background(), plan.ID)
	if err != nil || len(msgs) == 0 {
		t.Fatalf("mensagens = %v (%v)", msgs, err)
	}
	var meta struct {
		Autor string `json:"autor"`
	}
	if err := json.Unmarshal(msgs[0].Meta, &meta); err != nil {
		t.Fatalf("meta: %v", err)
	}
	if meta.Autor != "Root" {
		t.Fatalf("autor = %q, quero Root (chat colaborativo mostra quem falou)", meta.Autor)
	}
}
