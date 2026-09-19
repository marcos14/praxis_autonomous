package api

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/marcos14/praxis-autonomous/internal/db"
)

// registrarRotasBoard registra as rotas do quadro kanban (Fase 4a): a listagem
// enriquecida (com progresso e motor por card) e a reordenação por prioridade.
func (s *Servidor) registrarRotasBoard(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/board", s.handleBoard)
	mux.HandleFunc("PUT /api/v1/demands/ordem", s.handleReordenarDemandas)
}

// handleBoard devolve as demandas enriquecidas com os agregados do card do
// kanban (total/concluídas de fases e motor da última execução), opcionalmente
// filtradas por ?project=<id> e ?status=<status>. É o mesmo filtro do
// GET /demands, mas com os campos que o card precisa para progresso e motor.
func (s *Servidor) handleBoard(w http.ResponseWriter, r *http.Request) {
	var filtro db.FiltroDemandas
	if v := strings.TrimSpace(r.URL.Query().Get("project")); v != "" {
		pid, err := strconv.ParseInt(v, 10, 64)
		if err != nil || pid <= 0 {
			erroT(w, r, http.StatusBadRequest, "invalido", "erro.project_invalido")
			return
		}
		filtro.ProjectID = &pid
	}
	filtro.Status = strings.TrimSpace(r.URL.Query().Get("status"))
	visao, ok := s.visaoComEscopo(w, r)
	if !ok {
		return
	}
	filtro.Visao = visao

	resumos, err := s.banco.ListarDemandasResumo(r.Context(), filtro)
	if err != nil {
		s.responderErroDemanda(w, r, err)
		return
	}
	responderJSON(w, http.StatusOK, resumos)
}

// handleReordenarDemandas reatribui a prioridade das demandas conforme a ordem
// informada (arraste no kanban — só reordena prioridade, nunca muda status).
func (s *Servidor) handleReordenarDemandas(w http.ResponseWriter, r *http.Request) {
	var req reqOrdem
	if !decodificarCorpo(w, r, &req) {
		return
	}
	if len(req.IDs) == 0 {
		erroT(w, r, http.StatusBadRequest, "invalido", "erro.ordem_sem_ids")
		return
	}
	// Os ids vieram no corpo (o middleware só checa o caminho): uma demanda que
	// este usuário não enxerga não pode ser reordenada por ele — 404, como lá.
	if v := s.visaoDaRequisicao(r); v.ACL != nil || v.Dono != nil {
		for _, id := range req.IDs {
			ve, err := s.banco.DemandaVisivel(r.Context(), id, v)
			if err != nil {
				s.responderErroDemanda(w, r, err)
				return
			}
			if !ve {
				erroT(w, r, http.StatusNotFound, "nao_encontrado", "erro.demanda_nao_encontrada")
				return
			}
		}
	}
	if err := s.banco.ReordenarDemandas(r.Context(), req.IDs); err != nil {
		s.responderErroDemanda(w, r, err)
		return
	}
	responderJSON(w, http.StatusOK, map[string]any{"ok": true})
}
