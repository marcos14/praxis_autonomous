package api

import (
	"log/slog"
	"net/http"

	"github.com/marcos14/praxis-autonomous/internal/db"
)

// Opcoes reúne as dependências do servidor HTTP.
type Opcoes struct {
	// Banco é o acesso ao SQLite. Se não-nil, o /healthz verifica a
	// conectividade do banco (ping no pool de leitura). Pode ser nil para um
	// servidor sem banco (usado em testes de handlers isolados).
	Banco *db.DB
	// Log é o logger estruturado. Se nil, usa slog.Default().
	Log *slog.Logger
}

// Servidor encapsula o roteador e as dependências da API. Construa com Novo e
// exponha o http.Handler via Handler.
type Servidor struct {
	banco   *db.DB
	log     *slog.Logger
	handler http.Handler
}

// Novo monta o servidor: registra as rotas e encadeia os middlewares base (log
// e recover). O recover fica na camada mais externa para capturar panics de
// qualquer camada, inclusive do próprio log.
func Novo(opts Opcoes) *Servidor {
	logger := opts.Log
	if logger == nil {
		logger = slog.Default()
	}
	s := &Servidor{banco: opts.Banco, log: logger}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealth)
	s.registrarRotasProjetos(mux)

	s.handler = encadear(mux, comRecover(logger), comLog(logger))
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
