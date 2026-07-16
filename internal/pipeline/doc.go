// Package pipeline conduz o ciclo de uma fase (executor → gates → corretor →
// revisor → commit) sobre um ContextoExec, incluindo os gates com semáforo
// global e o fallback entre motores. Portado de executar.go/gates.go/fallback.go
// do Praxis atual a partir da Fase 2b.
package pipeline
