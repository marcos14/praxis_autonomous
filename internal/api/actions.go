package api

import (
	"context"
	"fmt"
	"github.com/marcos14/praxis-autonomous/internal/i18n"
	"net/http"
	"strings"

	"github.com/marcos14/praxis-autonomous/internal/db"
)

// ControladorExecucao permite à API interromper a execução AO VIVO de uma demanda
// em andamento (ações `cancelar`/`pausar`). É um seam: em produção o
// *scheduler.Scheduler o satisfaz (Interromper cancela o ctx do worker, abortando
// o run do harness). Quando nil (scheduler ainda não acoplado ao serve — pendência
// 2g.n1/M3), a ação apenas transita o status no banco: uma demanda `pausada`/
// `cancelada` deixa de ser agendável e não é mais conduzida.
type ControladorExecucao interface {
	Interromper(demandaID int64) bool
	// Reenfileirar devolve a demanda ao alcance do scheduler depois de um estado
	// terminal (a marca em memória `concluidas` impediria o redespacho) — usado
	// pela ação `tentar_novamente` ao refilar uma demanda que falhou na execução.
	Reenfileirar(demandaID int64)
}

// reqAcao é o corpo de POST /demands/{id}/actions.
type reqAcao struct {
	Acao string `json:"acao"`
}

// handleAcaoDemanda executa uma ação de controle de execução sobre a demanda
// (Fase 2i): pausar, retomar ou cancelar. Valida a transição a partir do status
// atual, persiste o novo status, registra o evento e — para pausar/cancelar —
// pede ao controlador de execução para interromper o worker em andamento.
//
// Semântica:
//   - pausar: de {pronta, executando, aguardando_franquia} → pausada. A demanda
//     deixa de ser agendada; a fase corrente termina e nenhuma outra começa
//     ("pausar interrompe entre fases"). Interromper é chamado como cortesia (não
//     mata a fase corrente à força — quem faz isso é cancelar).
//   - retomar: de {pausada, aguardando_franquia} → pronta. Volta à fila.
//   - cancelar: de qualquer estado não-terminal → cancelada (terminal). Interrompe
//     o worker em andamento imediatamente.
//
// Ação já satisfeita (ex.: pausar uma demanda já pausada) é no-op idempotente
// (200). Transição inválida → 409 estado_invalido. Ação desconhecida → 400.
func (s *Servidor) handleAcaoDemanda(w http.ResponseWriter, r *http.Request) {
	dem, ok := s.obterDemandaOu404(w, r)
	if !ok {
		return
	}
	var req reqAcao
	if !decodificarCorpo(w, r, &req) {
		return
	}

	// A permissão exigida varia com a ação: controle de execução (pausar/retomar/
	// cancelar) requer demandas.operar; as ações de integração (publicar/integrar/
	// atualizar branch, que mexem em worktree/merge) requerem integracao.gerir. O
	// middenware só garantiu que o chamador está autenticado.
	acao := strings.TrimSpace(strings.ToLower(req.Acao))
	switch acao {
	case "pausar", "retomar", "cancelar", "tentar_novamente":
		if !exigirPermissao(w, r, db.PermDemandasOperar) {
			return
		}
	case "publicar_branch", "integrar", "atualizar_branch":
		if !exigirPermissao(w, r, db.PermIntegracaoGerir) {
			return
		}
	}

	switch acao {
	case "pausar":
		s.aplicarAcao(w, r, dem, acaoPausar)
	case "retomar":
		s.aplicarAcao(w, r, dem, acaoRetomar)
	case "cancelar":
		s.aplicarAcao(w, r, dem, acaoCancelar)
	case "tentar_novamente":
		s.acaoTentarNovamente(w, r, dem)
	case "publicar_branch":
		s.acaoPublicarBranch(w, r, dem)
	case "integrar":
		s.acaoIntegrar(w, r, dem)
	case "atualizar_branch":
		s.acaoAtualizarBranch(w, r, dem)
	default:
		erroT(w, r, http.StatusBadRequest, "invalido", "erro.acao_desconhecida")
	}
}

// descAcao descreve uma ação de controle: o status-alvo, a validação de origem, o
// evento gerado e se deve interromper o worker em andamento.
type descAcao struct {
	nome        string
	alvo        string
	jaNoAlvo    func(status string) bool // no-op idempotente
	permitido   func(status string) bool // transição válida a partir do status atual
	interromper bool                     // pedir ao controlador para abortar o worker
	tipoEvento  string
	tituloEv    string
	detalheEv   string
	msgInvalido string
}

var acaoPausar = descAcao{
	nome:     "pausar",
	alvo:     db.StatusDemandaPausada,
	jaNoAlvo: func(st string) bool { return st == db.StatusDemandaPausada },
	permitido: func(st string) bool {
		return st == db.StatusDemandaPronta || st == db.StatusDemandaExecutando || st == db.StatusDemandaAguardandoFranquia
	},
	interromper: true,
	tipoEvento:  "demanda_pausada",
	tituloEv:    i18n.TI("evento.demanda_pausada.titulo"),
	detalheEv:   i18n.TI("evento.demanda_pausada.detalhe"),
	msgInvalido: "só é possível pausar uma demanda pronta, executando ou aguardando franquia",
}

var acaoRetomar = descAcao{
	nome:     "retomar",
	alvo:     db.StatusDemandaPronta,
	jaNoAlvo: func(st string) bool { return st == db.StatusDemandaPronta || st == db.StatusDemandaExecutando },
	permitido: func(st string) bool {
		return st == db.StatusDemandaPausada || st == db.StatusDemandaAguardandoFranquia
	},
	tipoEvento:  "demanda_retomada",
	tituloEv:    i18n.TI("evento.demanda_retomada.titulo"),
	detalheEv:   i18n.TI("evento.demanda_retomada.detalhe"),
	msgInvalido: "só é possível retomar uma demanda pausada ou aguardando franquia",
}

var acaoCancelar = descAcao{
	nome:        "cancelar",
	alvo:        db.StatusDemandaCancelada,
	jaNoAlvo:    func(st string) bool { return st == db.StatusDemandaCancelada },
	permitido:   func(st string) bool { return !statusTerminal(st) },
	interromper: true,
	tipoEvento:  "demanda_cancelada",
	tituloEv:    i18n.TI("evento.demanda_cancelada.titulo"),
	detalheEv:   i18n.TI("evento.demanda_cancelada.detalhe"),
	msgInvalido: "não é possível cancelar uma demanda já concluída, integrada, cancelada ou que falhou",
}

// statusTerminal informa se o status é definitivo (não admite cancelamento).
func statusTerminal(status string) bool {
	switch status {
	case db.StatusDemandaConcluida, db.StatusDemandaIntegrada,
		db.StatusDemandaCancelada, db.StatusDemandaFalhou:
		return true
	}
	return false
}

// aplicarAcao valida e aplica uma ação sobre a demanda.
func (s *Servidor) aplicarAcao(w http.ResponseWriter, r *http.Request, dem db.Demanda, a descAcao) {
	if a.jaNoAlvo(dem.Status) {
		// idempotente: já está no estado desejado.
		s.responderDemandaComFases(w, r, dem)
		return
	}
	if !a.permitido(dem.Status) {
		responderErro(w, http.StatusConflict, "estado_invalido", a.msgInvalido+" (status atual: "+dem.Status+")")
		return
	}

	dem.Status = a.alvo
	atual, err := s.banco.AtualizarDemanda(r.Context(), dem)
	if err != nil {
		s.responderErroDemanda(w, r, err)
		return
	}

	// evento (best-effort) + interrupção do worker em andamento.
	pid, did := atual.ProjectID, atual.ID
	ev := db.Evento{Tipo: a.tipoEvento, Titulo: a.tituloEv, Detalhe: a.detalheEv}
	if pid > 0 {
		ev.ProjectID = &pid
	}
	if did > 0 {
		ev.DemandID = &did
	}
	if _, err := s.banco.RegistrarEvento(r.Context(), ev); err != nil {
		s.log.Warn("registrar evento de ação da demanda", "erro", err, "acao", a.nome)
	}
	if a.interromper && s.exec != nil {
		s.exec.Interromper(atual.ID)
	}

	s.responderDemandaComFases(w, r, atual)
}

// acaoTentarNovamente reativa uma demanda `falhou`, retomando do estágio que
// falhou em vez de exigir recriá-la do zero. O caso típico é um run que estourou
// o teto de custo (error_max_budget_usd): o usuário ajusta o budget do motor e
// tenta de novo — cada estágio relê a config do banco ao rodar. O estágio é
// deduzido do estado persistido:
//   - alguma fase `falhou` → essas fases voltam a `pendente` e a demanda a
//     `pronta`; o scheduler reexecuta do ponto onde parou (worktree preservado);
//   - sem fase falhada, mas o intake já tinha passado da análise (fases de um
//     plano anterior, perguntas todas respondidas ou fala do analista no chat)
//     → replaneja (`planejando` + planejador em background);
//   - caso contrário a falha foi na análise → reanalisa (`recebida` + analista
//     em background); respostas já dadas permanecem até a nova análise
//     substituir as perguntas.
func (s *Servidor) acaoTentarNovamente(w http.ResponseWriter, r *http.Request, dem db.Demanda) {
	if dem.Status != db.StatusDemandaFalhou {
		erroT(w, r, http.StatusConflict, "estado_invalido", "erro.acao_tentar_novamente_invalida", "status", dem.Status)
		return
	}
	fases, err := s.banco.ListarFases(r.Context(), dem.ID)
	if err != nil {
		s.responderErroDemanda(w, r, err)
		return
	}
	var falhas []db.Fase
	for _, f := range fases {
		if f.Status == db.StatusFaseFalhou {
			falhas = append(falhas, f)
		}
	}

	var alvo, detalhe string
	switch {
	case len(falhas) > 0:
		for _, f := range falhas {
			f.Status = db.StatusFasePendente
			f.Observacao = ""
			if _, err := s.banco.AtualizarFase(r.Context(), f); err != nil {
				s.responderErroDemanda(w, r, err)
				return
			}
		}
		alvo = db.StatusDemandaPronta
		detalhe = fmt.Sprintf("%d fase(s) falhada(s) voltaram a pendente; demanda refilada para o scheduler reexecutar.", len(falhas))
	case s.intakePassouDaAnalise(r.Context(), dem, fases):
		alvo = db.StatusDemandaPlanejando
		detalhe = i18n.TI("evento.demanda_reativada.planejamento")
	default:
		alvo = db.StatusDemandaRecebida
		detalhe = i18n.TI("evento.demanda_reativada.analise")
	}

	dem.Status = alvo
	dem.Erro = ""
	atual, err := s.banco.AtualizarDemanda(r.Context(), dem)
	if err != nil {
		s.responderErroDemanda(w, r, err)
		return
	}

	pid, did := atual.ProjectID, atual.ID
	ev := db.Evento{Tipo: "demanda_reativada", Titulo: i18n.TI("evento.demanda_reativada.titulo"), Detalhe: detalhe}
	if pid > 0 {
		ev.ProjectID = &pid
	}
	if did > 0 {
		ev.DemandID = &did
	}
	if _, err := s.banco.RegistrarEvento(r.Context(), ev); err != nil {
		s.log.Warn("registrar evento de tentar novamente", "erro", err, "demanda", did)
	}

	switch alvo {
	case db.StatusDemandaPronta:
		if s.exec != nil {
			s.exec.Reenfileirar(atual.ID)
		}
	case db.StatusDemandaPlanejando:
		if s.planejamento != nil {
			s.planejamento.DispararPlanejamento(atual.ID)
		}
	case db.StatusDemandaRecebida:
		if s.intake != nil {
			s.intake.Disparar(atual.ID)
		}
	}

	s.responderDemandaComFases(w, r, atual)
}

// intakePassouDaAnalise informa se a demanda que falhou já tinha concluído a
// análise — a falha então foi no planejamento (primeiro plano ou replanejar).
// Sinais, em ordem: fases persistidas (um plano anterior existiu), perguntas
// todas respondidas (o usuário já submeteu as respostas e o planejador rodou) ou
// uma fala do analista no chat (análise concluída sem perguntas + plano gerado
// direto). Pergunta sem resposta indica falha na (re)análise — reanalisar.
func (s *Servidor) intakePassouDaAnalise(ctx context.Context, dem db.Demanda, fases []db.Fase) bool {
	if len(fases) > 0 {
		return true
	}
	perguntas, err := s.banco.ListarPerguntas(ctx, dem.ID)
	if err != nil {
		return false
	}
	if len(perguntas) > 0 {
		for _, p := range perguntas {
			if strings.TrimSpace(p.RespondidaEm) == "" {
				return false
			}
		}
		return true
	}
	msgs, err := s.banco.ListarMensagensChat(ctx, dem.ID)
	if err != nil {
		return false
	}
	for _, m := range msgs {
		if m.Papel == db.PapelAnalista {
			return true
		}
	}
	return false
}

// responderDemandaComFases devolve 200 com a demanda e suas fases (mesmo contrato
// do GET /demands/{id}), para a UI atualizar o card sem uma segunda chamada.
func (s *Servidor) responderDemandaComFases(w http.ResponseWriter, r *http.Request, dem db.Demanda) {
	fases, err := s.banco.ListarFases(r.Context(), dem.ID)
	if err != nil {
		s.responderErroDemanda(w, r, err)
		return
	}
	responderJSON(w, http.StatusOK, respDemanda{Demanda: dem, Fases: fases})
}
