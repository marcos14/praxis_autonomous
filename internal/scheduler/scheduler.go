package scheduler

import (
	"context"
	"strconv"
	"sync"
	"time"

	"github.com/marcos14/praxis-autonomous/internal/db"
)

// Defaults dos limites do scheduler. Ajustaveis por Opcoes (o wiring com a config
// do banco — chaves execucoes_simultaneas/execucoes_por_projeto — chega ao subir o
// serviço nas fases seguintes).
const (
	MaxGlobalDefault     = 2                // execucoes simultaneas no processo inteiro
	MaxPorProjetoDefault = 1                // execucoes simultaneas por projeto
	IntervaloDefault     = 2 * time.Second  // polling da fonte / granularidade do reagendamento
	BackoffErroDefault   = 30 * time.Second // espera antes de retentar apos erro de infraestrutura
)

// Desfecho e o que o Executor devolve ao scheduler depois de rodar uma demanda.
// O scheduler nao decide o que aconteceu — so reage:
//   - Concluido: a demanda terminou (sucesso/falha/cancelada) e sai da fila; o
//     Executor ja carimbou o status final da demanda no banco.
//   - RetomarEm no futuro (e nao Concluido): reagendamento por franquia — a
//     demanda so volta a ser candidata apos esse horario (o worker e liberado
//     imediatamente, nao dorme). Espelha ResultadoFase.RetomarEm da Fase 2b.
//   - nem Concluido nem RetomarEm: a demanda tem mais trabalho e volta a fila
//     assim que houver folga nos limites.
type Desfecho struct {
	Concluido bool
	RetomarEm time.Time
}

// Executor roda uma demanda (uma ou mais fases, conforme a Fase 2g) e devolve o
// Desfecho. E um seam: a implementacao real (monta o ContextoExec da Fase 2b no
// worktree da demanda — Fases 2e/2g) e injetada aqui; os testes usam um stub.
// A `conta` e o alias/CLAUDE_CONFIG_DIR resolvido pela afinidade conta↔demanda
// ("" quando nao ha contas configuradas). Um erro devolvido e tratado como falha
// de infraestrutura: a demanda e reagendada com backoff (nao fica em loop tenso).
type Executor interface {
	Executar(ctx context.Context, item Item, conta string) (Desfecho, error)
}

// Opcoes configura o Scheduler. Fonte e Executor sao obrigatorios; o resto tem
// defaults. Agora/Log sao seams (relogio e observabilidade) — nil cai no padrao.
type Opcoes struct {
	Fonte         Fonte
	Executor      Executor
	MaxGlobal     int           // <=0 → MaxGlobalDefault
	MaxPorProjeto int           // <0 → MaxPorProjetoDefault; 0 = sem limite por projeto
	Contas        []string      // contas p/ afinidade demanda↔conta; vazio = sem restricao de conta
	Intervalo     time.Duration // <=0 → IntervaloDefault
	BackoffErro   time.Duration // <=0 → BackoffErroDefault
	Store         *db.DB        // opcional: status da demanda (executando/aguardando_franquia)
	Agora         func() time.Time
	Log           func(string)
}

// Scheduler agenda execucoes de demandas num worker pool de goroutines,
// respeitando os limites (global, por projeto) e a afinidade conta↔demanda, e
// reagendando as demandas cuja franquia esgotou sem bloquear os workers.
type Scheduler struct {
	fonte         Fonte
	exec          Executor
	maxGlobal     int
	maxPorProjeto int
	contas        []string
	intervalo     time.Duration
	backoffErro   time.Duration
	store         *db.DB
	agora         func() time.Time
	logf          func(string)

	acordar chan struct{}
	wg      sync.WaitGroup

	mu           sync.Mutex
	ativos       int                 // total de workers em execucao (worker pool)
	porProjeto   map[int64]int       // projeto → workers ativos
	rodando      map[int64]bool      // demandas em execucao agora
	concluidas   map[int64]bool      // demandas ja finalizadas (nao reagendar)
	contaDe      map[int64]string    // afinidade: demanda → conta fixa
	contaOcupada map[string]bool     // conta → em uso por um worker
	naoAntesDe   map[int64]time.Time // demanda → nao redespachar antes deste horario
}

// Novo cria um Scheduler a partir das Opcoes, aplicando os defaults.
func Novo(o Opcoes) *Scheduler {
	s := &Scheduler{
		fonte:         o.Fonte,
		exec:          o.Executor,
		maxGlobal:     o.MaxGlobal,
		maxPorProjeto: o.MaxPorProjeto,
		contas:        o.Contas,
		intervalo:     o.Intervalo,
		backoffErro:   o.BackoffErro,
		store:         o.Store,
		agora:         o.Agora,
		logf:          o.Log,
		acordar:       make(chan struct{}, 1),
		porProjeto:    map[int64]int{},
		rodando:       map[int64]bool{},
		concluidas:    map[int64]bool{},
		contaDe:       map[int64]string{},
		contaOcupada:  map[string]bool{},
		naoAntesDe:    map[int64]time.Time{},
	}
	if s.maxGlobal <= 0 {
		s.maxGlobal = MaxGlobalDefault
	}
	if o.MaxPorProjeto < 0 {
		s.maxPorProjeto = MaxPorProjetoDefault
	}
	if s.intervalo <= 0 {
		s.intervalo = IntervaloDefault
	}
	if s.backoffErro <= 0 {
		s.backoffErro = BackoffErroDefault
	}
	if s.agora == nil {
		s.agora = time.Now
	}
	if s.logf == nil {
		s.logf = func(string) {}
	}
	return s
}

// Rodar bloqueia executando o loop de agendamento ate o ctx ser cancelado. A
// cada volta despacha o que couber nos limites e dorme ate o proximo tick, um
// worker terminar (acordar) ou o ctx encerrar. Ao encerrar, aguarda os workers em
// voo terminarem (eles observam o mesmo ctx e devem parar por conta propria).
func (s *Scheduler) Rodar(ctx context.Context) {
	tick := time.NewTicker(s.intervalo)
	defer tick.Stop()
	for {
		s.despachar(ctx)
		select {
		case <-ctx.Done():
			s.wg.Wait()
			return
		case <-tick.C:
		case <-s.acordar:
		}
	}
}

// despachar percorre a fila e tenta despachar cada item dentro dos limites.
func (s *Scheduler) despachar(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	itens, err := s.fonte.Prontas(ctx)
	if err != nil {
		s.logf("scheduler: fonte falhou: " + err.Error())
		return
	}
	for _, it := range itens {
		if ctx.Err() != nil {
			return
		}
		s.tentarDespachar(ctx, it)
	}
}

// tentarDespachar reserva os recursos (slot global, slot do projeto, conta) sob o
// mutex e, se tudo couber, dispara o worker. Devolve true quando despachou.
func (s *Scheduler) tentarDespachar(ctx context.Context, it Item) bool {
	s.mu.Lock()
	if s.concluidas[it.DemandaID] || s.rodando[it.DemandaID] {
		s.mu.Unlock()
		return false
	}
	if t, ok := s.naoAntesDe[it.DemandaID]; ok && s.agora().Before(t) {
		s.mu.Unlock()
		return false
	}
	if s.ativos >= s.maxGlobal {
		s.mu.Unlock()
		return false
	}
	if s.maxPorProjeto > 0 && s.porProjeto[it.ProjectID] >= s.maxPorProjeto {
		s.mu.Unlock()
		return false
	}
	conta, ok := s.reservarConta(it.DemandaID)
	if !ok {
		s.mu.Unlock()
		return false
	}
	// reserva os slots
	s.rodando[it.DemandaID] = true
	s.ativos++
	s.porProjeto[it.ProjectID]++
	delete(s.naoAntesDe, it.DemandaID)
	s.mu.Unlock()

	s.marcarStatus(ctx, it.DemandaID, db.StatusDemandaExecutando)

	s.wg.Add(1)
	go s.trabalhar(ctx, it, conta)
	return true
}

// reservarConta aplica a afinidade conta↔demanda (chamada sob s.mu):
//   - sem contas configuradas → "" (sem restricao);
//   - demanda ja tem conta fixa → usa-a se estiver livre, senao espera (mantem a
//     afinidade em vez de trocar de conta);
//   - demanda nova → pega a primeira conta livre e fixa a afinidade.
//
// Devolve (conta, true) quando reservou; ("", false) quando nao ha conta livre.
func (s *Scheduler) reservarConta(demandaID int64) (string, bool) {
	if len(s.contas) == 0 {
		return "", true
	}
	if c, ok := s.contaDe[demandaID]; ok {
		if !s.contaOcupada[c] {
			s.contaOcupada[c] = true
			return c, true
		}
		return "", false
	}
	for _, c := range s.contas {
		if !s.contaOcupada[c] {
			s.contaOcupada[c] = true
			s.contaDe[demandaID] = c
			return c, true
		}
	}
	return "", false
}

// trabalhar roda o Executor e, ao terminar, libera os recursos e decide o
// reagendamento. Um erro de infraestrutura vira backoff; Concluido tira a demanda
// da fila; RetomarEm no futuro segura o proximo despacho (franquia).
func (s *Scheduler) trabalhar(ctx context.Context, it Item, conta string) {
	defer s.wg.Done()

	des, err := s.exec.Executar(ctx, it, conta)

	s.mu.Lock()
	delete(s.rodando, it.DemandaID)
	s.ativos--
	if s.porProjeto[it.ProjectID] > 0 {
		s.porProjeto[it.ProjectID]--
		if s.porProjeto[it.ProjectID] == 0 {
			delete(s.porProjeto, it.ProjectID)
		}
	}
	if conta != "" {
		delete(s.contaOcupada, conta)
	}

	if err != nil {
		// erro de infraestrutura: nao conclui, reagenda com backoff.
		des.Concluido = false
		des.RetomarEm = s.agora().Add(s.backoffErro)
	}

	franquia := false
	switch {
	case des.Concluido:
		s.concluidas[it.DemandaID] = true
		delete(s.contaDe, it.DemandaID)
		delete(s.naoAntesDe, it.DemandaID)
	case !des.RetomarEm.IsZero():
		s.naoAntesDe[it.DemandaID] = des.RetomarEm
		franquia = err == nil // adiamento por franquia (nao por backoff de erro)
	default:
		delete(s.naoAntesDe, it.DemandaID)
	}
	s.mu.Unlock()

	if err != nil {
		s.logf("scheduler: erro ao executar demanda " + strconv.FormatInt(it.DemandaID, 10) + ": " + err.Error())
	}
	if franquia {
		// contrato da Fase 2b: demanda em aguardando_franquia ate RetomarEm.
		s.marcarStatus(ctx, it.DemandaID, db.StatusDemandaAguardandoFranquia)
	}

	s.sinalizar()
}

// marcarStatus atualiza o status da demanda no banco (best-effort). No-op se nao
// ha Store (testes) ou se o status ja e o desejado. Erros so viram log.
func (s *Scheduler) marcarStatus(ctx context.Context, demandaID int64, status string) {
	if s.store == nil {
		return
	}
	dem, err := s.store.ObterDemanda(ctx, demandaID)
	if err != nil {
		s.logf("scheduler: obter demanda " + strconv.FormatInt(demandaID, 10) + ": " + err.Error())
		return
	}
	if dem.Status == status {
		return
	}
	dem.Status = status
	if _, err := s.store.AtualizarDemanda(ctx, dem); err != nil {
		s.logf("scheduler: atualizar status da demanda " + strconv.FormatInt(demandaID, 10) + ": " + err.Error())
	}
}

// sinalizar acorda o loop para reavaliar a fila (nao bloqueia se ja ha um aviso
// pendente).
func (s *Scheduler) sinalizar() {
	select {
	case s.acordar <- struct{}{}:
	default:
	}
}

// Ativos devolve o numero de workers em execucao (observabilidade/testes).
func (s *Scheduler) Ativos() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ativos
}
