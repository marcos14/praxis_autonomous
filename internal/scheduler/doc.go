// Package scheduler agenda as execuções de demandas em um worker pool de
// goroutines, respeitando os limites (global, por projeto, gates) e o
// reagendamento por franquia. A implementação começa na Fase 2d.
package scheduler
