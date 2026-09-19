// Tela "Home" (Fase 4b): tiles de gasto/ativas/fases/integradas/franquia,
// gráfico de gastos por dia, tabela por projeto, lista "Precisa de você" e
// atividade recente (backlog + SSE global ao vivo).

import { api } from "./api.js";
import { el, limpar, bannerErro } from "./ui.js";
import { abrirCard, setProjetos, dinheiro, quando, STATUS } from "./demandas.js";
import { t } from "./i18n.js";

let projetos = [];
let sseEventos = null;

export async function montarHome() {
  try {
    projetos = (await api.listarProjetos()) || [];
  } catch {
    projetos = [];
  }
  setProjetos(projetos);
  await Promise.all([carregarMetricas(), carregarPendencias(), carregarAtividade()]);
  assinarAtividade();
}

export function desmontarHome() {
  if (sseEventos) { sseEventos.close(); sseEventos = null; }
}

function nomeProjeto(id) {
  const p = projetos.find((x) => x.id === id);
  return p ? p.nome : t("comum.projeto_n", { id });
}

function tile(rot, val, sub2) {
  return el("div", { class: "tile" },
    el("div", { class: "rot", text: rot }),
    el("div", { class: "val", text: val }),
    sub2 ? el("div", { class: "sub2", text: sub2 }) : null,
  );
}

async function carregarMetricas() {
  let m;
  try {
    m = await api.metricas();
  } catch (e) {
    bannerErro(t("home.falha_metricas", { erro: e.message }));
    return;
  }
  const tiles = limpar(document.getElementById("home-tiles"));
  tiles.append(
    tile(t("home.gasto_mes"), dinheiro(m.gasto_mes)),
    tile(t("home.demandas_ativas"), String(m.demandas_ativas)),
    tile(t("home.fases_7d"), String(m.fases_concluidas_7d)),
    tile(t("home.integradas_mes"), String(m.integradas_mes)),
    tile(t("home.aguardando_franquia"), String(m.aguardando_franquia)),
  );
  renderGrafico(m.gastos_por_dia || []);
  renderPorProjeto(m.por_projeto || []);
}

function renderGrafico(dias) {
  const cont = limpar(document.getElementById("home-grafico"));
  cont.append(el("h3", { text: t("home.gastos_dia") }));
  if (dias.length === 0) {
    cont.append(el("p", { class: "sub", style: "margin:8px 0 0", text: t("home.sem_gastos") }));
    return;
  }
  const max = Math.max(...dias.map((d) => d.custo_usd), 0.0001);
  const g = el("div", { class: "grafico-barras" });
  for (const d of dias) {
    const h = Math.round((d.custo_usd / max) * 100);
    g.append(el("div", { class: "gb-col", title: `${d.dia}: ${dinheiro(d.custo_usd)}` },
      el("div", { class: "gb-bar", style: `height:${h}%` }),
      el("div", { class: "gb-dia", text: d.dia.slice(8) }),
    ));
  }
  cont.append(g);
}

function renderPorProjeto(linhas) {
  const cont = limpar(document.getElementById("home-projetos"));
  cont.append(el("h3", { text: t("home.por_projeto") }));
  if (linhas.length === 0) {
    cont.append(el("p", { class: "sub", style: "margin:8px 0 0", text: t("home.nenhum_projeto") }));
    return;
  }
  const tbl = el("table", { class: "plain" },
    el("thead", {}, el("tr", {},
      el("th", { text: t("home.col_projeto") }), el("th", { text: t("home.col_ativas") }), el("th", { text: t("home.col_custo") }))));
  const tb = el("tbody");
  for (const l of linhas) {
    tb.append(el("tr", {},
      el("td", { text: nomeProjeto(l.project_id) }),
      el("td", { text: String(l.demandas_ativas) }),
      el("td", { text: dinheiro(l.custo_usd) }),
    ));
  }
  tbl.append(tb);
  cont.append(tbl);
}

async function carregarPendencias() {
  const cont = limpar(document.getElementById("home-precisa"));
  cont.append(el("h3", { text: t("home.precisa") }));
  let pend;
  try {
    pend = (await api.pendencias()) || [];
  } catch (e) {
    cont.append(el("p", { class: "sub", text: t("comum.falha_carregar", { erro: e.message }) }));
    return;
  }
  if (pend.length === 0) {
    cont.append(el("p", { class: "sub", style: "margin:8px 0 0", text: t("home.nada_pendente") }));
    return;
  }
  for (const d of pend) {
    const [rotulo] = STATUS[d.status] || [d.status];
    cont.append(el("div", { class: "precisa-item", onclick: () => abrirCard(d.id) },
      el("div", { class: "pt", text: `#${d.id} — ${d.titulo}` }),
      el("div", { class: "pd", text: `${nomeProjeto(d.project_id)} · ${rotulo}` }),
    ));
  }
}

async function carregarAtividade() {
  const cont = limpar(document.getElementById("home-atividade"));
  cont.append(el("h3", { text: t("home.atividade") }));
  const lista = el("div", { id: "home-ativ-lista" });
  cont.append(lista);
  let evs;
  try {
    evs = (await api.atividade(20)) || [];
  } catch (e) {
    lista.append(el("p", { class: "sub", text: t("comum.falha_carregar", { erro: e.message }) }));
    return;
  }
  if (evs.length === 0) {
    lista.append(el("p", { class: "sub", style: "margin:8px 0 0", text: t("home.sem_atividade") }));
    return;
  }
  for (const ev of evs) lista.append(itemAtividade(ev));
}

function itemAtividade(ev) {
  return el("div", { class: "ativ-item" },
    el("span", { class: "at", text: ev.titulo || ev.tipo }),
    el("span", { class: "aq", text: " · " + quando(ev.criado_em) }),
  );
}

// assinarAtividade abre o SSE global e prepende novos eventos à atividade recente.
function assinarAtividade() {
  desmontarHome();
  // O stream renova o token e reabre sozinho (api.abrirStream).
  sseEventos = api.streamEventos({
    eventos: {
      evento: (e) => {
        let ev;
        try { ev = JSON.parse(e.data); } catch { return; }
        const lista = document.getElementById("home-ativ-lista");
        if (!lista) return;
        const vazio = lista.querySelector("p.sub");
        if (vazio) vazio.remove();
        lista.prepend(itemAtividade(ev));
        while (lista.children.length > 30) lista.lastChild.remove();
        // Um evento pode mudar métricas/pendências — recarrega em segundo plano.
        carregarMetricas();
        carregarPendencias();
      },
    },
  });
}
