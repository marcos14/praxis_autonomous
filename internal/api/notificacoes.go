package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/marcos14/praxis-autonomous/internal/db"
)

// limiteNotificacoesPadrao é quantas notificações a listagem devolve sem
// `?limite=` (a caixa de entrada do sino mostra as mais recentes).
const limiteNotificacoesPadrao = 50

// registrarRotasNotificacoes registra a caixa de entrada por usuário (M4 do
// PLANO_INTERNET): listar, marcar lida(s) e o SSE que alimenta o sino e o
// toast na aba aberta. Todas operam só sobre o usuário da requisição.
func (s *Servidor) registrarRotasNotificacoes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/notificacoes", s.handleListarNotificacoes)
	mux.HandleFunc("POST /api/v1/notificacoes/lidas", s.handleMarcarTodasLidas)
	mux.HandleFunc("POST /api/v1/notificacoes/{id}/lida", s.handleMarcarNotificacaoLida)
	mux.HandleFunc("GET /api/v1/notificacoes/stream", s.handleStreamNotificacoes)
}

// usuarioComNotificacoes devolve o id do usuário logado; tokens de API e o
// bootstrap não têm caixa de entrada → 400 e ok=false.
func usuarioComNotificacoes(w http.ResponseWriter, r *http.Request) (int64, bool) {
	pr := principalDaRequisicao(r)
	if pr.userID <= 0 {
		erroT(w, r, http.StatusBadRequest, "invalido", "erro.credencial_sem_notificacoes")
		return 0, false
	}
	return pr.userID, true
}

// respListaNotificacoes é a resposta da listagem: os itens e o total de não
// lidas (o contador do sino, independente do filtro/limite aplicado).
type respListaNotificacoes struct {
	Itens    []db.Notificacao `json:"itens"`
	NaoLidas int              `json:"nao_lidas"`
}

// handleListarNotificacoes lista as notificações do próprio usuário, mais
// recentes primeiro. `?nao_lidas=1` filtra; `?limite=N` limita (padrão 50).
func (s *Servidor) handleListarNotificacoes(w http.ResponseWriter, r *http.Request) {
	uid, ok := usuarioComNotificacoes(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	soNaoLidas := q.Get("nao_lidas") == "1" || q.Get("nao_lidas") == "true"
	limite := limiteNotificacoesPadrao
	if v := q.Get("limite"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limite = n
		}
	}
	itens, err := s.banco.ListarNotificacoes(r.Context(), uid, soNaoLidas, limite)
	if err != nil {
		s.log.Error("listar notificações", "erro", err)
		erroT(w, r, http.StatusInternalServerError, "erro_interno", "erro.interno")
		return
	}
	naoLidas, err := s.banco.ContarNaoLidas(r.Context(), uid)
	if err != nil {
		s.log.Error("contar notificações não lidas", "erro", err)
		erroT(w, r, http.StatusInternalServerError, "erro_interno", "erro.interno")
		return
	}
	responderJSON(w, http.StatusOK, respListaNotificacoes{Itens: itens, NaoLidas: naoLidas})
}

// handleMarcarNotificacaoLida marca uma notificação do próprio usuário como
// lida (idempotente). De outro usuário ou inexistente → 404.
func (s *Servidor) handleMarcarNotificacaoLida(w http.ResponseWriter, r *http.Request) {
	uid, ok := usuarioComNotificacoes(w, r)
	if !ok {
		return
	}
	id, ok := idDaRota(w, r)
	if !ok {
		return
	}
	if err := s.banco.MarcarNotificacaoLida(r.Context(), id, uid); err != nil {
		if errors.Is(err, db.ErrNaoEncontrado) {
			erroT(w, r, http.StatusNotFound, "nao_encontrado", "erro.notificacao_nao_encontrada")
			return
		}
		s.log.Error("marcar notificação lida", "erro", err)
		erroT(w, r, http.StatusInternalServerError, "erro_interno", "erro.interno")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleMarcarTodasLidas marca todas as não lidas do usuário; devolve quantas.
func (s *Servidor) handleMarcarTodasLidas(w http.ResponseWriter, r *http.Request) {
	uid, ok := usuarioComNotificacoes(w, r)
	if !ok {
		return
	}
	n, err := s.banco.MarcarTodasLidas(r.Context(), uid)
	if err != nil {
		s.log.Error("marcar todas as notificações lidas", "erro", err)
		erroT(w, r, http.StatusInternalServerError, "erro_interno", "erro.interno")
		return
	}
	responderJSON(w, http.StatusOK, map[string]int64{"marcadas": n})
}

// handleStreamNotificacoes transmite, via SSE, as notificações novas do
// usuário conforme surgem (`event: notificacao`, Notificacao em JSON no
// `data`). O cursor inicial é a última notificação existente (só o que vier
// depois), a menos que `?after=<id>` peça o backlog a partir daquele id — é
// como o cliente recupera o que perdeu numa reconexão. Mesmo laço do SSE de
// eventos: polling da tabela, heartbeat e encerramento no vencimento do JWT.
func (s *Servidor) handleStreamNotificacoes(w http.ResponseWriter, r *http.Request) {
	uid, ok := usuarioComNotificacoes(w, r)
	if !ok {
		return
	}
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
		if ultimo, err := s.banco.UltimaNotificacaoID(r.Context(), uid); err == nil {
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

	intervalo := s.intervaloPollEventos
	if intervalo <= 0 {
		intervalo = intervaloPollEventosPadrao
	}
	ticker := time.NewTicker(intervalo)
	defer ticker.Stop()
	ultimoTrafego := time.Now()
	for {
		novas, err := s.banco.NotificacoesApos(ctx, uid, cursor, 0)
		if err == nil && len(novas) > 0 {
			for _, n := range novas {
				b, err := json.Marshal(n)
				if err != nil {
					continue
				}
				enviarEventoSSE(w, "notificacao", string(b))
				cursor = n.ID
			}
			flusher.Flush()
			ultimoTrafego = time.Now()
		}
		select {
		case <-ctx.Done():
			avisarTokenExpirado(ctx, r, w, flusher)
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
