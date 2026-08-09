package api

import (
	"github.com/marcos14/praxis-autonomous/internal/i18n"
	"net/http"
	"os"
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
		erroT(w, r, http.StatusConflict, "estado_invalido", "erro.plano_editar_fases_estado", "status", dem.Status)
		return
	}

	var req reqEditarFases
	if !decodificarCorpo(w, r, &req) {
		return
	}
	if len(req.Fases) == 0 {
		erroT(w, r, http.StatusBadRequest, "invalido", "erro.plano_ao_menos_uma_fase")
		return
	}
	fases, msg := validarFasesReq(req.Fases)
	if msg != "" {
		responderErro(w, http.StatusBadRequest, "invalido", msg)
		return
	}

	if _, err := s.banco.SubstituirFases(r.Context(), dem.ID, fases); err != nil {
		s.responderErroDemanda(w, r, err)
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
		erroT(w, r, http.StatusConflict, "estado_invalido", "erro.plano_aprovar_estado", "status", dem.Status)
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
		s.responderErroDemanda(w, r, err)
		return
	}
	if len(fases) == 0 {
		erroT(w, r, http.StatusConflict, "estado_invalido", "erro.plano_sem_fases")
		return
	}

	dem.Status = db.StatusDemandaPronta
	atual, err := s.banco.AtualizarDemanda(r.Context(), dem)
	if err != nil {
		s.responderErroDemanda(w, r, err)
		return
	}

	s.registrarEventoDemanda(r, atual, "plano_aprovado", i18n.TI("evento.plano_aprovado"),
		i18n.TI("evento.plano_aprovado_detalhe"))
	s.responderDemandaComFases(w, r, atual)
}

// rejeitarPlano registra o comentário do usuário no chat, volta a demanda para
// `planejando` e redispara o planejador (replanejar).
func (s *Servidor) rejeitarPlano(w http.ResponseWriter, r *http.Request, dem db.Demanda, comentario string) {
	if comentario == "" {
		erroT(w, r, http.StatusBadRequest, "invalido", "erro.plano_comentario_obrigatorio")
		return
	}

	// o comentário vira uma fala do usuário no chat (orienta o replanejamento).
	if _, err := s.banco.CriarMensagemChat(r.Context(), db.MensagemChat{
		DemandID: dem.ID, Papel: db.PapelUser, Conteudo: comentario,
	}); err != nil {
		s.responderErroDemanda(w, r, err)
		return
	}

	dem.Status = db.StatusDemandaPlanejando
	atual, err := s.banco.AtualizarDemanda(r.Context(), dem)
	if err != nil {
		s.responderErroDemanda(w, r, err)
		return
	}

	s.registrarEventoDemanda(r, atual, "plano_rejeitado", i18n.TI("evento.plano_rejeitado"),
		i18n.TI("evento.plano_rejeitado_detalhe"))

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
		erroT(w, r, http.StatusConflict, "estado_invalido", "erro.plano_demanda_encerrada", "status", dem.Status)
		return
	}

	codigo := strings.TrimSpace(r.PathValue("codigo"))
	if codigo == "" {
		erroT(w, r, http.StatusBadRequest, "invalido", "erro.plano_codigo_fase")
		return
	}

	fases, err := s.banco.ListarFases(r.Context(), dem.ID)
	if err != nil {
		s.responderErroDemanda(w, r, err)
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
		erroT(w, r, http.StatusNotFound, "nao_encontrado", "erro.plano_fase_nao_encontrada")
		return
	}
	if !alvo.RequerHumano {
		erroT(w, r, http.StatusConflict, "estado_invalido", "erro.plano_fase_automatica")
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
		s.responderErroDemanda(w, r, err)
		return
	}
	s.registrarEventoDemanda(r, dem, "fase_humana_concluida", i18n.TI("evento.fase_humana_concluida"),
		"Fase "+codigo+" ("+alvo.Titulo+") marcada como concluída por um humano; execução liberada.")

	// Se a demanda estava pausada aguardando intervenção humana, volta à fila para
	// o scheduler seguir com as próximas fases. (Uma pausa por outro motivo do
	// usuário permanece — ele retoma quando quiser.)
	atual := dem
	if dem.Status == db.StatusDemandaPausada {
		dem.Status = db.StatusDemandaPronta
		atual, err = s.banco.AtualizarDemanda(r.Context(), dem)
		if err != nil {
			s.responderErroDemanda(w, r, err)
			return
		}
		s.registrarEventoDemanda(r, atual, "demanda_retomada", i18n.TI("evento.demanda_retomada.titulo"),
			i18n.TI("evento.demanda_retomada_detalhe"))
	}

	s.responderDemandaComFases(w, r, atual)
}

// handleReiniciarFase força o reinício de uma fase automática travada:
// interrompe o run em andamento da demanda (se houver), DESCARTA as mudanças não
// commitadas do worktree (sobra do run interrompido) e devolve a fase a
// `pendente` — ela recomeça do zero na próxima passada do scheduler. É a saída
// pela UI para uma fase presa (ex.: `executando` órfã após uma queda, ou um run
// que não progride) sem mexer no banco à mão.
//
// Regras: a demanda precisa estar num estado de execução (pronta, executando,
// pausada, aguardando franquia ou falhou); a fase precisa existir, ser
// automática (fase requer_humano não é executada pelo scheduler — use "Marcar
// como feito") e estar executando/pausada/falhou. Fase pendente é no-op
// idempotente (já vai rodar); concluída → 409 (as seguintes podem ter construído
// sobre o resultado dela). Exige demandas.operar.
func (s *Servidor) handleReiniciarFase(w http.ResponseWriter, r *http.Request) {
	dem, ok := s.obterDemandaOu404(w, r)
	if !ok {
		return
	}
	if !exigirPermissao(w, r, db.PermDemandasOperar) {
		return
	}
	switch dem.Status {
	case db.StatusDemandaPronta, db.StatusDemandaExecutando, db.StatusDemandaPausada,
		db.StatusDemandaAguardandoFranquia, db.StatusDemandaFalhou:
		// ok: estados de execução, dá para reiniciar uma fase.
	default:
		erroT(w, r, http.StatusConflict, "estado_invalido", "erro.plano_reiniciar_estado", "status", dem.Status)
		return
	}

	codigo := strings.TrimSpace(r.PathValue("codigo"))
	if codigo == "" {
		erroT(w, r, http.StatusBadRequest, "invalido", "erro.plano_codigo_fase")
		return
	}
	fases, err := s.banco.ListarFases(r.Context(), dem.ID)
	if err != nil {
		s.responderErroDemanda(w, r, err)
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
		erroT(w, r, http.StatusNotFound, "nao_encontrado", "erro.plano_fase_nao_encontrada")
		return
	}
	if alvo.RequerHumano {
		erroT(w, r, http.StatusConflict, "estado_invalido", "erro.plano_fase_humana")
		return
	}
	switch alvo.Status {
	case db.StatusFasePendente:
		// idempotente: já está na fila para rodar.
		s.responderDemandaComFases(w, r, dem)
		return
	case db.StatusFaseConcluida:
		erroT(w, r, http.StatusConflict, "estado_invalido", "erro.plano_fase_concluida")
		return
	}

	// 1) tira a demanda da fila (pausada) ANTES de mexer no worktree: o scheduler
	// não despacha demanda pausada, então nenhum run novo começa no meio do
	// descarte. O status final (pronta) é gravado no passo 4.
	dem.Status = db.StatusDemandaPausada
	if dem, err = s.banco.AtualizarDemanda(r.Context(), dem); err != nil {
		s.responderErroDemanda(w, r, err)
		return
	}

	// 2) aborta o run em andamento, se houver (o pipeline trata como pausa).
	interrompido := false
	if s.exec != nil {
		interrompido = s.exec.Interromper(dem.ID)
	}

	// 3) descarta a sobra não commitada do run interrompido — a fase recomeça do
	// zero. Uma falha aqui não bloqueia o reinício (a pré-checagem de árvore
	// limpa da fase dá um erro claro e o usuário pode reiniciar de novo).
	descarte := ""
	if dir := strings.TrimSpace(dem.WorktreePath); dir != "" {
		if _, statErr := os.Stat(dir); statErr == nil {
			descarte = i18n.TI("evento.fase_reiniciada_descartado")
			if err := s.descartarComRetentativas(dir); err != nil {
				descarte = "Não consegui descartar as mudanças não commitadas do worktree: " + err.Error()
				s.log.Warn("reiniciar fase: descartar mudanças do worktree", "erro", err, "demanda", dem.ID)
			}
		}
	}

	// 4) fase volta ao zero (pendente) e a demanda à fila (pronta).
	alvo.Status = db.StatusFasePendente
	alvo.Observacao = "reiniciada manualmente pelo usuário"
	if _, err := s.banco.AtualizarFase(r.Context(), *alvo); err != nil {
		s.responderErroDemanda(w, r, err)
		return
	}
	dem.Status = db.StatusDemandaPronta
	dem.Erro = ""
	atual, err := s.banco.AtualizarDemanda(r.Context(), dem)
	if err != nil {
		s.responderErroDemanda(w, r, err)
		return
	}
	if s.exec != nil {
		// se a demanda tinha falhado, a marca em memória do scheduler impediria o
		// redespacho; reenfileirar é no-op nos demais casos.
		s.exec.Reenfileirar(dem.ID)
	}

	detalhe := "Fase " + codigo + " (" + alvo.Titulo + ") devolvida a pendente pelo usuário; ela recomeça do zero."
	if interrompido {
		detalhe += " O run em andamento foi interrompido."
	}
	if descarte != "" {
		detalhe += " " + descarte
	}
	s.registrarEventoDemanda(r, atual, "fase_reiniciada", i18n.TI("evento.fase_reiniciada"), detalhe)
	s.responderDemandaComFases(w, r, atual)
}

// descartarComRetentativas descarta as mudanças não commitadas do worktree
// tolerando o processo do harness recém-interrompido ainda segurar arquivos
// (locks do Windows): tenta algumas vezes com uma pausa curta entre elas.
func (s *Servidor) descartarComRetentativas(dir string) error {
	if s.git == nil {
		return nil
	}
	var err error
	for tent := 0; tent < 4; tent++ {
		if tent > 0 {
			time.Sleep(500 * time.Millisecond)
		}
		if err = s.git.DescartarMudancas(dir); err == nil {
			return nil
		}
	}
	return err
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
