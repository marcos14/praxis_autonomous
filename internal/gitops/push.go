package gitops

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// EsperaEntreTentativas e a base da espera entre tentativas de push (cresce
// linearmente: 1x, 2x, ...). Exposta como var para os testes reduzirem a espera.
var EsperaEntreTentativas = 500 * time.Millisecond

// Push publica a branch da demanda no remote origin (git push -u origin
// <branch>). GUARDA de seguranca: so aceita branches com prefixo praxis/ — a
// main (ou qualquer outra) nunca e empurrada. Tenta ate `tentativas` vezes
// (>=1), com espera crescente entre falhas; devolve o ultimo erro se todas
// falharem. Falha de push nao deve bloquear a execucao (a Fase 2f trata o alerta
// e o retry no proximo commit) — aqui so entregamos a primitiva com retry.
//
// Autenticacao usa as credenciais git ja configuradas na maquina (credential
// manager/SSH); o Praxis nunca armazena senha de git.
func (o *Ops) Push(repo, branch string, tentativas int) error {
	if err := validarBranchPraxis(branch); err != nil {
		return err
	}
	if tentativas < 1 {
		tentativas = 1
	}
	defer o.trava(repo)()

	var err error
	for i := 0; i < tentativas; i++ {
		if i > 0 {
			time.Sleep(time.Duration(i) * EsperaEntreTentativas)
		}
		if _, err = git(repo, "push", "-u", "origin", branch); err == nil {
			return nil
		}
	}
	return err
}

// CommitsNaoPublicados conta os commits da branch que ainda nao estao em nenhuma
// ref remota origin/* — ou seja, os commits locais aguardando push. Alimenta o
// alerta "commits nao publicados (N)" da Fase 2f. Como a branch da demanda nasce
// de origin/<main>, antes do primeiro push bem-sucedido isto conta exatamente os
// commits proprios da demanda; apos um push completo, conta 0 (origin/<branch>
// alcanca o topo). E somente leitura — nao toma o mutex do repo. Pode ser chamada
// com o caminho do repo principal ou de um worktree vinculado (as refs sao
// compartilhadas).
func CommitsNaoPublicados(repo, branch string) (int, error) {
	branch = strings.TrimSpace(branch)
	if branch == "" {
		return 0, fmt.Errorf("gitops: branch vazia")
	}
	out, err := git(repo, "rev-list", "--count", branch, "--not", "--remotes=origin")
	if err != nil {
		return 0, err
	}
	n, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		return 0, fmt.Errorf("gitops: contagem de commits nao publicados invalida %q: %w", out, err)
	}
	return n, nil
}
