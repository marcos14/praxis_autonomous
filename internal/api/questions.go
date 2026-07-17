package api

import (
	"net/http"

	"github.com/marcos14/praxis-autonomous/internal/db"
)

// handleListarPerguntas devolve as perguntas do analista para a demanda (aba
// Perguntas do card). Ordem crescente. Demanda inexistente → 404.
func (s *Servidor) handleListarPerguntas(w http.ResponseWriter, r *http.Request) {
	dem, ok := s.obterDemandaOu404(w, r)
	if !ok {
		return
	}
	perguntas, err := s.banco.ListarPerguntas(r.Context(), dem.ID)
	if err != nil {
		s.responderErroDemanda(w, err)
		return
	}
	responderJSON(w, http.StatusOK, perguntas)
}

// reqRespostas é o corpo de POST /demands/{id}/answers: as respostas do usuário
// às perguntas do analista ("responder tudo e gerar plano").
type reqRespostas struct {
	Respostas []db.RespostaPergunta `json:"respostas"`
}

// handleResponderPerguntas persiste as respostas do usuário e avança a demanda de
// `aguardando_respostas` para `planejando` (o planejador a retoma na Fase 3c).
// Respostas de perguntas de outra demanda são ignoradas (guarda no store).
//
// Semântica de status: só é possível responder uma demanda que está aguardando
// respostas (409 caso contrário). A transição para `planejando` é o gatilho do
// planejador — mesmo sem perguntas respondidas (o usuário pode gerar o plano
// direto quando a análise não fez perguntas).
func (s *Servidor) handleResponderPerguntas(w http.ResponseWriter, r *http.Request) {
	dem, ok := s.obterDemandaOu404(w, r)
	if !ok {
		return
	}
	if dem.Status != db.StatusDemandaAguardandoRespostas {
		responderErro(w, http.StatusConflict, "estado_invalido",
			"só é possível responder uma demanda aguardando respostas (status atual: "+dem.Status+")")
		return
	}

	var req reqRespostas
	if !decodificarCorpo(w, r, &req) {
		return
	}

	if len(req.Respostas) > 0 {
		if _, err := s.banco.ResponderPerguntas(r.Context(), dem.ID, req.Respostas); err != nil {
			s.responderErroDemanda(w, err)
			return
		}
	}

	dem.Status = db.StatusDemandaPlanejando
	atual, err := s.banco.AtualizarDemanda(r.Context(), dem)
	if err != nil {
		s.responderErroDemanda(w, err)
		return
	}

	// evento (best-effort) para alimentar a aba Eventos / SSE.
	pid, did := atual.ProjectID, atual.ID
	ev := db.Evento{
		Tipo:    "respostas_recebidas",
		Titulo:  "Praxis: respostas recebidas",
		Detalhe: "Usuário respondeu as perguntas; demanda encaminhada ao planejador.",
	}
	if pid > 0 {
		ev.ProjectID = &pid
	}
	if did > 0 {
		ev.DemandID = &did
	}
	if _, err := s.banco.RegistrarEvento(r.Context(), ev); err != nil {
		s.log.Warn("registrar evento de respostas", "erro", err, "demanda", did)
	}

	s.responderDemandaComFases(w, r, atual)
}
