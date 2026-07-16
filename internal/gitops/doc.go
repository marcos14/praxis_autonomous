// Package gitops encapsula as operações git: worktree add/remove/prune, push da
// branch da demanda (com retry, apenas branches praxis/*), merge-preview
// (git merge-tree --write-tree), merge --no-ff e mutex por projeto. Portado de
// git.go do Praxis atual a partir da Fase 1g.
package gitops
