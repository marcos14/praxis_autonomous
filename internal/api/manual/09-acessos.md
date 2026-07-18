# 9. Usuários, papéis e acesso a projetos

## Usuários e papéis

- O primeiro acesso ao Praxis pede a criação do **primeiro administrador**; a partir daí todo acesso exige login.
- Em `Usuários` você cadastra as pessoas e atribui **papéis**. Cada papel é um conjunto de permissões (criar demandas, responder, operar, integrar, gerir projetos, consultas etc.) — monte papéis como "Desenvolvedor", "Produto" ou "Suporte" com só o necessário.
- Quem não tem nenhum papel ainda consegue **visualizar** (acompanhar andamento e métricas); as ações é que são bloqueadas.

## Grupos de usuários

Em `Grupos de usuários` você agrupa pessoas (cada usuário pertence a no máximo um grupo, definido no cadastro do usuário). O grupo serve para duas coisas:

1. **Consultas**: fixar o motor/modelo das consultas dos membros (ex.: grupo "Suporte" com um modelo econômico) — ver a seção Consultas.
2. **Acesso a projetos**: liberar projetos restritos para todos os membros de uma vez, como descrito abaixo.

## Acesso a projetos (quem enxerga o quê)

Por padrão **todo projeto é visível a todos os usuários autenticados**. Quando mais pessoas da empresa passam a usar o Praxis, você pode restringir projeto a projeto:

1. Abra o projeto em `Projetos` e vá à seção **Acesso — quem enxerga este projeto**.
2. Selecione os **grupos de usuários** e/ou **usuários** liberados e salve.
   - Nada selecionado = projeto aberto a todos (o padrão).
   - Com seleção, só os liberados enxergam o projeto — além dos **administradores** e de quem tem a permissão **Projetos** (`projetos.gerir`), que sempre veem tudo. Tokens de API (integrações) também não são filtrados.

A restrição vale para o produto inteiro, não só para a lista de projetos: demandas (kanban, cards, chat, logs, diff), pendências e métricas da Home, atividade recente e eventos ao vivo, e as Consultas do projeto. Para quem não foi liberado, é como se o projeto não existisse.

Notas:

- Um **grupo de repositórios** (tela `Grupos`, usada nas Consultas) só aparece para quem enxerga **todos** os projetos dele — um único projeto restrito esconde o grupo inteiro.
- Se todos os usuários/grupos de uma restrição forem excluídos do sistema, o projeto volta a ficar aberto a todos.
- A seção Acesso só aparece/salva para quem tem a permissão **Projetos** (`projetos.gerir`).
