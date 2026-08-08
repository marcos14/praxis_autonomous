# 10. Planejamentos (PRD e ADRs com o estrategista)

A tela **Planejamentos** é o espaço de PMs, POs e arquitetos: você descreve uma necessidade e o **estrategista** — lendo o código dos repositórios em modo somente leitura — lapida com você um **PRD** (visão de negócio), **ADRs** (decisões arquiteturais) ou ambos, em conversa iterativa. O resultado é um documento fundamentado no que o sistema realmente é hoje, pronto para virar demanda com um clique.

## Para que serve

- **PRD completo antes da demanda**: em vez de colar um PRD cru na Nova demanda, chegue lá com requisitos, critérios de aceite e questões já decididas — menos rodadas de perguntas do analista, plano melhor.
- **ADRs para decisões de arquitetura**: registrar contexto, decisão, alternativas e consequências — com o estrategista confirmando no código as restrições reais.
- **Visualizar o plano**: além do texto, o estrategista gera uma **apresentação visual** (infográficos, fluxogramas) e, se você pedir, um **protótipo navegável** das telas propostas — para alinhar a visão antes de gastar desenvolvimento.

## Como usar

1. Vá em `Planejamentos → Novo planejamento`, escolha um **projeto** ou um **grupo**, o **foco** (PRD, ADRs ou ambos) e o **nível visual**:
   - **Documento** — só os `.md`;
   - **Apresentação** (padrão) — + um HTML com resumo executivo, infográficos e fluxogramas;
   - **Protótipo** — + uma simulação navegável das telas/fluxos propostos.
2. Descreva a necessidade do seu jeito. Se algo estiver ambíguo, o estrategista faz **perguntas de decisão** antes de escrever — responda no chat.
3. A cada turno, os documentos aparecem na aba **Documentos** (com histórico de revisões) e os artefatos na aba **Artefatos** (abrem em nova aba). Peça mudanças no chat até o documento ficar redondo.
4. Você pode **mudar o foco e o nível visual no meio da conversa** — o próximo turno já obedece.
5. Quando o PRD estiver pronto, clique **Criar demanda**: o documento vira a primeira mensagem de uma demanda nova (com os ADRs anexados, quando houver) e o analista do intake assume dali. O planejamento fica vinculado à demanda criada.

## Documentos e artefatos

- Os `.md` são a **fonte da verdade** e ficam versionados no banco — cada turno que altera um documento grava uma revisão nova, e você pode reler qualquer revisão antiga.
- Os artefatos `.html` são camada de apresentação, sempre regenerados a partir dos documentos. São **arquivos autocontidos** (funcionam offline, sem CDN) gravados em `PRAXIS_HOME/planejamentos/p<id>/`.
- Ao abrir um artefato, ele roda numa **sandbox do navegador**: o JavaScript do artefato não tem acesso à sua sessão do Praxis nem à API — é só apresentação.

## Motor e modelo

Os planejamentos usam a mesma resolução de motor das consultas (campo **Modelo para consultas** do motor; grupo de usuários do criador pode fixar motor/modelo próprios). Como é trabalho de análise e escrita — não de código de produção — um modelo mais econômico costuma bastar.

## Segurança

- O acesso é controlado pela permissão `planejamentos.usar`. **Atenção**: diferente das Consultas, aqui NÃO há filtro anti-código — ADRs citam componentes, caminhos e tecnologias por definição. Conceda a permissão a quem pode ver detalhes internos da arquitetura.
- O estrategista **nunca modifica os repositórios**: ele roda com escrita confinada à pasta do planejamento, os repositórios entram como somente leitura e, como rede de segurança final, o Praxis compara o `git status` de cada repositório antes e depois do turno — qualquer alteração detectada falha o turno na hora e lista os arquivos para você conferir (nada é revertido automaticamente).
- Os planejamentos respeitam o **acesso a projetos** (ACL), como as consultas: projeto restrito não aparece para quem não foi liberado.
- Excluir um planejamento remove a conversa, os documentos, os artefatos e a pasta de trabalho.
