# Praxis Autonomous — Orquestrador de desenvolvimento multi-projeto

## Como este plano será executado

- **Diretório deste projeto (onde o plano executa):** `C:\Projetos\praxis-autonomous`
- **Projeto de referência (SOMENTE LEITURA):** `C:\Projetos\praxis` — é o Praxis atual, em produção. Todo código citado como "portar" deve ser **lido de lá e copiado/adaptado para cá**. É PROIBIDO modificar qualquer arquivo em `C:\Projetos\praxis`.
- Este plano será quebrado em micro-fases pelo próprio Praxis (`inicializar`) e executado fase a fase (executor → gates → corretor → revisor → commit). Cada fase deve ser pequena, compilável e verificável pelos gates.
- **Documentos fundadores neste diretório:** `PROTOTIPO_PRAXIS_AUTONOMOUS.html` (referência visual e de fluxo de TODAS as telas) e `FLUXOGRAMA_PRAXIS_AUTONOMOUS.html` (topologia, atores, branches). A UI implementada deve seguir o protótipo.

### Preparação (antes ou como fase 0)

1. `git init` neste diretório (se ainda não for repo git) e commit inicial com os documentos fundadores.
2. `go mod init github.com/marcos14/praxis-autonomous` (Go 1.26).
3. Dependência única de runtime: `modernc.org/sqlite` (driver SQLite puro Go, sem cgo).
4. Gates sugeridos no `autopilot.json`: `go build ./...`, `go vet ./...`, `go test ./... -count=1`.
5. Layout alvo: `cmd/praxis/` (main), `internal/{db,api,scheduler,pipeline,motor,gitops,intake,notify}`, `web/` (embed).

## Contexto

O Praxis atual (`C:\Projetos\praxis`) é um binário copiado para `automacao/` dentro de cada projeto: fila sequencial em `fases.csv`, config em `autopilot.json`, um único working tree git e nenhum lock entre processos. O dev que dispara uma implementação fica travado — e a esteira de suporte recebe vários chamados ao mesmo tempo.

**O Praxis Autonomous é um produto novo, neste repositório novo**, que redesenha tudo para o objetivo real: orquestrar o desenvolvimento de demandas em **vários projetos ao mesmo tempo, inclusive várias no mesmo projeto**, com **gestão 100% em interface web** (nenhuma linha de comando no uso diário). O Praxis atual continua existindo e serve de base de código: motores, gates, pipeline de fases e operações git são portados dele (leitura em `C:\Projetos\praxis`, cópia adaptada aqui).

### Princípios do redesenho

1. **Zero linha de comando** — o único comando é subir o serviço; todo o resto (projetos, motores, demandas, acompanhamento, integração) acontece na web.
2. **Tudo no banco (SQLite)** — projetos, motores, demandas, chat, perguntas/respostas, planos (markdown), fases, execuções, custos, eventos e logs. Nada de CSV/JSON em pastas dos projetos; nas pastas dos projetos só entram os commits nas branches.
3. **Background por padrão** — cadastrou a demanda, tudo anda sozinho; o usuário acompanha e interage pelos cards do kanban. Só três momentos exigem humano: **responder perguntas**, **aprovar o plano** e **abrir o Merge Request**.
4. **A demanda nasce como chat** — PRD, complementos, perguntas do analista e respostas ficam em formato de conversa (persistida no banco) até o plano ser aprovado; aí vira kanban/fases.
5. **Config em camadas** — global → override por projeto. Motores são entidade própria com prioridade (ordem de fallback), modelo, budget e contas.

## Fluxo da demanda (máquina de estados)

```
Recebida (UI-chat ou API) → Analisando (harness readonly lê o código, gera perguntas)
→ Aguardando respostas (usuário responde/aceita sugestões no card)
→ Planejando (harness combina PRD + respostas → plano md + fases)
→ Aguardando aprovação (usuário revisa/edita fases, aprova ou rejeita c/ comentário)
→ Pronta → Executando (worktree + branch EXCLUSIVA praxis/d<id>-<slug>, criada a partir da main
   atualizada; executor→gates→corretor→revisor→commit por fase — commits só na branch da demanda,
   com PUSH AUTOMÁTICO da branch após cada commit)
→ Concluída → integração conforme o modo do projeto (ver abaixo) → Integrada
Desvios: Pausada · Aguardando franquia · Falhou · Conflito · Cancelada
```

### Modo de integração (config por projeto)

Requisito do git flow da empresa: toda demanda nasce da main, é desenvolvida em branch própria e termina em **merge request criado pelo desenvolvedor**. A branch exclusiva por demanda é garantida por construção (worktree + branch dedicada). O fechamento tem dois modos:

- **`merge_request` (default)** — a branch é publicada no primeiro commit (`git push -u origin praxis/d<id>-<slug>`) e **cada commit de fase é seguido de push automático** — push SOMENTE da branch da demanda, nunca da main. O progresso fica visível no GitLab/GitHub em tempo real (CI da empresa pode rodar por fase). Demanda concluída vai para "Pronta para MR": o card mostra a branch, os commits, o preview de conflito com a main e o link direto para abrir o MR (via `url_plataforma`). O merge em si é feito pelo dev na plataforma, seguindo o fluxo normal de revisão.
- **`merge_local`** — botão Integrar faz `merge --no-ff` local na main (para projetos sem esteira de MR; sem push).

Falha de push (rede/credenciais/branch protegida) **não bloqueia a execução**: a fase conclui normalmente, o card mostra o alerta "commits não publicados (N)", registra evento e o push é retentado no próximo commit ou pela ação manual `publicar_branch`. Autenticação usa as credenciais git já configuradas na máquina (credential manager/SSH) — o Praxis não armazena senha de git.

Em ambos os modos, o botão "Atualizar branch" (trazer a main para a branch da demanda) resolve conflitos antes do MR/merge. A branch parte sempre da main **atualizada**: com remote configurado, `git fetch origin <main>` antes do `worktree add`; sem remote, parte da main local.

## Arquitetura

- **Este repositório**, Go 1.26, layout: `cmd/praxis/` (main: sobe o servidor), `internal/{db,api,scheduler,pipeline,motor,gitops,intake,notify}`, `web/` (embed).
- **Um processo único** (`praxis.exe serve`, instalável como serviço Windows/systemd): HTTP + scheduler + worker pool. Execuções são goroutines; os filhos são os processos dos harnesses (claude/codex/opencode), como no Praxis atual.
- **SQLite via `modernc.org/sqlite`** (puro Go, sem cgo — build simples no Windows). `database/sql`, WAL, `busy_timeout=5000`, escritor único (`SetMaxOpenConns(1)`) + pool de leitura. Transações curtas — nunca abertas durante um run de harness.
- **`PRAXIS_HOME`** (default `%LOCALAPPDATA%\praxis`): `praxis.db` + `worktrees/<projeto>/<demanda>/`. Logs `.jsonl` em `PRAXIS_HOME/logs` com ponteiro no banco (tabela `runs.log_ref`).
- **Frontend embutido** (`//go:embed web/*`), sem framework/npm: HTML + CSS + ES modules vanilla, SSE para tempo real. O `PROTOTIPO_PRAXIS_AUTONOMOUS.html` é a referência visual e de fluxo.

### O que é portado do Praxis atual (ler em `C:\Projetos\praxis`, copiar adaptado para cá)

| Origem (C:\Projetos\praxis) | Destino (este repo) | Adaptação |
|---|---|---|
| `claude.go`, `codex.go`, `opencode.go`, `motor.go`, `claude_alias.go` | `internal/motor/` | `OpcoesRun` ganha `Dir` (worktree) e `DirLogs`; config vem do banco, não de arquivo |
| `executar.go` (função `pipelineFase`, linha ~304), `fallback.go` | `internal/pipeline/` | recebe um `ContextoExec` (demanda, worktree, config resolvida, fila no banco); `esperarResetFranquia` não bloqueia — devolve horário e o scheduler reagenda |
| `gates.go` | `internal/pipeline/gates.go` | semáforo global `max_gates_simultaneos` |
| `git.go` | `internal/gitops/` | + worktree add/remove/prune, push automático da branch da demanda pós-commit (com retry), merge-preview (`git merge-tree --write-tree`), merge `--no-ff`, mutex por projeto |
| `inicializar.go` + prompts `defaults/*.md` | `internal/intake/` + banco | vira **analista** (perguntas) + **planejador** (fases); prompts ficam no banco com default embutido |
| `notificacoes.go`, `notificar.go` | `internal/notify/` | eventos vêm do banco; mesmos webhooks (Telegram/Discord/Slack/Google Chat) |
| `auth.go` | `internal/api/auth.go` | tokens no banco com papel (leitor/operador/admin) |

Regras de porte: manter o estilo do código de origem quando fizer sentido; testes unitários para cada pacote portado; nunca depender de `automacao/` nem de arquivos dentro dos projetos-alvo.

## Modelo de dados (SQLite)

```sql
projects(id, nome, slug, pasta, branch_principal, modo_integracao merge_request|merge_local,
         url_plataforma,          -- GitLab/GitHub, para montar o link "abrir MR"
         add_dirs JSON, ativo, criado_em)
engines(id, nome, prioridade, ativo, modelo_exec, modelo_analise,
        budget_fase_usd, timeout_min, params JSON)          -- cadastro de motores
engine_accounts(id, engine_id, alias, config_dir, ativo)     -- contas (CLAUDE_CONFIG_DIR)
config_entries(escopo global|project, project_id, chave, valor JSON)  -- overrides por projeto
demands(id, project_id, titulo, origem ui|api, origem_ref, status, prioridade,
        branch, worktree_path, plano_md TEXT, custo_usd, budget_usd, erro, criado_em, atualizado_em)
chat_messages(id, demand_id, papel user|analista|planejador|sistema, conteudo, meta JSON, criado_em)
questions(id, demand_id, ordem, pergunta, contexto, tipo, opcoes JSON, sugestao, impacto,
          resposta, respondida_em)
phases(id, demand_id, codigo, titulo, status, depende_de JSON, requer_humano,
       gate_extra, modelo, tentativas, custo_usd, concluido_em, observacao, ordem)
runs(id, demand_id, phase_id, operacao, engine, modelo, custo_usd, tokens_in, tokens_out,
     is_error, log_ref, iniciado_em, terminado_em)
events(id, project_id, demand_id, tipo, titulo, detalhe, criado_em)   -- alimenta SSE + histórico
api_tokens(id, nome, token_hash, papel, criado_em, revogado_em)
metrics_dia(dia, project_id, engine, custo_usd, fases_concluidas)     -- agregado p/ Home (ou view)
```

Franquia/esgotamento: estado em memória no `gestorFranquia` (mapa engine/conta → esgotado_até) + espelho em `engine_accounts` para sobreviver a restart.

## Interface web (referência: PROTOTIPO_PRAXIS_AUTONOMOUS.html)

- **Home** — tiles: gasto no mês, demandas ativas, fases concluídas (7d), integradas no mês, franquia; gráfico de gastos por dia; tabela por projeto; lista "Precisa de você" (perguntas/aprovação/conflito); atividade recente (SSE).
- **Kanban** — colunas = status da demanda; filtros por projeto/motor. Card mostra projeto, fase atual, motor, custo, progresso e alerta quando precisa do usuário. Transições de estado só por botões de ação (não arrastar para "Integrada"); arrastar só reordena prioridade.
- **Card (modal)** — abas: **Chat/PRD** (conversa persistida, aceita complementos), **Perguntas** (chips de sugestão + texto livre, "responder tudo e gerar plano"), **Plano & Fases** (editar/reordenar/remover/exigir humano; aprovar/rejeitar), **Log ao vivo** (SSE do `.jsonl`), **Eventos**. Ações: pausar, retomar, cancelar, publicar branch, integrar (com preview de conflito), atualizar branch.
- **Nova demanda** — seleciona projeto + chat para colar o PRD; o card nasce e anda sozinho.
- **Projetos** — cadastro: nome, pasta (repo git), branch principal, modo de integração, url da plataforma, e parâmetros com herança explícita do global (motor, execuções simultâneas, máx. correções, máx. ciclos de revisão, máx. fases novas, budget, gates, add_dirs). Botões "testar gates", "testar push" e "ver config efetiva".
- **Motores** — lista ordenável (prioridade = ordem de fallback), liga/desliga, modelo para execução vs. análise, budget/fase, timeout, contas com % de uso e estado de franquia.
- **Configurações** — globais + notificações + tokens de API.
- **Manual** — seção embutida ensinando o fluxo (conteúdo-base já rascunhado no protótipo).

## API REST (`/api/v1`, Bearer com papéis)

```
POST/GET  /projects            GET/PUT /projects/{id}         GET/PUT /projects/{id}/config
POST/GET  /engines             PUT /engines/{id}              PUT /engines/ordem
POST      /projects/{id}/demands        ← intake automatizado (sistema de chamados)
GET       /demands?project=&status=     GET /demands/{id}
POST      /demands/{id}/chat            (mensagem do usuário no chat da demanda)
POST      /demands/{id}/answers         POST /demands/{id}/approve-plan
POST      /demands/{id}/actions {pausar|retomar|cancelar|publicar_branch|integrar|atualizar_branch|replanejar}
GET       /demands/{id}/merge-preview   GET /demands/{id}/logs (SSE)
GET       /events (SSE global)          GET /metrics?periodo=
POST/DELETE /tokens                     GET /manual/*
```

## Marcos (a serem quebrados em micro-fases pelo inicializador)

**M1 — Fundação:** go.mod + layout de pastas + main mínimo que compila; schema SQLite com migrações (`user_version`); `serve` com HTTP básico e health; CRUD de projects/engines/config via API; porte de `internal/motor` (dos arquivos de `C:\Projetos\praxis`: `motor.go`, `claude.go`, `codex.go`, `opencode.go`, `claude_alias.go`) e `internal/gitops` (`git.go` + worktree/push/merge-preview) com testes. Web: shell da UI (navegação, telas de projetos, motores, config — seguindo o protótipo).

**M2 — Pipeline no worktree:** porte de pipeline/gates/fallback (de `executar.go`, `gates.go`, `fallback.go`) para `ContextoExec`; scheduler com limites (global/por projeto/gates); worktree por demanda com push automático da branch a cada commit de fase (tolerante a falha, com retry); demanda criada manualmente já com fases (sem intake ainda) executa em background; card com fases + log SSE; pausar/retomar/cancelar; retomada pós-restart (executando → pausada → refila).

**M3 — Intake PRD:** chat de demanda persistido; prompts `analista` (readonly, JSON: resumo, arquivos_provaveis, perguntas com tipo/opções/sugestão/impacto) e `planejador` (deriva do `inicializador.md` do repo de referência; gera plano_md + fases); telas de perguntas e aprovação; rejeitar-com-comentário replaneja. **Fluxo completo de ponta a ponta.**

**M4 — Kanban + Home + Integração:** kanban com SSE; Home com métricas e "Precisa de você"; fechamento nos dois modos — `merge_request` (publicar branch + link do MR) e `merge_local` (Integrar com merge-preview); tratamento de conflito (status `conflito` + arquivos + "atualizar branch"); limpeza de worktree/branch pós-integração (no modo MR, após o merge ser detectado na main); notificações.

**M5 — Operação:** API de intake para o sistema de chamados (tokens/papéis); manual embutido; badge de sobreposição entre demandas (interseção de `arquivos_provaveis` + `git diff --name-only` entre branches); serviço Windows; retenção/backup do banco; importador opcional de projetos do Praxis atual (lê `autopilot.json`/`fases.csv` uma única vez).

## Riscos e mitigação

1. **SQLITE_BUSY** — WAL + busy_timeout + escritor único; transações curtas.
2. **Custo/franquia com paralelismo** — limites conservadores (2 execuções default), teto de budget por demanda (estourou → pausa e avisa), afinidade conta↔demanda, fallback por prioridade de motor.
3. **Gates paralelos saturando a máquina** — semáforo `max_gates_simultaneos=1` default.
4. **Demandas tocando os mesmos arquivos** — sem merge automático; preview contínuo + badge de sobreposição; conflito volta ao kanban com opção de resolver via agente ("atualizar branch") ou manual no worktree.
5. **Windows** — `core.longpaths`; matar árvore de processos do harness antes de `worktree remove`; `git worktree prune` no boot.
6. **Push automático** — exige credenciais git válidas na máquina do serviço (credential manager/SSH); push protegido por regra "somente branches `praxis/*`" (validado no gitops antes de executar); harness continua proibido de commitar/pushar — commit e push são sempre do orquestrador. Cadastro do projeto ganha botão "testar push" (cria e remove uma branch `praxis/teste-conexao`).

## Verificação por marco

- **M1:** subir `serve`, cadastrar projeto e motores pela web, conferir persistência no banco e config efetiva (global × override); testes dos pacotes `motor` e `gitops` verdes.
- **M2:** demanda com fases manuais executa em worktree próprio; 2 demandas paralelas no mesmo projeto → 2 branches com commits independentes e push automático; pausar/retomar; derrubar o serviço no meio e ver a retomada.
- **M3:** colar PRD no chat → perguntas geradas → responder → plano gerado → editar fases → aprovar → execução completa.
- **M4:** fluxo inteiro só pelo kanban; fechar uma demanda no modo `merge_request` (publicar + link MR) e uma no `merge_local` com conflito proposital; Home refletindo custos/atividade em tempo real.
- **M5:** `POST /projects/{id}/demands` com token do sistema de chamados cria e conduz a demanda sem toque humano até "Aguardando respostas".

## Registro de Andamento

(preenchido pelo Praxis a cada fase concluída)
