package api

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/marcos14/praxis-autonomous/internal/db"
	"github.com/marcos14/praxis-autonomous/internal/pipeline"
)

// planejadorStub registra as demandas cujo planejamento foi disparado (Fase 3c).
type planejadorStub struct {
	mu  sync.Mutex
	ids []int64
}

func (p *planejadorStub) DispararPlanejamento(demandaID int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.ids = append(p.ids, demandaID)
}

func (p *planejadorStub) capturados() []int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]int64(nil), p.ids...)
}

// seedDemandaAguardandoAprovacao cria uma demanda em aguardando_aprovacao com
// fases, para exercitar os endpoints de plano.
func seedDemandaAguardandoAprovacao(t *testing.T, banco *db.DB) int64 {
	t.Helper()
	ctx := context.Background()
	proj, err := banco.CriarProjeto(ctx, db.Projeto{Nome: "P", Slug: "p-aprov", Pasta: `C:\repo`,
		BranchPrincipal: "main", ModoIntegracao: "merge_request", Ativo: true})
	if err != nil {
		t.Fatalf("criar projeto: %v", err)
	}
	dem, err := banco.CriarDemanda(ctx, db.Demanda{ProjectID: proj.ID, Titulo: "D",
		Status: db.StatusDemandaAguardandoAprovacao, PlanoMD: "# plano"})
	if err != nil {
		t.Fatalf("criar demanda: %v", err)
	}
	if _, err := banco.SubstituirFases(ctx, dem.ID, []db.Fase{
		{Codigo: "1", Titulo: "Fundação"},
		{Codigo: "2", Titulo: "API", DependeDe: []string{"1"}},
	}); err != nil {
		t.Fatalf("substituir fases: %v", err)
	}
	return dem.ID
}

func TestResponderPerguntasDisparaPlanejador(t *testing.T) {
	banco := abrirBancoTemp(t)
	stub := &planejadorStub{}
	srv := Novo(Opcoes{Banco: banco, Planejamento: stub})
	dem, _ := seedDemandaAguardandoRespostas(t, banco)

	rec := fazerReq(t, srv, http.MethodPost, "/api/v1/demands/"+strconv.FormatInt(dem, 10)+"/answers",
		map[string]any{"respostas": []map[string]any{}})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (corpo=%q)", rec.Code, rec.Body.String())
	}
	if got := stub.capturados(); len(got) != 1 || got[0] != dem {
		t.Fatalf("planejamento disparado = %v, quero [%d]", got, dem)
	}
}

func TestEditarFasesSubstitui(t *testing.T) {
	banco := abrirBancoTemp(t)
	srv := Novo(Opcoes{Banco: banco})
	dem := seedDemandaAguardandoAprovacao(t, banco)

	rec := fazerReq(t, srv, http.MethodPut, "/api/v1/demands/"+strconv.FormatInt(dem, 10)+"/phases",
		map[string]any{"fases": []map[string]any{
			{"codigo": "a", "titulo": "Nova primeira", "requer_humano": true},
			{"codigo": "b", "titulo": "Nova segunda", "depende_de": []string{"a"}},
			{"codigo": "c", "titulo": "Nova terceira", "depende_de": []string{"b"}},
		}})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (corpo=%q)", rec.Code, rec.Body.String())
	}
	d := decodDemanda(t, rec)
	if len(d.Fases) != 3 {
		t.Fatalf("len fases = %d, quero 3", len(d.Fases))
	}
	if d.Fases[0].Codigo != "a" || !d.Fases[0].RequerHumano {
		t.Fatalf("fase editada mal persistida: %+v", d.Fases[0])
	}
	if d.Fases[0].Ordem != 1 || d.Fases[2].Ordem != 3 {
		t.Fatalf("ordem não reatribuída: %+v", d.Fases)
	}
}

func TestEditarFasesDependenciaInexistente(t *testing.T) {
	banco := abrirBancoTemp(t)
	srv := Novo(Opcoes{Banco: banco})
	dem := seedDemandaAguardandoAprovacao(t, banco)

	rec := fazerReq(t, srv, http.MethodPut, "/api/v1/demands/"+strconv.FormatInt(dem, 10)+"/phases",
		map[string]any{"fases": []map[string]any{
			{"codigo": "1", "titulo": "x", "depende_de": []string{"99"}},
		}})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, quero 400 (corpo=%q)", rec.Code, rec.Body.String())
	}
}

func TestEditarFasesStatusInvalido(t *testing.T) {
	banco := abrirBancoTemp(t)
	srv := Novo(Opcoes{Banco: banco})
	dem, _ := seedDemandaAguardandoRespostas(t, banco) // não está aguardando aprovação

	rec := fazerReq(t, srv, http.MethodPut, "/api/v1/demands/"+strconv.FormatInt(dem, 10)+"/phases",
		map[string]any{"fases": []map[string]any{{"codigo": "1", "titulo": "x"}}})
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, quero 409", rec.Code)
	}
}

func TestAprovarPlano(t *testing.T) {
	banco := abrirBancoTemp(t)
	srv := Novo(Opcoes{Banco: banco})
	dem := seedDemandaAguardandoAprovacao(t, banco)

	rec := fazerReq(t, srv, http.MethodPost, "/api/v1/demands/"+strconv.FormatInt(dem, 10)+"/approve-plan",
		map[string]any{"aprovar": true})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (corpo=%q)", rec.Code, rec.Body.String())
	}
	d := decodDemanda(t, rec)
	if d.Status != db.StatusDemandaPronta {
		t.Fatalf("status = %q, quero pronta", d.Status)
	}
	evs, _ := banco.ListarEventos(context.Background(), db.FiltroEventos{DemandID: &dem})
	if !temEvento(evs, "plano_aprovado") {
		t.Fatalf("evento plano_aprovado não registrado: %+v", evs)
	}
}

func TestAprovarPlanoSemFases(t *testing.T) {
	banco := abrirBancoTemp(t)
	srv := Novo(Opcoes{Banco: banco})
	ctx := context.Background()
	proj, _ := banco.CriarProjeto(ctx, db.Projeto{Nome: "P", Slug: "p-vazio", Pasta: `C:\r`,
		BranchPrincipal: "main", ModoIntegracao: "merge_request", Ativo: true})
	dem, _ := banco.CriarDemanda(ctx, db.Demanda{ProjectID: proj.ID, Titulo: "D",
		Status: db.StatusDemandaAguardandoAprovacao})

	rec := fazerReq(t, srv, http.MethodPost, "/api/v1/demands/"+strconv.FormatInt(dem.ID, 10)+"/approve-plan",
		map[string]any{"aprovar": true})
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, quero 409 (aprovar sem fases)", rec.Code)
	}
}

func TestAprovarPlanoStatusInvalido(t *testing.T) {
	banco := abrirBancoTemp(t)
	srv := Novo(Opcoes{Banco: banco})
	dem, _ := seedDemandaAguardandoRespostas(t, banco) // não está aguardando aprovação

	rec := fazerReq(t, srv, http.MethodPost, "/api/v1/demands/"+strconv.FormatInt(dem, 10)+"/approve-plan",
		map[string]any{"aprovar": true})
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, quero 409", rec.Code)
	}
}

func TestRejeitarPlanoComComentarioReplaneja(t *testing.T) {
	banco := abrirBancoTemp(t)
	stub := &planejadorStub{}
	srv := Novo(Opcoes{Banco: banco, Planejamento: stub})
	dem := seedDemandaAguardandoAprovacao(t, banco)

	rec := fazerReq(t, srv, http.MethodPost, "/api/v1/demands/"+strconv.FormatInt(dem, 10)+"/approve-plan",
		map[string]any{"aprovar": false, "comentario": "Divida a fase 2 em API e UI."})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (corpo=%q)", rec.Code, rec.Body.String())
	}
	d := decodDemanda(t, rec)
	if d.Status != db.StatusDemandaPlanejando {
		t.Fatalf("status = %q, quero planejando", d.Status)
	}
	// comentário virou fala do usuário no chat.
	msgs, _ := banco.ListarMensagensChat(context.Background(), dem)
	achou := false
	for _, m := range msgs {
		if m.Papel == db.PapelUser && m.Conteudo == "Divida a fase 2 em API e UI." {
			achou = true
		}
	}
	if !achou {
		t.Fatalf("comentário não registrado no chat: %+v", msgs)
	}
	// replanejamento disparado.
	if got := stub.capturados(); len(got) != 1 || got[0] != dem {
		t.Fatalf("replanejamento disparado = %v, quero [%d]", got, dem)
	}
	evs, _ := banco.ListarEventos(context.Background(), db.FiltroEventos{DemandID: &dem})
	if !temEvento(evs, "plano_rejeitado") {
		t.Fatalf("evento plano_rejeitado não registrado: %+v", evs)
	}
}

func TestRejeitarPlanoSemComentario(t *testing.T) {
	banco := abrirBancoTemp(t)
	srv := Novo(Opcoes{Banco: banco})
	dem := seedDemandaAguardandoAprovacao(t, banco)

	rec := fazerReq(t, srv, http.MethodPost, "/api/v1/demands/"+strconv.FormatInt(dem, 10)+"/approve-plan",
		map[string]any{"aprovar": false, "comentario": "   "})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, quero 400 (rejeitar sem comentário)", rec.Code)
	}
}

// seedDemandaPausadaHumano cria uma demanda `pausada` (como o scheduler a deixa
// ao só restarem fases requer_humano) com uma fase humana pendente e uma fase
// automática que depende dela, para exercitar a conclusão manual.
func seedDemandaPausadaHumano(t *testing.T, banco *db.DB) int64 {
	t.Helper()
	ctx := context.Background()
	proj, err := banco.CriarProjeto(ctx, db.Projeto{Nome: "P", Slug: "p-humano", Pasta: `C:\repo`,
		BranchPrincipal: "main", ModoIntegracao: "merge_request", Ativo: true})
	if err != nil {
		t.Fatalf("criar projeto: %v", err)
	}
	dem, err := banco.CriarDemanda(ctx, db.Demanda{ProjectID: proj.ID, Titulo: "D",
		Status: db.StatusDemandaPausada})
	if err != nil {
		t.Fatalf("criar demanda: %v", err)
	}
	if _, err := banco.SubstituirFases(ctx, dem.ID, []db.Fase{
		{Codigo: "1", Titulo: "Migração manual do banco", RequerHumano: true},
		{Codigo: "2", Titulo: "API", DependeDe: []string{"1"}},
	}); err != nil {
		t.Fatalf("substituir fases: %v", err)
	}
	return dem.ID
}

func TestConcluirFaseHumanaLiberaExecucao(t *testing.T) {
	banco := abrirBancoTemp(t)
	srv := Novo(Opcoes{Banco: banco})
	dem := seedDemandaPausadaHumano(t, banco)

	rec := fazerReq(t, srv, http.MethodPost,
		"/api/v1/demands/"+strconv.FormatInt(dem, 10)+"/phases/1/complete", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (corpo=%q)", rec.Code, rec.Body.String())
	}
	d := decodDemanda(t, rec)
	// demanda pausada por humano volta à fila (pronta).
	if d.Status != db.StatusDemandaPronta {
		t.Fatalf("status = %q, quero pronta", d.Status)
	}
	// a fase humana ficou concluída; a automática segue pendente.
	var f1, f2 db.Fase
	for _, f := range d.Fases {
		switch f.Codigo {
		case "1":
			f1 = f
		case "2":
			f2 = f
		}
	}
	if f1.Status != db.StatusFaseConcluida {
		t.Fatalf("fase 1 = %q, quero concluida", f1.Status)
	}
	if f1.ConcluidoEm == "" {
		t.Fatalf("fase 1 sem concluido_em")
	}
	if f2.Status != db.StatusFasePendente {
		t.Fatalf("fase 2 = %q, quero pendente", f2.Status)
	}
	evs, _ := banco.ListarEventos(context.Background(), db.FiltroEventos{DemandID: &dem})
	if !temEvento(evs, "fase_humana_concluida") {
		t.Fatalf("evento fase_humana_concluida não registrado: %+v", evs)
	}
}

func TestConcluirFaseHumanaIdempotente(t *testing.T) {
	banco := abrirBancoTemp(t)
	srv := Novo(Opcoes{Banco: banco})
	dem := seedDemandaPausadaHumano(t, banco)
	base := "/api/v1/demands/" + strconv.FormatInt(dem, 10) + "/phases/1/complete"

	if rec := fazerReq(t, srv, http.MethodPost, base, nil); rec.Code != http.StatusOK {
		t.Fatalf("1ª conclusão: status = %d", rec.Code)
	}
	// segunda chamada: idempotente (fase já concluída) → 200, sem erro.
	if rec := fazerReq(t, srv, http.MethodPost, base, nil); rec.Code != http.StatusOK {
		t.Fatalf("2ª conclusão (idempotente): status = %d (corpo=%q)", rec.Code, rec.Body.String())
	}
}

func TestConcluirFaseNaoHumanaRejeita(t *testing.T) {
	banco := abrirBancoTemp(t)
	srv := Novo(Opcoes{Banco: banco})
	dem := seedDemandaPausadaHumano(t, banco)

	// fase 2 não é requer_humano — não pode ser concluída à mão.
	rec := fazerReq(t, srv, http.MethodPost,
		"/api/v1/demands/"+strconv.FormatInt(dem, 10)+"/phases/2/complete", nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, quero 409 (fase automática)", rec.Code)
	}
}

func TestConcluirFaseInexistente(t *testing.T) {
	banco := abrirBancoTemp(t)
	srv := Novo(Opcoes{Banco: banco})
	dem := seedDemandaPausadaHumano(t, banco)

	rec := fazerReq(t, srv, http.MethodPost,
		"/api/v1/demands/"+strconv.FormatInt(dem, 10)+"/phases/99/complete", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, quero 404 (fase inexistente)", rec.Code)
	}
}

// temEvento informa se algum evento tem o tipo dado.
func temEvento(evs []db.Evento, tipo string) bool {
	for _, e := range evs {
		if e.Tipo == tipo {
			return true
		}
	}
	return false
}

// seedDemandaFaseTravada cria projeto + demanda com worktree real (repo git com
// commit inicial e uma sobra NÃO commitada) e fases: 1 presa em `executando`
// (órfã de uma queda) e 2 pendente dependendo dela. É o cenário do "Reiniciar ↻".
func seedDemandaFaseTravada(t *testing.T, banco *db.DB, status string) (int64, string) {
	t.Helper()
	ctx := context.Background()

	wt := t.TempDir()
	gitTeste(t, wt, "init", "-q", "-b", "main")
	gitTeste(t, wt, "config", "user.email", "t@praxis.local")
	gitTeste(t, wt, "config", "user.name", "Praxis Teste")
	if err := os.WriteFile(filepath.Join(wt, "a.txt"), []byte("v0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitTeste(t, wt, "add", "-A")
	gitTeste(t, wt, "commit", "-q", "-m", "inicial")
	// sobra do run interrompido (não commitada).
	if err := os.WriteFile(filepath.Join(wt, "sobra.txt"), []byte("parcial\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	proj, err := banco.CriarProjeto(ctx, db.Projeto{Nome: "P", Slug: "p-travada-" + status, Pasta: wt,
		BranchPrincipal: "main", ModoIntegracao: "merge_request", Ativo: true})
	if err != nil {
		t.Fatalf("criar projeto: %v", err)
	}
	dem, err := banco.CriarDemanda(ctx, db.Demanda{ProjectID: proj.ID, Titulo: "D",
		Status: status, WorktreePath: wt})
	if err != nil {
		t.Fatalf("criar demanda: %v", err)
	}
	if _, err := banco.SubstituirFases(ctx, dem.ID, []db.Fase{
		{Codigo: "1", Titulo: "Presa", Status: db.StatusFaseExecutando},
		{Codigo: "2", Titulo: "Dependente", DependeDe: []string{"1"}},
		{Codigo: "3", Titulo: "Homologação", RequerHumano: true},
	}); err != nil {
		t.Fatalf("substituir fases: %v", err)
	}
	return dem.ID, wt
}

// TestReiniciarFaseDestravaEExecutaDoZero: o reinício forçado interrompe o run,
// descarta a sobra não commitada do worktree, devolve a fase a `pendente` e a
// demanda a `pronta` (com Reenfileirar), registrando o evento.
func TestReiniciarFaseDestravaEExecutaDoZero(t *testing.T) {
	banco := abrirBancoTemp(t)
	ctl := &ctlFake{rodando: map[int64]bool{}}
	srv := Novo(Opcoes{Banco: banco, Exec: ctl})
	dem, wt := seedDemandaFaseTravada(t, banco, db.StatusDemandaExecutando)
	ctl.rodando[dem] = true

	rec := fazerReq(t, srv, http.MethodPost,
		"/api/v1/demands/"+strconv.FormatInt(dem, 10)+"/phases/1/restart", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (corpo=%q)", rec.Code, rec.Body.String())
	}
	d := decodDemanda(t, rec)
	if d.Status != db.StatusDemandaPronta {
		t.Fatalf("demanda = %q, quero pronta", d.Status)
	}
	for _, f := range d.Fases {
		if f.Codigo == "1" {
			if f.Status != db.StatusFasePendente {
				t.Fatalf("fase 1 = %q, quero pendente", f.Status)
			}
			if !strings.Contains(f.Observacao, "reiniciada manualmente") {
				t.Fatalf("observação da fase 1 = %q", f.Observacao)
			}
		}
	}
	// o run em andamento foi interrompido e a demanda reenfileirada.
	if len(ctl.interrompidas) != 1 || ctl.interrompidas[0] != dem {
		t.Fatalf("Interromper não foi chamado: %v", ctl.interrompidas)
	}
	if len(ctl.reenfileiradas) != 1 || ctl.reenfileiradas[0] != dem {
		t.Fatalf("Reenfileirar não foi chamado: %v", ctl.reenfileiradas)
	}
	// a sobra não commitada foi descartada; o commit inicial permanece.
	if _, err := os.Stat(filepath.Join(wt, "sobra.txt")); !os.IsNotExist(err) {
		t.Fatalf("sobra.txt deveria ter sido descartada (err=%v)", err)
	}
	if _, err := os.Stat(filepath.Join(wt, "a.txt")); err != nil {
		t.Fatalf("a.txt (commitado) não deveria sumir: %v", err)
	}
	evs, _ := banco.ListarEventos(context.Background(), db.FiltroEventos{DemandID: &dem})
	if !temEvento(evs, "fase_reiniciada") {
		t.Fatalf("evento fase_reiniciada não registrado: %+v", evs)
	}
}

// TestReiniciarFaseRegras: requer_humano e concluída → 409; pendente → 200
// idempotente; inexistente → 404; demanda fora dos estados de execução → 409.
func TestReiniciarFaseRegras(t *testing.T) {
	banco := abrirBancoTemp(t)
	srv := Novo(Opcoes{Banco: banco})
	dem, _ := seedDemandaFaseTravada(t, banco, db.StatusDemandaPausada)
	base := "/api/v1/demands/" + strconv.FormatInt(dem, 10) + "/phases/"

	// fase requer_humano não é reiniciável (não roda sozinha).
	if rec := fazerReq(t, srv, http.MethodPost, base+"3/restart", nil); rec.Code != http.StatusConflict {
		t.Fatalf("requer_humano: status = %d, quero 409", rec.Code)
	}
	// fase pendente: no-op idempotente.
	if rec := fazerReq(t, srv, http.MethodPost, base+"2/restart", nil); rec.Code != http.StatusOK {
		t.Fatalf("pendente: status = %d, quero 200 (corpo=%q)", rec.Code, rec.Body.String())
	}
	// pendente é no-op: a demanda NÃO deve ter sido refilada.
	if cur, _ := banco.ObterDemanda(context.Background(), dem); cur.Status != db.StatusDemandaPausada {
		t.Fatalf("no-op não deveria mudar a demanda: %q", cur.Status)
	}
	// fase inexistente.
	if rec := fazerReq(t, srv, http.MethodPost, base+"99/restart", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("inexistente: status = %d, quero 404", rec.Code)
	}
	// fase concluída não se reinicia.
	ctx := context.Background()
	fases, _ := banco.ListarFases(ctx, dem)
	for _, f := range fases {
		if f.Codigo == "1" {
			f.Status = db.StatusFaseConcluida
			if _, err := banco.AtualizarFase(ctx, f); err != nil {
				t.Fatal(err)
			}
		}
	}
	if rec := fazerReq(t, srv, http.MethodPost, base+"1/restart", nil); rec.Code != http.StatusConflict {
		t.Fatalf("concluída: status = %d, quero 409", rec.Code)
	}
	// demanda fora dos estados de execução.
	demAprov, err := banco.CriarDemanda(ctx, db.Demanda{ProjectID: 1, Titulo: "aprov",
		Status: db.StatusDemandaAguardandoAprovacao})
	if err != nil {
		t.Fatal(err)
	}
	if rec := fazerReq(t, srv, http.MethodPost,
		"/api/v1/demands/"+strconv.FormatInt(demAprov.ID, 10)+"/phases/1/restart", nil); rec.Code != http.StatusConflict {
		t.Fatalf("aguardando_aprovacao: status = %d, quero 409", rec.Code)
	}
}

// TestReiniciarFaseDescartaOsCommitsDeResguardoDaFase: reiniciar precisa ser um
// recomeço de verdade. Quando uma fase falha, a pipeline guarda o que ela chegou
// a produzir num commit de resguardo (pipeline/residuo.go) — sem desfazê-lo, a
// fase voltaria a rodar em cima do trabalho que o usuário acabou de mandar jogar
// fora. O que NÃO é resguardo dessa fase continua intocado.
func TestReiniciarFaseDescartaOsCommitsDeResguardoDaFase(t *testing.T) {
	banco := abrirBancoTemp(t)
	ctl := &ctlFake{rodando: map[int64]bool{}}
	srv := Novo(Opcoes{Banco: banco, Exec: ctl})
	dem, wt := seedDemandaFaseTravada(t, banco, db.StatusDemandaFalhou)

	// resguardo da fase 1, no topo da branch.
	if err := os.WriteFile(filepath.Join(wt, "parcial.txt"), []byte("meio caminho\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitTeste(t, wt, "add", "-A")
	gitTeste(t, wt, "commit", "-q", "-m", pipeline.AssuntoParcial("1", "Presa"))

	rec := fazerReq(t, srv, http.MethodPost,
		"/api/v1/demands/"+strconv.FormatInt(dem, 10)+"/phases/1/restart", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (corpo=%q)", rec.Code, rec.Body.String())
	}

	if _, err := os.Stat(filepath.Join(wt, "parcial.txt")); !os.IsNotExist(err) {
		t.Fatalf("o commit de resguardo deveria ter sido desfeito (err=%v)", err)
	}
	if _, err := os.Stat(filepath.Join(wt, "a.txt")); err != nil {
		t.Fatalf("a.txt (commit anterior à fase) não deveria sumir: %v", err)
	}
	out, err := exec.Command("git", "-C", wt, "log", "--format=%s").CombinedOutput()
	if err != nil {
		t.Fatalf("git log: %v — %s", err, out)
	}
	if strings.Contains(string(out), pipeline.MarcadorParcial) {
		t.Fatalf("histórico ainda tem resguardo da fase:\n%s", out)
	}
}
