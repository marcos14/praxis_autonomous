// Catálogo dos eventos notificáveis e dos canais de notificação externa.
// Compartilhado pela tela de Configurações (padrão do sistema) e pela tela de
// Projetos (override por projeto). Reflete os tipos de evento que o backend
// registra na tabela `events` e que o despachante (internal/notify) envia.
//
// Semântica do backend (notify.EventoLigado): um evento sem entrada explícita no
// mapa cai no default LIGADO — para não silenciar eventos novos. Por isso a UI
// grava um valor explícito (true/false) para cada evento do catálogo.

import { t } from "./i18n.js";

// GRUPOS_EVENTOS organiza os eventos por área, na ordem de exibição. Cada evento
// tem { tipo, rotulo, padrao } — `padrao` é o valor sugerido para o padrão do
// sistema (marcado ao abrir a tela pela primeira vez, sem config salva).
export const GRUPOS_EVENTOS = [
  {
    grupo: t("notif.grupo_demandas"),
    itens: [
      { tipo: "demanda_criada", rotulo: t("notif.ev_demanda_criada"), padrao: true },
      { tipo: "demanda_pausada", rotulo: t("notif.ev_demanda_pausada"), padrao: true },
      { tipo: "demanda_retomada", rotulo: t("notif.ev_demanda_retomada"), padrao: false },
      { tipo: "demanda_cancelada", rotulo: t("notif.ev_demanda_cancelada"), padrao: true },
      { tipo: "demanda_integrada", rotulo: t("notif.ev_demanda_integrada"), padrao: true },
      { tipo: "aguardando_humano", rotulo: t("notif.ev_aguardando_humano"), padrao: true },
    ],
  },
  {
    grupo: t("notif.grupo_fases"),
    itens: [
      { tipo: "fase_iniciada", rotulo: t("notif.ev_fase_iniciada"), padrao: false },
      { tipo: "fase_concluida", rotulo: t("notif.ev_fase_concluida"), padrao: false },
      { tipo: "fase_falhou", rotulo: t("notif.ev_fase_falhou"), padrao: true },
      { tipo: "fase_nova", rotulo: t("notif.ev_fase_nova"), padrao: false },
      { tipo: "fase_pausada", rotulo: t("notif.ev_fase_pausada"), padrao: false },
      { tipo: "gates_falharam", rotulo: t("notif.ev_gates_falharam"), padrao: true },
      { tipo: "revisor_reprovou", rotulo: t("notif.ev_revisor_reprovou"), padrao: false },
      { tipo: "correcao_iniciada", rotulo: t("notif.ev_correcao_iniciada"), padrao: false },
      { tipo: "troca_de_harness", rotulo: t("notif.ev_troca_de_harness"), padrao: false },
      { tipo: "franquia_esgotada", rotulo: t("notif.ev_franquia_esgotada"), padrao: true },
      { tipo: "worktree_criado", rotulo: t("notif.ev_worktree_criado"), padrao: false },
    ],
  },
  {
    grupo: t("notif.grupo_planejamento"),
    itens: [
      { tipo: "analise_iniciada", rotulo: t("notif.ev_analise_iniciada"), padrao: false },
      { tipo: "analise_concluida", rotulo: t("notif.ev_analise_concluida"), padrao: false },
      { tipo: "planejamento_iniciado", rotulo: t("notif.ev_planejamento_iniciado"), padrao: false },
      { tipo: "planejamento_concluido", rotulo: t("notif.ev_planejamento_concluido"), padrao: false },
      { tipo: "planejamento_falhou", rotulo: t("notif.ev_planejamento_falhou"), padrao: true },
      { tipo: "analise_falhou", rotulo: t("notif.ev_analise_falhou"), padrao: true },
      { tipo: "plano_aprovado", rotulo: t("notif.ev_plano_aprovado"), padrao: true },
      { tipo: "plano_rejeitado", rotulo: t("notif.ev_plano_rejeitado"), padrao: false },
      { tipo: "respostas_recebidas", rotulo: t("notif.ev_respostas_recebidas"), padrao: false },
    ],
  },
  {
    grupo: t("notif.grupo_git"),
    itens: [
      { tipo: "branch_publicada", rotulo: t("notif.ev_branch_publicada"), padrao: false },
      { tipo: "branch_atualizada", rotulo: t("notif.ev_branch_atualizada"), padrao: false },
      { tipo: "push_falhou", rotulo: t("notif.ev_push_falhou"), padrao: true },
      { tipo: "merge_falhou", rotulo: t("notif.ev_merge_falhou"), padrao: true },
    ],
  },
  {
    grupo: t("notif.grupo_consultas"),
    itens: [
      { tipo: "consulta_respondida", rotulo: t("notif.ev_consulta_respondida"), padrao: false },
      { tipo: "consulta_falhou", rotulo: t("notif.ev_consulta_falhou"), padrao: false },
      { tipo: "estrategia_respondida", rotulo: t("notif.ev_estrategia_respondida"), padrao: false },
      { tipo: "estrategia_falhou", rotulo: t("notif.ev_estrategia_falhou"), padrao: false },
      { tipo: "overview_gerado", rotulo: t("notif.ev_overview_gerado"), padrao: false },
      { tipo: "overview_falhou", rotulo: t("notif.ev_overview_falhou"), padrao: false },
      { tipo: "projeto_criado", rotulo: t("notif.ev_projeto_criado"), padrao: false },
      { tipo: "config_alterada", rotulo: t("notif.ev_config_alterada"), padrao: false },
      { tipo: "recuperada_pos_restart", rotulo: t("notif.ev_recuperada_pos_restart"), padrao: false },
      { tipo: "codigo_acessado", rotulo: t("notif.ev_codigo_acessado"), padrao: false },
      { tipo: "aviso", rotulo: t("notif.ev_aviso"), padrao: false },
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
    hint: t("notif.hint_telegram"),
    campos: [
      { chave: "token", rotulo: t("notif.campo_token_bot"), tipo: "password", placeholder: "123456:ABC-DEF…" },
      { chave: "chat_id", rotulo: t("notif.campo_chat_id"), tipo: "text", placeholder: "-1001234567890" },
    ],
  },
  {
    chave: "discord",
    rotulo: "Discord",
    hint: t("notif.hint_discord"),
    campos: [
      { chave: "webhook_url", rotulo: t("notif.campo_webhook_url"), tipo: "password", placeholder: "https://discord.com/api/webhooks/…" },
    ],
  },
  {
    chave: "slack",
    rotulo: "Slack",
    hint: t("notif.hint_slack"),
    campos: [
      { chave: "webhook_url", rotulo: t("notif.campo_webhook_url"), tipo: "password", placeholder: "https://hooks.slack.com/services/…" },
    ],
  },
  {
    chave: "google_chat",
    rotulo: "Google Chat",
    hint: t("notif.hint_google_chat"),
    campos: [
      { chave: "webhook_url", rotulo: t("notif.campo_webhook_url"), tipo: "password", placeholder: "https://chat.googleapis.com/v1/spaces/…" },
    ],
  },
  {
    chave: "webhook",
    rotulo: t("notif.canal_webhook"),
    hint: t("notif.hint_webhook"),
    campos: [
      { chave: "url", rotulo: t("notif.campo_url"), tipo: "text", placeholder: "https://sistema.empresa.com/hook" },
      { chave: "header", rotulo: t("notif.campo_header"), tipo: "text", placeholder: "Authorization: Bearer …" },
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
