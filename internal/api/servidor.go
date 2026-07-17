package api

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/marcos14/praxis-autonomous/internal/db"
	"github.com/marcos14/praxis-autonomous/internal/gitops"
)

// intervaloPollLogPadrao é a cadência com que o SSE de log ao vivo relê o .jsonl
// da execução em busca de novas linhas (e checa se uma execução mais nova
// começou). Os testes sobrescrevem para um valor pequeno.
const intervaloPollLogPadrao = 700 * time.Millisecond

// Opcoes reúne as dependências do servidor HTTP.
type Opcoes struct {
	// Banco é o acesso ao SQLite. Se não-nil, o /healthz verifica a
	// conectividade do banco (ping no pool de leitura). Pode ser nil para um
	// servidor sem banco (usado em testes de handlers isolados).
	Banco *db.DB
	// Log é o logger estruturado. Se nil, usa slog.Default().
	Log *slog.Logger
	// Exec permite às ações da demanda (pausar/cancelar) interromper o worker em
	// andamento. Opcional: nil = a ação só transita o status no banco (o scheduler
	// ainda não está acoplado ao serve — pendência 2g.n1/M3).
	Exec ControladorExecucao
	// Intake dispara o analista (perguntas readonly) em background quando uma
	// demanda nasce por chat (Fase 3b). Opcional: nil = a demanda fica em
	// `recebida` até ser analisada (mecanismo antes do wiring).
	Intake Analisador
	// Planejamento dispara o planejador (plano + fases readonly) em background
	// quando a demanda entra em `planejando` (respostas recebidas) ou o plano é
	// rejeitado com comentário (Fase 3c). Opcional: nil = a demanda fica em
	// `planejando` até ser planejada (mecanismo antes do wiring).
	Planejamento Planejador
	// Git executa as operações de integração (merge-preview, push, merge --no-ff,
	// remoção de worktree) das Fases 4c/4d/4e. Se nil, o Novo usa gitops.Novo().
	Git *gitops.Ops
}

// Analisador dispara a análise readonly de uma demanda em background (Fase 3b). É
// um seam: em produção o *intake.Servico o satisfaz (resolve config do banco e
// roda o harness em modo somente leitura). Quando nil, a criação por chat não
// dispara análise automática.
type Analisador interface {
	Disparar(demandaID int64)
}

// Planejador dispara o planejamento readonly de uma demanda em background (Fase
// 3c). É um seam: em produção o *intake.Servico o satisfaz. Quando nil, a demanda
// que entra em `planejando` fica lá até ser planejada.
type Planejador interface {
	DispararPlanejamento(demandaID int64)
}

// Servidor encapsula o roteador e as dependências da API. Construa com Novo e
// exponha o http.Handler via Handler.
type Servidor struct {
	banco        *db.DB
	log          *slog.Logger
	handler      http.Handler
	exec         ControladorExecucao
	intake       Analisador
	planejamento Planejador
	git          *gitops.Ops

	// intervaloPollLog é a cadência de releitura do .jsonl no SSE de log ao vivo.
	// Definido no Novo (intervaloPollLogPadrao); os testes ajustam para acelerar.
	intervaloPollLog time.Duration
	// intervaloPollEventos é a cadência de releitura da tabela events no SSE
	// global (Fase 4a). Definido no Novo; os testes ajustam para acelerar.
	intervaloPollEventos time.Duration
}

// Novo monta o servidor: registra as rotas e encadeia os middlewares base (log
// e recover). O recover fica na camada mais externa para capturar panics de
// qualquer camada, inclusive do próprio log.
func Novo(opts Opcoes) *Servidor {
	logger := opts.Log
	if logger == nil {
		logger = slog.Default()
	}
	gitOps := opts.Git
	if gitOps == nil {
		gitOps = gitops.Novo()
	}
	s := &Servidor{banco: opts.Banco, log: logger, exec: opts.Exec, intake: opts.Intake,
		planejamento: opts.Planejamento, git: gitOps, intervaloPollLog: intervaloPollLogPadrao,
		intervaloPollEventos: intervaloPollEventosPadrao}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealth)
	s.registrarRotasProjetos(mux)
	s.registrarRotasDemandas(mux)
	s.registrarRotasBoard(mux)
	s.registrarRotasEventos(mux)
	s.registrarRotasHome(mux)
	s.registrarRotasIntegracao(mux)
	s.registrarRotasMotores(mux)
	s.registrarRotasConfig(mux)
	s.registrarRotasTokens(mux)
	s.registrarRotasWeb(mux)

	// A ordem coloca o recover na camada mais externa e a autorização (comAuth)
	// logo dentro do log — a auth roda depois do log/recover e antes dos handlers.
	s.handler = encadear(mux, comRecover(logger), comLog(logger), s.comAuth)
	return s
}

// Handler devolve o http.Handler pronto para ser servido (com middlewares já
// aplicados). É o que `serve` entrega ao http.Server.
func (s *Servidor) Handler() http.Handler { return s.handler }

// respHealth é o corpo do /healthz.
type respHealth struct {
	Status string `json:"status"`           // "ok" ou "degradado"
	Versao string `json:"versao,omitempty"` // versão do binário, quando conhecida
	Banco  string `json:"banco,omitempty"`  // estado do banco: "ok" | mensagem de erro | "" (sem banco)
}

// Versao é a versão do binário exibida no /healthz. É preenchida pelo `serve` a
// partir da variável de versão do main; fica em branco em testes.
var Versao string

// handleHealth responde ao health check. Sem banco configurado, é uma verificação
// de liveness (o processo está no ar). Com banco, faz também um ping no pool de
// leitura e devolve 503 se o banco estiver inacessível (readiness).
func (s *Servidor) handleHealth(w http.ResponseWriter, r *http.Request) {
	resp := respHealth{Status: "ok", Versao: Versao}
	status := http.StatusOK
	if s.banco != nil {
		if err := s.banco.Leitor.PingContext(r.Context()); err != nil {
			resp.Status = "degradado"
			resp.Banco = err.Error()
			status = http.StatusServiceUnavailable
		} else {
			resp.Banco = "ok"
		}
	}
	responderJSON(w, status, resp)
}
