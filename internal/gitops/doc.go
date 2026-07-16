// Package gitops encapsula as operacoes git do Praxis Autonomous: deteccao de
// repo, commit, worktree add/remove/prune, push da branch da demanda (com retry
// e guarda "somente branches praxis/*"), previa de merge (git merge-tree
// --write-tree), merge --no-ff e mutex por projeto (serializacao das operacoes
// que mudam refs/worktrees do mesmo repo). Portado/adaptado do git.go do Praxis
// atual (C:\Projetos\praxis) na Fase 1g. No Windows, core.longpaths e ativado e
// worktrees orfaos sao podados no boot.
package gitops
