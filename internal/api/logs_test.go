package api

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/marcos14/praxis-autonomous/internal/db"
)

// demandaComRun cria uma demanda pronta com uma fase e uma execução cujo log_ref
// aponta para um .jsonl vazio recém-criado. Devolve o id da demanda e o caminho
// do arquivo de log (para o teste escrever linhas ao vivo).
func demandaComRun(t *testing.T, banco *db.DB, projID int64) (int64, string) {
	t.Helper()
	ctx := context.Background()
	dem, fases, err := banco.CriarDemandaComFases(ctx,
		db.Demanda{ProjectID: projID, Titulo: "com log", Status: db.StatusDemandaExecutando},
		[]db.Fase{{Codigo: "1", Titulo: "fase 1", Status: db.StatusFaseExecutando}})
	if err != nil {
		t.Fatalf("criar demanda com fases: %v", err)
	}
	logPath := filepath.Join(t.TempDir(), "fase-1-executor.jsonl")
	if err := os.WriteFile(logPath, nil, 0o644); err != nil {
		t.Fatalf("criar arquivo de log: %v", err)
	}
	faseID := fases[0].ID
	run, err := banco.CriarExecucao(ctx, db.Execucao{
		DemandID: dem.ID, PhaseID: &faseID, Operacao: db.OperacaoExecutor, Engine: "claude",
	})
	if err != nil {
		t.Fatalf("criar execução: %v", err)
	}
	run.LogRef = logPath
	run.TerminadoEm = "2026-07-16T10:00:00.000Z"
	if _, err := banco.AtualizarExecucao(ctx, run); err != nil {
		t.Fatalf("atualizar execução com log_ref: %v", err)
	}
	return dem.ID, logPath
}

// escreverLinha anexa uma linha (+\n) a um arquivo de log, como o motor faz.
func escreverLinha(t *testing.T, caminho, linha string) {
	t.Helper()
	f, err := os.OpenFile(caminho, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("abrir log para anexar: %v", err)
	}
	defer f.Close()
	if _, err := f.WriteString(linha + "\n"); err != nil {
		t.Fatalf("anexar linha: %v", err)
	}
}

// TestLogsSSEEntregaLinhas confirma que o endpoint SSE entrega as linhas já
// gravadas no .jsonl da execução, precedidas de um evento `exec`.
func TestLogsSSEEntregaLinhas(t *testing.T) {
	banco := abrirBancoTemp(t)
	srv := Novo(Opcoes{Banco: banco})
	srv.intervaloPollLog = 5 * time.Millisecond

	proj := criarProjetoTeste(t, srv)
	demID, logPath := demandaComRun(t, banco, proj)
	escreverLinha(t, logPath, `{"type":"assistant","message":{"content":[{"type":"text","text":"linha um"}]}}`)
	escreverLinha(t, logPath, `{"type":"result","subtype":"success","is_error":false,"result":"linha dois"}`)

	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/demands/"+strconv.FormatInt(demID, 10)+"/logs", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("Content-Type = %q, quero text/event-stream", ct)
	}
	corpo := rec.Body.String()
	if !strings.Contains(corpo, "event: exec") {
		t.Fatalf("corpo não traz o evento exec:\n%s", corpo)
	}
	if !strings.Contains(corpo, "linha um") || !strings.Contains(corpo, "linha dois") {
		t.Fatalf("corpo não traz as linhas do log:\n%s", corpo)
	}
}

// TestLogsSSEAppendAoVivo confirma que linhas anexadas ao .jsonl DEPOIS de a
// conexão estar aberta chegam ao cliente (tailing ao vivo).
func TestLogsSSEAppendAoVivo(t *testing.T) {
	banco := abrirBancoTemp(t)
	srv := Novo(Opcoes{Banco: banco})
	srv.intervaloPollLog = 5 * time.Millisecond

	proj := criarProjetoTeste(t, srv)
	demID, logPath := demandaComRun(t, banco, proj)
	escreverLinha(t, logPath, `{"type":"assistant","message":{"content":[{"type":"text","text":"ola"}]}}`)

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet,
		ts.URL+"/api/v1/demands/"+strconv.FormatInt(demID, 10)+"/logs", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET logs: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, quero 200", resp.StatusCode)
	}

	dados := make(chan string, 16)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		var eventoAtual string
		for sc.Scan() {
			linha := sc.Text()
			switch {
			case linha == "":
				eventoAtual = ""
			case strings.HasPrefix(linha, ":"):
				// comentário SSE (keep-alive) — ignora
			case strings.HasPrefix(linha, "event:"):
				eventoAtual = strings.TrimSpace(strings.TrimPrefix(linha, "event:"))
			case strings.HasPrefix(linha, "data:"):
				d := strings.TrimPrefix(strings.TrimPrefix(linha, "data:"), " ")
				if eventoAtual == "" { // só mensagens default carregam linha de log
					dados <- d
				}
			}
		}
	}()

	if got := esperarDado(t, dados); !strings.Contains(got, "ola") {
		t.Fatalf("1ª linha SSE = %q, quero conter 'ola'", got)
	}
	// anexa uma linha ao vivo — deve chegar sem reabrir a conexão.
	escreverLinha(t, logPath, `{"type":"result","subtype":"success","is_error":false,"result":"pronto"}`)
	if got := esperarDado(t, dados); !strings.Contains(got, "pronto") {
		t.Fatalf("2ª linha SSE (ao vivo) = %q, quero conter 'pronto'", got)
	}
}

// esperarDado lê o próximo dado do canal ou falha por timeout.
func esperarDado(t *testing.T, ch <-chan string) string {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(3 * time.Second):
		t.Fatal("timeout esperando linha do SSE")
		return ""
	}
}

// TestLogsSSEDemandaInexistente confirma 404 antes de abrir o stream.
func TestLogsSSEDemandaInexistente(t *testing.T) {
	srv := Novo(Opcoes{Banco: abrirBancoTemp(t)})
	rec := fazerReq(t, srv, http.MethodGet, "/api/v1/demands/999/logs", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, quero 404 (corpo=%q)", rec.Code, rec.Body.String())
	}
}

// TestLerNovasLinhas exercita o tailing incremental do .jsonl.
func TestLerNovasLinhas(t *testing.T) {
	caminho := filepath.Join(t.TempDir(), "l.jsonl")
	if err := os.WriteFile(caminho, []byte("a\nb\n"), 0o644); err != nil {
		t.Fatalf("escrever: %v", err)
	}
	linhas, off, err := lerNovasLinhas(caminho, 0)
	if err != nil {
		t.Fatalf("lerNovasLinhas: %v", err)
	}
	if len(linhas) != 2 || linhas[0] != "a" || linhas[1] != "b" {
		t.Fatalf("linhas = %v, quero [a b]", linhas)
	}
	if off != 4 {
		t.Fatalf("offset = %d, quero 4", off)
	}
	// linha parcial (sem \n) não é entregue e não avança o offset.
	f, _ := os.OpenFile(caminho, os.O_APPEND|os.O_WRONLY, 0o644)
	f.WriteString("parcial")
	f.Close()
	linhas, off2, err := lerNovasLinhas(caminho, off)
	if err != nil {
		t.Fatalf("lerNovasLinhas 2: %v", err)
	}
	if len(linhas) != 0 || off2 != off {
		t.Fatalf("linha parcial não deveria ser entregue: linhas=%v off=%d", linhas, off2)
	}
	// ao completar a linha, ela é entregue.
	escreverLinha(t, caminho, "-fim")
	linhas, _, err = lerNovasLinhas(caminho, off)
	if err != nil {
		t.Fatalf("lerNovasLinhas 3: %v", err)
	}
	if len(linhas) != 1 || linhas[0] != "parcial-fim" {
		t.Fatalf("linhas = %v, quero [parcial-fim]", linhas)
	}
}
