package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/marcos14/praxis-autonomous/internal/db"
)

// reqEditarFases é o corpo de PUT /demands/{id}/phases: o conjunto COMPLETO de
// fases após a edição do usuário na aba Plano & Fases (editar/reordenar/remover/
// exigir humano). A ordem é a posição no array.
type reqEditarFases struct {
	Fases []reqFaseNova `json:"fases"`
}

// handleEditarFases substitui TODAS as fases da demanda pelas informadas (Fase
// 3c). É como a aba Plano & Fases persiste edições: reordenar (a nova ordem é a
// do array), remover (a fase some do array), editar título/código/dependências e
// marcar "exige humano". Só é permitido enquanto a demanda aguarda aprovação —
// depois disso as fases estão em execução e não devem ser reescritas em massa.
func (s *Servidor) handleEditarFases(w http.ResponseWriter, r *http.Request) {
	dem, ok := s.obterDemandaOu404(w, r)
	if !ok {
		return
	}
	if dem.Status != db.StatusDemandaAguardandoAprovacao {
		responderErro(w, http.StatusConflict, "estado_invalido",
			"só é possível editar as fases enquanto a demanda aguarda aprovação (status atual: "+dem.Status+")")
		return
	}

	var req reqEditarFases
	if !decodificarCorpo(w, r, &req) {
		return
	}
	if len(req.Fases) == 0 {
		responderErro(w, http.StatusBadRequest, "invalido", "informe ao menos uma fase")
		return
	}
	fases, msg := validarFasesReq(req.Fases)
	if msg != "" {
		responderErro(w, http.StatusBadRequest, "invalido", msg)
		return
	}

	if _, err := s.banco.SubstituirFases(r.Context(), dem.ID, fases); err != nil {
		s.responderErroDemanda(w, err)
		return
	}
	s.responderDemandaComFases(w, r, dem)
}

// reqAprovarPlano é o corpo de POST /demands/{id}/approve-plan.
type reqAprovarPlano struct {
	Aprovar    bool   `json:"aprovar"`
	Comentario string `json:"comentario"`
}

// handleAprovarPlano decide o plano de uma demanda que aguarda aprovação (Fase
// 3c):
//   - aprovar=true → aguardando_aprovacao → pronta (o scheduler passa a conduzir
//     as fases). Exige ao menos uma fase.
//   - aprovar=false → rejeição com comentário: o comentário vira uma fala do
//     usuário no chat, a demanda volta a `planejando` e o planejador é redisparado
//     (replanejar). O comentário é obrigatório (é o que orienta o novo plano).
//
// Só é possível decidir uma demanda que está aguardando aprovação (409 caso
// contrário).
func (s *Servidor) handleAprovarPlano(w http.ResponseWriter, r *http.Request) {
	dem, ok := s.obterDemandaOu404(w, r)
	if !ok {
		return
	}
	if dem.Status != db.StatusDemandaAguardandoAprovacao {
		responderErro(w, http.StatusConflict, "estado_invalido",
			"só é possível aprovar/rejeitar uma demanda aguardando aprovação (status atual: "+dem.Status+")")
		return
	}

	var req reqAprovarPlano
	if !decodificarCorpo(w, r, &req) {
		return
	}

	if req.Aprovar {
		s.aprovarPlano(w, r, dem)
		return
	}
	s.rejeitarPlano(w, r, dem, strings.TrimSpace(req.Comentario))
}

// aprovarPlano transita a demanda para `pronta` (aprovação do plano).
func (s *Servidor) aprovarPlano(w http.ResponseWriter, r *http.Request, dem db.Demanda) {
	fases, err := s.banco.ListarFases(r.Context(), dem.ID)
	if err != nil {
		s.responderErroDemanda(w, err)
		return
	}
	if len(fases) == 0 {
		responderErro(w, http.StatusConflict, "estado_invalido",
			"não é possível aprovar um plano sem fases")
		return
	}

	dem.Status = db.StatusDemandaPronta
	atual, err := s.banco.AtualizarDemanda(r.Context(), dem)
	if err != nil {
		s.responderErroDemanda(w, err)
		return
	}

	s.registrarEventoDemanda(r, atual, "plano_aprovado", "Praxis: plano aprovado",
		"Plano aprovado pelo usuário; demanda pronta para execução.")
	s.responderDemandaComFases(w, r, atual)
}

// rejeitarPlano registra o comentário do usuário no chat, volta a demanda para
// `planejando` e redispara o planejador (replanejar).
func (s *Servidor) rejeitarPlano(w http.ResponseWriter, r *http.Request, dem db.Demanda, comentario string) {
	if comentario == "" {
		responderErro(w, http.StatusBadRequest, "invalido",
			"informe um comentário explicando o que ajustar no plano")
		return
	}

	// o comentário vira uma fala do usuário no chat (orienta o replanejamento).
	if _, err := s.banco.CriarMensagemChat(r.Context(), db.MensagemChat{
		DemandID: dem.ID, Papel: db.PapelUser, Conteudo: comentario,
	}); err != nil {
		s.responderErroDemanda(w, err)
		return
	}

	dem.Status = db.StatusDemandaPlanejando
	atual, err := s.banco.AtualizarDemanda(r.Context(), dem)
	if err != nil {
		s.responderErroDemanda(w, err)
		return
	}

	s.registrarEventoDemanda(r, atual, "plano_rejeitado", "Praxis: plano rejeitado",
		"Usuário rejeitou o plano com comentário; replanejando.")

	if s.planejamento != nil {
		s.planejamento.DispararPlanejamento(atual.ID)
	}
	s.responderDemandaComFases(w, r, atual)
}

// handleConcluirFaseHumana marca uma fase que exige intervenção humana
// (requer_humano) como concluída — o "Requer humano: feito". O scheduler NUNCA
// executa uma fase requer_humano sozinho: quando só restam fases assim, a
// demanda fica `pausada` aguardando alguém fazer o trabalho manual (migração,
// deploy, aprovação externa…) e sinalizar a conclusão aqui. Ao concluir a fase,
// se a demanda estava pausada por isso, ela volta para `pronta` e o scheduler
// segue com as próximas fases.
//
// Regras: a demanda não pode estar encerrada; a fase precisa existir, ser
// requer_humano e estar pendente/pausada (já concluída → 200 idempotente; fase
// automática → 409, ela não se conclui à mão). Exige demandas.operar.
func (s *Servidor) handleConcluirFaseHumana(w http.ResponseWriter, r *http.Request) {
	dem, ok := s.obterDemandaOu404(w, r)
	if !ok {
		return
	}
	if !exigirPermissao(w, r, db.PermDemandasOperar) {
		return
	}
	if statusTerminal(dem.Status) {
		responderErro(w, http.StatusConflict, "estado_invalido",
			"não é possível concluir fases de uma demanda encerrada (status atual: "+dem.Status+")")
		return
	}

	codigo := strings.TrimSpace(r.PathValue("codigo"))
	if codigo == "" {
		responderErro(w, http.StatusBadRequest, "invalido", "informe o código da fase")
		return
	}

	fases, err := s.banco.ListarFases(r.Context(), dem.ID)
	if err != nil {
		s.responderErroDemanda(w, err)
		return
	}
	var alvo *db.Fase
	for i := range fases {
		if fases[i].Codigo == codigo {
			alvo = &fases[i]
			break
		}
	}
	if alvo == nil {
		responderErro(w, http.StatusNotFound, "nao_encontrado", "fase não encontrada nesta demanda")
		return
	}
	if !alvo.RequerHumano {
		responderErro(w, http.StatusConflict, "estado_invalido",
			"esta fase não exige intervenção humana; ela é conduzida automaticamente")
		return
	}
	if alvo.Status == db.StatusFaseConcluida {
		// idempotente: já concluída.
		s.responderDemandaComFases(w, r, dem)
		return
	}
	if alvo.Status != db.StatusFasePendente && alvo.Status != db.StatusFasePausada {
		responderErro(w, http.StatusConflict, "estado_invalido",
			"só é possível concluir manualmente uma fase pendente (status da fase: "+alvo.Status+")")
		return
	}

	alvo.Status = db.StatusFaseConcluida
	alvo.ConcluidoEm = time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	alvo.Observacao = "concluída manualmente (requer humano)"
	if _, err := s.banco.AtualizarFase(r.Context(), *alvo); err != nil {
		s.responderErroDemanda(w, err)
		return
	}
	s.registrarEventoDemanda(r, dem, "fase_humana_concluida", "Praxis: fase concluída manualmente",
		"Fase "+codigo+" ("+alvo.Titulo+") marcada como concluída por um humano; execução liberada.")

	// Se a demanda estava pausada aguardando intervenção humana, volta à fila para
	// o scheduler seguir com as próximas fases. (Uma pausa por outro motivo do
	// usuário permanece — ele retoma quando quiser.)
	atual := dem
	if dem.Status == db.StatusDemandaPausada {
		dem.Status = db.StatusDemandaPronta
		atual, err = s.banco.AtualizarDemanda(r.Context(), dem)
		if err != nil {
			s.responderErroDemanda(w, err)
			return
		}
		s.registrarEventoDemanda(r, atual, "demanda_retomada", "Praxis: demanda retomada",
			"Fase humana concluída; demanda de volta à fila do scheduler.")
	}

	s.responderDemandaComFases(w, r, atual)
}

// registrarEventoDemanda grava um evento associado à demanda (best-effort: uma
// falha só vira log, não quebra a resposta).
func (s *Servidor) registrarEventoDemanda(r *http.Request, dem db.Demanda, tipo, titulo, detalhe string) {
	pid, did := dem.ProjectID, dem.ID
	ev := db.Evento{Tipo: tipo, Titulo: titulo, Detalhe: detalhe}
	if pid > 0 {
		ev.ProjectID = &pid
	}
	if did > 0 {
		ev.DemandID = &did
	}
	if _, err := s.banco.RegistrarEvento(r.Context(), ev); err != nil {
		s.log.Warn("registrar evento da demanda", "erro", err, "tipo", tipo, "demanda", did)
	}
}
