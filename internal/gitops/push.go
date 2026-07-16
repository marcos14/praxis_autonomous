package gitops

import "time"

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
