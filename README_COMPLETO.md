# Praxis Autonomous — Guia completo de operação

> Este é o guia de referência (instalação, TLS, API, segurança, troubleshooting).
> Para a apresentação do projeto, comece pelo [README.md](README.md). As telas de
> **Consultas** e **Planejamentos** são documentadas no *Manual* dentro da própria web.

Orquestrador de desenvolvimento multi-projeto: cadastre uma demanda (PRD ou chamado)
e ela **anda sozinha** — o analista lê o código e faz perguntas, o planejador gera o
plano em fases, e após a sua aprovação a demanda executa em background (worktree +
branch dedicada, ciclo executor → gates → corretor → revisor → commit por fase),
até ficar pronta para o Merge Request. Toda a operação é pela **interface web**; o
único comando do dia a dia é subir o serviço.

> Os três (e únicos) momentos que exigem um humano: **responder as perguntas**,
> **aprovar o plano** e **abrir o MR / integrar**.

---

## 1. Requisitos

- **Go 1.26+** (o build é puro Go — o SQLite usa `modernc.org/sqlite`, sem cgo).
- **git** no PATH (worktrees, branches, push, merge-preview).
- Um **harness de IA** instalado e autenticado para a execução real das fases:
  `claude`, `codex` ou `opencode` (o que você cadastrar como motor). Sem um motor
  válido, a UI e o intake funcionam, mas as fases não executam.
- Para **push automático**: credenciais git já configuradas na máquina
  (credential manager / SSH). O Praxis nunca armazena senha de git.

---

## 2. Build

```sh
# a partir da raiz do repositório
go build -o praxis ./cmd/praxis        # Linux/macOS
go build -o praxis.exe ./cmd/praxis    # Windows
```

Conferir a instalação:

```sh
./praxis -version
```

Rodar os testes (gates do próprio projeto):

```sh
go build ./...
go vet ./...
go test ./... -count=1
```

---

## 3. Subir o serviço

```sh
./praxis serve                           # bind padrão 127.0.0.1:7799 (uso local)
./praxis serve -addr 127.0.0.1:9000      # porta alternativa
./praxis serve -addr 0.0.0.0:7799 -tls   # acesso pela rede, HTTPS autoassinado
```

Abra **http://127.0.0.1:7799** no navegador. O serviço faz o encerramento gracioso
com `Ctrl+C` (SIGINT/SIGTERM), drenando as conexões e as tarefas em voo.

### Acesso pela rede (HTTPS)

Para acessar de outras máquinas, faça o bind em `0.0.0.0` **com TLS**:

- `-tls` gera (e reutiliza) um certificado **autoassinado** em `PRAXIS_HOME/tls`, com
  SANs para `localhost`, o hostname e os IPs da máquina no momento da geração. Se o
  IP do servidor mudar, apague `PRAXIS_HOME/tls` para regenerar.
- `-tls-cert cert.pem -tls-key key.pem` usa um certificado próprio (CA interna da
  empresa ou certificado válido) — sem nenhum passo nos dispositivos.

**Instale o certificado nos dispositivos** (necessário para o IDE web): apenas
"aceitar o risco" no aviso do navegador NÃO basta — o Chrome aplica a exceção à
página, mas **recusa o certificado nas conexões WebSocket**, e o IDE web depende
delas (sintoma: workbench abre e cai com "WebSocket close 1006"). Em cada
dispositivo, baixe `https://<servidor>:7799/cert` e instale como confiável:

- **Windows:** baixe o `praxis.crt`, duplo clique → *Instalar certificado* →
  *Usuário atual* → armazenar em **Autoridades de Certificação Raiz Confiáveis**.
  Ou, em terminal: `certutil -addstore -user Root praxis.crt`. Reinicie o navegador.
- **Android:** Configurações → Segurança → Instalar certificado (CA).
- **iOS/macOS:** abra o arquivo, instale o perfil e marque como confiável em
  Ajustes → Geral → Confiança de certificados.

O TLS não é opcional para o acesso remoto ao **IDE web** (§5): o VS Code no navegador
exige contexto seguro (`https://` ou `localhost`). Sem TLS, apenas o uso local
funciona. Um túnel SSH ou reverse-proxy com TLS próprio continuam sendo alternativas
válidas (ver §9).

### O que sobe junto com o `serve`

| Componente | O que faz |
|---|---|
| **HTTP + web** | Interface e API REST (`/api/v1`), assets embutidos no binário. |
| **Scheduler** | Executa as demandas prontas em background (worker pool com limites). |
| **Intake** | Dispara o analista (perguntas) e o planejador (plano) em background. |
| **Notificações** | Envia eventos para os canais configurados (Telegram/Discord/Slack/Google Chat). |
| **Manutenção** | Backup periódico do banco + rotação + retenção de logs/eventos. |
| **Recuperação pós-restart** | Prune de worktrees, mata harnesses órfãos e re-enfileira demandas presas em `executando`. |

---

## 4. Onde ficam os dados — `PRAXIS_HOME`

Tudo vive fora das pastas dos projetos. A raiz é `PRAXIS_HOME`:

- **Padrão:** `%LOCALAPPDATA%\praxis` (Windows) · `~/.config/praxis` (Linux/macOS).
- **Override:** variável de ambiente `PRAXIS_HOME`.

```
PRAXIS_HOME/
├─ praxis.db            # SQLite (WAL): projetos, motores, demandas, chat, fases, custos, eventos…
├─ worktrees/<projeto>/<demanda>/   # working trees git isolados por demanda
├─ logs/d<id>/          # .jsonl do log ao vivo de cada execução
├─ backups/             # praxis-YYYYMMDD-HHMMSS.db (rotação: mantém os 7 mais recentes)
├─ pids/                # PIDs dos harnesses e do IDE web (para matar órfãos no boot)
├─ tls/                 # cert.pem/key.pem autoassinados do -tls (gerados na 1ª vez)
└─ tools/               # CLI do VS Code + dados do serve-web (IDE web, baixados na 1ª vez)
```

Nas pastas dos **projetos-alvo** só entram os commits nas branches `praxis/d<id>-<slug>`.
O working tree do desenvolvedor nunca é tocado.

---

## 5. Operação pela web

Navegação (menu lateral):

- **Home** — gasto no mês, demandas ativas, fases concluídas (7d), integradas, franquia;
  gráfico de gastos por dia, tabela por projeto, lista **"Precisa de você"** e atividade
  recente (tempo real via SSE).
- **Kanban** — colunas por status; filtros por projeto/motor; arraste um card para
  reordenar a prioridade (transições de estado só por botão). Badge **⚠ sobrepõe N**
  quando duas demandas tocam os mesmos arquivos.
- **Demandas** — lista simples + o **card (modal)** com abas: Chat/PRD, Perguntas,
  Plano & Fases, Integração, Log ao vivo, Eventos.
- **Nova demanda** — escolha o projeto e cole o PRD; a demanda nasce como conversa.
- **Projetos** — cadastro e parâmetros (com herança do global).
- **Motores** — motores por prioridade (ordem de fallback), modelos, budget, contas.
- **Configurações** — config global, **Tokens de API**.
- **Manual** — o guia do fluxo dentro da própria web.

### 5.1 Fluxo completo de uma demanda

1. **Cadastre um projeto** (Projetos → *Cadastrar projeto*): informe a pasta (repo git),
   a branch principal, o modo de integração e a URL da plataforma (para o link do MR).
2. **Crie a demanda** (Nova demanda): cole o PRD. O **analista** roda em modo somente
   leitura e gera perguntas → status `Aguardando respostas`.
3. **Responda as perguntas** no card e clique *Responder tudo e gerar plano*. O
   **planejador** monta o plano e as fases → status `Aguardando aprovação`.
4. **Revise e aprove** na aba *Plano & Fases* (edite/reordene/remova fases, marque as
   que exigem humano). Aprovar → a demanda entra na fila e **executa sozinha**.
   *Rejeitar com comentário* → replaneja.
5. **Acompanhe** pelo Kanban e pela aba *Log ao vivo*. Você pode **pausar**, **retomar**
   ou **cancelar** a qualquer momento.
6. **Integre** (aba *Integração*):
   - **modo `merge_request`** (padrão): a branch é publicada a cada commit; a demanda
     concluída mostra os commits, o preview de conflito com a main e o **link para abrir
     o MR** na plataforma. O merge é feito por você lá.
   - **modo `merge_local`**: o botão **Integrar** faz `merge --no-ff` na main; sem
     conflito, worktree e branch são removidos e a demanda vai para `Integrada`.
   - **Conflito** → a demanda volta destacada com os arquivos; use **Atualizar branch**
     (traz a main para a branch) ou resolva no worktree.

### 5.1b Editar o código manualmente (IDE web)

Para ajustes e correções manuais no worktree de uma demanda, a aba **Integração** tem
o botão **Editar código ⧉**: abre o **VS Code no navegador** (`code serve-web`), direto
na pasta do worktree, com terminal integrado — sem instalar nada na máquina de quem
acessa.

Como funciona:

- **Sob demanda:** na primeira vez, o Praxis baixa o CLI oficial do VS Code do endpoint
  da Microsoft (`update.code.visualstudio.com`) para `PRAXIS_HOME/tools` e sobe uma
  instância única do serve-web (só no loopback, com connection-token gerado). A
  instância é derrubada após ~30 min sem uso; o próximo acesso sobe de novo em segundos.
- **Mesma porta, mesma segurança:** o IDE é exposto pelo proxy `/ide/*` do próprio
  Praxis — nenhuma porta extra, o connection-token nunca chega ao navegador e o acesso
  exige a permissão **`codigo.editar`** (papéis em Configurações → Usuários).
- **Gate de estado:** só com a demanda **pausada, falhada, em conflito ou encerrada** —
  nunca enquanto o scheduler pode escrever no worktree. Para mexer numa demanda em
  execução, **pause-a** primeiro; ao terminar, **retome**.
- Cada abertura do IDE gera um evento de auditoria (`codigo_acessado`) na demanda.
- **Uso local:** quem acessa por `localhost` também vê o atalho **Abrir no VS Code
  local** (`vscode://`), que usa o VS Code instalado na própria máquina.

> ⚠ O IDE web dá acesso de **desenvolvedor** ao servidor (o terminal integrado roda
> como o usuário do serviço). Conceda `codigo.editar` só a quem você daria shell na
> máquina — e, na internet pública, prefira VPN (ver §9).

### 5.2 Parâmetros de configuração (global → override por projeto)

Chaves reconhecidas (todas herdam do global quando não definidas no projeto):

| Chave | Efeito |
|---|---|
| `motor_preferido` | Motor usado por padrão na execução. |
| `execucoes_simultaneas` | Limite global de execuções em paralelo (default 2). |
| `execucoes_por_projeto` | Limite de execuções simultâneas por projeto (default 1). |
| `gates_simultaneos` | Quantas baterias de gates rodam ao mesmo tempo (default 1). |
| `max_correcoes` | Ciclos de corretor por rodada de gates. |
| `max_ciclos_revisao` | Ciclos de correção após reprovação do revisor. |
| `max_fases_novas` | Teto de fases descobertas inseridas por rodada. |
| `budget_demanda_usd` | Teto de custo por demanda. |
| `gates` | Comandos de validação (um por linha), ex.: `go build ./...`, `go test ./...`. Uma fase só conclui com todos verdes. |

> Os **gates** rodam no repositório-alvo, então use os comandos daquele projeto
> (build/lint/test). Sem gates configurados, a fase depende só da autoverificação do harness.

---

## 6. API REST (`/api/v1`)

Base: `http://127.0.0.1:7799/api/v1`. Respostas e erros em JSON
(`{"erro":{"codigo","mensagem"}}`). Health: `GET /healthz`.

Endpoints principais:

```
GET/POST /projects            GET/PUT /projects/{id}      GET/PUT /projects/{id}/config
GET/POST /engines             PUT /engines/{id}           PUT /engines/ordem
POST     /projects/{id}/demands       # intake: com "prd" (chat) ou com "fases" (manual)
GET      /demands?project=&status=    GET /demands/{id}
GET      /board                       # kanban enriquecido (progresso + motor)
PUT      /demands/ordem               # reordenar prioridade
POST     /demands/{id}/chat           POST /demands/{id}/answers
PUT      /demands/{id}/phases         POST /demands/{id}/approve-plan
POST     /demands/{id}/actions {pausar|retomar|cancelar|publicar_branch|integrar|atualizar_branch}
GET      /demands/{id}/merge-preview  GET /demands/{id}/overlap
GET      /demands/{id}/logs (SSE)     GET /demands/{id}/events
GET      /events (SSE global)         GET /metrics  GET /pendencias  GET /activity
GET      /overlaps                    GET /manual   GET /manual/{slug}
POST/GET /tokens                      DELETE /tokens/{id}
```

### 6.1 Autenticação e papéis

- **Sem token** (loopback confiável) → acesso **admin** local. É o modo da UI local.
- **Com token** (`Authorization: Bearer <token>` ou header `X-Praxis-Token`) → o papel
  do token. Um token `leitor` fica **barrado de escrita** (403).
- **Token inválido/revogado** → 401.

Papéis: `leitor` (só leitura) · `operador` (criar/agir em demandas) · `admin` (gerir
tokens, projetos, motores, config).

Crie tokens em **Configurações → Tokens de API** (o valor aparece **uma única vez**) ou
via API. Exemplo — intake automatizado de um sistema de chamados:

```sh
# 1) criar um token operador (admin)
curl -s -X POST http://127.0.0.1:7799/api/v1/tokens \
  -H 'Content-Type: application/json' \
  -d '{"nome":"chamados","papel":"operador"}'
# → { "id":1, "papel":"operador", "token":"<COPIE-AGORA>" }

# 2) abrir uma demanda pelo chamado (conduz sozinha até "Aguardando respostas")
curl -s -X POST http://127.0.0.1:7799/api/v1/projects/1/demands \
  -H 'Authorization: Bearer <TOKEN>' -H 'Content-Type: application/json' \
  -d '{"prd":"Como usuário quero X...","origem":"api","origem_ref":"chamado #4812"}'
```

---

## 7. Notificações

Canais suportados: **Telegram, Discord, Slack, Google Chat** e um webhook genérico.
A config vive na config global, chave `notificacoes` (lida a cada ciclo — muda sem
reiniciar). Formato:

```json
{
  "cabecalho": "Praxis · Produção",
  "eventos": { "push_falhou": true, "conflito": true },
  "canais": {
    "telegram":    { "ativo": true,  "token": "<bot-token>", "chat_id": "<id>" },
    "discord":     { "ativo": false, "webhook_url": "https://discord.com/api/webhooks/..." },
    "slack":       { "ativo": false, "webhook_url": "https://hooks.slack.com/services/..." },
    "google_chat": { "ativo": false, "webhook_url": "https://chat.googleapis.com/..." },
    "webhook":     { "ativo": false, "url": "https://...", "header": "Authorization: Bearer ..." }
  }
}
```

Gravar via API (a UI de edição é opcional; qualquer canal ausente em `eventos` notifica
por padrão):

```sh
curl -s -X PUT http://127.0.0.1:7799/api/v1/config \
  -H 'Content-Type: application/json' \
  -d '{"notificacoes": { ... o objeto acima ... }}'
```

---

## 8. Outros subcomandos

```sh
# Gerar a configuração para rodar como serviço (imprime; o registro efetivo exige admin):
./praxis service                      # unit systemd (Linux) ou comando sc.exe (Windows)
./praxis service -exe /opt/praxis/praxis -addr 127.0.0.1:7799 -nome praxis

# Importar projetos do Praxis clássico (lê automacao/autopilot.json + fases.csv; idempotente):
./praxis import /caminho/do/projeto [/outro/projeto ...]
```

### 8.1 Rodar como serviço

**Linux (systemd):**
```sh
./praxis service > /etc/systemd/system/praxis.service   # como root, revise o conteúdo
systemctl daemon-reload && systemctl enable --now praxis
```

**Windows (como Administrador):**
```powershell
# cole/execute o comando "sc.exe create ..." impresso por:
.\praxis.exe service
sc.exe start praxis
```

---

## 9. Segurança e boas práticas

- **Acesso remoto: sempre com TLS.** Na LAN/VPN, `-addr 0.0.0.0:7799 -tls` (ou
  certificado próprio) é o caminho direto; túnel SSH e reverse-proxy com TLS continuam
  valendo. Na **internet pública**, prefira VPN (WireGuard/Tailscale) na frente — com o
  IDE web habilitado, uma conta com `codigo.editar` comprometida equivale a um shell no
  servidor.
- Quem estiver no loopback **antes do primeiro admin ser criado** tem acesso pleno
  (modo bootstrap) — crie o primeiro usuário logo após subir o serviço.
- **Tokens** para chamadores programáticos (sistema de chamados, integrações): dê o
  menor papel necessário (`operador` para criar demandas; `leitor` para dashboards).
- **Push protegido:** o Praxis só empurra branches `praxis/*`; a main nunca é empurrada;
  o harness é proibido de commitar/pushar (commit e push são sempre do orquestrador).
- **Backups:** automáticos em `PRAXIS_HOME/backups` (mantém os 7 mais recentes). Para
  um backup manual, copie `praxis.db` com o serviço parado, ou use um dos backups gerados.

---

## 10. Solução de problemas

| Sintoma | O que verificar |
|---|---|
| Demanda fica em `pronta` e não executa | Há um **motor ativo** cadastrado e autenticado? Veja o log do serviço e a aba Log ao vivo. |
| "commits não publicados (N)" | Falha de push (rede/credenciais/branch protegida). Use **Publicar branch** no card; as credenciais git da máquina precisam estar válidas. |
| Fase reprova nos gates | Abra o **Log ao vivo**; os comandos de `gates` do projeto precisam passar no worktree. |
| Conflito na integração | Use **Atualizar branch** (traz a main) ou resolva no worktree indicado no card. |
| `/healthz` retorna `degradado` | Banco inacessível — cheque permissões de `PRAXIS_HOME` e se o disco tem espaço. |
| Porta ocupada | Suba com `-addr` em outra porta. |

---

## 11. Estrutura do repositório

```
cmd/praxis/            # binário: subcomandos serve / service / import
internal/
  db/                  # SQLite, migrações, stores
  api/                 # servidor HTTP, rotas REST, auth, SSE
  scheduler/           # fila + worker pool, executor da demanda
  pipeline/            # ciclo de fase (executor→gates→corretor→revisor→commit), gates, fallback
  motor/               # harnesses (claude/codex/opencode)
  gitops/              # git: worktree, push, merge-preview, merge
  intake/              # analista + planejador + prompts embutidos
  notify/              # notificações (canais + despachante de eventos)
  manutencao/          # backup, rotação, retenção
  importador/          # importador do Praxis clássico
  procs/               # árvore de processos dos harnesses
web/                   # frontend embutido (HTML + CSS + ES modules vanilla)
```
