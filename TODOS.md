# Ideias e backlog de motores

Atualizado em: 2026-07-23

Este arquivo mantém o que foi deliberadamente deixado fora do primeiro incremento de login e múltiplos perfis Claude/Codex.

## Uso e franquia (`/usage`)

- [x] Criar uma visão de uso local do Praxis por motor e perfil a partir de `runs` + `consulta_runs` (execuções, custo e tokens em hoje/7 dias/total). *(Entregue: painel "Uso e franquia" na tela Motores + `GET /engines/uso`; recortes por período livre e por projeto ficam para depois.)*
- [ ] Persistir `engine_id` e `engine_account_id` em cada execução para atribuição precisa. *(Hoje a atribuição é por nome do motor + alias vigente — `runs.engine`/`runs.conta`.)*
- [x] Integrar a franquia estruturada do Codex quando oferecida pelo `app-server` (`account/rateLimits/read`; versão sem o contrato aparece como indisponível com mensagem). *(Verificação periódica pelo monitor — config `uso_intervalo_min`, default 5 min; renovação/última leitura expostas por janela via `reset_em`/`verificado_em`.)*
- [x] Para Claude, mostrar uso local e marcar franquia do vendor como indisponível enquanto não existir API headless estável; não automatizar scraping da tela interativa `/usage`.
- [ ] Para OpenCode, separar `opencode stats` (uso local) de limites de cada provider.
- [x] Sempre informar fonte, horário da coleta e estado indisponível com mensagem; ausência de dado nunca aparece como zero (franquia sem leitura fica "aguardando verificação"/mensagem, não 0%).

## Instalação assistida dos motores

- [ ] Criar catálogo versionado por vendor e sistema operacional com comandos fixos e allowlist — sem terminal web genérico.
- [ ] Detectar sistema, arquitetura, versão instalada e caminho absoluto do executável.
- [ ] Executar instalações como job assíncrono do mesmo usuário do serviço, preferencialmente sem elevação.
- [ ] Tratar instalação que exige administrador como instrução pendente, sem elevar silenciosamente.
- [ ] Verificar versão, caminho e integridade depois da instalação e armazenar o executável resolvido para não depender de atualização do `PATH`.
- [ ] Cobrir Windows, Linux e macOS; começar por Claude e Codex e adicionar OpenCode depois.

## OpenCode

- [ ] Implementar diagnóstico/login orientado por provider.
- [ ] Definir isolamento real de credenciais. `OPENCODE_CONFIG`/`OPENCODE_CONFIG_DIR` isolam configuração, mas a autenticação também usa o diretório de dados; não anunciar múltiplas contas até validar um contrato suportado.
- [ ] Adaptar a tela para selecionar provider e método de autenticação quando necessário.

## Agendamento, uso de quota e recuperação

- [x] Ao detectar quota esgotada, tentar outro perfil saudável do mesmo motor antes de usar outro vendor. *(Entregue: o pipeline esgota os perfis ativos do motor — na ordem da afinidade — antes de trocar de motor; ver PLANO.md.)*
- [ ] Persistir reset de quota e impedir novas tentativas até a janela reabrir. *(Hoje o "esgotado" vale só dentro da fase em execução; entre fases o perfil é tentado de novo.)*
- [ ] Adicionar limites de concorrência por perfil/conta.
- [ ] Definir política explícita de seleção (round-robin, afinidade por demanda ou menor uso).
- [ ] Evitar agrupamento de contas para contornar limites contratuais; priorizar chaves organizacionais, service accounts e planos empresariais quando disponíveis.

## Operação e observabilidade

- [ ] Dashboard de saúde: instalado, versão, caminho, autenticação, perfil ativo e última verificação.
- [ ] Auditoria de criação/remoção de perfil, início/cancelamento/conclusão de login e alterações de ativação.
- [ ] Alertas de sessão expirada, login requerido e incompatibilidade de versão do CLI.
- [ ] Jobs de limpeza para sessões temporárias encerradas e diretórios de perfis removidos com confirmação explícita.
