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

## Fases detalhadas (micro-fases executáveis)

> Cada fase é uma fatia vertical: pequena, compilável, com testes próprios e verde nos gates
> (`go build ./...`, `go vet ./...`, `go test ./... -count=1`) antes de contar como concluída.
> "Depende de:" lista apenas dependências reais. Fase concluída → registrar em **Registro de Andamento**.

### Fase 0 — Fundação do repositório e build mínimo
**Meta:** repositório git inicializado, módulo Go criado e um `main` que compila e roda.
- [x] `git init` + `.gitignore` (ignorar `*.exe`, `worktrees/`, `*.db`, `logs/`) e commit inicial dos documentos fundadores (HTMLs + plano)
- [x] `go mod init github.com/marcos14/praxis-autonomous` (Go 1.26)
- [x] `go get modernc.org/sqlite` (driver único de runtime, puro Go)
- [x] criar layout de pastas: `cmd/praxis/`, `internal/{db,api,scheduler,pipeline,motor,gitops,intake,notify}/`, `web/`
- [x] `cmd/praxis/main.go` mínimo: subcomando `serve` (stub) + flag `-version`, compilando
**Depende de:** —
**Testes:** `go build ./...` compila; teste trivial de versão/subcomando passa.
**Observação:** cria o repositório git exigido pelo modelo "um commit por fase".

### Fase 1a — Camada de banco e framework de migrações
**Meta:** abrir SQLite com as garantias de concorrência e aplicar o schema núcleo por migração.
- [x] `internal/db`: abrir `modernc.org/sqlite` com WAL, `busy_timeout=5000`, escritor único (`SetMaxOpenConns(1)`) + pool de leitura
- [x] migrações versionadas por `PRAGMA user_version` (idempotentes, transacionais)
- [x] schema núcleo: `projects`, `engines`, `engine_accounts`, `config_entries`
- [x] resolução de `PRAXIS_HOME` (default `%LOCALAPPDATA%\praxis`) para localizar `praxis.db`
**Depende de:** 0
**Testes:** migração aplica em db temporário; `user_version` avança; reaplicar é no-op; WAL/busy_timeout verificados.

### Fase 1b — Servidor HTTP e `serve` com health
**Meta:** `praxis.exe serve` sobe o HTTP com roteador e health check.
- [x] `internal/api`: servidor HTTP, roteador, middleware base (log, recover)
- [x] endpoint `GET /healthz`
- [x] `cmd/praxis serve` inicializa db (1a) e sobe o servidor com shutdown gracioso
- [x] infraestrutura de resposta JSON e erros padronizados
**Depende de:** 0
**Testes:** `httptest` em `/healthz` retorna 200; shutdown encerra sem vazar goroutine.

### Fase 1c — CRUD de projetos (API + store)
**Meta:** cadastrar/editar projetos pela API, persistidos no banco.
- [x] store `projects` no `internal/db`
- [x] `POST/GET /api/v1/projects`, `GET/PUT /api/v1/projects/{id}`
- [x] validação: pasta existe e é repo git, `modo_integracao` ∈ {merge_request, merge_local}, slug único
**Depende de:** 1a, 1b
**Testes:** criar/listar/atualizar via `httptest`; validações rejeitam entradas inválidas.

### Fase 1d — CRUD de motores e contas
**Meta:** motores como entidade própria com prioridade (ordem de fallback) e contas.
- [x] store `engines` + `engine_accounts`
- [x] `POST/GET /api/v1/engines`, `PUT /api/v1/engines/{id}`, `PUT /api/v1/engines/ordem`
- [x] ligar/desligar, `modelo_exec`/`modelo_analise`, `budget_fase_usd`, `timeout_min`, `params`
- [x] contas por motor (`alias`, `config_dir`/CLAUDE_CONFIG_DIR, ativo)
**Depende de:** 1a, 1b
**Testes:** CRUD + reordenação de prioridade persistem e retornam ordenados.

### Fase 1e — Config em camadas (global → override por projeto)
**Meta:** resolução de config efetiva por projeto herdando do global.
- [ ] store `config_entries` (escopo global/project)
- [ ] `GET/PUT /api/v1/projects/{id}/config` + endpoint de "config efetiva"
- [ ] merge determinístico global × override, com origem de cada chave
**Depende de:** 1a, 1c
**Testes:** override por projeto sobrepõe global; chave ausente cai no global; efetiva reporta a origem.

### Fase 1f — Porte de `internal/motor`
**Meta:** portar os motores do Praxis atual, lendo config do banco.
- [ ] copiar/adaptar de `C:\Projetos\praxis`: `motor.go`, `claude.go`, `codex.go`, `opencode.go`, `claude_alias.go`
- [ ] `OpcoesRun` ganha `Dir` (worktree) e `DirLogs`; config vem do banco (motores/contas), não de arquivo
- [ ] portar testes unitários de cada motor (stub do processo filho)
**Depende de:** 1a
**Testes:** testes portados de `motor`/`claude`/`codex`/`opencode` verdes.
**Observação:** LER de `C:\Projetos\praxis` (SOMENTE LEITURA — proibido modificar a origem).

### Fase 1g — Porte de `internal/gitops`
**Meta:** operações git com suporte a worktree e branch da demanda.
- [ ] copiar/adaptar `git.go`; adicionar worktree add/remove/prune
- [ ] push da branch da demanda pós-commit (com retry), guarda "somente branches `praxis/*`"
- [ ] merge-preview (`git merge-tree --write-tree`), merge `--no-ff`, mutex por projeto
- [ ] `core.longpaths` e prune no boot (Windows)
**Depende de:** 0
**Testes:** repo bare local como remote; add/remove worktree, push, merge-preview e merge `--no-ff` cobertos.
**Observação:** LER de `C:\Projetos\praxis` (proibido modificar a origem).

### Fase 1h — Shell da UI web (projetos, motores, config)
**Meta:** frontend embutido navegável seguindo o protótipo.
- [ ] `//go:embed web/*`; HTML + CSS + ES modules vanilla, sem npm
- [ ] navegação e telas: Projetos, Motores, Configurações (consumindo 1c/1d/1e)
- [ ] botões "ver config efetiva" e formulários com herança explícita do global
**Depende de:** 1c, 1d, 1e
**Testes:** handlers dos assets embutidos servem 200; smoke de rotas da API usadas pelas telas.

### Fase 2a — Schema de demandas, fases, execuções e eventos
**Meta:** migração das tabelas do ciclo de execução + stores.
- [ ] migração: `demands`, `phases`, `runs`, `events`, `metrics_dia`
- [ ] stores com transações curtas (nunca abertas durante run de harness)
**Depende de:** 1a
**Testes:** migração aplica; CRUD básico de demand/phase/run/event.

### Fase 2b — Porte de pipeline/fallback para `ContextoExec`
**Meta:** portar o pipeline de fase para o novo modelo com contexto explícito.
- [ ] copiar/adaptar `executar.go` (`pipelineFase`) e `fallback.go` para `internal/pipeline`
- [ ] `ContextoExec` (demanda, worktree, config resolvida, fila no banco)
- [ ] `esperarResetFranquia` não bloqueia: devolve horário para o scheduler reagendar
**Depende de:** 1f, 1g, 2a
**Testes:** portados de `fallback`; pipeline de uma fase com motor stub executa até o commit.
**Observação:** LER de `C:\Projetos\praxis`.

### Fase 2c — Gates com semáforo global
**Meta:** portar gates com limite de concorrência.
- [ ] copiar/adaptar `gates.go` para `internal/pipeline/gates.go`
- [ ] semáforo global `max_gates_simultaneos` (default 1)
- [ ] execução dos gates configurados (`go build/vet/test`) por fase
**Depende de:** 2b
**Testes:** portados de gates; semáforo serializa conforme limite; gate vermelho reprova a fase.

### Fase 2d — Scheduler e worker pool com limites
**Meta:** agendar execuções respeitando limites global/por projeto/gates.
- [ ] `internal/scheduler`: fila no banco + worker pool (goroutines)
- [ ] limites: execuções simultâneas global e por projeto; afinidade conta↔demanda
- [ ] reagendamento por franquia (usa horário devolvido em 2b)
**Depende de:** 2a, 2b
**Testes:** limites respeitados sob carga simulada; reagendamento por franquia não bloqueia workers.

### Fase 2e — Worktree e branch por demanda com ciclo de fase
**Meta:** cada demanda executa em worktree/branch dedicada com o ciclo completo de fase.
- [ ] branch `praxis/d<id>-<slug>` a partir da main atualizada (`git fetch` se houver remote)
- [ ] `worktree add` em `PRAXIS_HOME/worktrees/<projeto>/<demanda>/`
- [ ] ciclo executor→gates→corretor→revisor→commit por fase (commit só do orquestrador)
**Depende de:** 1g, 2b
**Testes:** demanda de 1 fase cria branch/worktree, roda o ciclo e gera 1 commit na branch.

### Fase 2f — Push automático da branch (tolerante a falha)
**Meta:** publicar e empurrar a branch da demanda a cada commit, sem bloquear.
- [ ] `git push -u origin` no 1º commit; push da branch após cada commit de fase
- [ ] falha de push não bloqueia: evento + alerta "commits não publicados (N)" + retry no próximo commit
- [ ] ação manual `publicar_branch`
**Depende de:** 2e
**Testes:** remote bare local; push ok publica; remote indisponível → fase conclui + alerta + retry no próximo commit.
**Observação:** usa credenciais git da máquina (nunca armazenadas); push só de branches `praxis/*`.

### Fase 2g — Demanda manual executa em background
**Meta:** demanda criada com fases manuais (sem intake) anda sozinha do início ao fim.
- [ ] `POST /api/v1/projects/{id}/demands` cria demanda com fases informadas
- [ ] scheduler puxa a demanda e conduz todas as fases automaticamente
- [ ] fila de fases respeitando `depende_de` e `requer_humano`
**Depende de:** 2c, 2d, 2e
**Testes:** 2 demandas paralelas no mesmo projeto → 2 branches com commits independentes e push automático.

### Fase 2h — Card da demanda com fases e log ao vivo (SSE)
**Meta:** modal do card mostra fases e log da execução em tempo real.
- [ ] aba Plano & Fases (leitura) + aba Log ao vivo via SSE do `.jsonl` (`runs.log_ref`)
- [ ] aba Eventos consumindo `events`
- [ ] `GET /api/v1/demands/{id}/logs` (SSE)
**Depende de:** 1h, 2g
**Testes:** SSE entrega linhas de log de uma execução; fases refletem status do banco.

### Fase 2i — Pausar/retomar/cancelar e retomada pós-restart
**Meta:** controle de execução e recuperação após queda do serviço.
- [ ] `POST /api/v1/demands/{id}/actions {pausar|retomar|cancelar}`
- [ ] no boot: `git worktree prune`, matar árvore de processos órfãos, `executando → pausada → refila`
**Depende de:** 2d, 2g
**Testes:** pausar interrompe entre fases; retomar continua; derrubar no meio e reiniciar retoma a demanda.

### Fase 3a — Chat da demanda persistido e nova demanda
**Meta:** a demanda nasce como conversa; PRD colado no chat.
- [ ] migração: `chat_messages`, `questions`
- [ ] `POST /api/v1/demands/{id}/chat`; tela "Nova demanda" (projeto + chat do PRD) e aba Chat/PRD
**Depende de:** 1a, 1h
**Testes:** mensagens persistem com papel; card nasce a partir do chat.

### Fase 3b — Analista (perguntas readonly)
**Meta:** harness readonly lê o código e gera perguntas estruturadas.
- [ ] prompt `analista` (readonly) → JSON: resumo, arquivos_provaveis, perguntas (tipo/opções/sugestão/impacto)
- [ ] prompts no banco com default embutido; persistir em `questions`
- [ ] aba Perguntas (chips de sugestão + texto livre) e `POST /api/v1/demands/{id}/answers`
**Depende de:** 3a, 1f
**Testes:** com motor stub, saída JSON válida vira perguntas; respostas persistem e mudam o status.

### Fase 3c — Planejador (plano + fases) e aprovação
**Meta:** combinar PRD + respostas em plano_md e fases; aprovar/rejeitar.
- [ ] prompt `planejador` (derivado do `inicializar.go`/`defaults/*.md` do repo de referência) → `plano_md` + fases
- [ ] aba Plano & Fases: editar/reordenar/remover/exigir humano; `POST .../approve-plan`
- [ ] rejeitar-com-comentário → `replanejar`
**Depende de:** 3b, 2a
**Testes:** ponta a ponta: PRD → perguntas → respostas → plano → editar fases → aprovar → execução completa.
**Observação:** LER prompts base de `C:\Projetos\praxis` (`inicializar.go`, `defaults/`).

### Fase 4a — Kanban com SSE
**Meta:** quadro de demandas por status, tempo real, ações por botão.
- [ ] colunas = status; filtros por projeto/motor; card com projeto/fase/motor/custo/progresso/alerta
- [ ] `GET /api/v1/events` (SSE global); transições só por botão; arrastar só reordena prioridade
**Depende de:** 1h, 2a
**Testes:** mudança de status reflete no board via SSE; reordenar altera prioridade.

### Fase 4b — Home com métricas e "Precisa de você"
**Meta:** visão executiva com tiles, gráfico e pendências.
- [ ] tiles (gasto no mês, ativas, fases 7d, integradas no mês, franquia) via `metrics_dia`/view
- [ ] gráfico de gastos por dia + tabela por projeto
- [ ] lista "Precisa de você" (perguntas/aprovação/conflito) + atividade recente (SSE)
**Depende de:** 1h, 2a
**Testes:** métricas agregadas conferem com dados de teste; "Precisa de você" lista pendências reais.

### Fase 4c — Fechamento modo `merge_request`
**Meta:** demanda concluída vira "Pronta para MR" com link e preview.
- [ ] branch publicada; card mostra branch, commits, preview de conflito com a main e link "abrir MR" (`url_plataforma`)
- [ ] `GET /api/v1/demands/{id}/merge-preview`
**Depende de:** 2f, 2g
**Testes:** demanda concluída em modo MR mostra link correto e preview de conflito.

### Fase 4d — Fechamento `merge_local`, conflito e atualizar branch
**Meta:** merge local e tratamento de conflito com resolução.
- [ ] ação `integrar` → `merge --no-ff` local na main (com preview); ação `atualizar_branch` (traz main para a branch)
- [ ] conflito → status `conflito` + lista de arquivos; resolver via agente ou manual
**Depende de:** 1g, 2g
**Testes:** merge_local com conflito proposital → status `conflito` + arquivos; atualizar branch resolve.

### Fase 4e — Limpeza pós-integração e notificações
**Meta:** encerrar o ciclo e avisar.
- [ ] limpeza de worktree/branch pós-integração (no MR, após merge detectado na main)
- [ ] porte de `internal/notify` (de `notificacoes.go`/`notificar.go`): eventos do banco → Telegram/Discord/Slack/Google Chat
**Depende de:** 4c, 4d
**Testes:** worktree/branch removidos após integração; webhooks disparam nos eventos (mock HTTP).

### Fase 5a — API de intake para sistema de chamados (tokens/papéis)
**Meta:** criação e condução automática de demandas via token.
- [ ] porte de `auth.go` → `internal/api/auth.go`; `api_tokens` com papel (leitor/operador/admin)
- [ ] `POST/DELETE /api/v1/tokens`; Bearer com papéis nos endpoints
- [ ] `POST /projects/{id}/demands` (intake automatizado) conduz até "Aguardando respostas"
**Depende de:** 3b
**Testes:** token do sistema cria demanda e conduz sem toque humano até "Aguardando respostas"; papéis barram acesso.

### Fase 5b — Manual embutido
**Meta:** documentação do fluxo dentro da web.
- [ ] seção Manual (conteúdo-base do protótipo) + `GET /api/v1/manual/*`
**Depende de:** 1h
**Testes:** rotas do manual servem 200; navegação renderiza as seções.

### Fase 5c — Badge de sobreposição entre demandas
**Meta:** sinalizar demandas que tocam os mesmos arquivos.
- [ ] interseção de `arquivos_provaveis` + `git diff --name-only` entre branches
- [ ] badge no card/kanban com as demandas em sobreposição
**Depende de:** 1g, 4a
**Testes:** duas demandas tocando o mesmo arquivo exibem badge; sem interseção, sem badge.

### Fase 5d — Serviço Windows, retenção e backup
**Meta:** operar como serviço com manutenção do banco.
- [ ] instalação como serviço Windows (e systemd) do `praxis.exe serve`
- [ ] retenção de logs/eventos e backup periódico de `praxis.db`
**Depende de:** 1a, 1b
**Testes:** rotina de backup/retenção coberta por teste; smoke do modo serviço.
**Observação:** requer_humano — registro/validação como serviço exige privilégios de admin na máquina-alvo (código e testes de backup/retenção são automatizáveis).

### Fase 5e — Importador opcional do Praxis atual
**Meta:** importar projetos existentes uma única vez.
- [ ] ler `autopilot.json`/`fases.csv` do Praxis atual e criar `projects`/config correspondentes (idempotente, uma vez)
**Depende de:** 1a, 1c
**Testes:** importa fixtures de `autopilot.json`/`fases.csv` para projetos/config; reimportar não duplica.
**Observação:** opcional; ativação é decisão de negócio.

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

> Memória compartilhada entre fases. Cada execução de fase, ao concluir com gates verdes, acrescenta
> uma linha aqui (fase, data, commit, decisões relevantes e pendências que a próxima fase precisa saber).
> A próxima fase LÊ este registro antes de começar.

| Fase | Título | Status | Commit | Data | Notas / decisões / pendências |
|------|--------|--------|--------|------|-------------------------------|
| —    | (nenhuma fase concluída ainda) | — | — | — | Plano quebrado em micro-fases; aguardando início da Fase 0. |
| 0    | Fundação do repositório e build mínimo | Concluída (gates verdes) | (pelo orquestrador) | 2026-07-15 | Ver detalhes abaixo. |
| 1a   | Camada de banco e framework de migrações | Concluída (gates verdes) | (pelo orquestrador) | 2026-07-15 | Ver detalhes abaixo. Tabelas reais: `projects`, `engines`, `engine_accounts`, `config_entries` (schema versão 1). |
| 1b   | Servidor HTTP e `serve` com health | Concluída (gates verdes) | (pelo orquestrador) | 2026-07-15 | Ver detalhes abaixo. `internal/api` com `Novo(Opcoes)`/`Handler()`; `GET /healthz`; middlewares `comLog`/`comRecover`; `serve` inicializa db + shutdown gracioso. Bind default `127.0.0.1:7799`. |
| 1c   | CRUD de projetos (API + store) | Concluída (gates verdes) | (pelo orquestrador) | 2026-07-15 | Ver detalhes abaixo. Store em métodos de `*db.DB` (`CriarProjeto`/`ListarProjetos`/`ObterProjeto`/`AtualizarProjeto`); rotas `POST/GET /api/v1/projects`, `GET/PUT /api/v1/projects/{id}`; erros sentinela `db.ErrNaoEncontrado`/`db.ErrSlugDuplicado`; validação de repo git via `git rev-parse --is-inside-work-tree`. Códigos de erro API: `invalido`(400), `nao_encontrado`(404), `slug_duplicado`(409). |
| 1d   | CRUD de motores e contas | Concluída (gates verdes) | (pelo orquestrador) | 2026-07-15 | Ver detalhes abaixo. Store em métodos de `*db.DB` (motores: `CriarMotor`/`ListarMotores`/`ObterMotor`/`AtualizarMotor`/`ReordenarMotores`/`ProximaPrioridadeMotor`; contas: `CriarConta`/`AtualizarConta`/`RemoverConta`/`ListarContas`). Rotas: `POST/GET /api/v1/engines`, `GET/PUT /api/v1/engines/{id}`, `PUT /api/v1/engines/ordem`, `POST /api/v1/engines/{id}/accounts`, `PUT/DELETE /api/v1/engines/{id}/accounts/{contaId}`. Motor GET/list já traz `contas[]`. Novos erros sentinela `db.ErrNomeDuplicado`/`db.ErrAliasDuplicado`/`db.ErrOrdemInvalida`; códigos API novos: `nome_duplicado`(409), `alias_duplicado`(409). Prioridade só muda via `/ordem` (não pelo PUT do motor). |

### Fase 0 — Fundação do repositório e build mínimo (2026-07-15)

**O que foi feito**
- Repositório git já estava inicializado (commits `init` → `praxis plano`); os documentos fundadores (HTMLs + plano) já estavam versionados. `.gitignore` complementado **fora** dos marcadores gerenciados pelo próprio Praxis (bloco `>>> praxis <<<`, que não deve ser editado) com: `*.exe`, `*.db`, `*.db-shm`, `*.db-wal`, `/worktrees/`, `/logs/`. Verificado com `git check-ignore` (todos ignorados).
- `go mod init github.com/marcos14/praxis-autonomous`; diretiva normalizada para `go 1.26` (o `go mod init` gera `go 1.26.4`; ajustado para casar com o plano e com o repo de referência `C:\Projetos\praxis`).
- `go get modernc.org/sqlite` → **v1.53.0** (driver puro Go, sem cgo). Ancorado como dependência **direta** via blank import (`_ "modernc.org/sqlite"`) em `internal/db/doc.go`, para sobreviver a `go mod tidy` sem que Fase 1a precise reintroduzi-la. `go.sum` gerado e versionado.
- Layout criado: `cmd/praxis/` e `internal/{db,api,scheduler,pipeline,motor,gitops,intake,notify}/`, cada pacote com um `doc.go` mínimo (declaração de `package` + docstring apontando a fase que o implementa) para que a pasta seja um pacote Go real, rastreável pelo git e compilável por `go build ./...`. `web/` com `.gitkeep` (não é pacote Go; receberá os assets embutidos na Fase 1h).
- `cmd/praxis/main.go`: entrypoint testável `run(args, out, errOut)` com flag `-version` e despacho de subcomando; `serve` é stub (imprime aviso; servidor real a partir da Fase 1b). Testes em `main_test.go` cobrem `-version`, `serve`, ausência de subcomando e subcomando desconhecido.

**Gates (verdes)**
- `go build ./...` OK · `go vet ./...` OK · `go test ./... -count=1` OK (pacote `cmd/praxis` testado; demais pacotes ainda sem testes, esperado nesta fase).
- Binário exercitado manualmente: `-version` → `0.0.0-dev`; `serve` → mensagem stub; sem subcomando e subcomando inválido → erro + exit code 1.

**Decisões / desvios**
- `versao` é `var` (não `const`) para permitir override via `-ldflags "-X main.versao=..."` em builds futuros; valor atual `0.0.0-dev`.
- Optou-se por `doc.go` por pacote em vez de `.gitkeep` nos diretórios `internal/*`: mantém a pasta versionada **e** já constitui um pacote Go válido, evitando pastas vazias que o Go ignora.
- Não foi feito `git commit`/`push` (responsabilidade do orquestrador). `automacao/` (config do Praxis clássico) é gerenciado pela esteira e não foi tocado.

**Achados úteis para as próximas fases**
- Versões resolvidas nesta máquina: Go **1.26.4**; `modernc.org/sqlite` **v1.53.0** (traz dependências indiretas: `modernc.org/libc`, `modernc.org/memory`, `modernc.org/mathutil`, `golang.org/x/sys`, `github.com/dustin/go-humanize`, `github.com/ncruces/go-strftime`, `github.com/mattn/go-isatty`, `github.com/remyoudompheng/bigfft`, `github.com/google/uuid`).
- O nome do driver `database/sql` do modernc é **`"sqlite"`** (não `"sqlite3"`) — usar `sql.Open("sqlite", dsn)` na Fase 1a.
- Repo de referência `C:\Projetos\praxis` usa `module github.com/marcos14/praxis` e `go 1.26` — mantivemos o mesmo prefixo de módulo/estilo.
- O `.gitignore` tem um bloco gerenciado pelo Praxis clássico (entre marcadores `>>> praxis <<<`) que **não deve ser editado**; qualquer ignore novo vai fora dele.
- `PRAXIS_HOME` (default `%LOCALAPPDATA%\praxis`) ainda não é resolvido em código — fica para a Fase 1a, conforme o plano.

**Pendências descobertas:** nenhuma. Todo o escopo da Fase 0 foi entregue.

### Fase 1a — Camada de banco e framework de migrações (2026-07-15)

**O que foi feito**
- `internal/db/paths.go`: `PraxisHome()` e `CaminhoDB()`. Precedência de resolução: `PRAXIS_HOME` (se não-vazia) → no Windows `%LOCALAPPDATA%\praxis` → nos demais SOs `<os.UserConfigDir>/praxis`. `CaminhoDB()` cria o diretório (`MkdirAll 0755`) e retorna `<home>/praxis.db`. Valor em branco/espaços na env é ignorado (cai no default).
- `internal/db/db.go`: tipo `DB{ Escritor, Leitor *sql.DB, Caminho string }`. `Abrir(caminho)` abre o **escritor único** (`SetMaxOpenConns(1)`), faz `Ping`, roda `Migrar`, e só então abre o **pool de leitura** (`MaxOpenConns = max(4, NumCPU)`). `AbrirPadrao()` resolve via `CaminhoDB()`. `Fechar()` fecha ambas (leitor primeiro) devolvendo o primeiro erro.
- PRAGMAs aplicadas **via DSN** (parâmetros `_pragma=` do modernc, garantindo que toda conexão do pool receba): escritor → `busy_timeout(5000)`, `journal_mode(WAL)`, `synchronous(NORMAL)`, `foreign_keys(1)`; leitor → `busy_timeout(5000)`, `foreign_keys(1)`, `query_only(1)` (leitor não seta WAL — já persiste no arquivo; `query_only` é salvaguarda contra escrita acidental pelo pool de leitura).
- `internal/db/migracoes.go`: framework de migrações. `[]migracao{versao,nome,sql}` aplicado em ordem; cada migração roda o SQL **e** o `PRAGMA user_version = N` na **mesma transação** (atômico, idempotente). `Migrar(db) (versaoFinal, aplicadas, err)`; `VersaoAtual(db)`; `VersaoSchema()` = `len(migracoes)`. Downgrade (banco em versão maior que o binário) → erro explícito, sem "desmigrar".
- Migração **versão 1** = schema núcleo com as 4 tabelas do modelo de dados: `projects`, `engines`, `engine_accounts`, `config_entries`. Inclui CHECKs (`modo_integracao ∈ {merge_request,merge_local}`, `ativo ∈ {0,1}`, `escopo ∈ {global,project}` com coerência escopo↔project_id), FKs com `ON DELETE CASCADE`, e índices únicos parciais de config (`ux_config_global` por `chave`; `ux_config_project` por `(project_id,chave)`).
- Removido o blank import de `modernc.org/sqlite` que estava em `internal/db/doc.go` (Fase 0) — agora o import (blank) vive em `db.go`, onde o driver é de fato usado por `sql.Open("sqlite", …)`. `go mod tidy` mantém a dependência **direta** (verificado; `go.mod`/`go.sum` sem alteração).

**Gates (verdes)**
- `go build ./...` OK · `go vet ./...` OK · `go test ./... -count=1` OK. Pacote `internal/db` com 16 testes passando (`paths_test.go`, `db_test.go`, `migracoes_test.go`): aplica migração em db temporário, `user_version` avança, reaplicar é no-op, rejeita versão futura, WAL/`busy_timeout`/`foreign_keys`/`query_only` verificados por `PRAGMA`, escrita-no-escritor→leitura-no-leitor, reabrir preserva dados/versão, CHECKs e FK rejeitam entradas inválidas.

**Decisões / desvios**
- Identificadores/estrutura em **português** e estilo do repo de referência (`C:\Projetos\praxis`, que é CSV — **não** usa SQLite; portanto `internal/db` é código **novo**, não portado).
- PRAGMAs por **DSN `_pragma=`** em vez de `Exec` pós-conexão: cada conexão nova do pool herda automaticamente, sem hook `Connect`.
- Leitor **não** abre em `mode=ro` (falharia se o arquivo ainda não existisse antes do escritor criar); usa `query_only(1)`, que dá a mesma garantia sem depender de ordem de criação.
- `engine_accounts` ficou **exatamente** com as colunas do modelo do plano (`id, engine_id, alias, config_dir, ativo` + índice de FK). O "espelho de franquia (`esgotado_até`)" citado na seção *Franquia/esgotamento* **não** foi adicionado agora para não adiantar escopo de outra fase — ver Pendências descobertas.
- Datas default em ISO-8601 UTC via `strftime('%Y-%m-%dT%H:%M:%fZ','now')`; campos JSON como `TEXT` com default válido (`'[]'`, `'{}'`, `'null'`).
- Não foi feito `git commit`/`push` (responsabilidade do orquestrador).

**Achados úteis para as próximas fases**
- **API do pacote `db`** para as fases 1c/1d/1e/2a: use `db.Abrir(caminho)`/`db.AbrirPadrao()`; escreva por `d.Escritor` (serializado) e leia por `d.Leitor`; **nunca** escreva pelo `d.Leitor` (falha por `query_only`). Transações de escrita são naturalmente serializadas pelo escritor único — mantenha-as curtas (nunca abertas durante um run de harness).
- **Adicionar tabelas (Fase 2a etc.):** acrescente `{versao: 2, …}` ao slice `migracoes` em `migracoes.go` — **nunca** edite a migração 1 já liberada. `VersaoSchema()` avança sozinho.
- **Nomes reais das tabelas/colunas** já criadas (conferir antes de escrever SQL nas próximas fases):
  - `projects(id, nome, slug UNIQUE, pasta, branch_principal='main', modo_integracao, url_plataforma, add_dirs JSON='[]', ativo, criado_em)`
  - `engines(id, nome UNIQUE, prioridade, ativo, modelo_exec, modelo_analise, budget_fase_usd, timeout_min, params JSON='{}')`
  - `engine_accounts(id, engine_id→engines, alias, config_dir, ativo, UNIQUE(engine_id,alias))`
  - `config_entries(id, escopo, project_id→projects, chave, valor JSON)` com únicos parciais por escopo.
- **Config em camadas (Fase 1e):** a unicidade já é garantida pelo banco — 1 linha por `chave` global e 1 por `(project_id,chave)`. O merge determinístico global×override e "origem de cada chave" ficam na Fase 1e (não implementados aqui, conforme o plano).
- **`serve` ainda não inicializa o db** — isso é da Fase 1b (`AbrirPadrao()` no boot + shutdown gracioso). `cmd/praxis/main.go` segue stub.
- Driver `database/sql` do modernc é **`"sqlite"`** (const `nomeDriver`); WAL fica **persistido no arquivo** após o primeiro escritor.

**Pendências descobertas**
- **Espelho de franquia em `engine_accounts`** — Meta: persistir `esgotado_até` por conta para sobreviver a restart (a seção *Franquia/esgotamento* do plano cita "espelho em `engine_accounts`", mas o modelo de dados da tabela não lista a coluna). Não implementado por pertencer ao escopo de franquia/scheduler (Fases 2b/2d), não ao schema núcleo de 1a. Mini-checklist quando for a hora: [ ] migração nova adicionando coluna `esgotado_ate TEXT` (ISO-8601, default `''`) a `engine_accounts`; [ ] store lê/grava ao entrar/sair de esgotamento; [ ] `gestorFranquia` em memória hidrata a partir dessa coluna no boot.

---

## Fases descobertas (adicionadas pelo Praxis)

Fases inseridas automaticamente a partir de pendencias descobertas pelo revisor.

### 1a.n1 — Persistência do espelho de franquia em engine_accounts

Status: avaliar viabilidade
Depende de: 1a

> Baixo valor tecnico: aguarda avaliacao humana de viabilidade. Nao sera executada automaticamente enquanto o status for `avaliar viabilidade`.

Meta: Persistir o horário de esgotamento por conta (esgotado_até) em engine_accounts para o gestorFranquia sobreviver a restart, conforme a seção Franquia/esgotamento do plano — o modelo de dados descreve o espelho mas a tabela do schema núcleo (versão 1) não o inclui, e nenhum checklist das fases de franquia (2b/2d) o cita explicitamente.

- [ ] Nova migração (versão >=2) adicionando coluna esgotado_ate TEXT (ISO-8601, default '') a engine_accounts, sem editar a migração 1
- [ ] Store lê/grava esgotado_ate ao entrar/sair de esgotamento de conta
- [ ] gestorFranquia hidrata o estado em memória a partir da coluna no boot
- [ ] Teste cobrindo persistência do esgotamento através de fechar/reabrir o banco

---

### Fase 1b — Servidor HTTP e `serve` com health (2026-07-15)

**O que foi feito**
- `internal/api/servidor.go`: tipo `Servidor` construído por `Novo(Opcoes{Banco *db.DB, Log *slog.Logger})`; expõe o `http.Handler` já com middlewares via `Handler()`. Roteador é o `http.ServeMux` da stdlib usando **method-pattern** do Go 1.22+ (`mux.HandleFunc("GET /healthz", …)`) — POST em `/healthz` cai automaticamente em `405` e rota inexistente em `404`, sem código extra.
- Endpoint `GET /healthz`: sem banco → liveness (`{"status":"ok"}`, 200); com banco → faz `Leitor.PingContext` e devolve **200 `{"status":"ok","banco":"ok"}`** ou **503 `{"status":"degradado","banco":"<erro>"}`** (readiness). Inclui `versao` (preenchida por `api.Versao`, setada no `serve` a partir da versão do binário).
- `internal/api/middleware.go`: `comLog` (loga método/caminho/status/bytes/duração via `slog`) e `comRecover` (captura panic de handler, loga e responde `500` padronizado se nada foi escrito). Encadeados por `encadear(h, comRecover, comLog)` — **recover na camada mais externa**, envolvendo inclusive o log. `capturaStatus` embrulha o `ResponseWriter` para lembrar status/bytes e repassa `Flush()` (preparado para SSE das fases futuras).
- `internal/api/respostas.go`: infraestrutura de resposta JSON (`responderJSON`) e **erro padronizado** — envelope `ErroResp{ Erro: ErroDetalhe{ Codigo, Mensagem } }` via `responderErro`. Content-Type `application/json; charset=utf-8` + `Cache-Control: no-store`.
- `cmd/praxis/main.go`: `serve` agora **inicializa o banco** (`db.AbrirPadrao()`), monta o `api.Servidor` e sobe o HTTP com **shutdown gracioso**. `run`/`serve` receberam `context.Context`; `main` usa `signal.NotifyContext(…, os.Interrupt)` — 1º sinal dispara `Shutdown` (drena por até 10s); listener/serve extraídos em `servirHTTP(addr)`/`servirListener(ln)` para teste determinístico. Flag `serve -addr` (default `127.0.0.1:7799`).

**Gates (verdes)**
- `go build ./...` OK · `go vet ./...` OK · `go test ./... -count=1` OK. Novos testes: `internal/api` (healthz sem banco/com banco ok/banco indisponível→503/405/404, recover→500 padronizado, `responderErro`, `capturaStatus`) e `cmd/praxis` (`servirListener` atende requisição real e encerra limpo no cancelamento, sem vazar goroutine).
- Smoke manual do binário real: `serve -addr 127.0.0.1:7811` com `PRAXIS_HOME` temporário → cria `praxis.db`, `GET /healthz` = `200 {"status":"ok","versao":"0.0.0-dev","banco":"ok"}`, rota inexistente = `404`.

**Decisões / desvios**
- **Bind default restrito ao loopback** (`127.0.0.1:7799`, mesma porta do painel do Praxis clássico): acesso externo por túnel/reverse-proxy, coerente com o princípio de operação da máquina do serviço. Configurável por `-addr`.
- `/healthz` **usa o pool de leitura** para o ping (nunca o escritor único, para não competir com escritas). Banco fechado → 503, exercitado em teste.
- Roteamento por **method-pattern da stdlib** (sem router de terceiros) — mantém a dependência única de runtime (`modernc.org/sqlite`); nenhuma lib nova adicionada.
- **Sem `-race` nos gates**: o detector exige cgo, e o projeto é puro Go sem cgo por decisão de arquitetura. A concorrência do serve/shutdown é coberta por teste funcional (goroutine de `Serve` + cancelamento + verificação de não-vazamento de goroutines).
- No Windows, `kill -INT` do Git Bash **não** entrega o sinal de console ao processo — o shutdown gracioso é validado pelo teste Go (`servirListener` + `context.CancelFunc`), não pelo smoke de shell.

**Achados úteis para as próximas fases**
- **Como registrar handlers (Fases 1c/1d/1e):** adicionar rotas em `Novo` no `http.ServeMux` com method-pattern, ex.: `mux.HandleFunc("POST /api/v1/projects", s.handleCriarProjeto)`. Path params do Go 1.22+ (`mux.HandleFunc("GET /api/v1/projects/{id}", …)` + `r.PathValue("id")`) já estão disponíveis — usar isso em vez de parsing manual.
- **Padrões de resposta prontos:** use `responderJSON(w, status, v)` para sucesso e `responderErro(w, status, codigo, mensagem)` para erro (envelope `{"erro":{"codigo","mensagem"}}`). Ambos setam Content-Type/no-store. Mantenha os `codigo` estáveis (ex.: `nao_encontrado`, `invalido`, `slug_duplicado`) — o frontend vai depender deles.
- **Acesso ao banco no handler:** o `Servidor` guarda `s.banco *db.DB`; escreva por `s.banco.Escritor` (serializado) e leia por `s.banco.Leitor`. `Opcoes.Banco` pode ser nil para testar handlers que não tocam o banco.
- **`serve` já abre o banco no boot e fecha no shutdown** — as próximas fases não precisam mexer no ciclo de vida do banco no `main`; só recebem o `*db.DB` via `api.Opcoes`.
- **`api.Versao`** é uma variável de pacote setada pelo `serve`; se precisar da versão em outro handler, ela já está disponível ali (em testes fica `""`).
- **Middleware base já cobre todas as rotas** (log + recover) por estarem no `Handler()`; handlers novos herdam isso automaticamente. Para SSE (Fase 2h), o `capturaStatus` já repassa `Flush()`.

**Pendências descobertas:** nenhuma. Todo o escopo da Fase 1b foi entregue (servidor + roteador + middlewares log/recover, `GET /healthz`, `serve` com db + shutdown gracioso, resposta JSON e erros padronizados).

### Fase 1c — CRUD de projetos (API + store) (2026-07-15)

**O que foi feito**
- `internal/db/projects.go`: store de projetos como **métodos de `*db.DB`** (o `DB` já é o handle escritor/leitor). Tipo `Projeto` com tags JSON snake_case (é a forma serializada pela API). Métodos: `CriarProjeto`, `ListarProjetos`, `ObterProjeto`, `AtualizarProjeto` — todos recebem `context.Context`. Escritas por `d.Escritor`, leituras por `d.Leitor`.
- Erros sentinela do pacote `db`: `ErrNaoEncontrado` (linha ausente) e `ErrSlugDuplicado` (violação de `projects.slug` UNIQUE). `AtualizarProjeto`/`CriarProjeto` traduzem o erro do driver via `strings.Contains(err.Error(), "projects.slug")`.
- `CriarProjeto` usa `INSERT ... RETURNING id, criado_em` (SQLite 3.35+/modernc suportam) para devolver a linha persistida numa única ida ao banco. `AtualizarProjeto` faz `UPDATE ... WHERE id` e relê via `ObterProjeto` (preserva `criado_em`); `RowsAffected()==0` vira `ErrNaoEncontrado`.
- `add_dirs` (JSON `[]string`): normalização (`normalizarLista` — apara espaços, descarta vazias, **nunca nil** → serializa `[]` e não `null`) e `decodificarLista` tolerante a vazio/`null`.
- `internal/api/projects.go`: handlers `handleCriarProjeto` (201), `handleListarProjetos` (200), `handleObterProjeto` (200), `handleAtualizarProjeto` (200), registrados em `registrarRotasProjetos(mux)` chamado no `Novo`. Decodificação de corpo com `DisallowUnknownFields` + `io.LimitReader(1MiB)` + corpo vazio ⇒ 400.
- `montarProjeto(req, base, criando)` centraliza defaults+validação. **Semântica de update:** campos opcionais omitidos (`branch_principal`, `modo_integracao`, `ativo`) **preservam o valor atual** do projeto (`base`); só caem no default quando não há valor prévio (criação). `ativo` é `*bool` no request para distinguir "omitido" de `false`. `nome` e `pasta` são obrigatórios sempre.
- **Slug:** aceita o informado; se vazio, deriva do `nome` na criação (via `gerarSlug`: minúsculo, ASCII alfanumérico com hífens, sem hífens nas pontas); no update sem slug, mantém o atual. Unicidade garantida pelo índice do banco → 409.
- **Validação de repo git** (`validarPastaRepoGit`): pasta existe + é diretório + `git -C <pasta> rev-parse --is-inside-work-tree` == "true" (mesma detecção do Praxis atual, `git.go:gitToplevel`). Erros amigáveis (400 `invalido`) antes de tocar o banco.

**Gates (verdes)**
- `go build ./...` OK · `go vet ./...` OK · `go test ./... -count=1` OK. Novos testes: `internal/db/projects_test.go` (12: criar preenche id/criado_em, slug duplicado, normalização de add_dirs, add_dirs vazio→`[]`, listar ordenado por nome NOCASE + vazio não-nil, obter/atualizar/inexistente, slug dup no update, CHECK de modo rejeita) e `internal/api/projects_test.go` (16, via `httptest`: criar OK/slug explícito/pasta inexistente/pasta não-git/modo inválido/nome obrigatório/slug dup 409/corpo inválido/campo desconhecido, listar ordenado, obter/404/id inválido, atualizar/preserva ativo/preserva modo/404).
- Smoke do binário real (`serve` + `curl`, `PRAXIS_HOME` temp, repo git de teste): `POST` → 201 (slug `praxis-auto` derivado, `branch_principal=main`, `ativo=true`, `add_dirs` preservado); `PUT` → 200 (nome/ativo alterados, slug e `criado_em` preservados, modo `merge_local` preservado quando omitido); `POST` repetido → 409 `slug_duplicado`; pasta não-git → 400 `invalido`.

**Decisões / desvios**
- Store como métodos de `*db.DB` (não um struct `Loja` separado): o `DB` já encapsula escritor/leitor e é o que os handlers recebem via `api.Opcoes.Banco` — menos indireção, coerente com a API descrita na Fase 1a.
- `Projeto` (tipo do `db`) carrega as tags JSON e é serializado direto pela API — evita um DTO espelho. O request de entrada (`reqProjeto`) é um tipo próprio do `api` porque tem semântica diferente (`Ativo *bool`, defaults).
- Validação de repo git roda `git` como subprocesso (não há `internal/gitops` ainda — é a Fase 1g). Quando o gitops for portado, considerar centralizar essa checagem lá (ver Pendências descobertas).
- Update tem semântica de **merge sobre o estado atual** (campos opcionais omitidos preservam o valor corrente), não replace-total — mais previsível para o formulário da UI (Fase 1h) e evita reverter modo/branch por engano.
- Não foi feito `git commit`/`push` (responsabilidade do orquestrador).

**Achados úteis para as próximas fases**
- **Rotas já registradas:** `POST/GET /api/v1/projects`, `GET/PUT /api/v1/projects/{id}`. Path param via `r.PathValue("id")` (Go 1.22+). O helper `lerIDProjeto(w,r)` valida `{id}` (>0) e devolve 400 `invalido`.
- **Contrato JSON do projeto** (o frontend da Fase 1h e demais fases dependem): `{id, nome, slug, pasta, branch_principal, modo_integracao, url_plataforma, add_dirs[], ativo, criado_em}`. `add_dirs` sempre vem como array (nunca `null`).
- **Códigos de erro estáveis** (envelope `{"erro":{"codigo","mensagem"}}`): `invalido` (400, validação/corpo), `nao_encontrado` (404), `slug_duplicado` (409), `erro_interno` (500). Reutilizar os mesmos códigos nas fases 1d/1e.
- **Padrão de store reutilizável para 1d/1e/2a:** métodos em `*db.DB` com `context`, `RETURNING` na criação, erros sentinela no pacote `db`, tradução de UNIQUE por `strings.Contains` do nome da coluna. Slice de saída sempre não-nil (`[]T{}`).
- **`decodificarCorpo(w,r,dst)`** (em `internal/api/projects.go`) é genérico (recusa campo desconhecido + limita 1MiB + corpo vazio→400) e pode ser reaproveitado pelos handlers de engines/config.
- **`gerarSlug`** e o mapa `modosIntegracao` estão no pacote `api`; se outra fase precisar do slug/validação de modo, extrair para um helper compartilhado.
- **Detecção de repo git** hoje vive em `api.validarPastaRepoGit` — quando `internal/gitops` existir (1g), o botão "testar gates/push" e essa validação devem convergir para lá.

**Pendências descobertas**
- **Deleção/inativação de projeto** — o plano não pede `DELETE /projects/{id}` nesta fase (só POST/GET/PUT). Já existe `ativo` para desativar via PUT; se a UI precisar de exclusão física, será uma fase própria. Meta: avaliar se `DELETE` é necessário ou se "desativar" (ativo=false) basta. Mini-checklist: [ ] decidir soft-delete vs hard-delete; [ ] se hard, cuidar do `ON DELETE CASCADE` de `config_entries`.
- **Centralizar validação de repo git no `internal/gitops`** — hoje `api.validarPastaRepoGit` roda `git rev-parse` direto. Quando a Fase 1g portar o gitops, mover a checagem para lá (fonte única) e o handler passa a chamá-la. Meta: uma única implementação de "isso é um repo git?" no projeto. Mini-checklist: [ ] expor `gitops.EhRepoGit(pasta)`; [ ] `api` passa a usar; [ ] remover o `exec.Command` local.

### 1c.n1 — Centralizar detecção de repo git no gitops

Status: avaliar viabilidade
Depende de: 1c

> Baixo valor tecnico: aguarda avaliacao humana de viabilidade. Nao sera executada automaticamente enquanto o status for `avaliar viabilidade`.

Meta: Mover a checagem "isto é um repositório git?" de api.validarPastaRepoGit para uma função única em internal/gitops (ex.: gitops.EhRepoGit) quando o pacote existir, eliminando a duplicação com o exec.Command local do handler de projetos.

- [ ] Expor gitops.EhRepoGit(pasta) como fonte única da detecção
- [ ] Handler de projetos (montarProjeto/validarPastaRepoGit) passa a usar gitops.EhRepoGit
- [ ] Remover o exec.Command local em internal/api/projects.go
- [ ] Testes da validação de projeto continuam verdes após a troca

### Fase 1d — CRUD de motores e contas (2026-07-15)

**O que foi feito**
- `internal/db/engines.go`: store de motores e contas como **métodos de `*db.DB`** (mesmo padrão da Fase 1c). Tipos `Motor` (com `Contas []Conta` embutido) e `Conta`, ambos com tags JSON snake_case (forma serializada pela API). `Motor.Params` é `json.RawMessage` (objeto JSON livre). Escritas por `d.Escritor`, leituras por `d.Leitor`.
- Métodos de motor: `CriarMotor` (INSERT ... RETURNING id), `ListarMotores` (ordenado por `prioridade, id`, carrega **todas** as contas de uma vez e agrupa por `engine_id` — evita N+1), `ObterMotor` (motor + `ListarContas`), `AtualizarMotor` (UPDATE dos campos editáveis **exceto prioridade**, relê via `ObterMotor`), `ReordenarMotores` (redefine `prioridade` conforme a ordem dos ids, numa transação), `ProximaPrioridadeMotor` (`MAX(prioridade)+1`, 0 se vazio).
- Métodos de conta: `CriarConta` (INSERT ... RETURNING id), `AtualizarConta` (UPDATE `WHERE id AND engine_id` — isola a conta ao seu motor), `RemoverConta` (DELETE `WHERE id AND engine_id`), `ListarContas` (por motor, ordenado por id, slice não-nil).
- Erros sentinela novos no pacote `db`: `ErrNomeDuplicado` (engines.nome UNIQUE), `ErrAliasDuplicado` (engine_accounts (engine_id,alias) UNIQUE), `ErrOrdemInvalida` (lista de reordenação não é permutação exata dos motores). `traduzirErroConta` mapeia violação de **FK** (`engine_id` inexistente) para `ErrNaoEncontrado`.
- `internal/api/engines.go`: handlers `handleCriarMotor` (201), `handleListarMotores` (200), `handleObterMotor` (200), `handleAtualizarMotor` (200), `handleReordenarMotores` (200, devolve a lista reordenada), `handleCriarConta` (201), `handleAtualizarConta` (200), `handleRemoverConta` (204). Registrados em `registrarRotasMotores(mux)`, chamado no `Novo`.
- `montarMotor`/`montarConta` centralizam defaults+validação com a **mesma semântica de merge da Fase 1c**: no update, campos opcionais omitidos preservam o valor atual (`base`); só caem no default na criação. Campos numéricos/booleanos usam ponteiro no request (`*bool`/`*int`/`*float64`) para distinguir "omitido" de zero/false. Validações: `nome`/`alias` obrigatórios; `budget_fase_usd`/`timeout_min` não-negativos; `params` deve ser objeto JSON válido (`validarParams` faz `Unmarshal` em `map[string]any`).
- **Prioridade:** motor novo entra no fim (`ProximaPrioridadeMotor`) quando o request não informa `prioridade`. O PUT de motor **não** altera prioridade (ignorada) — a ordem de fallback só muda por `PUT /engines/ordem`, coerente com "arraste para reordenar" do protótipo.
- Rota literal `PUT /api/v1/engines/ordem` convive com `PUT /api/v1/engines/{id}` sem conflito: o `http.ServeMux` do Go 1.22+ dá precedência ao padrão mais específico (literal > wildcard).

**Gates (verdes)**
- `go build ./...` OK · `go vet ./...` OK · `go test ./... -count=1` OK. 38 testes novos: `internal/db/engines_test.go` (18: criar/id, params vazio→`{}`, nome duplicado, próxima prioridade, listar ordenado/vazio-não-nil, obter inexistente, update não altera prioridade, update inexistente, reordenar OK/lista incompleta/id desconhecido/repetido, contas CRUD, alias duplicado, alias igual em motores diferentes OK, conta em motor inexistente, remover inexistente, cascade ao apagar motor) e `internal/api/engines_test.go` (20: criar OK/prioridade auto/nome obrigatório/nome duplicado/params inválido/budget negativo, listar ordenado, obter 404/id inválido, update preserva campos/ignora prioridade/404, reordenar OK/inválido, contas CRUD via HTTP, conta em motor inexistente/alias obrigatório/alias duplicado/conta de outro motor 404/remover inexistente 404).
- Smoke do binário real (`serve` + `curl`, `PRAXIS_HOME` temp): `POST /engines` → 201 (`prioridade` auto 0→1, `ativo=true`, `params` round-trip, `contas:[]`); nome repetido → 409 `nome_duplicado`; `PUT /engines/ordem` inverte a ordem e devolve a lista reordenada; `POST /engines/{id}/accounts` → 201 e a conta aparece em `GET /engines/{id}`; `PUT` da conta atualiza; `DELETE` → 204.

**Decisões / desvios**
- **Contas expostas de forma aninhada** (`/engines/{id}/accounts[...]`) e embutidas no JSON do motor (`contas[]`). A seção de API do plano lista só os endpoints de `engines`, mas o checklist da fase exige "contas por motor"; os endpoints aninhados são a forma RESTful e ficam dentro do escopo. O contrato do motor passa a incluir sempre `contas` (array, nunca `null`).
- **Prioridade fora do PUT do motor** (só via `/ordem`): evita divergência/duplicação de prioridade entre os dois caminhos e casa com o gesto de arrastar do protótipo. Documentado e coberto por teste (`TestAtualizarMotorPrioridadeIgnorada`).
- `Motor.Params` como `json.RawMessage` (não `map`): preserva o JSON como veio e evita reserializações; a coluna guarda sempre um objeto válido (`normalizarParams` cai em `{}` para vazio/`null`).
- `ReordenarMotores` exige **permutação exata** de todos os motores (todos os ids, sem repetir) — comportamento previsível; lista parcial/ id desconhecido/ repetido → `ErrOrdemInvalida` (400). A UI (Fase 1h) deve enviar a lista completa na ordem nova.
- Reaproveitados os helpers da Fase 1c: `decodificarCorpo`, `responderJSON`/`responderErro`, `booleanParaInt`. Novo helper genérico `lerID(w,r,nome)` (valida qualquer path param inteiro>0) — o antigo `lerIDProjeto` de projetos permanece; considerar unificar depois (ver Pendências).
- Não foi feito `git commit`/`push` (responsabilidade do orquestrador).

**Achados úteis para as próximas fases**
- **Rotas registradas:** `POST/GET /api/v1/engines`, `GET/PUT /api/v1/engines/{id}`, `PUT /api/v1/engines/ordem`, `POST /api/v1/engines/{id}/accounts`, `PUT/DELETE /api/v1/engines/{id}/accounts/{contaId}`. Path params via `r.PathValue(...)`; use `lerID(w,r,"id")`/`lerID(w,r,"contaId")`.
- **Contrato JSON do motor** (Fase 1h/1f dependem): `{id, nome, prioridade, ativo, modelo_exec, modelo_analise, budget_fase_usd, timeout_min, params{}, contas[]}`. `contas` sempre array. **Contrato da conta:** `{id, engine_id, alias, config_dir, ativo}`. `config_dir` é o `CLAUDE_CONFIG_DIR` que a Fase 1f vai injetar no processo filho do motor.
- **Códigos de erro API** (envelope `{"erro":{"codigo","mensagem"}}`): reusa `invalido`(400), `nao_encontrado`(404), `erro_interno`(500); **novos**: `nome_duplicado`(409), `alias_duplicado`(409). Reordenação inválida usa `invalido`(400).
- **Ordem de fallback = coluna `prioridade`** (menor = tentado antes); `ListarMotores` já devolve ordenado. A Fase 2b (fallback entre motores) deve iterar os motores **ativos** nessa ordem. Filtro por `ativo` ainda **não** existe no store — `ListarMotores` devolve todos; o consumidor filtra por `Ativo` (ou adiciona um `ListarMotoresAtivos` quando precisar).
- **Contas por motor** já disponíveis via `ObterMotor(...).Contas` / `ListarContas(ctx, engineID)`. Para a afinidade conta↔demanda e distribuição (Fase 2d), essas são as contas `Ativo=true` de cada motor. O espelho de franquia `esgotado_até` **continua fora** de `engine_accounts` (fase descoberta 1a.n1, ainda em "avaliar viabilidade").
- **`ProximaPrioridadeMotor`** já existe para quem precisar inserir um motor no fim da fila.
- `AtualizarMotor` **não** persiste `contas` (o campo é ignorado no UPDATE); contas são geridas só pelos endpoints/métodos de conta.

**Pendências descobertas**
- **Deleção de motor** — o plano pede só `POST/GET/PUT` para engines (sem `DELETE /engines/{id}`); existe `ativo` para desligar via PUT. `engine_accounts` tem `ON DELETE CASCADE`, então uma remoção física é segura no schema. Meta: decidir se motor precisa de hard-delete ou se "desativar" basta. Mini-checklist: [ ] decidir soft vs hard delete de motor; [ ] se hard, expor `RemoverMotor` + rota `DELETE`; [ ] reavaliar impacto na prioridade (recompactar a ordem após remoção).
- **Unificar `lerID`/`lerIDProjeto`** — hoje há dois helpers quase idênticos no pacote `api` (`lerIDProjeto` da 1c e `lerID` genérico da 1d). Meta: uma única função para path params inteiros. Mini-checklist: [ ] trocar usos de `lerIDProjeto` por `lerID(w,r,"id")`; [ ] remover `lerIDProjeto`.
- **Filtro `ativo` no store de motores** — `ListarMotores` devolve todos; as fases de scheduler/fallback (2b/2d) vão querer só os ativos na ordem de prioridade. Meta: evitar filtragem repetida no consumidor. Mini-checklist: [ ] avaliar `ListarMotores(ctx, apenasAtivos bool)` ou `ListarMotoresAtivos`; [ ] usar no fallback.
