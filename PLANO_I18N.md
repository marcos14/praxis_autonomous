# Plano de implementação — i18n da plataforma (en · pt-BR · es · zh-CN)

Atualizado em: 2026-08-09 — **em execução**: E1 concluída; E2 e E3 iniciadas
(ver *Status da execução* ao final).

## Objetivo

A interface, as mensagens do servidor, o conteúdo gerado pela IA e o manual devem
funcionar em **inglês, português (Brasil), espanhol e chinês simplificado**, com o
idioma escolhido por usuário. O contrato da API (campos JSON, slugs de status,
permissões) **não muda** — i18n é camada de apresentação.

## Avaliação do estado atual

| Camada | Situação | Volume |
|---|---|---|
| UI web (`web/`) | Strings pt-BR embutidas no `index.html` (225 linhas) e em 20 módulos JS (~5.600 linhas, padrão `el(..., {text: "..."})`, toasts, labels) | ~700–900 strings estimadas |
| Erros da API | `responderErro(w, ...)` com mensagem pt-BR literal | 201 chamadas em 25 arquivos |
| Eventos (`db.Evento`) | `Titulo`/`Detalhe` humanos **persistidos** no banco no momento da gravação | — |
| Notificações (`internal/notify`) | Despacham `ev.Titulo`/`ev.Detalhe` para canais compartilhados (Telegram etc.) | — |
| Conteúdo de IA | Prompts mandam responder "em pt-BR" fixo ([consultor.md:27](internal/intake/prompts/consultor.md#L27), [estrategista.md:38](internal/intake/prompts/estrategista.md#L38), [overview.md:7](internal/intake/prompts/overview.md#L7); analista/planejador herdam o idioma do prompt) | 10 prompts |
| Manual web | 11 arquivos `.md` pt-BR embutidos, servidos por `GET /manual/{slug}` | ×4 idiomas |
| Statuses/permissões | Já são códigos (`aguardando_respostas`, `consultas.usar`) — tradução só na exibição | ok ✅ |
| Preferência do usuário | Tabela `users` não tem campo de idioma | migração |
| Datas/moeda | Formatação manual no JS | trocar por `Intl.*` |

## Decisões de arquitetura

### Sem dependências novas (stdlib-first, padrão do projeto)

Nada de `go-i18n`/`golang.org/x/text`. Catálogos em **JSON plano** (`chave.pontuada`
→ texto, interpolação `{nome}`), embutidos com `//go:embed`:

- `internal/i18n/locales/{en,pt-BR,es,zh-CN}.json` — mensagens do servidor;
- `web/locales/{en,pt-BR,es,zh-CN}.json` — strings da UI (servidos como asset);
- `t(lang, chave, args)` no Go (~50 linhas) e `t(chave, args)` no JS (~30 linhas).

Plural: mínimo necessário — sufixos `.one`/`.other` e um helper `tn(chave, n)`
(zh não flexiona; en/pt/es seguem a regra 1/n). Nada de CLDR completo.

### Resolução do idioma (precedência)

1. **Preferência do usuário** — coluna nova `users.idioma` (nullable), editável no
   próprio rodapé/perfil da UI; enviada ao servidor na sessão.
2. **Antes do login** — `localStorage` → `Accept-Language` do navegador (a tela de
   login já abre no idioma certo).
3. **Idioma da instância** — chave nova `idioma` na config global (default `pt-BR`,
   preserva o comportamento das instalações existentes).

O servidor resolve o idioma por requisição (sessão → `Accept-Language` → instância)
e o injeta num helper de contexto; o JS carrega o catálogo certo no boot e aplica
`<html lang="...">`.

### Eventos e notificações: idioma da instância (v1)

- **Notificações** vão para canais compartilhados do time → idioma da instância,
  sempre. Correto por definição (não existe "idioma do leitor" num canal Telegram).
- **Eventos** são persistidos → gravados no idioma da instância no momento do fato.
  Eventos antigos permanecem como foram escritos (trade-off aceito e documentado).
  Uma v2 opcional pode neutralizar (`tipo` + params JSON, render localizado na
  leitura), mas exige migração de dados e não bloqueia esta entrega.

### Conteúdo gerado pela IA: parâmetro de idioma no prompt

Os prompts ganham o placeholder `{{IDIOMA}}` ("Responda/produza os documentos em
{{IDIOMA}}") preenchido assim:

- **Consultas e Planejamentos** — idioma do usuário criador (a conversa é dele);
- **Demandas (perguntas do analista, plano)** — idioma do usuário criador; via API
  sem usuário, idioma da instância;
- **Overview do repositório** — idioma da instância (é um artefato do projeto).

A instrução de idioma deve ser **explícita e reforçada no fim do prompt** (para
zh-CN a diretiva fraca degrada). Os prompts em si continuam escritos em pt-BR —
o que muda é a diretiva de saída. O pós-filtro anti-código do consultor precisa
manter as mensagens de recusa/remoção no catálogo (hoje são literais).

### Manual multilíngue

`internal/api/manual/{pt-BR,en,es,zh-CN}/*.md` — pt-BR é a fonte da verdade;
`GET /manual/{slug}` honra o idioma resolvido com fallback pt-BR por página
(página ainda não traduzida cai no original em vez de 404). Tradução inicial
gerada por IA e revisada; o teste de paridade avisa páginas faltantes.

### CJK e formatação

- `app.css`: incluir fallbacks CJK no font-stack (`"PingFang SC", "Microsoft
  YaHei", "Noto Sans SC"`) e conferir `line-height` nos cards/kanban.
- Datas, números e moeda na UI trocam concatenação manual por
  `Intl.DateTimeFormat`/`Intl.NumberFormat` com o locale ativo.

### O que NÃO muda (contrato)

- Campos JSON da API (`erro.codigo`, `origem_ref`…), slugs de status, nomes de
  permissões, chaves de config (`motor_preferido`…) — são identificadores.
- `erro.codigo` continua sendo o campo estável para integrações; só `mensagem`
  passa a vir no idioma da requisição.
- Saída do CLI (`praxis serve/service/import`) fica fora desta entrega (pt-BR;
  candidata a en neutro depois).

## Etapas

### E1 — Infraestrutura de i18n

- `internal/i18n`: catálogos embed + `t()`/`tn()` + resolução de idioma por
  requisição; chave `idioma` na config global; migração `users.idioma`.
- `web/js/i18n.js`: carga do catálogo, `t()`/`tn()`, aplicação de `data-i18n` no
  `index.html`, seletor de idioma na UI (rodapé/perfil e tela de login).
- **Gate:** teste Go de paridade de chaves entre os 4 catálogos (server e web) —
  chave faltante/sobrando falha o build.

### E2 — Extração da UI web

- `index.html`: textos estáticos → atributos `data-i18n`.
- 20 módulos JS: literais → `t("...")`; labels de status/kanban ([demandas.js:26](web/js/demandas.js#L26),
  [kanban.js:12](web/js/kanban.js#L12)) viram mapa `t("status."+slug)`; `config-fields.js`
  (descrições de parâmetros) para o catálogo; `Intl.*` para datas/moeda.
- Ordem sugerida (risco crescente): manual.js → auth.js → home.js → kanban.js →
  … → demandas.js/planejamentos.js (os maiores).
- Preencher pt-BR (extração literal) + en; es/zh-CN gerados por IA e revisados.

### E3 — Mensagens do servidor

- 201 `responderErro` → `t(langDaRequisicao, "erro.chave", args)`.
- Geração de eventos e notificações → catálogo com idioma da instância.
- Mensagens do pós-filtro do consultor e "falas de sistema" de chat/planejamento.

### E4 — Conteúdo de IA

- `{{IDIOMA}}` nos 10 prompts + plumbing do idioma (criador/instância) no intake,
  consultas e planejamentos.
- Teste manual nos 4 idiomas: pergunta do analista, resposta do consultor e um
  PRD do estrategista (qualidade zh-CN é o ponto de atenção).

### E5 — Manual ×4

- Reorganizar `internal/api/manual` por idioma; traduzir os 11 arquivos (IA +
  revisão); fallback por página; teste de paridade de slugs.

### E6 — Fechamento

- Font-stack CJK + varredura visual nos 4 idiomas (overflow de labels em botões,
  kanban, tiles da Home).
- Atualizar READMEs: remover a nota "UI em pt-BR / i18n on the roadmap" e citar
  os 4 idiomas como vantagem.
- Gates do projeto verdes (`go build`, `go vet`, `go test -count=1`).

## Status da execução

**E1 — concluída** (gates verdes):

- `internal/i18n`: catálogos embutidos (4 idiomas), `T`/`TN`, `Normalizar`,
  `DoAcceptLanguage`; migração 15 (`users.idioma`) + `DefinirIdiomaUsuario`;
  `PUT /api/v1/auth/idioma`; `idioma` no `respUsuario`; `erroT` +
  `idiomaDaRequisicao` em `respostas.go`; header `X-Praxis-Idioma` enviado por
  `api.js`/`auth.js`.
- `web/js/i18n.js` (top-level await: módulos podem usar `t()` até em constantes),
  `web/locales/*` embutidos, seletor de idioma no login e no rodapé do menu,
  sincronização da preferência do usuário (login em outro dispositivo adota o
  idioma salvo), fontes CJK no `app.css`.
- Testes: paridade servidor+web, placeholders consistentes, chaves usadas na UI
  (`data-i18n` e `t("...")`) presentes no catálogo, Normalizar/Accept-Language.

**E2 — concluída.** Os 20 módulos JS + `index.html` convertidos para `t()`/
`tn()`; catálogos web com ~900 chaves nos 4 idiomas (fusão de fragmentos +
conversão automática por casamento exato + acabamento manual). Varredura final
por literais acentuados: zero sobras. Gates `TestChavesDaUIExistem` e paridade
verdes.

**E3 — essencialmente concluída.** Convertidos 162 dos 201 `responderErro`
(80%+) para `erroT`, incluindo os padrões dinâmicos `(status atual: {status})`
e `sem_permissao` com `{permissao}`; eventos e falas de sistema de api,
pipeline, intake e scheduler em `i18n.TI` (idioma da instância); notify não
tinha texto próprio (só cabeçalho do usuário + evento já traduzido). Catálogos
do servidor com ~260 chaves nos 4 idiomas. **Sobras conhecidas (39 chamadas +
9 títulos):** mensagens de validação construídas em helpers (`montarProjeto`,
`montarMotor`, `msgInvalido` da tabela de ações), repasses de `err.Error()` e
os eventos de consultor/estrategista (único fragmento não entregue) — exigem
refatoração pontual, não conversão mecânica; candidatas a demanda no próprio
Praxis. Os campos persistidos `demanda.Erro` e observação de fase também
seguem em pt (mesmo trade-off dos eventos antigos).

**E4 — concluída.** Os 5 prompts de conteúdo lido pelo usuário (analista,
planejador, consultor, estrategista, overview) ganharam o marcador `{IDIOMA}` na
diretiva existente **e um reforço explícito no fim do prompt** — diretiva fraca
degrada em zh-CN. O idioma resolvido é o de quem criou a conversa
(`db.IdiomaDoUsuario` a partir de `CriadoPor`), com o idioma da instância como
fallback; o overview do repositório usa sempre o da instância, por ser artefato
do projeto. Os prompts do ciclo de código (executor/corretor/revisor) ficaram
**de fora de propósito**: código, comentários e mensagens de commit seguem a
língua do repositório, não a preferência de quem abriu a demanda. Guardas:
`TestPromptsTemMarcadorIdioma` e `TestRenderPromptSubstituiIdioma`.

**E5 — concluída.** O manual virou `internal/api/manual/<idioma>/NN-slug.md` com
**fallback por página**: a ordem e o conjunto de slugs saem sempre de pt-BR e uma
página sem tradução cai no texto original — nunca 404, nunca navegação com
buracos. `GET /manual` e `GET /manual/{slug}` honram o idioma da requisição, com
cache por idioma. As 11 seções foram traduzidas para en, es e zh-CN mantendo os
rótulos da UI em pt-BR com a tradução entre parênteses na primeira ocorrência de
cada seção (a interface está em pt-BR na tela; o leitor precisa reconhecer o
botão). Guardas: `TestManualMesmosSlugsEmTodosIdiomas` (falha se um idioma
perder seções) e `TestManualIdiomaTraduzidoDifereDaFonte` (reporta, sem falhar,
quantas páginas ainda caem em fallback).

**E6 — concluída.** Fontes CJK no `app.css`; READMEs (pt e en) com o i18n como
vantagem no lugar da nota "i18n on the roadmap"; guias completos com a chave
`idioma` na tabela de config e a seção **5.3 Idiomas (i18n)** documentando a
precedência, o header `X-Praxis-Idioma`, o `PUT /auth/idioma` e a estabilidade
de `erro.codigo`.

**Guardas de qualidade acrescentados depois de um incidente real:** uma conversão
automática por casamento de texto corrompeu um literal (a chave
`nova.cadastre_depois` tinha o valor `"."`, que casou dentro de outra string) e
quebrou a UI com `SyntaxError` — sem que `node --check` acusasse, porque sem
`--input-type=module` ele não avalia o arquivo como ES module. Daí vieram
`TestModulosWebParseiam` (parse de cada módulo como ES module) e
`TestImportsDoI18nNoWeb` (chamada de `t()`/`tn()` sem o import correspondente,
que só falharia no navegador). Lição registrada: **nunca substituir texto por
chave via casamento cego de substring** — exigir que o valor seja um literal
completo e recusar valores curtos/pontuação.

**Bug pré-existente encontrado no caminho (não era do i18n).** A tela Manual
quebrava em pt-BR com `Cannot read properties of undefined (reading 'length')`:
os `.md` versionados estavam com **CRLF**, o `\r` sobrevivia ao `split("\n")` e
desancorava o padrão de título do `renderMarkdown` (`$` não casa antes de `\r`) —
a linha `## Título\r` deixava de ser título, caía no ramo de parágrafo, que não
a consumia, e o `buf` vazio explodia. Só aparecia em pt-BR porque os arquivos
traduzidos nasceram com LF. O mesmo caminho renderiza **PRD colado pelo
usuário**, que vem com CRLF o tempo todo no Windows — então o bug era bem mais
amplo que o manual. Corrigido no `renderMarkdown` (normaliza `\r\n?`→`\n` na
entrada + rede de segurança que avança o cursor se nenhum ramo consumir a
linha, evitando laço infinito) e guardado por `TestRenderMarkdownNaoQuebra`, que
roda o parser real contra 8 casos-limite e as 44 seções do manual — verificado
que o teste FALHA contra a versão anterior do parser.

## Riscos e pontos de atenção

1. **E2 é o grosso do trabalho** (~700–900 strings, 20 arquivos) e é mecânico —
   candidato ideal a rodar como demandas do próprio Praxis, uma por módulo JS,
   com o teste de paridade como gate.
2. **Qualidade das traduções zh-CN/es** — gerar por IA é barato; a revisão humana
   de zh-CN talvez precise de apoio externo. Publicar como "beta" é aceitável.
3. **Eventos antigos** permanecem no idioma em que foram gravados — documentar.
4. **Strings esquecidas** — o teste de paridade não pega literal não extraído;
   varredura final com regex de literais pt-BR (`[àáâãéêíóôõúç]`) nos JS/Go.
