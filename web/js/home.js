// Tela "Home" (Fase 4b): tiles de gasto/ativas/fases/integradas/franquia,
// gráfico de gastos por dia, tabela por projeto, lista "Precisa de você" e
// atividade recente (backlog + SSE global ao vivo).

import { api } from "./api.js";
import { el, limpar, bannerErro } from "./ui.js";
import { abrirCard, setProjetos, dinheiro, quando, STATUS } from "./demandas.js";

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
  return p ? p.nome : "projeto " + id;
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
    bannerErro("Falha ao carregar métricas: " + e.message);
    return;
  }
  const tiles = limpar(document.getElementById("home-tiles"));
  tiles.append(
    tile("Gasto no mês", dinheiro(m.gasto_mes)),
    tile("Demandas ativas", String(m.demandas_ativas)),
    tile("Fases concluídas (7d)", String(m.fases_concluidas_7d)),
    tile("Integradas no mês", String(m.integradas_mes)),
    tile("Aguardando franquia", String(m.aguardando_franquia)),
  );
  renderGrafico(m.gastos_por_dia || []);
  renderPorProjeto(m.por_projeto || []);
}

function renderGrafico(dias) {
  const cont = limpar(document.getElementById("home-grafico"));
  cont.append(el("h3", { text: "Gastos por dia" }));
  if (dias.length === 0) {
    cont.append(el("p", { class: "sub", style: "margin:8px 0 0", text: "Sem gastos registrados na janela." }));
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
  cont.append(el("h3", { text: "Por projeto" }));
  if (linhas.length === 0) {
    cont.append(el("p", { class: "sub", style: "margin:8px 0 0", text: "Nenhum projeto com demandas ainda." }));
    return;
  }
  const tbl = el("table", { class: "plain" },
    el("thead", {}, el("tr", {},
      el("th", { text: "Projeto" }), el("th", { text: "Ativas" }), el("th", { text: "Custo" }))));
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
  cont.append(el("h3", { text: "Precisa de você" }));
  let pend;
  try {
    pend = (await api.pendencias()) || [];
  } catch (e) {
    cont.append(el("p", { class: "sub", text: "Falha ao carregar: " + e.message }));
    return;
  }
  if (pend.length === 0) {
    cont.append(el("p", { class: "sub", style: "margin:8px 0 0", text: "Nada pendente. 🎉" }));
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
  cont.append(el("h3", { text: "Atividade recente" }));
  const lista = el("div", { id: "home-ativ-lista" });
  cont.append(lista);
  let evs;
  try {
    evs = (await api.atividade(20)) || [];
  } catch (e) {
    lista.append(el("p", { class: "sub", text: "Falha ao carregar: " + e.message }));
    return;
  }
  if (evs.length === 0) {
    lista.append(el("p", { class: "sub", style: "margin:8px 0 0", text: "Sem atividade ainda." }));
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
  sseEventos = new EventSource(api.urlEventos());
  sseEventos.addEventListener("evento", (e) => {
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
  });
  sseEventos.onerror = () => {};
}
