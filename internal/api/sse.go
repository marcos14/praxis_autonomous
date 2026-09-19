package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
)

// contextoDoStream deriva de r.Context() um contexto que também encerra quando
// a credencial vence (principal.expiraEm). Um stream SSE aberto com um JWT não
// sobrevive ao token: o cliente recebe o evento token_expirado e reabre com um
// token renovado — sem isto, um kanban aberto seguiria recebendo eventos
// indefinidamente com uma credencial já vencida. Credenciais sem expiração
// (token de API, bootstrap) só seguem o contexto da requisição.
func contextoDoStream(r *http.Request) (context.Context, context.CancelFunc) {
	pr := principalDaRequisicao(r)
	if pr.expiraEm.IsZero() {
		return context.WithCancel(r.Context())
	}
	return context.WithDeadline(r.Context(), pr.expiraEm)
}

// avisarTokenExpirado emite o evento token_expirado quando o stream encerrou
// pelo vencimento da credencial — e não porque o cliente foi embora —, para o
// cliente reabrir de imediato com um token renovado em vez de esperar a
// reconexão automática do EventSource esbarrar num 401.
func avisarTokenExpirado(ctx context.Context, r *http.Request, w io.Writer, flusher http.Flusher) {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) && r.Context().Err() == nil {
		fmt.Fprint(w, "event: token_expirado\ndata: {}\n\n")
		flusher.Flush()
	}
}
