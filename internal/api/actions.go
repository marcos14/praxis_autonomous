package api

import (
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
	case "pausar", "retomar", "cancelar":
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
	case "publicar_branch":
		s.acaoPublicarBranch(w, r, dem)
	case "integrar":
		s.acaoIntegrar(w, r, dem)
	case "atualizar_branch":
		s.acaoAtualizarBranch(w, r, dem)
	default:
		responderErro(w, http.StatusBadRequest, "invalido",
			"ação desconhecida: use pausar, retomar, cancelar, publicar_branch, integrar ou atualizar_branch")
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
	tituloEv:    "Praxis: demanda pausada",
	detalheEv:   "Execução pausada pelo usuário. A fase corrente termina e nenhuma outra começa até retomar.",
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
	tituloEv:    "Praxis: demanda retomada",
	detalheEv:   "Execução retomada pelo usuário; a demanda voltou à fila do scheduler.",
	msgInvalido: "só é possível retomar uma demanda pausada ou aguardando franquia",
}

var acaoCancelar = descAcao{
	nome:        "cancelar",
	alvo:        db.StatusDemandaCancelada,
	jaNoAlvo:    func(st string) bool { return st == db.StatusDemandaCancelada },
	permitido:   func(st string) bool { return !statusTerminal(st) },
	interromper: true,
	tipoEvento:  "demanda_cancelada",
	tituloEv:    "Praxis: demanda cancelada",
	detalheEv:   "Execução cancelada pelo usuário. Estado terminal; a demanda não será mais conduzida.",
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
		s.responderErroDemanda(w, err)
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

// responderDemandaComFases devolve 200 com a demanda e suas fases (mesmo contrato
// do GET /demands/{id}), para a UI atualizar o card sem uma segunda chamada.
func (s *Servidor) responderDemandaComFases(w http.ResponseWriter, r *http.Request, dem db.Demanda) {
	fases, err := s.banco.ListarFases(r.Context(), dem.ID)
	if err != nil {
		s.responderErroDemanda(w, err)
		return
	}
	responderJSON(w, http.StatusOK, respDemanda{Demanda: dem, Fases: fases})
}
