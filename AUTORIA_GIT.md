# Controle de autoria git

O Praxis roda num servidor e atende múltiplos usuários, mas quem executa `git commit`/`git push` é sempre o processo do servidor. Este documento descreve como os commits são identificados hoje (POC) e o que falta para uma identificação forte.

## O que já está implementado (POC)

**Objetivo desta etapa: mostrar QUEM fez, não provar.** A identidade nos commits é declarativa (metadado git), suficiente para rastreabilidade interna enquanto o multiusuário é validado.

### Autor = usuário da plataforma, committer = Praxis

Todo commit feito pelo orquestrador (commit de fase e merge da ação *integrar*) usa o modelo nativo do git para "fulano escreveu, o sistema aplicou":

- **Autor**: o usuário logado que criou a demanda (`demands.criado_por` → `users`). O e-mail é o login da plataforma — obrigatório e único por construção (`users.email NOT NULL UNIQUE`). No merge de integração, o autor é o usuário que disparou a ação.
- **Committer**: sempre `Praxis <praxis@praxis.invalid>` (constantes em `internal/gitops/identidade.go`). O domínio `.invalid` é reservado de propósito: não entrega e-mail e não colide com contas reais de GitHub/GitLab.
- **Sufixo " - Praxis"** no nome do autor (ex.: `Marcos Agnes - Praxis`): ligado por default; o admin desliga com a chave de config `git_sufixo_praxis` (escopo global ou por projeto):

  ```json
  { "git_sufixo_praxis": false }
  ```

- **Fallback**: demanda sem usuário (criada por token de API, modo bootstrap ou anterior à migração 10) ou usuário removido → autor = o próprio Praxis.

### Identidade por comando, nunca por config

A identidade é injetada por variáveis de ambiente (`GIT_AUTHOR_*`/`GIT_COMMITTER_*`) em cada `git commit`/`git merge` — nunca via `git config`. Motivos:

- worktrees compartilham o `.git/config` do repo: config por repo vazaria identidade entre demandas concorrentes de usuários diferentes;
- o commit deixa de depender da config git da máquina do servidor (estado invisível do deploy).

### Sanitização do ident

Nome e e-mail passam por saneamento antes de virar ident git (`gitops.sanitizarIdent`): remove `<`, `>`, quebras de linha e caracteres de controle — que permitiriam falsificar o ident ou injetar trailers falsos na mensagem. Ident vazio cai na identidade do Praxis (o git rejeitaria commit sem nome).

### Onde está no código

| Peça | Arquivo |
|---|---|
| Identidade (autor/committer, sufixo, sanitização) | `internal/gitops/identidade.go` |
| Commit/merge com env por comando | `internal/gitops/gitops.go`, `internal/gitops/merge.go` |
| Migração `demands.criado_por` (v10) | `internal/db/migracoes.go` |
| Captura do usuário logado na criação da demanda | `internal/api/demands.go` (+ `usuarioDaRequisicao` em `auth.go`) |
| Autor do merge da ação integrar | `internal/api/integracao.go` (`identidadeAutor`) |
| Resolução do autor por fase + chave `git_sufixo_praxis` | `internal/pipeline/demanda.go`, `internal/scheduler/executor.go` |

## Próximos passos

Em ordem de prioridade sugerida. Os itens 1–3 são os que transformam "identificação" em "garantia" quando o multiusuário sair da POC.

1. **Assinatura de commits (prova criptográfica).** `user.name`/`user.email` são texto livre — qualquer um com acesso ao repo comita "como" qualquer pessoa. Como o commit é sempre do servidor, o Praxis pode assinar com chave SSH própria (por instância ou por usuário) registrada no GitHub/GitLab → selo "Verified". É o único mecanismo que distingue um commit legítimo do Praxis de uma imitação.

2. **Credencial de push por projeto.** Hoje o push usa a credencial git da máquina: o forge enxerga o robô como pusher, e qualquer usuário do Praxis alcança qualquer repo que a credencial alcança (o `project_access` vira a única barreira). Mínimo: deploy key com write restrito por repositório. Evolução: GitHub App/GitLab com tokens curtos por operação; ideal: token por usuário, para o forge registrar o pusher real e aplicar as permissões dele.

3. **Auditoria fora do git.** O histórico git é reescrevível (amend/rebase/force-push por fora); a trilha confiável é interna. Registrar em `events` o SHA de cada commit criado + o usuário que disparou a ação (a tabela e o `criado_por` já existem — falta gravar o SHA). Complemento: trailer estruturado na mensagem (`Praxis-Demanda: d<id>`) para correlação máquina-legível commit ↔ demanda, independente do sufixo visual.

4. **Verificação de e-mail.** Obrigatório ≠ verificado: os forges vinculam commit → conta pelo e-mail, então um cadastro com e-mail alheio gera commits atribuídos a outra pessoa no GitHub (avatar, contribution graph). Enquanto só o admin cria usuários o risco é baixo; se houver auto-cadastro, verificação por link vira requisito.

5. **LGPD / e-mail noreply.** E-mail em commit é dado pessoal publicado de forma irreversível (o histórico se replica em cada clone). Avisar no cadastro; para usuários não-desenvolvedores, oferecer e-mail sintético (`usuario+<id>@praxis...`), aceitando perder o vínculo com a conta do forge.

6. **Ciclo de vida do usuário.** Commits antigos guardam a identidade da época — nunca reescrever; consolidação de identidades via `.mailmap`. Usuários referenciados por demandas/commits devem ser desativados (já é o modelo: `users.ativo`), nunca apagados, para a trilha não ficar órfã (a FK `criado_por` usa `ON DELETE SET NULL` como rede de segurança).

7. **Política de hooks.** Um `pre-commit` no `.git/hooks` de um projeto registrado executa código como o usuário do serviço a cada commit do Praxis. Decidir: respeitar hooks (comportamento atual) ou neutralizar com `core.hooksPath` vazio nos comandos do orquestrador.

8. **Tokens de API no audit.** Commit/merge disparado via `api_tokens` hoje sai como Praxis (token não tem usuário). Se tokens ganharem dono, registrar qual token disparou cada ação — um token vazado precisa ser rastreável.
