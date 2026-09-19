package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/marcos14/praxis-autonomous/internal/db"
)

// intervaloPollEventosPadrao é a cadência com que o SSE global relê a tabela
// events em busca de novas linhas. Os testes sobrescrevem para um valor pequeno.
const intervaloPollEventosPadrao = 1 * time.Second

// intervaloHeartbeatEventos é o intervalo máximo sem tráfego antes de o SSE
// global emitir um comentário de keep-alive (vence timeouts de proxy quando não
// há eventos novos).
const intervaloHeartbeatEventos = 25 * time.Second

// registrarRotasEventos registra o SSE global de eventos (Fase 4a). Alimenta o
// kanban (reagir a mudanças de status) e a "atividade recente" da Home.
func (s *Servidor) registrarRotasEventos(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/events", s.handleEventosGlobais)
}

// handleEventosGlobais transmite, via Server-Sent Events, os eventos novos
// (tabela events) conforme surgem. Cada mensagem é um `event: evento` com o
// Evento serializado em JSON no campo `data`. O cursor inicial é o último id
// existente (só transmite o que vier depois da conexão), a menos que o cliente
// informe `?after=<id>` para receber um backlog a partir daquele id.
//
// A conexão vive enquanto o cliente a mantém aberta. Sem estado no servidor: o
// tailing é por polling da tabela (mesmo modelo do SSE de log por arquivo).
func (s *Servidor) handleEventosGlobais(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		erroT(w, r, http.StatusInternalServerError, "sem_streaming", "erro.sem_streaming")
		return
	}

	cursor := int64(-1)
	if v := r.URL.Query().Get("after"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n >= 0 {
			cursor = n
		}
	}
	if cursor < 0 {
		// Sem `after`: parte do último id (só eventos novos).
		if ultimo, err := s.banco.UltimoEventoID(r.Context()); err == nil {
			cursor = ultimo
		} else {
			cursor = 0
		}
	}

	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, ": conectado\n\n")
	flusher.Flush()

	ctx, cancelar := contextoDoStream(r)
	defer cancelar()
	s.transmitirEventos(ctx, w, flusher, cursor, visibilidadeDaRequisicao(r))
	avisarTokenExpirado(ctx, r, w, flusher)
}

// transmitirEventos é o laço do SSE global: a cada ciclo lê os eventos com id >
// cursor e os emite, avançando o cursor. Emite um heartbeat quando fica muito
// tempo sem novidade. Retorna quando o contexto é cancelado.
func (s *Servidor) transmitirEventos(ctx context.Context, w io.Writer, flusher http.Flusher, cursor int64, visiveisPara *int64) {
	intervalo := s.intervaloPollEventos
	if intervalo <= 0 {
		intervalo = intervaloPollEventosPadrao
	}
	ticker := time.NewTicker(intervalo)
	defer ticker.Stop()

	ultimoTrafego := time.Now()
	for {
		novos, err := s.banco.EventosApos(ctx, cursor, 0, visiveisPara)
		if err == nil && len(novos) > 0 {
			for _, ev := range novos {
				enviarEventoSSE(w, "evento", serializarEvento(ev))
				cursor = ev.ID
			}
			flusher.Flush()
			ultimoTrafego = time.Now()
		}

		select {
		case <-ctx.Done():
			return
		case t := <-ticker.C:
			if t.Sub(ultimoTrafego) >= intervaloHeartbeatEventos {
				fmt.Fprint(w, ": ping\n\n")
				flusher.Flush()
				ultimoTrafego = t
			}
		}
	}
}

// serializarEvento serializa um Evento para o campo data do SSE.
func serializarEvento(ev db.Evento) string {
	b, err := json.Marshal(ev)
	if err != nil {
		return "{}"
	}
	return string(b)
}
