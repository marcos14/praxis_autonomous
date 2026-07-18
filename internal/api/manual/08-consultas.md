# 8. Consultas (produto e suporte)

A tela **Consultas** é um chat para quem NÃO desenvolve: times de produto e suporte perguntam como o sistema se comporta e o Praxis responde lendo o código em modo somente leitura — em linguagem de negócio, **sem nunca mostrar código-fonte**.

## Para que serve

- **Elaborar PRDs**: "como funciona a cobrança hoje?" — o consultor descreve o comportamento atual e aponta as lacunas que o PRD precisa decidir.
- **Entender uma rotina**: "o que acontece quando o boleto vence?" — passo a passo, condições e exceções.
- **Estratégia para um cliente**: "como implantar o Vulcano Chat para um cliente com duas filiais?" — o consultor propõe o passo a passo usando as rotinas existentes.

## Como usar

1. Vá em `Consultas → Nova consulta`, escolha um **projeto** ou um **grupo** (solução com vários repositórios) e escreva a pergunta do seu jeito — não precisa ser técnico.
2. Se a pergunta estiver ambígua, o consultor devolve **perguntas de clarificação** antes de responder. Responda no próprio chat.
3. Cada turno leva alguns minutos (o consultor lê o código de verdade). A linha de progresso mostra o que ele está investigando.

## Grupos de repositórios

Soluções com mais de um repositório (ex.: API + frontend) são cadastradas na tela `Grupos`. Numa consulta de grupo o consultor enxerga todos os repositórios; o primeiro do grupo é o principal. Um projeto pode participar de vários grupos.

## Overview do repositório

Cada projeto pode ter um **overview** (objetivo, domínio, fluxos — em `Projetos → Overview do repositório`), que orienta o consultor e melhora muito as respostas. Pode ser escrito à mão ou gerado pelo próprio Praxis ("Gerar com o Praxis").

## Motor e modelo das consultas

As consultas não usam o modelo de análise/planejamento: o motor tem um campo próprio **Modelo para consultas** (em `Motores`), que pode ser um modelo mais leve/barato — a consulta só explica comportamento, não produz código de produção. A precedência é:

1. **Grupo de usuários** de quem criou a consulta (tela `Grupos de usuários`): pode fixar motor e/ou modelo próprios — ex.: grupo "Suporte" com um modelo econômico.
2. **Modelo para consultas** do primeiro motor ativo.
3. **Modelo de análise** do motor, quando o campo de consultas está vazio.

O vínculo usuário↔grupo é feito no cadastro do usuário (um grupo por usuário).

## Código sempre atualizado

Antes de cada turno de consulta (e de cada análise/planejamento de demanda), o Praxis posiciona o repositório do projeto na **branch principal** e faz um **pull fast-forward** do origin — importante quando o serviço roda num servidor, onde o clone poderia ficar defasado. A execução de demandas já partia de `origin/<branch>` atualizada (via fetch + worktree isolado).

Nada é forçado: se o repositório tiver mudanças locais, estiver em outra branch com trabalho pendente ou tiver divergido do origin, o Praxis **não descarta nada** — segue com o estado disponível e registra um aviso (fala de sistema na consulta; evento na demanda).

## Segurança

- A resposta **nunca inclui código-fonte**: um pós-filtro no servidor remove qualquer trecho técnico que escape (aparece como "trecho técnico removido pela política de segurança"). Respostas técnicas demais são recusadas por inteiro.
- Pedidos de **burlar/contornar** validações ou explorar falhas são recusados. Explicar como uma validação funciona é permitido; ensinar a contorná-la, não.
- O acesso é controlado pela permissão `consultas.usar` — dá para criar um papel "Suporte"/"Produto" só com ela, sem nenhum acesso a demandas ou configurações.
- Nota técnica: o harness roda em modo somente leitura no repositório (sem editar, commitar ou push); comandos de leitura do repositório continuam disponíveis a ele — o mesmo modelo de confiança do analista de demandas.
