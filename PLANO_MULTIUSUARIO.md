# Plano de implementação — multiusuário e prontidão open source

Atualizado em: 2026-08-10 — **Fases A–D entregues** (gates verdes: `go build`,
`go vet` e `go test ./...` em Windows e Linux). A Fase E (ambientes
conteinerizados) é o próximo incremento.

Desvios de desenho registrados na entrega:

- **Fase A:** motores continuam geríveis só por `config.gerir` (o dono do motor
  é informativo + âncora da visibilidade); a UI usa a visibilidade simples
  (público/privado/grupo) e a ACL fina de projetos segue na aba Acesso.
- **Fase C:** a transferência de credencial SSH é o campo `ssh_user_id` do
  PUT /projects/{id} (0 remove; >0 exige que o usuário já tenha chave).
- **Fase D:** o "caminho explícito no cadastro do motor" virou o override por
  variável de ambiente `PRAXIS_CLI_<VENDOR>` — mesma capacidade, sem plumbing de
  um campo novo por engine; a resolução é PRAXIS_CLI_* → PATH → gerenciado.
  As URLs de download aceitam mirror por `PRAXIS_DOWNLOAD_BASE_<VENDOR>`.

## Objetivo

Tirar do usuário a necessidade de entender (e de acessar) o SO hospedeiro. Hoje o
Praxis pressupõe um operador único que é dono da máquina: os projetos são pastas
que ele mesmo clonou, os motores usam as credenciais da conta dele, o harness
precisa estar no PATH e os gates exigem o toolchain de cada projeto instalado no
host. Para abrir o projeto como open source, cada uma dessas dependências vira
uma barreira de adoção. Este plano fecha seis frentes:

1. **Donos e visibilidade** — projetos e motores pertencem a um usuário, a um
   grupo ou a todos; quem cria decide.
2. **Execução não-root no Linux** — o Claude recusa `--dangerously-skip-permissions`
   com UID 0; o Praxis passa a garantir estruturalmente que o harness nunca roda
   como root.
3. **Chave SSH por usuário + cadastro por clone** — o usuário gera a chave pela
   web, cadastra no GitHub/GitLab e clona projetos pela interface, sem shell no
   servidor.
4. **Instalador de harnesses** — o Praxis baixa o CLI oficial do vendor quando
   não o encontra; o login pela web (já entregue no PLANO.md) completa o fluxo.
5. **Ambientes conteinerizados** — o projeto define uma imagem onde a worktree é
   desenvolvida, executada e testada; o host não precisa de toolchain nenhum.
6. **Postura de segurança open source** — bootstrap, IDE web e Dockerfile oficial
   revisados para instalações operadas por desconhecidos.

## Decisões de arquitetura

### Fase A — donos e visibilidade (projetos e motores)

A semântica pedida — usuário XOR grupo, ausência = público — **já existe** no
banco: é exatamente a da `project_access` (migração 9), inclusive com a
filtragem de leitura implementada e testada. A fase A reusa esse desenho em vez
de inventar outro:

- **Projetos:** `projects.owner_user_id` (nullable; legados ficam sem dono). Na
  criação o usuário escolhe a visibilidade:
  - **privado** → grava `project_access(user_id=dono)`;
  - **do grupo** → grava `project_access(group_id=G)` (o dono continua vendo
    pelo grupo ou por linha própria);
  - **público** → nenhuma linha (comportamento atual).
  Alterar a visibilidade depois = regravar as linhas (dono ou admin).
- **Motores:** mesmo padrão espelhado — `engines.owner_user_id` + nova tabela
  `engine_access` (clone estrutural da `project_access`). Sem linha = público,
  retrocompatível com todos os bancos existentes.
- **Aplicação da ACL de motores** (o ponto novo de verdade):
  - tela Motores: cada usuário lista públicos + seus + dos seus grupos; admin
    lista tudo;
  - **scheduler/cadeia de fallback:** demandas, consultas e planejamentos já têm
    `criado_por` — os motores elegíveis para um trabalho são os visíveis ao seu
    criador. Trabalho **sem** criador (tokens de API/integrações) usa apenas
    motores públicos;
  - as contas (`engine_accounts`) seguem o motor: quem não vê o motor não
    consome a franquia de ninguém.
- **Permissões:** nova permissão `projetos.criar` (hoje criar projeto é de quem
  tem `projetos.gerir`); gestão/exclusão de um projeto ou motor restrito fica
  com o dono + quem tem a permissão de gestão correspondente. O papel `operador`
  ganha `projetos.criar` por default? **Não** — decisão explícita do admin.
- **Grupos:** `user_groups` já existe e já carrega `engine_id` (motor das
  consultas do grupo) — a ACL generaliza esse embrião sem removê-lo.

### Fase B — execução não-root no Linux

O Claude Code encerra com erro quando invocado com `--dangerously-skip-permissions`
sob UID 0 (proteção do próprio vendor). O Codex tem sandbox próprio mas a mesma
lógica se aplica: **o orquestrador não deve rodar harnesses autônomos como root**.
A solução é estrutural, em três camadas:

1. **`praxis service install` nunca registra o serviço como root.** Hoje a unit
   usa `User=$SUDO_USER` (cmd/praxis/service.go, `contaPadrao`). O caso que falta
   é o login root de verdade (VPS, `SUDO_USER` vazio ou "root"): o install passa
   a criar uma conta de sistema dedicada — `useradd --system --create-home praxis`
   — e registra a unit com ela, com `PRAXIS_HOME` no home dessa conta. O resumo
   da instalação informa a conta criada.
2. **Guardas de diagnóstico:** `serve` em Linux com euid 0 loga aviso destacado
   no boot; o motor claude verifica o UID **antes** de disparar e falha rápido
   com mensagem acionável ("rode o serviço com uma conta não-root — ver §8.1"),
   em vez de deixar o harness morrer com erro críptico no meio da demanda.
3. **Containers (Fases E e docker/):** dentro de containers gerenciados pelo
   Praxis o processo roda com `--user` não-root; para o Claude, o Praxis define
   `IS_SANDBOX=1` **apenas** dentro desses containers — onde a afirmação é
   verdadeira por construção. Nunca no host.

Sem `setuid`/`Credential` por processo: derrubar privilégio do harness mantendo
o Praxis como root criaria uma teia de permissões de arquivos (worktrees, logs,
perfis) pior que o problema. A regra é uma só: o serviço inteiro roda sem root.

### Fase C — chave SSH por usuário e cadastro por clone

- **Geração:** `ssh-keygen -t ed25519 -N "" -C "praxis-<email>"` gravando em
  `PRAXIS_HOME/ssh/u<id>/id_ed25519` (dir 0700, chave 0600). Usa o `ssh-keygen`
  do sistema — acompanha o OpenSSH/git que o Praxis já exige (Windows 10+ traz o
  cliente OpenSSH; Linux, `openssh-client`) — mantendo a regra de dependências
  mínimas. A chave privada **nunca** sai do servidor, nunca entra no SQLite e
  nunca é retornada pela API (mesma filosofia dos perfis de motor).
- **UI (perfil do usuário):** mostra a chave pública com botão copiar,
  fingerprint e instruções por plataforma (github.com/settings/keys, GitLab,
  Gitea); botão **testar** roda `git ls-remote` com a chave contra uma URL
  informada e devolve só o veredito sanitizado.
- **Cadastro por clone:** `POST /projects` aceita `url_git` (formato SSH) como
  alternativa à `pasta`. O clone roda em job assíncrono (mesmo padrão dos
  serviços de intake/consultor) para `PRAXIS_HOME/repos/<slug>`, com
  `GIT_SSH_COMMAND="ssh -i <chave> -o IdentitiesOnly=yes -o StrictHostKeyChecking=accept-new"`;
  ao concluir, o projeto nasce com `pasta` apontando para lá e com a
  visibilidade escolhida (Fase A).
- **Credencial do projeto:** `projects.ssh_user_id` (nullable) registra qual
  chave opera o repositório. **Todo** comando git de rede daquele projeto —
  `Push`, o fetch/pull do `PosicionarBranchPrincipal`, o clone — injeta o
  `GIT_SSH_COMMAND` pelo `gitEnv` (internal/gitops/gitops.go), que já aceita
  ambiente extra por comando. Projetos legados (sem `ssh_user_id`) continuam
  com as credenciais do SO: zero quebra.
- Efeito colateral desejado: o serviço (LocalSystem/conta dedicada) deixa de
  depender do `~/.ssh` de alguém — a credencial vive em `PRAXIS_HOME`, que é do
  serviço. Isso fecha o alerta documentado no §8.1 do guia.
- Se o dono da chave for removido, os projetos que a usam ficam com push/pull
  quebrados **explicitamente** (evento + aviso no card), e um admin transfere a
  credencial (`ssh_user_id`) para outro usuário.

### Fase D — instalador de harnesses

- Novo `internal/motor/instalador`, no molde do download do VS Code CLI
  (internal/ide/download.go): baixa o binário **standalone** oficial de cada
  vendor para `PRAXIS_HOME/tools/harness/<vendor>/bin/` — claude tem
  distribuição nativa standalone; codex e opencode publicam binários por
  plataforma nos releases do GitHub. Sem exigir Node/npm no host. Checksum
  verificado quando o vendor publica digest.
- **Resolução do executável em camadas:** caminho explícito no cadastro do motor
  → PATH → diretório gerenciado. Hoje os motores invocam por nome
  (`exec.CommandContext(ctx, "claude", ...)`); nasce `motor.ResolverCLI(vendor)`
  usada pela execução, pelo login web, pela detecção e pelo diagnóstico de
  autenticação — um ponto único.
- **UI (tela Motores):** onde a detecção hoje diz "não encontrado", entra o botão
  **Instalar** → job em background com progresso; ao concluir, o motor é
  cadastrado/atualizado e o fluxo de login existente assume. Botão **Atualizar**
  re-executa o download (o binário antigo vira `.old`, como no auto-update do
  serviço). Permissão: `config.gerir` (a mesma dos perfis/login).
- O login web continua cobrindo claude e codex (`VendorComPerfilIsolado`);
  opencode é instalável, mas a autenticação segue o fluxo atual do vendor.

### Fase E — ambientes conteinerizados por projeto

O objetivo é o host não precisar de toolchain nenhum e o agente autônomo ficar
contido. Duas entregas em degraus, ambas via **CLI** do runtime (`docker` ou
`podman`, detectados nessa ordem — sem SDK, dependências mínimas):

- **Configuração por projeto** na chave `ambiente` do escopo de projeto de
  `config_entries` (mecanismo que já existe e já tem PUT na API):

  ```json
  {
    "tipo": "docker",
    "imagem": "golang:1.26",
    "dockerfile": "",
    "env": { "GOFLAGS": "-mod=mod" },
    "montagens": [ { "volume": "praxis-cache-go", "destino": "/go/pkg/mod" } ],
    "rede": "none",
    "memoria": "4g",
    "cpus": 4,
    "usuario": ""
  }
  ```

  `imagem` OU `dockerfile` (caminho relativo no repo; o Praxis faz o build e
  cacheia por hash do arquivo). `usuario` vazio = UID/GID do serviço no Linux
  (dono da worktree) e default da imagem no Windows/Docker Desktop. Sem a chave
  `ambiente`, tudo roda no host como hoje.

- **E1 — gates no container** (maior valor, menor risco): o ponto único é o
  `execShell` do `RunnerGates` (internal/pipeline/gates.go) — com ambiente
  configurado, cada gate vira
  `docker run --rm -v <worktree>:/work -w /work --network <rede> [limites] <img> sh -lc "<comando>"`.
  Gates não precisam de rede (`rede: none` como default do exemplo) nem de
  credenciais. `falhaDeAmbiente` continua valendo — a mensagem passa a sugerir
  configurar o ambiente do projeto quando detecta toolchain ausente. Volumes
  nomeados de cache (go/npm/maven) mantêm a velocidade entre execuções.

- **E2 — harness no container**: wrapper no ponto em que cada motor cria o
  `exec.Cmd` — traduz `OpcoesRun.Dir` (worktree, rw), `PerfilDir` (credenciais
  do vendor, rw — tokens se renovam), `AddDirs` (ro) em montagens; o ambiente
  vai por `-e`; stdin/stdout streams funcionam idênticos com `docker run -i
  --rm`. Pontos de atenção resolvidos no desenho:
  - **git de rede fica FORA do container** — commit e push já são do
    orquestrador (`ProibirCommit`), então a chave SSH da Fase C nunca entra na
    imagem;
  - **rede do harness**: precisa alcançar a API do vendor — `bridge` por
    default nesta entrega; restrição por allowlist fica registrada como
    evolução;
  - **cancelamento/órfãos**: o `procs.Registro` guarda o id do container junto
    do PID; matar = `docker rm -f`, inclusive na recuperação pós-restart;
  - **não-root**: `--user` não-root + `IS_SANDBOX=1` (claude) — fecha a Fase B
    por construção dentro de containers;
  - a imagem do projeto precisa de `git` (o harness lê o repo) — validado no
    primeiro uso com mensagem clara.

- **Fallback e diagnóstico:** host sem docker/podman → configurar `ambiente`
  falha na validação com mensagem clara; `GET /sistema/container-runtime`
  informa o runtime detectado para a UI desabilitar a seção quando não há.
- **Windows:** funciona via Docker Desktop (bind mounts mais lentos —
  documentado). IDE web continua abrindo a worktree no host, inalterado.

### Postura de segurança (transversal, entra na Fase A)

- **Bootstrap:** o acesso total por loopback antes do 1º admin ganha limite —
  só o fluxo `/auth/setup` fica aberto; o restante da API exige o admin criado.
- **IDE web:** além da permissão `codigo.editar`, o guia passa a classificá-lo
  como equivalente a shell; default de instalação nova = desabilitado até o
  admin ligar (config global).
- **`docker/` oficial:** Dockerfile multi-stage com `USER praxis` (não-root),
  volume para `PRAXIS_HOME`, compose de exemplo. Resolve a Fase B por
  construção para quem adota via container.

## Contratos previstos

- **Fase A**
  - `POST/PUT /api/v1/projects`: + `visibilidade` (`privada|grupo|publica`),
    `grupo_id`; resposta ganha `dono`.
  - `POST/PUT /api/v1/engines`: idem (`visibilidade`, `grupo_id`, `dono`).
  - Migrações: `projects.owner_user_id`, `engines.owner_user_id`,
    `CREATE TABLE engine_access` (espelho da `project_access`),
    permissão `projetos.criar`.
- **Fase B** — sem API nova; mudança no `service install` (criação da conta de
  sistema) e guardas de boot/motor.
- **Fase C**
  - `GET /api/v1/me/ssh-key` → pública + fingerprint (404 se não gerada);
  - `POST /api/v1/me/ssh-key` → gera (409 se já existe);
  - `POST /api/v1/me/ssh-key/testar` `{url}` → veredito sanitizado;
  - `POST /api/v1/projects` com `{url_git, visibilidade, ...}` → `202` + job de
    clone; `GET /api/v1/projects/clones/{id}` → progresso/erro.
  - Migração: `projects.ssh_user_id`.
- **Fase D**
  - `GET /api/v1/engines/instalaveis` → vendors suportados, instalado (PATH ou
    gerenciado), versão, plataforma;
  - `POST /api/v1/engines/instalar` `{vendor}` → `202` + job;
    `GET /api/v1/engines/instalar/{id}` → progresso.
- **Fase E**
  - config de projeto `ambiente` (PUT já existente);
  - `GET /api/v1/sistema/container-runtime` → runtime detectado + versão.

## Etapas e andamento

### Fase A — donos e visibilidade
- [x] Migração: `owner_user_id` em `projects` e `engines`; `engine_access`; permissão `projetos.criar`.
- [x] Criação de projeto/motor grava dono + linhas de ACL conforme a visibilidade escolhida.
- [x] Filtragem de motores por visibilidade na tela Motores e nos endpoints de leitura.
- [x] Scheduler/intake/consultas/planejamentos: cadeia de fallback filtra motores pelo `criado_por` do trabalho; sem criador = só públicos.
- [x] Gestão/exclusão restrita a dono + permissão de gestão; troca de visibilidade.
- [x] UI: seletor de visibilidade (meu/grupo/público) no cadastro de projeto e de motor; badge de dono nas listas.
- [x] Bootstrap restrito ao `/auth/setup`; IDE web desligado por default em instalação nova.
- [x] Testes: ACL de motores (unidade + API), fallback filtrado, retrocompatibilidade (banco sem donos).

### Fase B — não-root no Linux
- [x] `service install`: sem `SUDO_USER` útil, criar conta de sistema `praxis` (`--create-home`) e registrar a unit com ela; resumo informa a conta.
- [x] Aviso destacado no boot do `serve` com euid 0 (Linux); pré-verificação de UID no motor claude com erro acionável.
- [x] `docker/`: Dockerfile oficial multi-stage com `USER praxis` + compose de exemplo.
- [x] Docs (§8.1 e troubleshooting): a regra "harness nunca roda como root" e o caminho de correção.

### Fase C — SSH por usuário e cadastro por clone
- [x] Geração/armazenamento da chave (`PRAXIS_HOME/ssh/u<id>/`, permissões restritas) via `ssh-keygen`; endpoints `me/ssh-key`.
- [x] UI do perfil: exibir pública, copiar, fingerprint, instruções por plataforma, testar conexão.
- [x] `gitops`: injeção de `GIT_SSH_COMMAND` por projeto (`ssh_user_id`) em clone/fetch/pull/push via `gitEnv`.
- [x] Cadastro por `url_git`: job assíncrono de clone com progresso/erro na UI; projeto nasce com dono, visibilidade e credencial.
- [x] Transferência de credencial por admin quando o dono sai; evento + aviso no card quando a credencial quebra.
- [x] Testes: chave nunca exposta pela API, clone com chave (repo local `file://` no teste), push com `GIT_SSH_COMMAND`, legados intactos.

### Fase D — instalador de harnesses
- [x] `motor.ResolverCLI(vendor)` (explícito → PATH → gerenciado) usado por execução, login, detecção e diagnóstico.
- [x] `internal/motor/instalador`: download por vendor/plataforma para `PRAXIS_HOME/tools/harness/`, checksum quando disponível, `.old` na atualização.
- [x] Endpoints `engines/instalaveis` + `engines/instalar` (job com progresso); permissão `config.gerir`.
- [x] UI Motores: botão Instalar/Atualizar com progresso; detecção passa a enxergar o diretório gerenciado.
- [x] Testes: resolução em camadas, instalação simulada (servidor de teste), atualização com binário em uso.

### Fase E — ambientes conteinerizados
- [ ] Detecção do runtime (`docker`/`podman`) + `GET /sistema/container-runtime`; validação da config `ambiente`.
- [ ] E1: `RunnerGates` executa gates via `docker run` quando o projeto tem ambiente; volumes de cache nomeados; build por `dockerfile` com cache por hash.
- [ ] UI do projeto: seção Ambiente (imagem/dockerfile, rede, limites, montagens), desabilitada sem runtime.
- [ ] E2: wrapper de containerização do `exec.Cmd` dos motores (montagens de worktree/perfil/add_dirs, `-e`, `-i`, `--user`, `IS_SANDBOX=1` no claude).
- [ ] `procs.Registro` guarda container-id; cancelamento e recuperação pós-restart fazem `docker rm -f`.
- [ ] Testes: gates em container (skip sem runtime no CI), tradução de montagens, cancelamento mata o container, host sem runtime falha claro.

## Critérios de aceite

- **A:** usuário sem papel de gestão cria projeto privado, não vê projetos/motores
  alheios restritos; demanda de usuário sem acesso a um motor restrito nunca o usa
  (nem por fallback); banco antigo migrado se comporta exatamente como antes.
- **B:** `sudo praxis service install` num host sem `SUDO_USER` sobe o serviço com
  conta dedicada; demanda com claude completa num Linux sem nenhum processo do
  Praxis rodando como root.
- **C:** um usuário novo — sem acesso ao SO — gera a chave, cadastra no GitHub,
  clona um repositório privado pela web e cria uma demanda que termina em push,
  tudo pela interface.
- **D:** num host limpo (sem harness no PATH), instalar o claude pela tela
  Motores + login pela web + executar uma consulta, sem tocar no terminal.
- **E1:** projeto Go com `ambiente` configurado passa gates num host **sem Go
  instalado**. **E2:** demanda completa executa com o harness containerizado;
  cancelar a demanda remove o container; reiniciar o serviço não deixa
  containers órfãos.

## Fora deste incremento

- `devcontainer.json` como fonte do ambiente (a config própria nasce compatível
  em espírito; o parser fica para depois).
- Allowlist de rede para o harness containerizado (E2 nasce com `bridge`).
- SSO/LDAP/OIDC; quotas de custo por usuário/grupo (o painel de uso já dá a
  leitura; o limite fica para depois).
- UI específica por plataforma git (GitHub App, deploy keys por repositório) —
  a chave por usuário cobre GitHub/GitLab/Gitea/Bitbucket de forma neutra.
- Rootless podman/userns detalhado — suportado se o CLI se comportar como o
  docker; ajustes finos ficam para quando houver demanda real.
