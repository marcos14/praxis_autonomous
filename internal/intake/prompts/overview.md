Você vai produzir o **overview de negócio** do repositório do projeto "{PROJETO}", em
modo somente leitura. O texto será lido por pessoas de produto/suporte e injetado como
contexto de um chat de consulta — ele orienta um assistente (e os leitores) a se
localizarem no sistema sem conhecer o código.

Leia o README, CLAUDE.md/AGENTS.md (se existirem) e a estrutura do projeto, navegue o
suficiente para entender o domínio, e produza `overview_md` em markdown {IDIOMA}
(400–900 palavras) com EXATAMENTE estas seções:

1. **Objetivo** — o que o sistema faz e para quem (o problema de negócio que resolve).
2. **Domínio** — os conceitos/entidades de negócio centrais e como se relacionam.
3. **Principais módulos e rotinas** — as áreas funcionais e o que cada uma cobre.
   Nomes de módulos/áreas são permitidos; trechos de código NÃO.
4. **Fluxos importantes** — 3 a 6 fluxos de negócio de ponta a ponta (ex.: "da criação
   do pedido à entrega"), descrevendo gatilhos, etapas e desfechos.
5. **Integrações e limites** — sistemas externos com que conversa e o que fica FORA
   deste repositório (importante quando a solução tem vários repos).

Regras:
- NENHUM trecho de código, SQL, configuração ou bloco cercado (```) — apenas prosa e
  nomes de negócio.
- Escreva para um leitor leigo em programação; evite jargão técnico.
- Se o repositório tiver documentação desatualizada em conflito com o código, confie
  no código e mencione a divergência em uma linha.

Responda APENAS com o JSON estruturado solicitado.

> **Idioma da saída:** escreva o overview inteiramente em **{IDIOMA}**. Nomes de módulos, pastas e símbolos do código permanecem como estão no repositório.
