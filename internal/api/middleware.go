package api

import (
	"log/slog"
	"net/http"
	"time"
)

// middleware é uma função que embrulha um http.Handler adicionando comportamento
// (log, recover, etc.) em volta dele.
type middleware func(http.Handler) http.Handler

// encadear aplica os middlewares a h na ordem em que são passados: o primeiro da
// lista fica na camada mais externa (é o primeiro a ver a requisição e o último
// a ver a resposta). Assim comRecover(comLog(h)) se escreve encadear(h, comLog,
// comRecover) — o recover envolve tudo, inclusive o log.
func encadear(h http.Handler, mws ...middleware) http.Handler {
	for i := len(mws) - 1; i >= 0; i-- {
		h = mws[i](h)
	}
	return h
}

// capturaStatus embrulha o ResponseWriter para lembrar o status HTTP escrito, de
// modo que o middleware de log possa registrá-lo. Também conta os bytes do
// corpo. Se o handler nunca chamar WriteHeader, o status efetivo é 200 (padrão
// do net/http), refletido aqui.
type capturaStatus struct {
	http.ResponseWriter
	status   int
	bytes    int
	escreveu bool
}

func (c *capturaStatus) WriteHeader(status int) {
	if !c.escreveu {
		c.status = status
		c.escreveu = true
	}
	c.ResponseWriter.WriteHeader(status)
}

func (c *capturaStatus) Write(b []byte) (int, error) {
	if !c.escreveu {
		c.status = http.StatusOK
		c.escreveu = true
	}
	n, err := c.ResponseWriter.Write(b)
	c.bytes += n
	return n, err
}

// Flush repassa o flush ao ResponseWriter subjacente quando ele suporta
// streaming (necessário para SSE, usado nas fases futuras).
func (c *capturaStatus) Flush() {
	if f, ok := c.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// comLog registra cada requisição atendida (método, caminho, status, duração)
// via slog. logger pode ser nil, caso em que usa o logger default.
func comLog(logger *slog.Logger) middleware {
	if logger == nil {
		logger = slog.Default()
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			inicio := time.Now()
			cw := &capturaStatus{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(cw, r)
			logger.Info("http",
				"metodo", r.Method,
				"caminho", r.URL.Path,
				"status", cw.status,
				"bytes", cw.bytes,
				"dur_ms", time.Since(inicio).Milliseconds(),
			)
		})
	}
}

// comRecover captura panics de handlers, registra o incidente e devolve um 500
// padronizado, evitando que um handler defeituoso derrube o servidor inteiro.
// Se a resposta já começou a ser escrita, não há como sobrescrever o status —
// nesse caso só registra.
func comRecover(logger *slog.Logger) middleware {
	if logger == nil {
		logger = slog.Default()
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cw := &capturaStatus{ResponseWriter: w, status: http.StatusOK}
			defer func() {
				if rec := recover(); rec != nil {
					logger.Error("panic em handler",
						"metodo", r.Method,
						"caminho", r.URL.Path,
						"panic", rec,
					)
					if !cw.escreveu {
						responderErro(cw, http.StatusInternalServerError, "erro_interno",
							"erro interno do servidor")
					}
				}
			}()
			next.ServeHTTP(cw, r)
		})
	}
}
