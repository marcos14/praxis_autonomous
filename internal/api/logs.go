package api

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/marcos14/praxis-autonomous/internal/db"
)

// handleLogsDemanda transmite, via Server-Sent Events, o log ao vivo da demanda:
// as linhas do .jsonl (runs.log_ref) da execução mais recente que já tem log.
//
// Protocolo SSE:
//   - mensagem default (campo `data`): uma linha crua do .jsonl (um JSON por
//     linha, o stream-json do motor). O frontend a formata para leitura.
//   - mensagem nomeada `event: exec`: metadados (id/operação/engine/modelo) da
//     execução cujo log passou a ser transmitido — o card usa como separador
//     quando a próxima etapa/fase começa.
//
// A conexão vive enquanto o cliente a mantém aberta (fecha o card → o navegador
// encerra o EventSource → r.Context() é cancelado → o handler retorna). Não há
// estado no servidor além da conexão.
func (s *Servidor) handleLogsDemanda(w http.ResponseWriter, r *http.Request) {
	dem, ok := s.obterDemandaOu404(w, r)
	if !ok {
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		responderErro(w, http.StatusInternalServerError, "sem_streaming", "streaming não suportado por esta conexão")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // evita buffering de proxies reversos
	w.WriteHeader(http.StatusOK)

	// Comentário inicial: confirma a abertura do stream e vence buffers intermediários.
	fmt.Fprint(w, ": conectado\n\n")
	flusher.Flush()

	s.transmitirLog(r.Context(), w, flusher, dem.ID)
}

// transmitirLog é o laço do SSE: a cada ciclo relê o .jsonl da execução-alvo e
// emite as linhas novas; ao surgir uma execução mais recente com log, troca de
// alvo (emitindo um evento `exec`). Retorna quando o contexto é cancelado.
func (s *Servidor) transmitirLog(ctx context.Context, w io.Writer, flusher http.Flusher, demandID int64) {
	intervalo := s.intervaloPollLog
	if intervalo <= 0 {
		intervalo = intervaloPollLogPadrao
	}
	ticker := time.NewTicker(intervalo)
	defer ticker.Stop()

	var (
		runAtual int64  // id da execução cujo log está sendo transmitido (0 = nenhuma)
		caminho  string // caminho do .jsonl atual
		offset   int64  // bytes já lidos (só linhas completas) do arquivo atual
	)
	for {
		// Alvo = execução mais recente da demanda que já gravou log. Enquanto a
		// fase roda, o log_ref só existe quando a etapa fecha; entre etapas o
		// alvo avança e emitimos o separador.
		if run, temLog, err := s.banco.UltimaExecucaoComLog(ctx, demandID); err == nil && temLog && run.ID != runAtual {
			runAtual = run.ID
			caminho = run.LogRef
			offset = 0
			enviarEventoSSE(w, "exec", metadadosRun(run))
			flusher.Flush()
		}
		if caminho != "" {
			linhas, novo, err := lerNovasLinhas(caminho, offset)
			if err == nil {
				offset = novo
				for _, ln := range linhas {
					enviarDadosSSE(w, ln)
				}
				if len(linhas) > 0 {
					flusher.Flush()
				}
			}
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// enviarDadosSSE escreve uma mensagem SSE default (campo `data`) com uma linha do
// log. A linha de um .jsonl é sempre um JSON de uma única linha, portanto não há
// quebra de linha interna capaz de romper o enquadramento SSE.
func enviarDadosSSE(w io.Writer, linha string) {
	fmt.Fprintf(w, "data: %s\n\n", linha)
}

// enviarEventoSSE escreve uma mensagem SSE nomeada (campos `event` + `data`).
func enviarEventoSSE(w io.Writer, evento, dados string) {
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", evento, dados)
}

// metadadosRun serializa os metadados de uma execução para o evento `exec`.
func metadadosRun(e db.Execucao) string {
	b, err := json.Marshal(struct {
		ID       int64  `json:"id"`
		Operacao string `json:"operacao"`
		Engine   string `json:"engine"`
		Modelo   string `json:"modelo"`
	}{ID: e.ID, Operacao: e.Operacao, Engine: e.Engine, Modelo: e.Modelo})
	if err != nil {
		return "{}"
	}
	return string(b)
}

// lerNovasLinhas lê caminho a partir de offset e devolve as LINHAS COMPLETAS novas
// (terminadas em \n) e o novo offset (logo após a última linha completa). Uma
// linha parcial ainda em escrita fica para a próxima leitura. Se o arquivo
// encolheu desde a última leitura (rotação/truncamento), relê do início.
func lerNovasLinhas(caminho string, offset int64) ([]string, int64, error) {
	f, err := os.Open(caminho)
	if err != nil {
		return nil, offset, err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil, offset, err
	}
	if info.Size() < offset {
		offset = 0
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return nil, offset, err
	}

	var (
		linhas    []string
		consumido int64
	)
	rd := bufio.NewReader(f)
	for {
		linha, err := rd.ReadString('\n')
		if err == io.EOF {
			// Linha parcial (sem \n): não consumimos — será relida quando completar.
			break
		}
		if err != nil {
			return linhas, offset + consumido, err
		}
		consumido += int64(len(linha))
		if ln := strings.TrimRight(linha, "\r\n"); ln != "" {
			linhas = append(linhas, ln)
		}
	}
	return linhas, offset + consumido, nil
}
