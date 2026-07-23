# Plano de implementação — login e múltiplos perfis Claude/Codex

Atualizado em: 2026-07-23 — **entrega concluída** (todas as etapas fechadas e gates verdes).

## Objetivo

Permitir que um administrador faça, pela tela **Motores**:

1. crie mais de um perfil isolado para um motor Claude ou Codex;
2. consulte o estado de autenticação de cada perfil;
3. inicie o login no servidor e conclua a autenticação no navegador do seu próprio dispositivo;
4. escolha quais perfis ficam ativos para o scheduler, sem compartilhar credenciais entre eles.

O desenho mantém um único motor por vendor e usa `engine_accounts` como os perfis desse motor. Uma segunda instância de motor só será necessária futuramente quando houver configuração operacional diferente (modelo, prioridade ou budget), não apenas outra conta.

## Decisões de arquitetura

### Isolamento dos perfis

- **Claude:** cada perfil terá um diretório próprio aplicado como `CLAUDE_CONFIG_DIR` tanto no login quanto em toda execução do harness.
- **Codex:** cada perfil terá um diretório próprio aplicado como `CODEX_HOME` tanto no login quanto em toda execução do harness. O diretório é criado antes do primeiro uso, pois o Codex exige que ele exista.
- O Praxis armazena somente o caminho do diretório e metadados de estado. Tokens continuam sendo gravados pelo CLI do vendor dentro do perfil; não serão retornados pela API nem copiados para o SQLite.
- Diretórios gerados pela aplicação ficarão sob `PRAXIS_HOME/engine-profiles/<vendor>/<engine-id>/<account-id>`. Também será aceito um caminho explícito para manter compatibilidade com os perfis Claude existentes.

### Estado e seleção

- Contas deixam de ser tratadas como aliases globais e passam a ser identificadas por `(engine_id, account_id)`.
- O scheduler e os fluxos de intake/consulta devem aplicar o ambiente do perfil selecionado para Claude e Codex.
- A lista de perfis ativos será relida entre execuções, evitando exigir reinício do serviço depois de criar, ativar ou desativar um perfil.
- A escolha do perfil preferido é determinística entre os perfis ativos do motor (afinidade pelo id da demanda/consulta). Quando a franquia do perfil esgota, o fallback tenta os DEMAIS perfis do mesmo motor (na ordem da afinidade) antes de trocar de motor — a prioridade do motor vale mais que a ordem de fallback.
- Cada motor tem o switch `fallback` (default ligado). Desligado, o motor sai da cadeia de troca automática e da escolha automática (pipeline, intake e consultas); ele só roda onde for definido manualmente — `motor_preferido` do projeto ou motor do grupo de usuários nas consultas — e, ao esgotar nesse uso manual, ainda cai na cadeia dos motores participantes.

### Login assistido

- O backend cria uma sessão de autenticação temporária, com ID aleatório, validade, log sanitizado e processo cancelável.
- O processo roda como o mesmo usuário do serviço e recebe o mesmo diretório isolado usado nas execuções.
- **Claude:** `claude auth login`; a URL emitida pelo CLI será entregue à UI. A conclusão será confirmada por `claude auth status --json` no mesmo `CLAUDE_CONFIG_DIR`.
- **Codex:** o fluxo estruturado do `codex app-server` será preferido para iniciar login ChatGPT e obter a URL; o backend confirmará o resultado no mesmo `CODEX_HOME`. Se a versão instalada não oferecer esse contrato, a API retornará erro de versão incompatível em vez de tentar interpretar credenciais.
- A UI abre a URL no navegador do usuário e acompanha a sessão por polling. Nenhum shell arbitrário será exposto no navegador.
- Apenas administradores podem criar/alterar perfis ou iniciar/cancelar login.

## Contratos previstos

### Persistência

- Generalizar o campo conceitual `config_dir` para representar o diretório raiz do perfil de ambos os vendors, mantendo migração compatível.
- Registrar estado observável (`autenticado`, `deslogado`, `verificando`, `erro`, `desconhecido`) apenas como resposta/diagnóstico; a fonte de verdade continua sendo o CLI.
- Persistir a conta usada em cada `run`: a migração 11 acrescenta `runs.conta` e `consulta_runs.conta` (alias do perfil vigente no momento da execução, `TEXT NOT NULL DEFAULT ''`). A coluna é aditiva e denormalizada de propósito — renomear/remover o perfil depois não reescreve o histórico, e os runs anteriores à migração ficam com `''` (relatórios atuais preservados).

### API REST

- `GET /api/v1/engines/{engineId}/accounts/{accountId}/auth` — verifica o login no perfil.
- `POST /api/v1/engines/{engineId}/accounts/{accountId}/login` — inicia o login assistido e retorna sessão/URL quando disponível.
- `GET /api/v1/engine-auth-sessions/{sessionId}` — retorna o andamento e a URL/código público do fluxo.
- `DELETE /api/v1/engine-auth-sessions/{sessionId}` — cancela a sessão e o processo associado.
- As respostas nunca incluem tokens, conteúdo de arquivos de credencial ou saída bruta não sanitizada.

### Interface

- Diferenciar perfil, diretório isolado, estado de login e ativação.
- Ações por perfil: **Verificar login**, **Entrar pelo navegador**, **Cancelar login**, **Ativar/desativar** e **Excluir**.
- Exibir instruções específicas quando o navegador está em outro computador que não o servidor.

## Etapas e andamento

- [x] Definir arquitetura, limites de segurança e critérios de aceite.
- [x] Criar a abstração de perfil por vendor (`CLAUDE_CONFIG_DIR`/`CODEX_HOME`) — `internal/motor/perfil.go` (`VendorComPerfilIsolado`, `PrepararPerfil`, `aplicarPerfil`, `DiretorioPerfilGerenciado`).
- [x] Ajustar banco e stores preservando os perfis Claude existentes — `engine_accounts.config_dir` vale para ambos os vendors; migração 11 adiciona `runs.conta`/`consulta_runs.conta` sem tocar nas linhas existentes.
- [x] Aplicar perfis em todas as invocações Claude/Codex (pipeline via `Config.ConfigDirs` — inclusive no fallback —, scheduler via `resolverConfigBanco`, intake via `resolverMotor`, consultas via `resolverMotorConsulta`).
- [x] Remover colisão de aliases entre motores e atualizar a seleção sem reinício — alias é único por `(engine_id, alias)` e a config é relida do banco a cada execução.
- [x] Implementar diagnóstico de autenticação Claude/Codex — `motor.VerificarAutenticacao` (saída sanitizada; nunca repassa a saída crua do CLI).
- [x] Implementar gerenciador de sessões de login com timeout e cancelamento — `motor.GerenteLogin` (sessões efêmeras em memória, id aleatório, 10 min de validade).
- [x] Implementar login Claude pelo navegador — `claude auth login` com captura da URL e envio de código pelo stdin; confirmação por `claude auth status --json` no mesmo perfil.
- [x] Implementar login Codex pelo navegador usando `app-server` — device code via JSON-RPC; versão incompatível vira erro explícito.
- [x] Expor endpoints autenticados e cobri-los com testes — rotas em `internal/api/engines.go`, exigem `config.gerir` (inclusive o GET de diagnóstico e as sessões de login).
- [x] Atualizar a tela Motores para criação, diagnóstico e login dos perfis — `web/js/motores.js` (verificar login, entrar pelo navegador com polling, código copiável, cancelar, ativar/desativar, excluir).
- [x] Cobrir isolamento, seleção, cancelamento e sanitização com testes — `motor/autenticacao_test.go`, `motor/claude_test.go`/`codex_test.go` (env do perfil), `api/engines_test.go`, `scheduler/executor_test.go`, `db/migracoes_test.go`.
- [x] Persistir a conta usada em cada run — migração 11 + `Execucao.Conta`/`ExecucaoConsulta.Conta`, propagado pelo pipeline (`Config.Contas`, refletindo o motor efetivamente usado após fallback), intake, consultor e overview; o separador do log ao vivo mostra `engine:conta/modelo`.
- [x] Fallback entre perfis do mesmo motor — `Config.Perfis` (todos os perfis ativos, o da afinidade primeiro) e `rodarComFallback` esgota perfil a perfil (evento `troca_de_perfil`) antes de trocar de motor (evento `troca_de_harness`); o run registra o perfil que executou de fato.
- [x] Switch `engines.fallback` por motor — migração 12 (default 1); desligado, o motor sai da cadeia e da escolha automática (scheduler, intake, consultas) e vale só no uso manual, com queda para a cadeia quando esgota. Exposto na API (`fallback`, default true na criação) e na tela Motores.
- [x] Executar `go test ./...`, `go vet ./...` e `go build ./...`.
- [x] Atualizar este documento com decisões finais, limitações verificadas e resultado dos gates.

## Resultado dos gates (2026-07-23)

- `go build ./...` — OK.
- `go vet ./...` — OK.
- `go test ./...` — OK (todos os pacotes: api, auth, consultor, db, gitops, ide, importador, intake, manutencao, motor, notify, pipeline, procs, scheduler).

## Decisões finais e limitações verificadas

- A conta registrada no run é o **alias vigente** no momento da execução (texto denormalizado), não uma FK: renomear ou excluir o perfil não reescreve nem apaga o histórico.
- Quando o fallback troca de perfil ou de motor no meio da fase, o registro fechado do run traz o motor e o perfil **efetivamente usados** (o perfil do motor de fallback também é isolado).
- A ordem de esgotamento é: perfis do motor atual (a partir do da afinidade) → próximo motor da cadeia → `ErroFranquia` com horário de retomada (o scheduler reagenda; nada bloqueia).
- O "esgotado" de perfil/motor vale **dentro da fase em execução** (`EstadoFallback`); a fase seguinte volta a tentar o perfil preferido. Persistir a janela de reset da quota entre fases continua no backlog ([TODOS.md](TODOS.md)).
- Um motor com `fallback` desligado usado manualmente ainda cai na cadeia dos participantes ao esgotar — ele não é alvo do fallback, mas tem fallback.
- O estado `verificando` existe apenas na UI (durante o polling); o backend responde `autenticado`/`deslogado`/`erro` — a fonte de verdade continua sendo o CLI do vendor.
- Balanceamento por menor uso/franquia e limites de concorrência por perfil continuam no backlog ([TODOS.md](TODOS.md)).

## Critérios de aceite

1. Dois perfis Claude podem estar autenticados em diretórios distintos e uma execução com um perfil não lê as credenciais do outro.
2. Dois perfis Codex podem estar autenticados em `CODEX_HOME` distintos com a mesma garantia.
3. O usuário remoto recebe no Praxis uma URL que abre no navegador local e a tela confirma conclusão, erro, expiração ou cancelamento.
4. Criar/ativar/desativar um perfil passa a valer sem reiniciar o serviço.
5. Perfis com o mesmo alias em motores diferentes não colidem internamente.
6. API e logs do Praxis não expõem token OAuth, API key ou conteúdo de `auth.json`.
7. Testes automatizados exercitam os contratos sem exigir contas reais dos vendors.

## Fora deste incremento

Os itens de uso/franquia, instalação automática, OpenCode e políticas avançadas de fallback estão registrados em [TODOS.md](TODOS.md).
