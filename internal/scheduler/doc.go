// Package scheduler agenda as execuções de demandas em um worker pool de
// goroutines, respeitando os limites de concorrência (global e por projeto) e a
// afinidade conta↔demanda, e reagendando — sem bloquear os workers — as demandas
// cuja franquia de tokens esgotou, a partir do horário devolvido pelo pipeline
// (Fase 2b).
//
// A fila vem do banco (Fonte/FonteBanco); o trabalho em si — montar o
// ContextoExec no worktree da demanda e rodar as fases — é um seam (Executor)
// preenchido pelas Fases 2e/2g. Aqui mora só o motor de concorrência.
package scheduler
