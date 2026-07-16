// Package api expõe o servidor HTTP: roteador, middlewares base (log, recover),
// respostas JSON/erros padronizados e os handlers da API REST em /api/v1.
//
// A Fase 1b entrega a fundação: o servidor (Novo/Handler), o health check
// GET /healthz e a infraestrutura de resposta/erro. Autenticação por token com
// papéis e os handlers de CRUD (projects/engines/config) entram a partir da
// Fase 1c.
package api
