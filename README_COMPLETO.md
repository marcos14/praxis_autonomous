🇺🇸 **English** · 🇧🇷 [Português (Brasil)](README_COMPLETO.pt-BR.md)

# Praxis Autonomous — Full Operations Guide

> This is the reference guide (installation, TLS, API, security, troubleshooting).
> For the project overview, start with the [README.md](README.md). The **Consultas**
> (Queries) and **Planejamentos** (Plannings) screens are documented in the *Manual*
> inside the web UI itself.

Multi-project development orchestrator: register a demand (a PRD or a ticket) and it
**moves on its own** — the analyst reads the code and asks questions, the planner
generates a phased plan, and after your approval the demand executes in the
background (worktree + dedicated branch, an executor → gates → fixer → reviewer →
commit-per-phase cycle) until it is ready for a Merge Request. Everything is operated
through the **web UI**; the only day-to-day command is starting the service.

> The three (and only) moments that require a human: **answering the questions**,
> **approving the plan**, and **opening the MR / merging**.

---

## 1. Requirements

- **Go 1.26+** (the build is pure Go — SQLite uses `modernc.org/sqlite`, no cgo).
- **git** on the PATH (worktrees, branches, push, merge preview).
- An installed and authenticated **AI harness** for the actual execution of phases:
  `claude`, `codex`, or `opencode` (whichever you register as an engine). Without a
  valid engine, the UI and the intake work, but phases do not execute.
- For **automatic push**: git credentials already configured on the machine
  (credential manager / SSH). Praxis never stores git passwords.

---

## 2. Build

```sh
# from the repository root
go build -o praxis ./cmd/praxis        # Linux/macOS
go build -o praxis.exe ./cmd/praxis    # Windows
```

Check the installation:

```sh
./praxis -version
```

Run the tests (this project's own gates):

```sh
go build ./...
go vet ./...
go test ./... -count=1
```

---

## 3. Running the service

```sh
./praxis serve                           # default bind 127.0.0.1:7799 (local use)
./praxis serve -addr 127.0.0.1:9000      # alternate port
./praxis serve -addr 0.0.0.0:7799 -tls   # network access, self-signed HTTPS
```

Open **http://127.0.0.1:7799** in the browser. The service shuts down gracefully on
`Ctrl+C` (SIGINT/SIGTERM), draining connections and in-flight tasks.

### Network access (HTTPS)

To access from other machines, bind to `0.0.0.0` **with TLS**:

- `-tls` generates (and reuses) a **self-signed** certificate in `PRAXIS_HOME/tls`,
  with SANs for `localhost`, the hostname, and the machine's IPs at generation time.
  If the server's IP changes, delete `PRAXIS_HOME/tls` to regenerate it.
- `-tls-cert cert.pem -tls-key key.pem` uses your own certificate (a company-internal
  CA or a valid certificate) — no steps needed on the devices.

**Install the certificate on the devices** (required for the web IDE): just
"accepting the risk" in the browser warning is NOT enough — Chrome applies the
exception to the page but **rejects the certificate on WebSocket connections**, and
the web IDE depends on them (symptom: the workbench opens and drops with "WebSocket
close 1006"). On each device, download `https://<server>:7799/cert` and install it
as trusted:

- **Windows:** download `praxis.crt`, double-click → *Install certificate* →
  *Current user* → store in **Trusted Root Certification Authorities**. Or, in a
  terminal: `certutil -addstore -user Root praxis.crt`. Restart the browser.
- **Android:** Settings → Security → Install a certificate (CA).
- **iOS/macOS:** open the file, install the profile, and mark it as trusted in
  Settings → General → Certificate Trust.

TLS is not optional for remote access to the **web IDE** (§5): VS Code in the
browser requires a secure context (`https://` or `localhost`). Without TLS, only
local use works. An SSH tunnel or a reverse proxy with its own TLS remain valid
alternatives (see §9).

### Behind a reverse proxy (internet)

When Praxis sits behind a reverse proxy that terminates TLS (nginx, Caddy, Cloudflare
Tunnel…), start it with `-proxy-confiavel` (or `PRAXIS_PROXY_CONFIAVEL=1`). Only then
does it trust `X-Forwarded-Proto` and `X-Forwarded-For`: the session cookie gets the
`Secure` flag even though the proxy talks plain HTTP to Praxis, sessions record the
real client IP, and the sign-in rate limit counts per client instead of per proxy.
Without the flag, ten failed sign-ins by anyone would lock every user behind that
proxy for 15 minutes. Never enable it when clients can reach Praxis directly — the
headers can be forged.

### What comes up with `serve`

| Component | What it does |
|---|---|
| **HTTP + web** | UI and REST API (`/api/v1`), assets embedded in the binary. |
| **Scheduler** | Runs ready demands in the background (worker pool with limits). |
| **Intake** | Triggers the analyst (questions) and the planner (plan) in the background. |
| **Notifications** | Sends events to the configured channels (Telegram/Discord/Slack/Google Chat). |
| **Maintenance** | Periodic database backup + rotation + log/event retention. |
| **Post-restart recovery** | Prunes worktrees, kills orphaned harnesses, and re-queues demands stuck in `executando` (running). |

---

## 4. Where the data lives — `PRAXIS_HOME`

Everything lives outside the project folders. The root is `PRAXIS_HOME`:

- **Default:** `%LOCALAPPDATA%\praxis` (Windows) · `~/.config/praxis` (Linux/macOS).
- **Override:** the `PRAXIS_HOME` environment variable.

```
PRAXIS_HOME/
├─ praxis.db            # SQLite (WAL): projects, engines, demands, chat, phases, costs, events…
├─ worktrees/<project>/<demand>/    # git working trees isolated per demand
├─ logs/d<id>/          # .jsonl live log of each execution
├─ planejamentos/p<id>/ # planning documents (.md), artifacts (.html) and referencias/
├─ consultas/c<id>/     # referencias/ — files the user attached to the query
├─ backups/             # praxis-YYYYMMDD-HHMMSS.db (rotation: keeps the 7 most recent)
├─ pids/                # PIDs of the harnesses and the web IDE (to kill orphans on boot)
├─ tls/                 # self-signed cert.pem/key.pem from -tls (generated on 1st run)
└─ tools/               # VS Code CLI + serve-web data (web IDE, downloaded on 1st use)
```

The **target projects'** folders only ever receive commits on `praxis/d<id>-<slug>`
branches. The developer's working tree is never touched.

---

## 5. Operating through the web

Navigation (side menu — labels are in Portuguese, glossed here):

- **Home** — month's spend, active demands, phases completed (7d), merged demands,
  quota; daily spend chart, per-project table, the **"Precisa de você"** ("needs
  you") list, and recent activity (real time via SSE).
- **Kanban** — columns by status; filters by project/engine; drag a card to reorder
  priority (state transitions only via buttons). A **⚠ sobrepõe N** (overlaps N)
  badge appears when two demands touch the same files.
- **Demandas** (Demands) — a simple list + the **card (modal)** with tabs: Chat/PRD,
  Questions, Plan & Phases, Integration, Live log, Events.
- **Nova demanda** (New demand) — pick the project and paste the PRD; the demand is
  born as a conversation.
- **Projetos** (Projects) — registration and parameters (inheriting from global).
- **Motores** (Engines) — engines by priority (fallback order), models, budget,
  accounts.
- **Configurações** (Settings) — global config, **API tokens**.
- **Manual** — the flow guide inside the web UI itself.

### 5.1 A demand's full flow

1. **Register a project** (Projetos → *Cadastrar projeto*): set the folder (git
   repo), the main branch, the integration mode, and the platform URL (for the MR
   link).
2. **Create the demand** (Nova demanda): paste the PRD. The **analyst** runs in
   read-only mode and generates questions → status `Aguardando respostas` (waiting
   for answers).
3. **Answer the questions** on the card and click *Responder tudo e gerar plano*
   (answer everything and generate the plan). The **planner** assembles the plan and
   its phases → status `Aguardando aprovação` (waiting for approval).
4. **Review and approve** on the *Plano & Fases* (Plan & Phases) tab (edit/reorder/
   remove phases, flag the ones that require a human). Approve → the demand joins
   the queue and **executes on its own**. *Reject with a comment* → it replans.
5. **Follow along** on the Kanban and the *Log ao vivo* (live log) tab. You can
   **pause**, **resume**, or **cancel** at any time.
6. **Merge** (the *Integração* tab):
   - **`merge_request` mode** (default): the branch is published on every commit; a
     finished demand shows the commits, the conflict preview against main, and the
     **link to open the MR** on your platform. You do the merge there.
   - **`merge_local` mode**: the **Integrar** button runs `merge --no-ff` into main;
     without conflicts, the worktree and branch are removed and the demand moves to
     `Integrada` (merged).
   - **Conflict** → the demand comes back highlighted with the conflicting files;
     use **Atualizar branch** (brings main into the branch) or resolve it in the
     worktree.

### 5.1b Editing the code manually (web IDE)

For manual adjustments and fixes in a demand's worktree, the **Integração** tab has
the **Editar código ⧉** (edit code) button: it opens **VS Code in the browser**
(`code serve-web`), straight in the worktree folder, with an integrated terminal —
nothing to install on the machine of whoever is accessing.

How it works:

- **On demand:** on first use, Praxis downloads the official VS Code CLI from
  Microsoft's endpoint (`update.code.visualstudio.com`) into `PRAXIS_HOME/tools` and
  starts a single serve-web instance (loopback only, with a generated connection
  token). The instance is torn down after ~30 min idle; the next access brings it
  back up in seconds.
- **Same port, same security:** the IDE is exposed through Praxis's own `/ide/*`
  proxy — no extra port, the connection token never reaches the browser, and access
  requires the **`codigo.editar`** permission (roles in Configurações → Usuários).
- **State gate:** only when the demand is **paused, failed, in conflict, or closed**
  — never while the scheduler might write to the worktree. To touch a running
  demand, **pause it** first; when done, **resume**.
- Every IDE opening generates an audit event (`codigo_acessado`) on the demand.
- **Local use:** whoever accesses via `localhost` also sees the **Abrir no VS Code
  local** (open in local VS Code) shortcut (`vscode://`), which uses the VS Code
  installed on their own machine.

> ⚠ The web IDE grants **developer** access to the server (the integrated terminal
> runs as the service's user). Grant `codigo.editar` only to people you would give a
> shell on the machine — and on the public internet, prefer a VPN (see §9).

### 5.2 Configuration parameters (global → per-project override)

Recognized keys (all inherit from global when not set on the project):

| Key | Effect |
|---|---|
| `motor_preferido` | Engine used by default for execution. |
| `execucoes_simultaneas` | Global limit of parallel executions (default 2). |
| `execucoes_por_projeto` | Limit of simultaneous executions per project (default 1). |
| `gates_simultaneos` | How many gate batteries run at the same time (default 1). |
| `max_correcoes` | Fixer cycles per round of gates. |
| `max_ciclos_revisao` | Fix cycles after a reviewer rejection. |
| `gates` | Validation commands (one per line), e.g. `go build ./...`, `go test ./...`. A phase only completes with all of them green. |
| `git_sufixo_praxis` | Appends " - Praxis" to the author name of commits made by Praxis (on by default). |
| `idioma` | The **instance** language (`pt-BR`, `en`, `es`, `zh-CN`; default `pt-BR`). Applies to whatever has no user in context: stored events, channel notifications, and the repository overview. Global only; takes effect without a restart. |

> The **gates** run in the target repository, so use that project's commands
> (build/lint/test). Without gates configured, a phase relies only on the harness's
> self-verification.

### 5.3 Languages (i18n)

Praxis speaks **Portuguese, English, Spanish, and Simplified Chinese**. The choice
is per person and covers the whole interface, the built-in manual, and the
language the AI answers in.

- **User preference** — the selector in the menu footer (and on the login screen)
  stores the language on your user; it follows you to any device.
- **Before login** — the browser language (`Accept-Language`) applies, falling
  back to `pt-BR`.
- **Instance language** — the global `idioma` key (§5.2) decides what has no
  owner: stored events, notifications to the team's channels, and the repository
  overview.
- **AI answers** — queries, plannings, and the analyst's questions come out in the
  language of **whoever created** the conversation; through the API with no user,
  in the instance language. Code, comments, and commit messages follow the
  repository's language, not the user's.
- **API** — the UI sends the `X-Praxis-Idioma` header; without it, the server
  falls back to `Accept-Language`. The `erro.codigo` field is stable and does
  **not** change with the language — only `erro.mensagem` is translated. To store
  the preference programmatically: `PUT /api/v1/auth/idioma` with
  `{"idioma":"en"}`.
- **Manual** — each section falls back to the pt-BR text while that page has no
  translation, so navigation never has holes.

---

## 6. REST API (`/api/v1`)

Base: `http://127.0.0.1:7799/api/v1`. Responses and errors in JSON
(`{"erro":{"codigo","mensagem"}}`). Health: `GET /healthz`.

Main endpoints:

```
GET/POST /projects            GET/PUT /projects/{id}      GET/PUT /projects/{id}/config
GET/POST /engines             PUT /engines/{id}           PUT /engines/ordem
POST     /projects/{id}/demands       # intake: with "prd" (chat) or "fases" (manual)
GET      /demands?project=&status=    GET /demands/{id}
GET      /board                       # enriched kanban (progress + engine)
PUT      /demands/ordem               # reorder priority
POST     /demands/{id}/chat           POST /demands/{id}/answers
PUT      /demands/{id}/phases         POST /demands/{id}/approve-plan
POST     /demands/{id}/actions {pausar|retomar|cancelar|publicar_branch|integrar|atualizar_branch}
GET      /demands/{id}/merge-preview  GET /demands/{id}/overlap
GET      /demands/{id}/logs (SSE)     GET /demands/{id}/events
GET      /events (SSE global)         GET /metrics  GET /pendencias  GET /activity
GET      /overlaps                    GET /manual   GET /manual/{slug}
POST/GET /consultas                   # queries: create (project or group) / list
GET      /consultas/{id}              DELETE /consultas/{id}
GET/POST /consultas/{id}/chat         GET /consultas/{id}/progresso
POST     /consultas/{id}/turno        # turn with no new message (pending attachments / retry)
GET/POST /consultas/{id}/referencias  # attached files (POST = multipart, field "arquivo")
GET/DEL  /consultas/{id}/referencias/{arquivo}
POST/GET /tokens                      DELETE /tokens/{id}
```

### 6.1 Authentication and roles

Two kinds of credentials:

- **Users** (the web UI): e-mail + password. Sign-in issues a short-lived JWT
  (`sessao_jwt_min`, default 60 min) that the browser keeps **only in memory**, plus a
  **session cookie** (`praxis_sessao`: HttpOnly, SameSite=Strict, scoped to
  `/api/v1/auth`) that renews the JWT through `POST /api/v1/auth/refresh`. The session
  slides on every use and expires after `sessao_inatividade_dias` without use (default
  30) or `sessao_maxima_dias` after sign-in (default 90) — all three are global config
  keys (**Settings → Sessions and sign-in**), applied without restart.
  `POST /api/v1/auth/logout` revokes the session. Changing the password ends the
  user's other sessions; a password reset by an admin or deactivating the user ends
  them all. Users list and end their own sessions in **My account**
  (`GET/DELETE /api/v1/auth/sessoes`). Sign-in is rate-limited: 10 failures in 15
  minutes per IP or per e-mail answer `429` with `Retry-After`.
- **API tokens** (integrations): `Authorization: Bearer <token>` or the
  `X-Praxis-Token` header → the token's role. A `leitor` (reader) token is **blocked
  from writes** (403). Invalid/revoked token → 401.
- **Bootstrap:** while no user exists, anyone reaching the service has admin access
  so the first admin can be created — do it right after the first start.

Permissions are resolved from the database on every request: changing a role or
deactivating a user takes effect immediately. Token roles: `leitor` (read-only) ·
`operador` (create/act on demands) · `admin` (manage tokens, projects, engines,
config).

Create tokens in **Configurações → Tokens de API** (the value is shown **only
once**) or via the API. Example — automated intake from a ticketing system:

```sh
# 1) create an operator token (admin)
curl -s -X POST http://127.0.0.1:7799/api/v1/tokens \
  -H 'Content-Type: application/json' \
  -d '{"nome":"tickets","papel":"operador"}'
# → { "id":1, "papel":"operador", "token":"<COPY-IT-NOW>" }

# 2) open a demand from a ticket (drives itself to "Aguardando respostas")
curl -s -X POST http://127.0.0.1:7799/api/v1/projects/1/demands \
  -H 'Authorization: Bearer <TOKEN>' -H 'Content-Type: application/json' \
  -d '{"prd":"As a user, I want X...","origem":"api","origem_ref":"ticket #4812"}'
```

---

## 7. Notifications

Supported channels: **Telegram, Discord, Slack, Google Chat**, and a generic
webhook. The config lives in the global config under the `notificacoes` key (read on
every cycle — changes apply without a restart). Format:

```json
{
  "cabecalho": "Praxis · Production",
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

Write it via the API (the editing UI is optional; any channel missing from `eventos`
notifies by default):

```sh
curl -s -X PUT http://127.0.0.1:7799/api/v1/config \
  -H 'Content-Type: application/json' \
  -d '{"notificacoes": { ... the object above ... }}'
```

---

## 8. Other subcommands

```sh
# Generate the config to run as a service (prints it; actual registration requires admin):
./praxis service                      # systemd unit (Linux) or sc.exe command (Windows)
./praxis service -exe /opt/praxis/praxis -addr 127.0.0.1:7799 -nome praxis

# Import projects from classic Praxis (reads automacao/autopilot.json + fases.csv; idempotent):
./praxis import /path/to/project [/another/project ...]
```

### 8.1 Running as a system service

**Linux (systemd):**
```sh
./praxis service > /etc/systemd/system/praxis.service   # as root, review the contents
systemctl daemon-reload && systemctl enable --now praxis
```

**Windows (as Administrator):**
```powershell
# paste/run the "sc.exe create ..." command printed by:
.\praxis.exe service
sc.exe start praxis
```

---

## 9. Security and good practices

- **Remote access: always with TLS.** On a LAN/VPN, `-addr 0.0.0.0:7799 -tls` (or
  your own certificate) is the direct path; an SSH tunnel or a reverse proxy with
  TLS also work. On the **public internet**, prefer a VPN (WireGuard/Tailscale) in
  front — with the web IDE enabled, a compromised account holding `codigo.editar`
  is equivalent to a shell on the server.
- Anyone on loopback **before the first admin is created** has full access
  (bootstrap mode) — create the first user right after starting the service.
- **Tokens** for programmatic callers (ticketing systems, integrations): grant the
  smallest role needed (`operador` to create demands; `leitor` for dashboards).
- **Sign-in protection:** 10 failed attempts in 15 minutes per IP or per e-mail block
  new attempts (429). Behind a proxy, enable `-proxy-confiavel` so the limit counts
  per real client and the session cookie is marked `Secure` (§3).
- **Protected push:** Praxis only pushes `praxis/*` branches; main is never pushed;
  the harness is forbidden from committing/pushing (commit and push are always done
  by the orchestrator).
- **Backups:** automatic in `PRAXIS_HOME/backups` (keeps the 7 most recent). For a
  manual backup, copy `praxis.db` with the service stopped, or use one of the
  generated backups.

---

## 10. Troubleshooting

| Symptom | What to check |
|---|---|
| Demand sits in `pronta` (ready) and doesn't run | Is there an **active engine** registered and authenticated? Check the service log and the Live log tab. |
| "commits não publicados (N)" (unpublished commits) | Push failure (network/credentials/protected branch). Use **Publicar branch** on the card; the machine's git credentials must be valid. |
| A phase fails the gates | Open the **Live log**; the project's `gates` commands must pass in the worktree. |
| Conflict on merge | Use **Atualizar branch** (brings main in) or resolve it in the worktree shown on the card. |
| `/healthz` returns `degradado` (degraded) | Database unreachable — check `PRAXIS_HOME` permissions and disk space. |
| Port already in use | Start with `-addr` on another port. |

---

## 11. Repository layout

```
cmd/praxis/            # binary: serve / service / import subcommands
internal/
  db/                  # SQLite, migrations, stores
  api/                 # HTTP server, REST routes, auth, SSE
  scheduler/           # queue + worker pool, demand executor
  pipeline/            # phase cycle (executor→gates→fixer→reviewer→commit), gates, fallback
  motor/               # harnesses (claude/codex/opencode)
  gitops/              # git: worktree, push, merge-preview, merge
  intake/              # analyst + planner + embedded prompts
  notify/              # notifications (channels + event dispatcher)
  manutencao/          # backup, rotation, retention
  importador/          # classic Praxis importer
  procs/               # harness process tree
web/                   # embedded frontend (HTML + CSS + vanilla ES modules)
```
