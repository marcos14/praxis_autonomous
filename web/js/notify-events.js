// Catálogo dos eventos notificáveis e dos canais de notificação externa.
// Compartilhado pela tela de Configurações (padrão do sistema) e pela tela de
// Projetos (override por projeto). Reflete os tipos de evento que o backend
// registra na tabela `events` e que o despachante (internal/notify) envia.
//
// Semântica do backend (notify.EventoLigado): um evento sem entrada explícita no
// mapa cai no default LIGADO — para não silenciar eventos novos. Por isso a UI
// grava um valor explícito (true/false) para cada evento do catálogo.

// GRUPOS_EVENTOS organiza os eventos por área, na ordem de exibição. Cada evento
// tem { tipo, rotulo, padrao } — `padrao` é o valor sugerido para o padrão do
// sistema (marcado ao abrir a tela pela primeira vez, sem config salva).
export const GRUPOS_EVENTOS = [
  {
    grupo: "Demandas",
    itens: [
      { tipo: "demanda_criada", rotulo: "Demanda criada", padrao: true },
      { tipo: "demanda_pausada", rotulo: "Demanda pausada", padrao: true },
      { tipo: "demanda_retomada", rotulo: "Demanda retomada", padrao: false },
      { tipo: "demanda_cancelada", rotulo: "Demanda cancelada", padrao: true },
      { tipo: "demanda_integrada", rotulo: "Demanda integrada (merge concluído)", padrao: true },
      { tipo: "aguardando_humano", rotulo: "Aguardando intervenção humana", padrao: true },
    ],
  },
  {
    grupo: "Fases & execução",
    itens: [
      { tipo: "fase_iniciada", rotulo: "Fase iniciada", padrao: false },
      { tipo: "fase_concluida", rotulo: "Fase concluída", padrao: false },
      { tipo: "fase_falhou", rotulo: "Fase falhou", padrao: true },
      { tipo: "fase_nova", rotulo: "Fase nova descoberta", padrao: false },
      { tipo: "fase_pausada", rotulo: "Fase pausada", padrao: false },
      { tipo: "gates_falharam", rotulo: "Gates de validação falharam", padrao: true },
      { tipo: "revisor_reprovou", rotulo: "Revisor reprovou", padrao: false },
      { tipo: "correcao_iniciada", rotulo: "Correção iniciada", padrao: false },
      { tipo: "troca_de_harness", rotulo: "Troca de motor (fallback)", padrao: false },
      { tipo: "franquia_esgotada", rotulo: "Franquia do motor esgotada", padrao: true },
      { tipo: "worktree_criado", rotulo: "Worktree criado", padrao: false },
    ],
  },
  {
    grupo: "Planejamento & intake",
    itens: [
      { tipo: "analise_iniciada", rotulo: "Análise iniciada", padrao: false },
      { tipo: "analise_concluida", rotulo: "Análise concluída", padrao: false },
      { tipo: "planejamento_iniciado", rotulo: "Planejamento iniciado", padrao: false },
      { tipo: "planejamento_concluido", rotulo: "Planejamento concluído", padrao: false },
      { tipo: "planejamento_falhou", rotulo: "Planejamento falhou", padrao: true },
      { tipo: "analise_falhou", rotulo: "Análise falhou", padrao: true },
      { tipo: "plano_aprovado", rotulo: "Plano aprovado", padrao: true },
      { tipo: "plano_rejeitado", rotulo: "Plano rejeitado", padrao: false },
      { tipo: "respostas_recebidas", rotulo: "Respostas recebidas", padrao: false },
    ],
  },
  {
    grupo: "Git & integração",
    itens: [
      { tipo: "branch_publicada", rotulo: "Branch publicada", padrao: false },
      { tipo: "branch_atualizada", rotulo: "Branch atualizada", padrao: false },
      { tipo: "push_falhou", rotulo: "Push falhou", padrao: true },
      { tipo: "merge_falhou", rotulo: "Merge falhou", padrao: true },
    ],
  },
  {
    grupo: "Consultas & sistema",
    itens: [
      { tipo: "consulta_respondida", rotulo: "Consulta respondida", padrao: false },
      { tipo: "consulta_falhou", rotulo: "Consulta falhou", padrao: false },
      { tipo: "estrategia_respondida", rotulo: "Planejamento (estrategista) respondido", padrao: false },
      { tipo: "estrategia_falhou", rotulo: "Planejamento (estrategista) falhou", padrao: false },
      { tipo: "overview_gerado", rotulo: "Overview gerado", padrao: false },
      { tipo: "overview_falhou", rotulo: "Overview falhou", padrao: false },
      { tipo: "projeto_criado", rotulo: "Projeto criado", padrao: false },
      { tipo: "config_alterada", rotulo: "Configuração alterada", padrao: false },
      { tipo: "recuperada_pos_restart", rotulo: "Demanda recuperada após reinício", padrao: false },
      { tipo: "codigo_acessado", rotulo: "Código aberto no IDE web", padrao: false },
      { tipo: "aviso", rotulo: "Aviso do sistema", padrao: false },
    ],
  },
];

// TODOS_EVENTOS é a lista achatada dos eventos do catálogo, na ordem de exibição.
export const TODOS_EVENTOS = GRUPOS_EVENTOS.flatMap((g) => g.itens);

// CANAIS descreve os canais de notificação e os campos que cada um usa. `campos`
// mapeia a chave do campo em notify.Canal → metadados do input.
export const CANAIS = [
  {
    chave: "telegram",
    rotulo: "Telegram",
    hint: "Crie um bot no @BotFather para obter o token; o chat_id é o destino (use @userinfobot).",
    campos: [
      { chave: "token", rotulo: "Token do bot", tipo: "password", placeholder: "123456:ABC-DEF…" },
      { chave: "chat_id", rotulo: "Chat ID", tipo: "text", placeholder: "-1001234567890" },
    ],
  },
  {
    chave: "discord",
    rotulo: "Discord",
    hint: "URL do webhook do canal (Configurações do canal → Integrações → Webhooks).",
    campos: [
      { chave: "webhook_url", rotulo: "Webhook URL", tipo: "password", placeholder: "https://discord.com/api/webhooks/…" },
    ],
  },
  {
    chave: "slack",
    rotulo: "Slack",
    hint: "URL de um Incoming Webhook do workspace.",
    campos: [
      { chave: "webhook_url", rotulo: "Webhook URL", tipo: "password", placeholder: "https://hooks.slack.com/services/…" },
    ],
  },
  {
    chave: "google_chat",
    rotulo: "Google Chat",
    hint: "URL de webhook do espaço (Gerenciar webhooks).",
    campos: [
      { chave: "webhook_url", rotulo: "Webhook URL", tipo: "password", placeholder: "https://chat.googleapis.com/v1/spaces/…" },
    ],
  },
  {
    chave: "webhook",
    rotulo: "Webhook genérico",
    hint: "POST com corpo JSON {titulo, texto}. Header opcional no formato \"Nome: valor\" (ex.: autenticação).",
    campos: [
      { chave: "url", rotulo: "URL", tipo: "text", placeholder: "https://sistema.empresa.com/hook" },
      { chave: "header", rotulo: "Header (opcional)", tipo: "text", placeholder: "Authorization: Bearer …" },
    ],
  },
];

// mapaPadrao devolve um mapa { tipo: padrao } com o padrão sugerido do catálogo.
export function mapaPadrao() {
  const m = {};
  for (const ev of TODOS_EVENTOS) m[ev.tipo] = ev.padrao;
  return m;
}

// resolverEventos combina o mapa salvo (parcial) com o catálogo: usa o valor
// salvo quando existe; senão, o padrão do catálogo. Devolve { tipo: bool } com
// todas as chaves do catálogo. `base` é o mapa que preenche os ausentes (útil no
// override por projeto, que herda o padrão do sistema).
export function resolverEventos(salvo, base) {
  const padroes = base || mapaPadrao();
  const m = {};
  for (const ev of TODOS_EVENTOS) {
    m[ev.tipo] = salvo && Object.prototype.hasOwnProperty.call(salvo, ev.tipo)
      ? !!salvo[ev.tipo]
      : !!padroes[ev.tipo];
  }
  return m;
}
