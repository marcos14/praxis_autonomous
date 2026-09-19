# Plano — abrir o Praxis para a internet: sessões, visibilidade por dono, PWA e notificações

Atualizado em: 2026-09-19 — **M1, M2 e M3 concluídos** (código, testes e docs); próxima etapa é a primeira do M4.

---

## Andamento (atualize aqui ao fim de cada etapa)

**Próxima etapa:** `M4.F5.E3`

**Pendência do M3 para o usuário (não automatizável):** auditoria Lighthouse "PWA" sem erros; instalar em Windows (Chrome/Edge), Android (Chrome) e iOS (Safari) com HTTPS válido; passar por todas as views a 400 px conferindo que nada rola na horizontal, que o ← volta da lista, que o kanban desliza coluna a coluna e que o card da demanda abre em tela cheia. Os handlers do SW e do manifest têm teste Go; o comportamento no navegador não.

**Pendência do M2 para o usuário (não automatizável):** roteiro manual no navegador com três usuários — um comum sem grupo, um comum com grupo e um admin — mais um token de API: criar consulta/planejamento/demanda em cada visibilidade, conferir listas, filtro Todos · Meus · Do grupo, kanban, Home, o select de "quem enxerga" no painel/card e a tela Configurações → Visibilidade (itens sem dono nos três modos). A lógica está coberta por testes de banco e de API; o que falta validar é a experiência na tela.

**Pendência do M1 para o usuário (não automatizável):** roteiro manual no navegador — com `sessao_jwt_min` em 15 min (mínimo da UI) ou `1` gravado via API, navegar sem cair e ver um único `/auth/refresh` por renovação; reiniciar o servidor com a página aberta e vê-la voltar sozinha; revogar a sessão no banco com texto digitado no chat e confirmar o portão por cima com o texto preservado; instalar em celular e checar o retorno ao primeiro plano. O backend foi validado também num servidor real com `curl` (setup → refresh por cookie → sessões → logout → refresh 401).

Legenda: `[ ]` pendente · `[~]` em andamento · `[x]` concluída. Abaixo de uma etapa `[~]`, escreva "Retomada:" com o que já foi feito, o que falta e decisões tomadas no caminho.

### M1 — Sessões duráveis
- [x] M1.F1.E1 Migração 16 + store de sessões — `internal/db/sessoes.go` (`CriarSessao`, `AutenticarSessao` com deslize gravado só após 5 min, `RevogarSessao`, `RevogarSessaoPorToken`, `RevogarSessoesDoUsuario`, `ListarSessoesDoUsuario`, `RemoverSessoesExpiradas`), `ErrSessaoInvalida`, `PrazosSessao`, helper `formatoISO`/`agoraISO`
- [x] M1.F1.E2 Config de sessão, TTL do JWT, `exp` no principal, segredo sem memoizar erro — `internal/api/auth_prazos.go` (`ChaveSessao*`, `prazosAuth`, `configInteiro`), `auth.Claims`/`AssinarClaims`/`ValidarClaims`, `principal.expiraEm`, `respAuth.expira_em` (RFC 3339, o frontend usa para agendar a renovação), `segredoJWT` com mutex
- [x] M1.F1.E3 Rotas refresh/logout, cookie, login cria sessão, revogações, fix `case "auth"` — `internal/api/auth_sessoes.go` (cookie `praxis_sessao`, `handleAuthRefresh`, `handleAuthLogout`, `conexaoSegura`, `ipDaRequisicao`, `abrirSessao`, `sessaoAtualID`, `revogarSessoesDoUsuario`); `responderLogin` em setup/login; troca de senha revoga as outras, reset pelo admin e desativação revogam todas; `Opcoes.ProxyConfiavel` + flag `-proxy-confiavel`/`PRAXIS_PROXY_CONFIAVEL` (`cmd/praxis/env.go`); i18n `erro.sessao_invalida` nos 4 catálogos
- [x] M1.F1.E4 Sessões do usuário (listar/encerrar), rate limit de login, limpeza na manutenção — `GET/DELETE /auth/sessoes` e `DELETE /auth/sessoes/{id}` em `auth_sessoes.go` (lista marca `atual` pelo cookie; "encerrar as outras" devolve `{revogadas}`); `internal/api/ratelimit.go` (`limitadorLogin`: 10 falhas / 15 min por IP e por e-mail, 429 + `Retry-After`, sucesso zera só o e-mail); `manutencao.Store` ganhou `RemoverSessoesExpiradas` (roda no ciclo diário); i18n `erro.sessao_nao_encontrada`, `erro.credencial_sem_sessao`, `erro.muitas_tentativas`
- [x] M1.F1.E5 SSE encerra no `exp` do token — `internal/api/sse.go` (`contextoDoStream` com deadline no `expiraEm` do principal; `avisarTokenExpirado` emite `event: token_expirado` antes de fechar, só quando foi o prazo e não o cliente); aplicado em `/events`, `/demands/{id}/logs`, `/consultas/{id}/progresso`, `/planejamentos/{id}/progresso`
- [x] M1.F2.E1 `auth.js`/`api.js`: token em memória, boot por refresh, renovação, retry em 401 — `auth.js` reescrito (JWT só em memória, `renovar()` single-flight, renovação proativa em 80 % do `expira_em`, renova ao voltar ao primeiro plano, `carregarSessao` só descarta em 401, `sessaoCaiu`/`logout` separados, `ErroAuth`/`sessaoInvalida`); `api.js` com `executar()` que renova e repete uma vez em 401; `app.js` ganhou `resolverSessao()` e a tela "servidor indisponível" com contagem (5→10→20→30 s) e "Tentar agora" — antecipada da E3; chaves `auth.indisponivel*`/`auth.tentar_agora` nos 4 catálogos web. Roteiro manual (navegar 3 min com `sessao_jwt_min=1`) pendente de execução no navegador
- [x] M1.F2.E2 Helper `abrirStream` e troca dos 6 `EventSource` — `abrirStream(caminho, {onopen, onmessage, eventos, onerror})` em `api.js`: fecha e reabre na hora ao receber `token_expirado` (renova só se o token ainda é o mesmo da abertura), reabre com backoff 1/2/5/10/30 s quando cai em CLOSED, derruba a sessão se o refresh der 401; expostos como `api.streamEventos`, `streamLogsDemanda`, `streamProgressoConsulta`, `streamProgressoPlanejamento`; kanban recarrega o board no `onopen` (eventos perdidos na reconexão)
- [x] M1.F2.E3 `app.js`: portão sem reload, Sair com logout — sessão caída abre o portão por cima da app sem esconder nem remontar nada (`mostrarPortao({anterior})`, e-mail pré-preenchido, texto `auth.sub_reautenticar`); mesmo usuário de volta → `retomarApp()` só reaplica permissões; outro usuário → reload; `sair()` chama `POST /auth/logout` e recarrega, com trava `saindo` para o callback não sobrepor; `sessaoCaiu` passa o usuário anterior; `abrirStream` espera o novo login (`aguardarLogin`, 2 s) e reabre sozinho
- [x] M1.F2.E4 Tela "Minha conta" v1 (senha, idioma, sessões) + i18n — view `conta` (`web/js/conta.js`, seção em `index.html`, botão e nome clicável no `nav-user`): dados (nome, e-mail, papéis, grupo), troca de senha com confirmação, seletor de idioma, sessões ativas com navegador/SO resumidos, IP, criada/último uso, "esta sessão", encerrar por sessão e "encerrar as outras"; `api.listarSessoes/encerrarSessao/encerrarOutrasSessoes`; `respUsuario` ganhou `grupo_id`/`grupo_nome` (`projetarUsuario`); 31 chaves `conta.*`/`nav.conta`/`view.conta.*` nos 4 catálogos web
- [x] M1.F3.E1 Fechamento: campos de config na UI, roteiro manual, docs — grupo "Sessões e login" em `config-fields.js` (`sessao_jwt_min`, `sessao_inatividade_dias`, `sessao_maxima_dias` com `select` e hints nos 4 idiomas); README_COMPLETO (EN e pt-BR): nova subseção "Atrás de um reverse proxy", §6.1 reescrita (usuários × tokens × bootstrap, cookie, refresh, sessões, rate limit), §9 com a proteção do login; manual embutido `09-acessos.md` nos 4 idiomas ganhou "Sua conta e sessões"; smoke test no servidor real com curl ok; roteiro no navegador fica com o usuário (ver pendência acima)

### M2 — Visibilidade meus / grupo / público
- [x] M2.F1.E1 Migração 17, `Visao`, `condDono`, config sem dono, testes em consultas — `internal/db/visao.go` (`Visao{Usuario, ACL, Dono, Escopo, SemDono, SemDonoGrupo}`, constantes `Visibilidade*`/`SemDono*`/`Escopo*`, `condDono`/`argsDono`, `anexarCondDono`, `anexarCondEscopo`); `consultas.visibilidade` no struct/scan/insert (default privada, valida), `FiltroConsultas{ProjectID, GroupID, Visao}`, `DefinirVisibilidadeConsulta`; matriz de 15 casos em `visao_test.go` + teste de backfill da migração
- [x] M2.F1.E2 Consultas e planejamentos filtrados no banco (+ `criado_por_nome`) — `planejamentos.visibilidade` (struct/scan/insert/`DefinirVisibilidadePlanejamento`), `FiltroPlanejamentos{ProjectID, GroupID, Visao}`; ACL de projeto OU grupo no SQL (`condAcessoAlvo`/`anexarCondAcessoAlvo` em `project_access.go`, grupo só visível com todos os membros visíveis) nas duas listagens; `LEFT JOIN users` → `CriadoPorNome`; `ConsultaVisivel`/`PlanejamentoVisivel(ctx, id, Visao)` (inexistente → true); os antigos `UsuarioVe*` e os filtros em memória da API saem na F2.E1
- [x] M2.F1.E3 Demandas e eventos com a regra de dono no banco — `demands.visibilidade` (struct/scan/2 inserts com default+validação), `FiltroDemandas.Visao` no lugar de `VisiveisPara`, `ListarDemandas`/`ListarDemandasResumo` com `LEFT JOIN users` (`CriadoPorNome`) e dono/escopo, `ListarDemandasPorStatus(…, Visao)`, `DemandaVisivel`, `DefinirVisibilidadeDemanda`; `FiltroEventos.Visao` e `EventosApos(…, Visao)` com `anexarCondEventos` (evento de demanda invisível não passa); `notify.FonteEventos` e chamadores da API ajustados (provisoriamente `Visao{ACL: uid}` até a F2)
- [x] M2.F2.E1 API: `visaoDaRequisicao`, middleware, consultas/planejamentos, PUT visibilidade, config — `internal/api/visao.go` (`visaoDe`/`visaoDaRequisicao`/`visaoComEscopo` com `?escopo=`, cache de 30 s da config `sem_dono_*` invalidado no PUT da config, `validarConfigSemDono`, `podeAlterarVisibilidade`); `autorizarVisibilidade` usa `DemandaVisivel`/`ConsultaVisivel`/`PlanejamentoVisivel`; listagens de consultas/planejamentos sem filtro em memória (`filtrar*Visiveis` removidos); campo `visibilidade` nos POST; `PUT /consultas/{id}/visibilidade` e `PUT /planejamentos/{id}/visibilidade`; 5 chaves i18n; `visao_api_test.go` (consultas, planejamentos, itens sem dono nos 3 modos da config). Os `UsuarioVe*` antigos do banco ficam (ainda usados em testes e no `ide.go` até a E2)
- [x] M2.F2.E2 API: demandas, board, home, overlap, ordem, SSE, herança do planejamento — `GET /demands` e `/board` com `visaoComEscopo`; pendências, atividade recente, sobreposições e SSE `/events` com `visaoDaRequisicao` (métricas seguem só com ACL, limitação registrada); `PUT /demands/ordem` responde 404 a id fora da visão; `/planejamentos/{id}/demandas` só lista vínculos visíveis; `PUT /demands/{id}/visibilidade` (mapeada como `""` em `permissaoMutacao`); criação aceita `visibilidade` nos dois modos (chat e fases) e `CriarDemandaComChat` no store também normaliza; demanda de planejamento herda a visibilidade dele; IDE web usa `DemandaVisivel`; testes em `visao_demandas_api_test.go`; testes antigos de ACL/RBAC ajustados (item privado alheio agora é 404)
- [x] M2.F3.E1 UI: tipo `select` na config, campos sem dono, seletor nos 3 formulários, i18n — a config já tinha `opcoes`/`opcoesDe` (viram `<select>`), então só entraram os campos `sem_dono_visibilidade` (opções fixas) e `sem_dono_grupo_id` (grupos de usuários carregados em `listasDinamicas`) no grupo "Visibilidade"; módulo novo `web/js/visibilidade.js` (seletor com "grupo" desabilitado sem grupo, última escolha lembrada, pills de visibilidade e de autor, `filtroEscopo` persistido por tela, `controleVisibilidade`); seletor em Nova demanda, nova consulta e novo planejamento; `api.js` com `escopo` nas 4 listagens e `definirVisibilidade*`; 23 chaves i18n nos 4 catálogos web (o teste de paridade exige chaves literais em `t("…")`)
- [x] M2.F3.E2 UI: consultas e planejamentos — pill, autor, filtro, alterar visibilidade — filtro segmentado acima das listas (`filtro-consultas`/`filtro-planejamentos`, lembrado por tela, `?escopo=`), pills 🔒/👥/🌐 e "por Fulano" nos cards, `controleVisibilidade` no cabeçalho do painel (dono ou admin) que recarrega a lista ao mudar
- [x] M2.F3.E3 UI: demandas e kanban — pill, filtro, alterar no card — filtro de escopo ao lado de projeto/status (Demandas) e projeto/motor (Kanban), pills de visibilidade e autor na lista e nos cards do quadro, select de "quem enxerga" no cabeçalho do card da demanda (dono/admin; os demais veem a pill)
- [x] M2.F4.E1 Fechamento: docs, manual, roteiro manual — README_COMPLETO (EN e pt-BR) ganhou §6.2 "Quem vê o quê" (duas camadas, visibilidades, rotas, escopo, itens sem dono, limitação das métricas); manual embutido `09-acessos.md` nos 4 idiomas ganhou "Quem vê cada consulta, planejamento e demanda"; roteiro no navegador fica com o usuário (ver pendência acima)

### M3 — PWA + layout móvel
- [x] M3.F1.E1 Manifest, ícones, metas no index, handler `/sw.js`, service worker — `web/manifest.webmanifest`, `web/icons/` (4 PNGs gerados por um programa Go de uma vez, "P" geométrico na paleta; maskable com zona segura), metas no `index.html` (`viewport-fit=cover`, theme-color, apple-touch-icon), `web/sw.js` (rede primeiro nas navegações, cache primeiro com revalidação nos estáticos, nunca intercepta `/api/`, `/ide/`, `/healthz`, `/cert`), handler `GET /sw.js` injetando `VERSAO` e `SHELL` (lista por `fs.WalkDir`) com `no-cache`, handler do manifest com `application/manifest+json`; teste `TestServiceWorkerEManifest`
- [x] M3.F1.E2 Registro do SW, aviso de versão nova, botão Instalar, dica iOS — `web/js/pwa.js` (`registrarServiceWorker` só em contexto seguro, toast em `updatefound`/`installed`, `beforeinstallprompt` guardado para o botão "Instalar app" no `nav-user`, oculto quando já instalado; no iOS o botão mostra a dica Compartilhar → Adicionar à Tela de Início); chaves `pwa.*` e `nav.menu`/`nav.voltar` (para a F2)
- [x] M3.F2.E1 Shell móvel: gaveta, barra superior, áreas seguras, toast — `header.topbar` (hambúrguer, título da view, "←"), `.sidebar` vira gaveta com backdrop abaixo de 768 px, `env(safe-area-inset-*)`, `.main` sem largura máxima, formulários `.row` empilhados, toast em largura total; `abrirMenu`/`atualizarTopbar` em `app.js`
- [x] M3.F2.E2 Kanban e card da demanda no celular — colunas de 86vw com scroll-snap (colunas vazias já não eram renderizadas), `.modal` em tela cheia, abas roláveis, `.fase-edit-row` empilhada
- [x] M3.F2.E3 Telas de duas colunas em páginas; chat e editores — `.two-col.painel-aberto` mostra só o painel; clique delegado na coluna da lista (item ou "+ Novo") abre, "←" da barra volta, trocar de view fecha; inputs com 16 px, chat a 92 %, toolbar do editor quebrando
- [x] M3.F2.E4 Tabelas, alvos de toque, esconder IDE/pastas, passada final a 400 px — `table.plain` e `.md pre/table` rolam no contêiner, botões/abas/itens do menu/switch com 40–44 px, tiles em 2 colunas, classe `so-desktop` no botão do IDE web e no "procurar pasta", `overflow-x: hidden` no body; a checagem visual a 400 px fica para o roteiro do usuário
- [x] M3.F3.E1 Fechamento: Lighthouse, instalar em 3 plataformas, docs — README (EN e pt-BR) §5.4 "Instalar como app e usar no celular"; FAQ do manual embutido nos 4 idiomas; Lighthouse e instalação nas 3 plataformas ficam com o usuário (ver pendência acima)

### M4 — Notificações ao usuário
- [x] M4.F1.E1 Migração 18 + stores de notificações, assinaturas push e preferências — `events.consulta_id/planejamento_id`, tabelas `notificacoes` e `push_subscriptions`, `users.notificacoes`, `auth_config.vapid_*`; `internal/db/notificacoes.go` (criar, listar, `NotificacoesApos`, última, contar não lidas, marcar lida/todas, `MarcarPushEnviado`, retenção) e `push.go` (upsert por endpoint, remover, listar, `RegistrarFalhaAssinatura` com descarte no limite, `MarcarUsoAssinatura`, retenção, `PreferenciasNotificacao`/`Definir…`, `ObterOuGerarVAPID` memorizado em `auth_config`)
- [x] M4.F1.E2 Eventos com `consulta_id`/`planejamento_id`, `demanda_concluida`, catálogo — `Evento.ConsultaID/PlanejamentoID` (scan/insert), `anexarCondEventos` também esconde eventos de consulta/planejamento invisíveis; consultor e estrategista preenchem o id; executor registra `demanda_concluida` (i18n `evento.demanda_concluida[.detalhe]` nos 4 catálogos); `notify-events.js` ganha `demanda_concluida` e o flag `padrao_usuario` (os demais flags entram na F5)
- [x] M4.F2.E1 `internal/webpush`: VAPID + `aes128gcm` com vetores das RFCs — `GerarChaves`, `AssinarVAPID` (ES256, exp ≤ 24 h), `Cifrar` (ECDH P-256 efêmero + HKDF + AES-128-GCM, um registro, rs 4096), `Enviar` (headers aes128gcm/TTL/Urgency/Topic/Authorization vapid) e `AssinaturaMorta`; teste reproduz o vetor do Apêndice A da RFC 8291 byte a byte, ida e volta com chaves novas, verificação ES256 e envio contra httptest
- [x] M4.F2.E2 Chaves VAPID no boot, config `push_contato` — `serve` chama `ObterOuGerarVAPID` com `webpush.GerarChaves` (best-effort, como o segredo do JWT); `db.EmailPrimeiroAdmin` e `db.ContatoPush` (config `push_contato` → senão e-mail do primeiro admin ativo; `mailto:` automático, URLs passam); campo `push_contato` no novo grupo "Notificações e push" do `config-fields.js` com i18n nos 4 idiomas; teste `TestContatoPush`
- [x] M4.F3.E1 Despachante: destinatário, preferências, gravação em `notificacoes` — `internal/notify/destinatario.go`: `Preferencias` (`PreferenciasPadrao`, `DecodificarPreferencias`, `TiposPadraoUsuario` espelhando o catálogo), interface `FonteUsuarios` (`*db.DB` satisfaz), `RotaDoEvento`, `destinatario` com cache por ciclo, `notificarUsuario`; `processar` grava a notificação do dono a cada evento; `serve` passa `Usuarios: banco`; testes com fakes (dono, sem dono, item apagado, tipo desligado, preferências parciais, cache)
- [x] M4.F3.E2 Despachante: envio push, falhas, retenção na manutenção — `internal/notify/push.go`: interface `FontePush` (`*db.DB` satisfaz), `enviarPush` em goroutine com timeout (payload `{id,titulo,detalhe,rota,tag}`, `Topic` = tipo, VAPID memorizado), aceito → `MarcarUsoAssinatura` + `push_em`; 404/410 apaga; 429/5xx/transporte → `RegistrarFalhaAssinatura` (descarte em `MaxFalhasPush` = 5); outros 4xx só log; `AguardarEnvios`; manutenção: `RemoverNotificacoesAntigas` (lidas 30 d, não lidas 90 d) e `RemoverAssinaturasSemUso` (180 d) no `Ciclo`; `serve` passa `Push: banco`; testes com serviço de push falso (httptest) e banco real
- [x] M4.F4.E1 API: listar, lida(s), stream SSE por usuário — `internal/api/notificacoes.go`: `GET /notificacoes?nao_lidas=1&limite=50` (`{itens, nao_lidas}`), `POST /notificacoes/{id}/lida` (404 se alheia), `POST /notificacoes/lidas` (`{marcadas}`), `GET /notificacoes/stream?after=` (`event: notificacao`, polling, heartbeat, encerra no exp do JWT com `token_expirado`); `permissaoMutacao` `case "notificacoes"`; tokens de API → 400 (`erro.credencial_sem_notificacoes`); i18n `erro.notificacao_nao_encontrada`; testes de listagem/lidas por usuário e do stream (só as próprias, backlog com `after=0`)
- [x] M4.F4.E2 API: push (chave, assinar, cancelar) e preferências — `internal/api/notificacoes_push.go`: `GET /notificacoes/push/chave` (VAPID pública), `POST/DELETE /notificacoes/push` (corpo = `PushSubscription.toJSON()`; só https; cancelar só a própria, idempotente), `GET/PUT /auth/preferencias` (efetivas + `assinaturas` + `padrao_tipos`; PUT valida tipos contra `notify.TiposConhecidos`, novo catálogo Go com teste de paridade contra `notify-events.js`); catálogo JS com `padrao_usuario` completo; i18n `erro.assinatura_push_invalida`/`erro.tipo_evento_desconhecido`; tokens de API → 400; testes
- [x] M4.F5.E1 Rotas com id (`#view/id`) nas 3 telas — `web/js/rota.js` (`separarRota`, `fixarRota` via `pushState` sem disparar `hashchange`, `limparRotaID`); `irPara`/`irParaHash` separam view e id e passam o id ao `montar*`; `montarConsultas(id)`/`montarPlanejamentos(id)` abrem o item direto, `montarDemandas(id)` abre o card; `abrirConsulta`/`abrirPlanejamento` fixam o hash; o card de demanda só fixa `#demandas/ID` quando aberto da tela Demandas e limpa ao fechar; voltar/avançar remonta com o id
- [x] M4.F5.E2 Stream no boot, toast, sino com badge e painel — `web/js/notificacoes.js`: `iniciarNotificacoes` em `entrarNaApp` (carrega lista+contador, abre `/notificacoes/stream?after=` com dedupe por id nas reconexões), toast clicável (aba visível) ou `registration.showNotification` com `tag` (aba escondida, permissão concedida), sino na sidebar (`botaoSino`) e na topbar (`#btn-sino-topbar`) com badge compartilhado e contador no `document.title`, painel lateral com lista, clique navega pela rota e marca lida, "marcar todas"; `api.js` ganha as chamadas de notificações/push/preferências; CSS `.sino-*`; i18n `sino.*`
- [ ] M4.F5.E3 SW push/click, Minha conta: preferências e ativar push
- [ ] M4.F6.E1 Fechamento: docs, manual, roteiro manual

---

## Protocolo de execução e retomada

**Cada etapa cabe numa sessão.** Nenhum marco é seguro em "one shot": M1 toca ~15 arquivos entre backend e frontend, M2 muda todas as listagens e o middleware, M3 é CSS em todas as views, M4 são cinco camadas novas. A quebra abaixo mantém cada etapa em poucos arquivos, com testes próprios e um estado consistente no fim (o sistema continua funcionando entre etapas).

**Ao começar uma sessão:**
1. Ler este arquivo: bloco *Andamento* (próxima etapa e notas de retomada) e a etapa correspondente na seção 3.
2. Rodar `git status` e `git log --oneline -5` para confirmar que o repositório está no estado esperado (a etapa anterior commitada, árvore limpa).
3. Ler os arquivos listados em "Contexto mínimo" do marco antes de editar qualquer coisa.
4. Executar **uma etapa só**. Se sobrar tempo, atualizar o andamento e perguntar antes de seguir para a próxima.

**Ao terminar uma etapa (ou ao interromper):**
1. Portões: `go build ./... ; go vet ./... ; go test ./...` verdes. Frontend: roteiro manual da etapa executado no navegador.
2. Marcar `[x]` (ou `[~]` com "Retomada:" descrevendo o ponto exato) no *Andamento* e mover **Próxima etapa**.
3. Atualizar o "Atualizado em" do topo.
4. Commit com a mensagem `M1.F1.E1: <resumo curto>` (uma etapa = um commit; pedir autorização se a sessão não a tiver dado).
5. Decisão tomada fora do plano durante a etapa: registrar na seção 2 (tabela de decisões) ou na própria etapa, nunca só no chat.

**Como pedir:** "executa a próxima etapa do PLANO_INTERNET" ou "executa M2.F1.E2".

---

## 1. Diagnóstico do código atual

### 1.1 Sessão e logout — por que "desloga muito rápido"

1. **JWT de 12 h fixo, sem renovação** (`ttlToken` em `internal/api/auth_handlers.go`). Quem loga às 8h cai às 20h; no dia seguinte precisa logar de novo. Todo dia.
2. **Qualquer 401 derruba a sessão e recarrega a página** (`req()` em `web/js/api.js` → `logout()` → `location.reload()` registrado em `web/js/app.js`). Como há requisições em background — poll de 5 s nas consultas/planejamentos "pensando", refresh do card de demanda, Home disparando 3 chamadas — o reload acontece "do nada", no meio de uma mensagem sendo digitada.
3. **Erro transitório é tratado como sessão inválida.** `carregarSessao()` em `web/js/auth.js` apaga o token em *qualquer* falha de `GET /auth/me` — inclusive queda de rede, 500 (banco "locked") e 503. Servidor reiniciando (deploy, backup) = usuário jogado para o login com token ainda válido.
4. **Segredo do JWT memoiza o erro.** `segredoJWT()` em `internal/api/servidor.go` usa `sync.Once`: se a primeira leitura falhar (banco ocupado no boot), *todas* as validações e logins passam a responder 500 até reiniciar o processo.
5. **SSE morre em silêncio.** Os streams (`/events`, `/logs`, `/progresso`) recebem o token em `?token=`. Quando o token expira, o `EventSource` recebe 401, fecha e **não reconecta** (comportamento da spec para status ≠ 200); todos os `onerror` do frontend ignoram o erro. Kanban/Home ficam congelados sem aviso. Na internet, o token em query vai parar nos logs do proxy/CDN.
6. **Rota após o logout:** o reload preserva o `#hash`; o portão cobre a app e nenhuma view é montada antes de logar; ao logar, `entrarNaApp()` navega para o hash guardado. Vai para o login **e** volta para a última rota. Não há "503 oculto" nesse caminho. Os 503 do código são `/healthz` com banco fora e consultas/planejamentos/IDE quando o serviço não subiu (sem `PRAXIS_HOME`). Os erros realmente ocultos são o item 5 e o item 3.
7. **Sem limite de tentativas de login.**
8. **Bug lateral:** `PUT /auth/senha` e `PUT /auth/idioma` caem no `default` de `permissaoMutacao()` (`internal/api/auth.go`) e exigem `*`. Usuário comum não troca a própria senha nem o idioma; os testes só cobrem admin. Não há tela "minha conta".

### 1.2 Visibilidade

- `criado_por` já existe em `consultas`, `planejamentos` e `demands` (nulo para token de API e bootstrap).
- O único controle é a **ACL por projeto** (`project_access`, `internal/db/project_access.go`): decide se o usuário vê o *projeto*; quem vê o projeto vê tudo dele.
- Acesso por id é barrado no middleware (`autorizarVisibilidade` → 404). Listagens filtram nos handlers: demandas no banco (`FiltroDemandas.VisiveisPara`); consultas e planejamentos em memória (`filtrarConsultasVisiveis`, `filtrarPlanejamentosVisiveis`).
- Excluir consulta/planejamento já exige dono-ou-admin.
- Grupo de usuários: cada usuário pertence a **no máximo um** grupo (`user_group_members.user_id` é PK).
- Outras listagens de demandas que precisam da mesma regra: board/kanban, Home (pendências, atividade, métricas), sobreposições, SSE global de eventos, `demands/ordem`, `/planejamentos/{id}/demandas`.

### 1.3 Eventos e notificações

- `events(project_id, demand_id, tipo, titulo, detalhe)`. Consultas e planejamentos registram eventos **sem** o id da consulta/planejamento — não dá para saber de quem é o evento.
- `notify.Despachante` faz polling de 2 s e envia aos canais externos; config global `notificacoes` + override por projeto; catálogo em `web/js/notify-events.js`.
- SSE global `/api/v1/events` por polling de 1 s, filtrado pela ACL de projeto.
- Não existe evento "demanda concluída" (status gravado em `internal/scheduler/executor.go` sem evento).
- Frontend: sem manifest, SW ou ícones; rota por hash só `#view`, sem id; `toast()` existe em `web/js/ui.js`.

---

## 2. Decisões de desenho (todas confirmadas em 2026-09-19)

| Tema | Decisão | Alternativa descartada |
|---|---|---|
| Modelo de sessão | Sessão persistida no banco (token opaco em cookie `HttpOnly; Secure; SameSite=Strict`) + JWT curto (60 min) só em memória. Revogável, lista de sessões, só stdlib. | JWT deslizante por header. |
| Rotação do refresh token | Sem rotação — desliza `expira_em` a cada uso. | Rotação a cada refresh. |
| Itens antigos na migração de visibilidade | Backfill como `publica`. Itens novos nascem `privada`. | Backfill `privada`. |
| Itens sem dono (token de API, bootstrap) | Config global `sem_dono_visibilidade`: `admins` (padrão) · `grupo` (+ `sem_dono_grupo_id`) · `publica`. Avaliada na leitura. | Sempre públicos. |
| Quem ignora a regra de dono | Só `*` (admin) e tokens de API. `projetos.gerir` ignora apenas a ACL de projeto (como hoje). | Estender a `projetos.gerir`. |
| Semântica de "grupo" | Dinâmica: quem está no mesmo grupo do criador no momento da leitura. Criador sem grupo + `grupo` = só ele vê (a UI avisa). | Congelar `group_id` na criação. |
| Filtro padrão das listas | `todos`, lembrado por tela em `localStorage`. | `meus`. |
| Destinatário da notificação | Resolvido no despachante a partir de `demand_id`/`consulta_id`/`planejamento_id` → `criado_por`. | Coluna `events.user_id`. |
| Web Push | Implementação própria com stdlib (`crypto/ecdh`, `crypto/hkdf`, `crypto/ecdsa`, AES-GCM). | `webpush-go`. |
| Transporte em tempo real | Polling do banco por conexão SSE (igual a `/events`). | Hub em memória. |
| Ícones do PWA | PNGs commitados em `web/icons/`. | Gerar no build. |
| Layout móvel | Entra em M3 e vai além da sidebar (3.3, fase F2). | Só o menu. |

---

## 3. Execução por fases e etapas

Cada etapa traz: **Objetivo**, **Arquivos**, **Pronto quando** (critério verificável). Os detalhes de desenho estão na seção 4 (referência); a etapa aponta para o trecho relevante.

### 3.1 M1 — Sessões duráveis e logout previsível

**Contexto mínimo** (ler antes): `internal/auth/jwt.go`, `internal/api/auth.go`, `internal/api/auth_handlers.go`, `internal/api/servidor.go` (`segredoJWT`, `Opcoes`), `internal/db/users.go` (`ObterOuGerarJWTSecret`, `AutenticarUsuario`), `internal/db/migracoes.go` (cabeçalho e última migração), `web/js/auth.js`, `web/js/api.js`, `web/js/app.js`, `cmd/praxis/main.go` (função `serve`).

#### F1 — Backend

**M1.F1.E1 — Migração 16 e store de sessões**
- Objetivo: tabela `sessoes` (4.1) e `internal/db/sessoes.go` com `CriarSessao`, `AutenticarSessao` (deslize de `expira_em` e `ultimo_uso`, regravando só se > 5 min), `RevogarSessao`, `RevogarSessoesDoUsuario(userID, exceto)`, `ListarSessoesDoUsuario`, `RemoverSessoesExpiradas`.
- Arquivos: `internal/db/migracoes.go`, `internal/db/sessoes.go`, `internal/db/sessoes_test.go`, `internal/db/migracoes_test.go` (versão 16).
- Pronto quando: testes cobrem criar/autenticar/deslizar/expirar (inatividade e teto)/revogar/limpar; `VersaoSchema() == 16`.

**M1.F1.E2 — Config de sessão, TTL do JWT, `exp` no principal, segredo sem memoizar erro**
- Objetivo: chaves globais `sessao_jwt_min` (60), `sessao_inatividade_dias` (30), `sessao_maxima_dias` (90) lidas na emissão; `emitirToken` usa a config; `auth.Validar` devolve também o `exp`; `principal` ganha `expiraEm`; `segredoJWT` troca `sync.Once` por mutex com nova tentativa em erro.
- Arquivos: `internal/api/auth_handlers.go`, `internal/api/auth.go`, `internal/api/servidor.go`, `internal/auth/jwt.go` (+ testes), `internal/db/config.go` se precisar de leitura tipada.
- Pronto quando: teste com `sessao_jwt_min=1` gera token que expira em 1 min; teste de segredo falhando na 1ª leitura e funcionando na 2ª.

**M1.F1.E3 — Rotas refresh/logout, cookie, login cria sessão, revogações, fix `case "auth"`**
- Objetivo: `POST /auth/refresh` e `POST /auth/logout` públicas (4.1); login/setup criam sessão e gravam cookie `praxis_sessao` (`HttpOnly`, `SameSite=Strict`, `Path=/api/v1/auth`, `Secure` com TLS ou proxy confiável); flag `-proxy-confiavel` (e env `PRAXIS_PROXY_CONFIAVEL`) no `serve`; troca de senha revoga as outras sessões; desativar usuário revoga todas; `permissaoMutacao` ganha `case "auth": return ""`.
- Arquivos: `internal/api/auth_handlers.go`, `internal/api/auth.go`, `internal/api/servidor.go` (`Opcoes.ProxyConfiavel`), `internal/api/users.go`, `cmd/praxis/main.go`, testes em `internal/api/auth_test.go` e `auth_users_test.go`.
- Pronto quando: testes de refresh ok/sem cookie/expirado/revogado; logout revoga e apaga cookie; `Secure` só com TLS ou proxy confiável; `auth/senha` e `auth/idioma` funcionam para usuário sem `*`.

**M1.F1.E4 — Sessões do usuário, rate limit de login, limpeza na manutenção**
- Objetivo: `GET /auth/sessoes`, `DELETE /auth/sessoes/{id}`, `DELETE /auth/sessoes` (todas menos a atual, identificada pelo cookie); rate limit em memória por IP e por e-mail (10 falhas / 15 min → 429 com `Retry-After`); manutenção chama `RemoverSessoesExpiradas`.
- Arquivos: `internal/api/auth_handlers.go`, novo `internal/api/ratelimit.go` (+ teste), `internal/manutencao/manutencao.go` (+ interface do store e teste).
- Pronto quando: testes das três rotas; 11ª falha responde 429; manutenção remove sessão expirada.

**M1.F1.E5 — SSE encerra no `exp` do token**
- Objetivo: os handlers de stream (`/events`, `/demands/{id}/logs`, `/consultas/{id}/progresso`, `/planejamentos/{id}/progresso`) derivam o contexto com `context.WithDeadline(principal.expiraEm)`.
- Arquivos: `internal/api/events.go`, `logs.go`, `consultas.go`, `planejamentos.go` (+ um teste em `events_test.go`).
- Pronto quando: teste com token de 1 s vê o stream fechar sozinho.

#### F2 — Frontend

**M1.F2.E1 — `auth.js`/`api.js`: token em memória, boot por refresh, renovação, retry em 401**
- Objetivo: 4.1 "Frontend", primeiros dois itens. Token só em memória; boot = `POST /auth/refresh`; 401 → portão, rede/5xx → estado "indisponível" (a tela vem na E3); `renovar()` single-flight; renovação proativa em ~80 % da validade, calculada a partir do `expira_em` que login/setup/refresh devolvem (não precisa decodificar o JWT); `req()` repete uma vez após renovar.
- Arquivos: `web/js/auth.js`, `web/js/api.js`.
- Pronto quando: com `sessao_jwt_min=1`, navegar por 3 min sem cair; Network mostra um único `/auth/refresh` por renovação mesmo com várias chamadas concorrentes.

**M1.F2.E2 — Helper `abrirStream` e troca dos 7 `EventSource`**
- Objetivo: `abrirStream(url, {onmessage, onevento})` em `api.js` com reabertura após renovar (backoff 1/2/5…30 s) e `close()`; substituir em `kanban.js`, `home.js`, `demandas.js` (2), `consultas.js`, `planejamentos.js`. O servidor emite `event: token_expirado` e fecha o stream quando o JWT vence (M1.F1.E5): ao receber esse evento o helper fecha o `EventSource` (senão ele reconecta sozinho com o token velho e leva 401), renova e reabre na hora; a reabertura por `onerror` + `readyState === CLOSED` fica como caminho de fallback.
- Pronto quando: com o stream fechando no `exp` (E5), o kanban continua recebendo eventos após a renovação sem reload.

**M1.F2.E3 — `app.js`: portão sem reload, Sair com logout**
- Objetivo: `aoDeslogar` mostra o portão sobre a app mantendo o DOM (aviso "sua sessão expirou"); ao logar de novo → `aplicarPermissoes()` + `irPara(viewAtual)`; botão Sair → `POST /auth/logout` → reload. (A tela "servidor indisponível" com retry e contagem foi entregue na E1.)
- Arquivos: `web/js/app.js`, `web/app.css` (estado do portão), `web/locales/*.json` (4 idiomas).
- Pronto quando: revogar a sessão pelo banco com texto digitado no chat → portão aparece, login, o texto continua lá; parar o servidor → tela de indisponível; subir → volta sozinho.

**M1.F2.E4 — Tela "Minha conta" v1 + i18n**
- Objetivo: nova view `conta` (item no `nav-user`): trocar senha (API já existe), idioma, lista de sessões com "encerrar" e "encerrar as outras". Strings nos 4 catálogos.
- Arquivos: `web/index.html`, `web/js/app.js` (rota), novo `web/js/conta.js`, `web/js/api.js`, `web/locales/*.json`.
- Pronto quando: usuário sem `*` troca a senha e encerra uma sessão de outro navegador, que cai no portão no próximo refresh.

#### F3 — Fechamento

**M1.F3.E1 — Campos de config na UI, roteiro manual, docs**
- Objetivo: `sessao_*` em `web/js/config-fields.js`; executar o roteiro (4.1 "Testes"); README_COMPLETO (§3 proxy/HTTPS, §6.1 sessões) nas duas línguas; manual embutido (`internal/api/manual/<idioma>/`).
- Pronto quando: roteiro passa, docs commitadas, M1 marcado entregue no Andamento.

### 3.2 M2 — Visibilidade: meus / grupo / público

**Contexto mínimo:** `internal/db/project_access.go`, `internal/db/consultas.go`, `internal/db/planejamentos.go`, `internal/db/demands.go`, `internal/db/events.go`, `internal/api/auth.go` (`filtroVisibilidade`, `autorizarVisibilidade`, `permissaoMutacao`), `internal/api/consultas.go`, `internal/api/planejamentos.go`, `internal/api/demands.go`, `internal/api/board.go`, `internal/api/home.go`, `internal/api/config.go`, `web/js/consultas.js`, `web/js/planejamentos.js`, `web/js/demandas.js`, `web/js/kanban.js`, `web/js/nova.js`, `web/js/config-fields.js`.

#### F1 — Banco

**M2.F1.E1 — Migração 17, `Visao`, `condDono`, config sem dono, testes em consultas**
- Objetivo: migração (4.2.1); `type Visao struct{ ACL *int64; Dono *int64; Escopo string; SemDono string; SemDonoGrupo int64 }` em `internal/db/visao.go`; `condDono(tabela)`/`argsDono(v)` implementando a regra de 4.2.2; aplicar só em `ListarConsultas` (que passa a receber `FiltroConsultas{ProjectID, GroupID, Visao}`) como prova.
- Arquivos: `internal/db/migracoes.go` (+ teste), `internal/db/visao.go` (+ teste), `internal/db/consultas.go` (+ teste).
- Pronto quando: matriz de testes dono × visibilidade × grupo × sem dono (3 modos) × escopo passa em consultas; backfill deixa linhas antigas `publica`.

**M2.F1.E2 — Consultas e planejamentos filtrados no banco (+ `criado_por_nome`)**
- Objetivo: `ListarPlanejamentos` com `FiltroPlanejamentos{…, Visao}`; ambas as listagens fazem `LEFT JOIN users` e devolvem `criado_por_nome`; `UsuarioVeConsulta`/`UsuarioVePlanejamento` recebem `Visao` (ACL + dono); novos `DefinirVisibilidadeConsulta/Planejamento`.
- Arquivos: `internal/db/consultas.go`, `internal/db/planejamentos.go`, `internal/db/project_access.go` (+ testes).
- Pronto quando: testes das duas tabelas; `filtrarConsultasVisiveis`/`filtrarPlanejamentosVisiveis` ficam sem uso (remover na E1 da F2).

**M2.F1.E3 — Demandas e eventos com a regra de dono no banco**
- Objetivo: `FiltroDemandas` troca `VisiveisPara` por `Visao`; `ListarDemandas`, `ListarDemandasResumo`, `ListarDemandasPorStatus`, `UsuarioVeDemanda`, `DefinirVisibilidadeDemanda`; `EventosApos`/`ListarEventos` passam a exigir, para evento com `demand_id`, que a demanda seja visível (`EXISTS` com `condDono("demands")`); `ListarSobreposicoes` (overlap) idem.
- Arquivos: `internal/db/demands.go`, `internal/db/events.go`, `internal/db/overlap.go`, `internal/db/home_test.go`, testes.
- Pronto quando: testes; `go build ./...` ainda verde (os handlers passam `Visao{ACL: uid}` provisoriamente até a F2).

#### F2 — API

**M2.F2.E1 — `visaoDaRequisicao`, middleware, consultas/planejamentos, PUT visibilidade, config**
- Objetivo: `visaoDaRequisicao(r)` em `auth.go` (ACL nil para `projetos.gerir`/token/bootstrap como hoje; Dono nil só para `*`/token/bootstrap; carrega `sem_dono_*` da config global); `autorizarVisibilidade` usa `Visao`; `handleListarConsultas/Planejamentos` passam o filtro (`?escopo=`) e removem o filtro em memória; `PUT /consultas/{id}/visibilidade` e `PUT /planejamentos/{id}/visibilidade` (dono ou `*`); campo `visibilidade` no POST; validação de `sem_dono_visibilidade`/`sem_dono_grupo_id` em `config.go`.
- Arquivos: `internal/api/auth.go`, `consultas.go`, `planejamentos.go`, `config.go`, testes (`project_access_test.go`, `consultas_test.go`, `planejamentos_test.go`, `config_test.go`).
- Pronto quando: 404 ao abrir item alheio privado por id; listagem respeita escopo; não-dono recebe 403 no PUT; config inválida → 400.

**M2.F2.E2 — Demandas, board, home, overlap, ordem, SSE, herança do planejamento**
- Objetivo: aplicar `Visao` em `handleListarDemandas`, `board.go`, `home.go` (pendências, atividade), `overlap.go`, `demands/ordem` (rejeitar id fora da visão), `/planejamentos/{id}/demandas`, `events.go` (SSE); `PUT /demands/{id}/visibilidade` mapeada em `permissaoMutacao` como `""`; criação por planejamento herda; criação por token nasce `privada`.
- Arquivos: `internal/api/demands.go`, `board.go`, `home.go`, `overlap.go`, `events.go`, `planejamentos.go`, `auth.go`, testes.
- Pronto quando: SSE não entrega evento de demanda invisível; kanban de usuário comum mostra só as suas/grupo/públicas; métricas da Home continuam só com ACL (limitação registrada).

#### F3 — Frontend

**M2.F3.E1 — Tipo `select` na config, campos sem dono, seletor nos 3 formulários, i18n**
- Objetivo: `config-fields.js` ganha `tipo: "select"` (opções fixas ou carregadas, ex.: grupos de usuários); campos `sem_dono_visibilidade` e `sem_dono_grupo_id`; seletor 🔒/👥/🌐 em nova consulta, novo planejamento e nova demanda (opção `grupo` desabilitada com dica quando `usuario.grupo_id` é nulo); última escolha em `localStorage`; strings nos 4 catálogos.
- Arquivos: `web/js/config-fields.js`, `web/js/config.js`, `web/js/consultas.js`, `web/js/planejamentos.js`, `web/js/nova.js`, `web/js/api.js`, `web/locales/*.json`.

**M2.F3.E2 — Consultas e planejamentos: pill, autor, filtro, alterar visibilidade**
- Objetivo: pill de visibilidade e autor (quando não é o próprio) nos cards; filtro segmentado "Meus · Grupo · Todos" acima das listas, persistido por tela; ação "alterar visibilidade" no painel (dono ou admin).
- Arquivos: `web/js/consultas.js`, `web/js/planejamentos.js`, `web/app.css`.

**M2.F3.E3 — Demandas e kanban: pill, filtro, alterar no card**
- Objetivo: mesmo tratamento em `demandas.js` (lista e card) e `kanban.js` (filtro ao lado de projeto/motor).
- Pronto quando: usuário comum, usuário com grupo e admin veem listas coerentes com a regra nas 4 telas.

#### F4 — Fechamento

**M2.F4.E1 — Docs, manual, roteiro manual**
- Objetivo: README (§6.1 visibilidade, config sem dono), manual embutido, roteiro com 3 usuários (comum sem grupo, comum com grupo, admin) + token de API.

### 3.3 M3 — PWA + layout móvel

**Contexto mínimo:** `web/web.go`, `internal/api/web.go`, `web/index.html`, `web/app.css`, `web/js/app.js`, `web/js/ui.js` (`toast`), `web/js/kanban.js`, `web/js/demandas.js` (card/modal).

#### F1 — PWA base

**M3.F1.E1 — Manifest, ícones, metas no index, handler `/sw.js`, service worker**
- Objetivo: 4.3.1. `web/manifest.webmanifest`, `web/icons/*.png` (gerar uma vez e commitar), metas no `index.html`, `//go:embed` atualizado, handler Go `GET /sw.js` com `Cache-Control: no-cache` e prefixo `VERSAO`/`SHELL` gerado por `fs.WalkDir`, `web/sw.js` com precache do shell, limpeza de caches antigos, fetch que não intercepta `/api/`, `/ide/`, `/healthz`.
- Arquivos: `web/web.go`, `internal/api/web.go` (+ teste), `web/index.html`, `web/manifest.webmanifest`, `web/sw.js`, `web/icons/`.
- Pronto quando: teste do handler (versão, `no-cache`, lista do shell); DevTools → Application mostra manifest válido e SW ativo; SSE e login continuam funcionando com o SW ativo.

**M3.F1.E2 — Registro do SW, aviso de versão nova, botão Instalar, dica iOS**
- Objetivo: 4.3.2.
- Arquivos: `web/js/app.js`, `web/locales/*.json`, `web/app.css`.
- Pronto quando: instala no Chrome desktop; trocar a versão do binário mostra o toast de nova versão.

#### F2 — Layout móvel (4.3.4)

**M3.F2.E1 — Shell móvel: gaveta, barra superior, áreas seguras, toast**
- Itens 1, 2 e 8 de 4.3.4. Breakpoint 768 px; `.sidebar` vira gaveta com botão na barra superior; `viewport-fit=cover` e `env(safe-area-inset-*)`; `.main` com padding menor; toast em largura total.
- Arquivos: `web/app.css`, `web/index.html`, `web/js/app.js`.

**M3.F2.E2 — Kanban e card da demanda no celular**
- Itens 3 e 4. Uma coluna por tela com seletor de status (ou scroll-snap) e ocultar colunas vazias; `.modal` em tela cheia, abas roláveis, `.fase-edit-row` empilhado.
- Arquivos: `web/app.css`, `web/js/kanban.js`, `web/js/demandas.js`.

**M3.F2.E3 — Telas de duas colunas em páginas; chat e editores**
- Itens 5 e 6. Lista ↔ painel com botão voltar em consultas, planejamentos, projetos, usuários, papéis, grupos; inputs com `font-size: 16px`; toolbar do `mdEditor` quebrando linha.
- Arquivos: `web/app.css`, `web/js/ui.js`, telas de duas colunas.

**M3.F2.E4 — Tabelas, alvos de toque, esconder IDE/pastas, passada final a 400 px**
- Itens 7, 9 e 10 e verificação de todas as views a 400 px sem rolagem horizontal da página.

#### F3 — Fechamento

**M3.F3.E1 — Lighthouse, instalar em 3 plataformas, docs**
- Lighthouse "PWA" sem erros; instalar em Windows (Chrome/Edge), Android (Chrome) e iOS (Safari, Adicionar à Tela de Início); README seção nova "Instalar como app"; manual.

### 3.4 M4 — Notificações ao usuário

**Contexto mínimo:** `internal/db/events.go`, `internal/notify/despachante.go`, `internal/notify/notify.go`, `internal/consultor/consultor.go` (`registrarEvento`), `internal/estrategista/estrategista.go` (`registrarEvento`), `internal/scheduler/executor.go` (`marcarDemanda`, `registrarEvento`), `internal/api/events.go` (`transmitirEventos`), `internal/manutencao/manutencao.go`, `cmd/praxis/scheduler.go` (`iniciarNotificacoes`), `web/js/notify-events.js`, `web/sw.js`, `web/js/app.js`, `web/js/conta.js` (de M1).

#### F1 — Banco e eventos

**M4.F1.E1 — Migração 18 + stores**
- Objetivo: 4.4.1. `internal/db/notificacoes.go` (`CriarNotificacao`, `ListarNotificacoes(userID, naoLidas, limite)`, `NotificacoesApos(userID, aposID)`, `MarcarLida`, `MarcarTodasLidas`, `ContarNaoLidas`, `RemoverNotificacoesAntesDe`), `internal/db/push.go` (`SalvarAssinatura`, `RemoverAssinatura(endpoint)`, `ListarAssinaturasDoUsuario`, `RegistrarFalhaAssinatura`), preferências em `users.go` (`ObterPreferencias`, `DefinirPreferencias`), chaves VAPID em `auth_config` (`ObterOuGerarVAPID`).
- Pronto quando: testes de cada store; `VersaoSchema() == 18`.

**M4.F1.E2 — Eventos com ids, `demanda_concluida`, catálogo**
- Objetivo: `Evento` ganha `ConsultaID`/`PlanejamentoID` (scan/insert); consultor e estrategista preenchem; executor registra `demanda_concluida` ao marcar `concluida`; `notify-events.js` ganha `demanda_concluida` e o flag `padrao_usuario` (4.4.1, lista de tipos); i18n do novo evento.
- Arquivos: `internal/db/events.go`, `internal/consultor/consultor.go`, `internal/estrategista/estrategista.go`, `internal/scheduler/executor.go`, `web/js/notify-events.js`, `internal/i18n/locales/*`, `web/locales/*.json`, testes.

#### F2 — Web Push

**M4.F2.E1 — `internal/webpush`: VAPID + `aes128gcm`**
- Objetivo: 4.4.4. `Cifrar(payload, p256dh, auth) ([]byte, error)` (RFC 8291/8188) e `AssinarVAPID(priv, aud, sub) (string, error)` (RFC 8292); `Enviar(ctx, cli, assinatura, chaves, payload, ttl, topic) (status int, err error)`.
- Pronto quando: teste reproduz o vetor do Apêndice A da RFC 8291 byte a byte; VAPID assina e o header tem o formato `vapid t=…, k=…`.

**M4.F2.E2 — Chaves VAPID no boot, config `push_contato`**
- Objetivo: `serve` chama `ObterOuGerarVAPID` (como faz com o JWT); `push_contato` na config global (default e-mail do primeiro admin) e em `config-fields.js`.
- Arquivos: `cmd/praxis/main.go`, `internal/db/users.go`, `web/js/config-fields.js`.

#### F3 — Despachante

**M4.F3.E1 — Destinatário, preferências, gravação em `notificacoes`**
- Objetivo: 4.4.3 itens 1 e 2. `destinatario(ev)` por `demand_id`/`consulta_id`/`planejamento_id` com cache por ciclo; preferências do usuário (padrão do catálogo quando vazias); `rota` derivada do id; interface `FonteEventos` estendida.
- Arquivos: `internal/notify/despachante.go`, `internal/notify/destinatario.go`, `cmd/praxis/scheduler.go` (`iniciarNotificacoes`), testes.
- Pronto quando: evento de consulta gera linha para o criador; tipo desligado nas preferências não gera; sem dono não gera.

**M4.F3.E2 — Envio push, falhas, retenção**
- Objetivo: 4.4.3 item 3 e retenção. Envio em goroutine com timeout; 404/410 apaga assinatura; 429/5xx incrementa `falhas`, remove em 5; `push_em` carimbado; manutenção remove notificações lidas > 30 d, não lidas > 90 d, assinaturas sem uso > 180 d.
- Arquivos: `internal/notify/push.go`, `internal/manutencao/manutencao.go`, testes com servidor HTTP falso.

#### F4 — API

**M4.F4.E1 — Listar, lida(s), stream SSE por usuário**
- Objetivo: 4.4.5, primeiras três linhas. `transmitirNotificacoes` reaproveita o laço de `transmitirEventos` (polling 1 s, heartbeat, deadline no `exp`); `permissaoMutacao` ganha `case "notificacoes": return ""`; tokens de API → 400.
- Arquivos: novo `internal/api/notificacoes.go` (+ teste), `internal/api/auth.go`.

**M4.F4.E2 — Push (chave, assinar, cancelar) e preferências**
- Objetivo: `GET /notificacoes/push/chave`, `POST/DELETE /notificacoes/push`, `GET/PUT /auth/preferencias` (valida tipos contra o catálogo do backend).
- Arquivos: `internal/api/notificacoes.go`, `internal/api/auth_handlers.go`, testes.

#### F5 — Frontend

**M4.F5.E1 — Rotas com id (`#view/id`)**
- Objetivo: `irPara()` aceita `#consultas/7`, `#planejamentos/3`, `#demandas/12` e entrega o id ao `montar*`; selecionar um item atualiza o hash; voltar/avançar do navegador funciona.
- Arquivos: `web/js/app.js`, `consultas.js`, `planejamentos.js`, `demandas.js`.

**M4.F5.E2 — Stream no boot, toast, sino com badge e painel**
- Objetivo: `abrirStream("/api/v1/notificacoes/stream")` em `entrarNaApp()`; toast; sino no topo com contador (carregado no boot) e painel com lista, clique navega e marca lida, "marcar todas"; se a aba não está visível e há permissão → `registration.showNotification` com `tag`.
- Arquivos: `web/js/app.js`, novo `web/js/notificacoes.js`, `web/js/api.js`, `web/index.html`, `web/app.css`, `web/locales/*.json`.

**M4.F5.E3 — SW push/click, Minha conta: preferências e ativar push**
- Objetivo: `sw.js` com `push` (não mostra se há janela `focused`) e `notificationclick` (foca janela existente + `postMessage({rota})`, ou `openWindow`); a página escuta e navega; em Minha conta: seções navegador/push/eventos e botão "Ativar notificações neste dispositivo" (permissão → `pushManager.subscribe` → `POST`), com "desativar".
- Arquivos: `web/sw.js`, `web/js/conta.js`, `web/js/app.js`, `web/locales/*.json`.
- Pronto quando: com o PWA fechado, uma consulta respondida gera notificação no sistema operacional; clicar abre a consulta certa; com a aba focada, só o toast aparece.

#### F6 — Fechamento

**M4.F6.E1 — Docs, manual, roteiro manual**
- README §7 (notificações por usuário e push, `push_contato`, VAPID), manual, roteiro: consulta, planejamento e demanda de três usuários diferentes, cada um recebe só as suas.

---

## 4. Desenho de referência

### 4.1 M1 — Sessões

**Migração 16**

```sql
CREATE TABLE sessoes (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash  TEXT    NOT NULL UNIQUE,          -- sha256 do token opaco (nunca o valor em claro)
    criado_em   TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    ultimo_uso  TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    expira_em   TEXT    NOT NULL,                 -- desliza a cada uso (inatividade)
    limite_em   TEXT    NOT NULL,                 -- teto absoluto (não desliza)
    user_agent  TEXT    NOT NULL DEFAULT '',
    ip          TEXT    NOT NULL DEFAULT '',
    revogada_em TEXT
);
CREATE INDEX ix_sessoes_user ON sessoes (user_id);
```

**Rotas**: `POST /auth/refresh` (pública; lê só o cookie; devolve `{token, usuario}`; 401 se inválido), `POST /auth/logout` (pública; revoga e apaga o cookie), `GET /auth/sessoes`, `DELETE /auth/sessoes/{id}`, `DELETE /auth/sessoes` (outras). Login/setup criam a sessão. Troca de senha revoga as demais; desativar usuário revoga todas.

**Cookie**: `praxis_sessao`; `HttpOnly`; `SameSite=Strict`; `Path=/api/v1/auth`; `Max-Age` = inatividade; `Secure` com TLS ou `X-Forwarded-Proto: https` de proxy confiável (`-proxy-confiavel` / `PRAXIS_PROXY_CONFIAVEL=1`; sem a flag, `X-Forwarded-*` é ignorado também para IP). CSRF: o refresh só devolve JSON e `SameSite=Strict` já impede o envio cross-site; nenhuma outra rota usa cookie.

**JWT**: TTL da config; `principal.expiraEm`; streams com deadline no `exp`; `segredoJWT` sem memoizar erro. **Rate limit** de login em memória por IP e e-mail.

**Frontend**: token só em memória; boot = refresh; 401 → portão, rede/5xx → "servidor indisponível" com retry; `renovar()` single-flight; renovação proativa em ~80 % do `exp`; `req()` repete uma vez após renovar; `abrirStream` reabre após renovar com backoff; portão sem reload mantendo o DOM; Sair → logout → reload.

**Roteiro manual**: `sessao_jwt_min=1` → kanban continua atualizando após 1 min sem reload; reiniciar o servidor com a página aberta não desloga; 2 dias fechado e reabrir sem login; mensagem em digitação sobrevive à queda de sessão.

### 4.2 M2 — Visibilidade

#### 4.2.1 Migração 17

```sql
ALTER TABLE consultas     ADD COLUMN visibilidade TEXT NOT NULL DEFAULT 'privada' CHECK (visibilidade IN ('privada','grupo','publica'));
ALTER TABLE planejamentos ADD COLUMN visibilidade TEXT NOT NULL DEFAULT 'privada' CHECK (visibilidade IN ('privada','grupo','publica'));
ALTER TABLE demands       ADD COLUMN visibilidade TEXT NOT NULL DEFAULT 'privada' CHECK (visibilidade IN ('privada','grupo','publica'));
UPDATE consultas SET visibilidade = 'publica';       -- compatibilidade: nada some
UPDATE planejamentos SET visibilidade = 'publica';
UPDATE demands SET visibilidade = 'publica';
CREATE INDEX ix_consultas_dono     ON consultas (criado_por);
CREATE INDEX ix_planejamentos_dono ON planejamentos (criado_por);
```

#### 4.2.2 Regra

Item visível ao usuário `u` quando **(ACL de projeto permite) E (regra de dono)**:

```
criado_por = u
OR visibilidade = 'publica'                          -- inclui o backfill
OR (visibilidade = 'grupo' AND EXISTS (
      SELECT 1 FROM user_group_members m1
      JOIN user_group_members m2 ON m2.group_id = m1.group_id
      WHERE m1.user_id = criado_por AND m2.user_id = u))
OR (criado_por IS NULL AND (                          -- sem dono: config da instância
      :sem_dono = 'publica'
      OR (:sem_dono = 'grupo' AND EXISTS (
            SELECT 1 FROM user_group_members m WHERE m.group_id = :sem_dono_grupo AND m.user_id = u))))
```

Config global: `sem_dono_visibilidade` (`admins` padrão · `grupo` · `publica`) e `sem_dono_grupo_id`. Lida na requisição e passada como parâmetro; mudar vale na hora. Itens sem dono nascem `privada` (o valor é ignorado para eles, exceto `publica`, que um admin pode marcar e que a migração aplica aos antigos).

Quem ignora a regra de dono: só `*` e tokens de API. `projetos.gerir` ignora apenas a ACL de projeto (é quem a administra). Exemplo: usuário com `projetos.gerir` sem `*` vê todos os projetos, mas nas consultas só as dele, do grupo e públicas.

Escopo `?escopo=`: `meus` → `criado_por = u`; `grupo` → criador ∈ membros do grupo de `u` (inclui `u`), sujeito à regra; `todos` (padrão) → só a regra.

`db.Visao{ACL *int64; Dono *int64; Escopo string; SemDono string; SemDonoGrupo int64}` substitui o `visiveisPara *int64` espalhado. `ACL` nil = ignora ACL; `Dono` nil = ignora regra de dono.

**Rotas de mutação** (dono ou `*`; corpo `{"visibilidade":"grupo"}`): `PUT /consultas/{id}/visibilidade`, `PUT /planejamentos/{id}/visibilidade`, `PUT /demands/{id}/visibilidade` (esta precisa de `""` em `permissaoMutacao`). Criação: campo opcional (default `privada`); demanda de planejamento herda; item por token nasce `privada`.

**Métricas da Home** (`metrics_dia`) são agregadas por projeto: continuam só com a ACL de projeto (limitação registrada).

**Frontend**: seletor 🔒 privada · 👥 grupo · 🌐 pública nos formulários; pill + autor nos cards; filtro segmentado "Meus · Grupo · Todos" persistido por tela; alterar visibilidade no painel/card; `config-fields.js` com tipo `select`.

### 4.3 M3 — PWA

#### 4.3.1 Arquivos

- `manifest.webmanifest`: `name` "Praxis Autonomous", `short_name` "Praxis", `id`/`start_url` "/", `scope` "/", `display` "standalone", `theme_color`/`background_color` do `app.css`, `lang`, ícones 192/512 + maskable.
- `icons/`: `icon-192.png`, `icon-512.png`, `icon-maskable-512.png`, `apple-touch-icon.png` (180).
- `sw.js` servido por handler Go em `GET /sw.js` com `Cache-Control: no-cache` e prefixo `const VERSAO = "<api.Versao>"; const SHELL = [...]` (lista por `fs.WalkDir(web.Assets)`). `install` pré-cacheia o shell em `praxis-<VERSAO>`; `activate` limpa outras versões e `clients.claim()`; `fetch` **nunca intercepta** `/api/`, `/ide/`, `/healthz`; navegações network-first com fallback ao `index.html`; estáticos cache-first com atualização em background.
- `index.html`: `<link rel="manifest">`, `<meta name="theme-color">`, `<link rel="apple-touch-icon">`, `apple-mobile-web-app-capable`, `apple-mobile-web-app-status-bar-style`, `viewport-fit=cover`.

#### 4.3.2 `app.js`

Registrar o SW só em `window.isSecureContext`; `updatefound` → toast "nova versão disponível — recarregue"; `beforeinstallprompt` → botão **Instalar app** no `nav-user`; iOS → dica "Compartilhar → Adicionar à Tela de Início"; permissão de notificação só por clique (Minha conta).

#### 4.3.3 Requisitos

HTTPS com certificado válido (o autoassinado do `-tls` não instala no Android); loopback conta como seguro.

#### 4.3.4 Layout móvel (≈ 400 px), por impacto

1. **Shell**: `.app` deixa de ser flex lado a lado abaixo de 768 px; `.sidebar` (220 px, sticky, 100vh) vira gaveta com botão hambúrguer numa barra superior fixa (título + sino + avatar); fecha ao navegar; `.main` com 12–16 px de padding.
2. **Áreas seguras**: `viewport-fit=cover` e `env(safe-area-inset-*)` na barra e no rodapé.
3. **Kanban**: uma coluna por tela com seletor de status (ou `scroll-snap-type: x mandatory`); ocultar colunas vazias.
4. **Card da demanda**: `.modal` em tela cheia, cabeçalho fixo, abas roláveis, `.fase-edit-row` empilhado.
5. **Telas `.two-col`**: lista e painel em "páginas" com voltar (o hash com id de M4 ajuda).
6. **Chat e editores**: inputs com `font-size ≥ 16px` (sem zoom no iOS); toolbar do `mdEditor` quebrando linha.
7. **Tabelas** (`table.plain`): contêiner com `overflow-x: auto` ou cartões abaixo de 640 px.
8. **Toast**: largura total com margem, acima da área segura inferior.
9. **Alvos de toque**: `.btn.sm`, `.switch`, `.modal-close` ≥ 44 px no breakpoint.
10. **Fora do celular**: esconder IDE web e `pasta-picker.js`; Manual já tem breakpoint em 720 px.

Critério: cada view opera em 400 px sem rolagem horizontal da página; testar como PWA em Chrome Android e Safari iOS.

### 4.4 M4 — Notificações

#### 4.4.1 Migração 18

```sql
ALTER TABLE events ADD COLUMN consulta_id     INTEGER REFERENCES consultas(id)     ON DELETE SET NULL;
ALTER TABLE events ADD COLUMN planejamento_id INTEGER REFERENCES planejamentos(id) ON DELETE SET NULL;

CREATE TABLE notificacoes (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    event_id   INTEGER REFERENCES events(id) ON DELETE SET NULL,
    tipo       TEXT NOT NULL,
    titulo     TEXT NOT NULL,
    detalhe    TEXT NOT NULL DEFAULT '',
    rota       TEXT NOT NULL DEFAULT '',   -- "#demandas/12", "#consultas/7", "#planejamentos/3"
    lida_em    TEXT,
    push_em    TEXT,
    criado_em  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
CREATE INDEX ix_notificacoes_user ON notificacoes (user_id, id);

CREATE TABLE push_subscriptions (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    endpoint   TEXT NOT NULL UNIQUE,
    p256dh     TEXT NOT NULL,
    auth       TEXT NOT NULL,
    user_agent TEXT NOT NULL DEFAULT '',
    criado_em  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    ultimo_uso TEXT,
    falhas     INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX ix_push_user ON push_subscriptions (user_id);

ALTER TABLE users       ADD COLUMN notificacoes TEXT NOT NULL DEFAULT '';   -- JSON de preferências
ALTER TABLE auth_config ADD COLUMN vapid_publica TEXT NOT NULL DEFAULT '';
ALTER TABLE auth_config ADD COLUMN vapid_privada TEXT NOT NULL DEFAULT '';
```

Preferências: `{"navegador": true, "push": true, "eventos": {"consulta_respondida": true, …}}`; vazio = padrão do catálogo. Tipos com `padrao_usuario` ligado ("sua atividade terminou / precisa de você"): `consulta_respondida`, `consulta_falhou`, `estrategia_respondida`, `estrategia_falhou`, `analise_concluida`, `planejamento_concluido`, `aguardando_humano`, `fase_falhou`, `gates_falharam`, `franquia_esgotada`, `demanda_concluida` (novo), `demanda_integrada`, `push_falhou`, `merge_falhou`.

#### 4.4.2 Registro dos eventos

`consultor.registrarEvento` e `estrategista.registrarEvento` preenchem `ConsultaID`/`PlanejamentoID`; os de demanda já têm `DemandID`; executor registra `demanda_concluida`.

#### 4.4.3 Despachante

1. `destinatario(ev)` → `criado_por` conforme o id presente (cache por ciclo); sem dono → ninguém.
2. Preferências ligam o tipo → `INSERT notificacoes` com `rota`.
3. `push` ligado + assinaturas → Web Push por assinatura, em goroutine com timeout; payload `{id, titulo, detalhe, rota, tag}`; 404/410 apaga; 429/5xx `falhas++`, remove em 5.

Retenção: lidas > 30 d, não lidas > 90 d, assinaturas sem uso > 180 d.

#### 4.4.4 Web Push (`internal/webpush`, stdlib)

VAPID (RFC 8292): ECDSA P-256 em `auth_config`; JWT ES256 com `aud` = origem do endpoint, `exp` ≤ 24 h, `sub` = `mailto:` (`push_contato`); header `Authorization: vapid t=<jwt>, k=<pública>`. Payload (RFC 8291 + 8188, `aes128gcm`): ECDH P-256 efêmero, `crypto/hkdf` com o `auth`, AES-128-GCM, cabeçalho salt/rs/keyid; vetor do Apêndice A da RFC 8291 como teste. Headers `Content-Encoding: aes128gcm`, `TTL: 86400`, `Urgency: normal`, `Topic` = tipo.

#### 4.4.5 API

| Rota | Permissão | Função |
|---|---|---|
| `GET /api/v1/notificacoes?nao_lidas=1&limite=50` | autenticado | lista do próprio usuário |
| `POST /api/v1/notificacoes/{id}/lida` · `POST /api/v1/notificacoes/lidas` | autenticado | marcar uma / todas |
| `GET /api/v1/notificacoes/stream?after=<id>` | autenticado | SSE por usuário (polling `user_id=? AND id>?`) |
| `GET /api/v1/notificacoes/push/chave` | autenticado | chave VAPID pública |
| `POST /api/v1/notificacoes/push` · `DELETE /api/v1/notificacoes/push` | autenticado | assinar / cancelar |
| `GET /api/v1/auth/preferencias` · `PUT /api/v1/auth/preferencias` | autenticado | preferências |

`permissaoMutacao`: `case "notificacoes": return ""`. Tokens de API → 400.

#### 4.4.6 Frontend

Rotas `#view/id`; stream aberto em `entrarNaApp()`; toast + badge + `showNotification` com `tag` quando a aba não está visível; sino com painel; SW `push` (não mostra com janela `focused`) e `notificationclick` (foca + `postMessage({rota})` ou `openWindow`); Minha conta com preferências e ativar/desativar push.

---

## 5. Antes de expor na internet (não pedido, mas necessário)

- Certificado válido no proxy; `-proxy-confiavel` ligado para `X-Forwarded-Proto`/`X-Forwarded-For`.
- HSTS e `X-Content-Type-Options` no proxy (ou no `comLog`).
- Criar o primeiro admin **antes** de expor (sem usuários o servidor opera em modo bootstrap sem credencial).
- Mascarar `?token=` nos logs do proxy.
- `codigo.editar` (IDE web = terminal no servidor) e `projetos.gerir` (`/fs/dirs` lista o disco): conceder só a quem precisa; avaliar desligar o IDE na instância pública com uma flag.
- Limite de tamanho nos uploads de referências e backup do banco fora da máquina.

## 6. Documentação a atualizar (por marco, na etapa de fechamento)

- `README_COMPLETO.md` / `README_COMPLETO.pt-BR.md`: §3 (rede/HTTPS/proxy), §6.1 (sessões, cookie, refresh — o texto atual ainda descreve "sem token = admin local"), §7 (notificações por usuário e push), seção nova "Instalar como app".
- Manual embutido (`internal/api/manual/<idioma>/`): visibilidade, filtros, Minha conta, instalar o app, notificações.
- `web/js/notify-events.js`: `demanda_concluida` e `padrao_usuario`.
- Este arquivo: Andamento e "Atualizado em".
