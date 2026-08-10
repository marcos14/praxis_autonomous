🇺🇸 [English](README.md) · 🇧🇷 **Português (Brasil)**

# Praxis Autonomous

**O conhecimento e as regras de negócio do seu sistema — servidos para o time
inteiro, sem distribuir o código-fonte.**

O Praxis fica em cima dos seus repositórios git e responde o que suporte, produto,
implantação e squads parceiras precisam saber — em linguagem de negócio, sem nunca
expor o código-fonte e sem distribuir acesso ao repositório. E quando o
entendimento vira trabalho, ele leva a demanda de ponta a ponta: PRD lapidado com
IA, plano em fases, execução autônoma, merge request.

*Go puro · um binário · SQLite embutido · interface 100% web*

*Disponível em português, English, español e 简体中文.*

---

## O problema — respostas trancadas no código, acesso distribuído sem necessidade

Quem mais precisa entender o código raramente é quem o escreveu:

- O **suporte** precisa saber o que acontece quando o boleto vence para responder
  o cliente — e abre chamado para a squad.
- A **implantação** precisa da estratégia de configuração para um cliente novo —
  e depende da agenda de um desenvolvedor.
- O **produto** precisa saber como a cobrança funciona *hoje* antes de escrever o
  PRD — e acaba escrevendo sem saber.
- O **dev de outra squad** precisa das regras de negócio para fazer uma integração,
  criar um mock ou prototipar um cenário — e vai ler um código que nunca viu.

As empresas resolvem isso do pior jeito: interrompendo os desenvolvedores donos do
código — ou dando acesso ao código-fonte para qualquer um que talvez precise de uma
resposta, até meia empresa poder clonar os repositórios. O conhecimento continua
tribal, e o menor privilégio vai pelo ralo.

O Praxis remove os dois problemas de uma vez: ele **lê o código de verdade**, em
modo somente leitura, e devolve respostas, documentos, protótipos — e até o
desenvolvimento pronto para merge — enquanto o código-fonte fica exatamente onde
deve ficar.

---

## Consultas — respostas sobre regras de negócio, sem expor o código-fonte

![Tela de Consultas: pergunta sobre o sistema respondida em linguagem de negócio](docs/media/praxis_consultas.png)

Pergunte como o sistema se comporta — *"o que acontece quando o boleto vence?"*,
*"consigo criar tools com base em APIs externas?"* — e o consultor responde lendo o
código, em linguagem de negócio, **sem nunca mostrar código-fonte** (um pós-filtro
no servidor garante). O código vira uma base de conhecimento self-service para os
times ao redor: o suporte responde o cliente com certeza, o produto elabora PRDs
sobre o comportamento real, a integração mapeia as regras de negócio de um sistema
legado antes de codificar.

> **Caso real — N2/N3 do suporte:** integrando a API do Praxis à ferramenta de
> chamados, o chamado que o N1 não resolve com o óbvio vira uma consulta — o
> Praxis aprofunda no código e resolve cerca de 90% desses casos, deixando as
> equipes avançadas focadas nos problemas realmente complexos.

A permissão é própria (`consultas.usar`): dá para criar um papel "Suporte" ou
"Produto" sem nenhum acesso a demandas, código ou configurações.

---

## Planejamentos — da necessidade ao PRD, com protótipo navegável

![Planejamento gerando PRD, ADRs, apresentação executiva e protótipo navegável](docs/media/praxis_planejamento.gif)

Descreva uma necessidade e o **estrategista** — lendo o código dos repositórios —
lapida com você um **PRD** e/ou **ADRs** em conversa iterativa. Além dos documentos,
ele gera uma **apresentação visual** (infográficos, fluxogramas) e, se você pedir,
um **protótipo navegável** das telas propostas — tudo autocontido, pronto para
apresentar ou anexar num e-mail.

O PO fecha a visão de negócio, o arquiteto continua **no mesmo planejamento** com
as decisões técnicas e, quando o conjunto estiver redondo, o PRD **vira demanda com
um clique**.

---

## Demandas — desenvolvimento autônomo de ponta a ponta

![Fluxo da demanda: PRD, perguntas do analista, plano em fases e execução](docs/media/praxis_demanda.gif)

Cole um PRD ou a descrição de um chamado. O **analista** lê o código e faz perguntas
objetivas (com sugestões prontas — responder é clicar), o **planejador** monta o
plano em fases e, aprovado o plano, a demanda **executa sozinha** em background:
worktree isolado, branch dedicada, ciclo executor → gates → corretor → revisor,
commit por fase e branch publicada para o Merge Request. Você acompanha pelo Kanban,
log ao vivo, diff e custo por fase — e pode pausar, retomar ou cancelar a qualquer
momento.

> Os três (e únicos) momentos que exigem um humano: **responder as perguntas**,
> **aprovar o plano** e **abrir o MR / integrar**.

---

## Por que usar

- **Multi-projeto e multi-motor** — orquestra `claude`, `codex` ou `opencode`, com
  modelo por tarefa e budget por demanda.
- **IA de ponta no custo da assinatura** — os motores são harnesses de IA que rodam
  nos planos que você já assina: custo fixo mensal em vez de conta de API por
  token. E se a franquia de um plano esgotar, o Praxis muda automaticamente para o
  próximo motor — o usuário nunca fica sem resposta.
- **Nada para instalar em quem usa** — toda a operação é pelo navegador, inclusive
  um **VS Code web** para ajustes manuais no worktree de uma demanda.
- **Código protegido** — execução em worktree isolado, push só de branches
  `praxis/*`, a main nunca é tocada; consultas respondem sem dar acesso ao
  repositório — menor privilégio por padrão.
- **Custo visível** — gasto por demanda, por projeto e por mês direto na Home.
- **Encaixa no seu fluxo** — API REST com tokens e papéis: abra demandas ou
  dispare consultas direto do seu sistema de chamados, além de notificações via
  Telegram, Discord, Slack e Google Chat.
- **Operação simples** — um binário Go com SQLite embutido: sem Docker, sem banco
  externo, sem dependências de runtime.
- **Fala a língua do seu time** — a interface, o manual embutido e as próprias
  respostas da IA saem em português, inglês, espanhol ou chinês, escolhidos por
  usuário: a mesma consulta respondida em português para o suporte no Brasil e
  em chinês para o time em Shenzhen.

---

## Como executar

Requisitos: **Go 1.26+**, **git** no PATH e um harness de IA autenticado
(`claude`, `codex` ou `opencode`).

**Linux/macOS**

```sh
go build -o praxis ./cmd/praxis
./praxis serve
```

**Windows**

```powershell
go build -o praxis.exe .\cmd\praxis
.\praxis.exe serve
```

Abra **http://127.0.0.1:7799**, cadastre um projeto (a pasta de um repositório git)
e crie a primeira consulta ou demanda. O menu **Manual**, dentro da própria web,
guia o fluxo completo.

Para deixar no ar sem um terminal aberto, instale como serviço do sistema — um
comando nas duas plataformas (com `sudo` no Linux, num shell de Administrador no
Windows):

```sh
praxis service install     # depois: praxis service status | stop | remove
```

Para acessar de outras máquinas sem ter um certificado próprio, suba com
`-tls` — o Praxis gera um certificado HTTPS autoassinado:

```sh
./praxis serve -addr 0.0.0.0:7799 -tls
```

Saiba mais no [guia completo](README_COMPLETO.pt-BR.md#acesso-pela-rede-https):
certificado próprio, instalação do certificado nos dispositivos e boas práticas.

---

## Documentação completa

O guia detalhado está em **[README_COMPLETO.pt-BR.md](README_COMPLETO.pt-BR.md)**:
acesso pela rede com TLS, execução como serviço (systemd / Windows), API REST,
notificações, parâmetros de configuração, segurança e solução de problemas.
