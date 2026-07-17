package api

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/marcos14/praxis-autonomous/internal/db"
)

// decodBoard decodifica a resposta do GET /api/v1/board.
func decodBoard(t *testing.T, rec *httptest.ResponseRecorder) []db.DemandaResumo {
	t.Helper()
	var b []db.DemandaResumo
	if err := json.Unmarshal(rec.Body.Bytes(), &b); err != nil {
		t.Fatalf("decodificar board: %v (corpo=%q)", err, rec.Body.String())
	}
	return b
}

func TestBoardTrazProgressoDasFases(t *testing.T) {
	banco := abrirBancoTemp(t)
	srv := Novo(Opcoes{Banco: banco})
	proj := criarProjetoTeste(t, srv)
	dem := criarDemandaTeste(t, srv, proj) // 2 fases, ambas pendentes

	rec := fazerReq(t, srv, http.MethodGet, "/api/v1/board", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, quero 200 (corpo=%q)", rec.Code, rec.Body.String())
	}
	board := decodBoard(t, rec)
	if len(board) != 1 {
		t.Fatalf("board = %d demandas, quero 1", len(board))
	}
	if board[0].FasesTotal != 2 || board[0].FasesConcluidas != 0 {
		t.Fatalf("progresso = %d/%d, quero 0/2", board[0].FasesConcluidas, board[0].FasesTotal)
	}

	// conclui uma fase e confere que o progresso avança.
	fases, err := banco.ListarFases(context.Background(), dem.ID)
	if err != nil {
		t.Fatalf("listar fases: %v", err)
	}
	fases[0].Status = db.StatusFaseConcluida
	if _, err := banco.AtualizarFase(context.Background(), fases[0]); err != nil {
		t.Fatalf("atualizar fase: %v", err)
	}
	rec = fazerReq(t, srv, http.MethodGet, "/api/v1/board", nil)
	board = decodBoard(t, rec)
	if board[0].FasesConcluidas != 1 {
		t.Fatalf("fases concluídas = %d, quero 1", board[0].FasesConcluidas)
	}
}

func TestBoardMotorDaUltimaExecucao(t *testing.T) {
	banco := abrirBancoTemp(t)
	srv := Novo(Opcoes{Banco: banco})
	proj := criarProjetoTeste(t, srv)
	dem := criarDemandaTeste(t, srv, proj)

	if _, err := banco.CriarExecucao(context.Background(), db.Execucao{
		DemandID: dem.ID, Operacao: db.OperacaoExecutor, Engine: "claude", Modelo: "opus",
	}); err != nil {
		t.Fatalf("criar execução: %v", err)
	}
	rec := fazerReq(t, srv, http.MethodGet, "/api/v1/board", nil)
	board := decodBoard(t, rec)
	if board[0].Motor != "claude" {
		t.Fatalf("motor = %q, quero claude", board[0].Motor)
	}
}

func TestReordenarDemandasAlteraPrioridade(t *testing.T) {
	banco := abrirBancoTemp(t)
	srv := Novo(Opcoes{Banco: banco})
	proj := criarProjetoTeste(t, srv)
	d1 := criarDemandaTeste(t, srv, proj)
	d2 := criarDemandaTeste(t, srv, proj)

	// Nova ordem: d1 antes de d2 (posição 0 e 1).
	rec := fazerReq(t, srv, http.MethodPut, "/api/v1/demands/ordem",
		map[string]any{"ids": []int64{d1.ID, d2.ID}})
	if rec.Code != http.StatusOK {
		t.Fatalf("reordenar: status %d (corpo=%q)", rec.Code, rec.Body.String())
	}
	demandas, err := banco.ListarDemandas(context.Background(), db.FiltroDemandas{})
	if err != nil {
		t.Fatalf("listar: %v", err)
	}
	if demandas[0].ID != d1.ID || demandas[1].ID != d2.ID {
		t.Fatalf("ordem = [%d,%d], quero [%d,%d]", demandas[0].ID, demandas[1].ID, d1.ID, d2.ID)
	}

	// Inverte a ordem.
	fazerReq(t, srv, http.MethodPut, "/api/v1/demands/ordem",
		map[string]any{"ids": []int64{d2.ID, d1.ID}})
	demandas, _ = banco.ListarDemandas(context.Background(), db.FiltroDemandas{})
	if demandas[0].ID != d2.ID {
		t.Fatalf("após inverter, topo = %d, quero %d", demandas[0].ID, d2.ID)
	}
}

func TestReordenarDemandasIDInexistente(t *testing.T) {
	srv := Novo(Opcoes{Banco: abrirBancoTemp(t)})
	rec := fazerReq(t, srv, http.MethodPut, "/api/v1/demands/ordem",
		map[string]any{"ids": []int64{999}})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, quero 404 (corpo=%q)", rec.Code, rec.Body.String())
	}
}

// TestEventosSSEGlobalEntregaNovos confirma que um evento registrado após a
// conexão chega no stream SSE global — o mecanismo que atualiza o kanban.
func TestEventosSSEGlobalEntregaNovos(t *testing.T) {
	banco := abrirBancoTemp(t)
	srv := Novo(Opcoes{Banco: banco})
	srv.intervaloPollEventos = 5 * time.Millisecond
	proj := criarProjetoTeste(t, srv)

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/api/v1/events", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET events: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, quero 200", resp.StatusCode)
	}

	dados := make(chan string, 16)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		var evento string
		for sc.Scan() {
			linha := sc.Text()
			switch {
			case linha == "":
				evento = ""
			case strings.HasPrefix(linha, ":"):
			case strings.HasPrefix(linha, "event:"):
				evento = strings.TrimSpace(strings.TrimPrefix(linha, "event:"))
			case strings.HasPrefix(linha, "data:"):
				d := strings.TrimPrefix(strings.TrimPrefix(linha, "data:"), " ")
				if evento == "evento" {
					dados <- d
				}
			}
		}
	}()

	// registra um evento após a conexão — deve chegar pelo stream.
	pid := proj
	if _, err := banco.RegistrarEvento(context.Background(), db.Evento{
		ProjectID: &pid, Tipo: "demanda_pausada", Titulo: "Praxis: teste",
	}); err != nil {
		t.Fatalf("registrar evento: %v", err)
	}

	got := esperarDado(t, dados)
	if !strings.Contains(got, "demanda_pausada") {
		t.Fatalf("evento SSE = %q, quero conter demanda_pausada", got)
	}
}

// TestEventosSSEBacklogComAfter confirma que ?after=0 entrega o backlog.
func TestEventosSSEBacklogComAfter(t *testing.T) {
	banco := abrirBancoTemp(t)
	srv := Novo(Opcoes{Banco: banco})
	srv.intervaloPollEventos = 5 * time.Millisecond
	proj := criarProjetoTeste(t, srv)

	pid := proj
	ev, err := banco.RegistrarEvento(context.Background(), db.Evento{
		ProjectID: &pid, Tipo: "demanda_criada", Titulo: "Praxis: já existia",
	})
	if err != nil {
		t.Fatalf("registrar evento: %v", err)
	}
	_ = ev

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/api/v1/events?after=0", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET events: %v", err)
	}
	defer resp.Body.Close()

	dados := make(chan string, 16)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		var evento string
		for sc.Scan() {
			linha := sc.Text()
			switch {
			case linha == "":
				evento = ""
			case strings.HasPrefix(linha, "event:"):
				evento = strings.TrimSpace(strings.TrimPrefix(linha, "event:"))
			case strings.HasPrefix(linha, "data:"):
				if evento == "evento" {
					dados <- strings.TrimPrefix(strings.TrimPrefix(linha, "data:"), " ")
				}
			}
		}
	}()
	if got := esperarDado(t, dados); !strings.Contains(got, "demanda_criada") {
		t.Fatalf("backlog SSE = %q, quero conter demanda_criada", got)
	}
}
