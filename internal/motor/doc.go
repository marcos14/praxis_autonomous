// Package motor abstrai os harnesses de IA (claude, codex, opencode) numa
// interface unica (Motor.Rodar), com selecao por nome e deteccao de instalacao.
// Portado de motor.go/claude.go/codex.go/opencode.go/claude_alias.go do Praxis
// atual na Fase 1f: a config vem do banco (motores/contas), OpcoesRun ganhou
// Dir (worktree) e DirLogs, e o laco de retentativa por franquia
// (esperarResetFranquia) ficou para o pipeline/scheduler (Fase 2b).
package motor
