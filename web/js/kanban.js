// Tela "Kanban" (Fase 4a): quadro de demandas por status, em tempo real (SSE
// global de eventos). Colunas = status; filtros por projeto e motor; card mostra
// projeto, fase atual/progresso, motor, custo e alerta quando precisa do usuário.
// Transições de estado só por botão (no card); arrastar só reordena a prioridade.

import { api } from "./api.js";
import { el, limpar, bannerErro } from "./ui.js";
import { abrirCard, setProjetos, pillStatus, dinheiro, STATUS } from "./demandas.js";
import { t, tn } from "./i18n.js";

// COLUNAS é a ordem das colunas do quadro (subconjunto/ordem dos status).
const COLUNAS = [
  "recebida", "analisando", "aguardando_respostas", "planejando", "aguardando_aprovacao",
  "pronta", "executando", "aguardando_franquia", "conflito", "concluida", "integrada",
  "pausada", "falhou", "cancelada",
];

// STATUS_ALERTA marca os status que exigem atenção humana (borda/realce do card).
const STATUS_ALERTA = new Set(["aguardando_respostas", "aguardando_aprovacao", "conflito"]);

let projetos = [];
let filtro = { project: "", motor: "" };
let sseEventos = null; // EventSource global (fechado ao sair da tela)
let recarregarTimer = null;

export async function montarKanban() {
  try {
    projetos = (await api.listarProjetos()) || [];
  } catch {
    projetos = [];
  }
  setProjetos(projetos);
  renderFiltros();
  await recarregar();
  assinarEventos();
}

// desmontarKanban é chamada pelo router ao sair da tela para fechar o SSE.
export function desmontarKanban() {
  if (sseEventos) { sseEventos.close(); sseEventos = null; }
  if (recarregarTimer) { clearTimeout(recarregarTimer); recarregarTimer = null; }
}

function renderFiltros() {
  const cont = limpar(document.getElementById("filtros-kanban"));
  const selProj = el("select", { onchange: (e) => { filtro.project = e.target.value; recarregar(); } },
    el("option", { value: "", text: t("comum.todos_projetos") }));
  for (const p of projetos) {
    const o = el("option", { value: String(p.id), text: p.nome });
    if (String(p.id) === filtro.project) o.selected = true;
    selProj.append(o);
  }
  // O filtro de motor é preenchido a partir dos motores presentes no board.
  const selMotor = el("select", { id: "filtro-motor", onchange: (e) => { filtro.motor = e.target.value; renderColunas(cacheBoard); } },
    el("option", { value: "", text: t("kanban.todos_motores") }));
  cont.append(selProj, selMotor);
}

let cacheBoard = [];
let cacheOverlaps = {}; // demandID (string) → sobreposições[]

async function recarregar() {
  let board;
  try {
    board = (await api.board({ project: filtro.project })) || [];
  } catch (e) {
    bannerErro(t("kanban.falha_quadro", { erro: e.message }));
    return;
  }
  bannerErro("");
  cacheBoard = board;
  // sobreposições (Fase 5c): best-effort; um erro não impede o board.
  try {
    cacheOverlaps = (await api.overlaps()) || {};
  } catch {
    cacheOverlaps = {};
  }
  atualizarFiltroMotor(board);
  renderColunas(board);
}

// atualizarFiltroMotor repovoa as opções do filtro de motor com os motores
// efetivamente presentes no board, preservando a seleção atual quando possível.
function atualizarFiltroMotor(board) {
  const sel = document.getElementById("filtro-motor");
  if (!sel) return;
  const motores = [...new Set(board.map((d) => d.motor).filter(Boolean))].sort();
  const atual = filtro.motor;
  limpar(sel).append(el("option", { value: "", text: t("kanban.todos_motores") }));
  for (const m of motores) {
    const o = el("option", { value: m, text: m });
    if (m === atual) o.selected = true;
    sel.append(o);
  }
  if (atual && !motores.includes(atual)) filtro.motor = "";
}

function renderColunas(board) {
  const wrap = limpar(document.getElementById("kanban-board"));
  const visiveis = board.filter((d) => !filtro.motor || d.motor === filtro.motor);
  const porStatus = {};
  for (const d of visiveis) (porStatus[d.status] ||= []).push(d);

  // Só renderiza colunas que existem em COLUNAS; um status desconhecido cai numa
  // coluna "outros" ao final.
  const colunasComDados = COLUNAS.filter((s) => porStatus[s]);
  const conhecidos = new Set(COLUNAS);
  const outros = visiveis.filter((d) => !conhecidos.has(d.status));

  if (visiveis.length === 0) {
    wrap.append(el("p", { class: "sub", text: t("kanban.nenhuma_demanda") }));
    return;
  }

  for (const status of colunasComDados) {
    wrap.append(colunaEl(status, porStatus[status]));
  }
  if (outros.length) wrap.append(colunaEl("outros", outros));
}

function colunaEl(status, demandas) {
  const [rotulo] = STATUS[status] || [status === "outros" ? t("status.outros") : status];
  const col = el("div", { class: "kb-col" },
    el("div", { class: "kb-col-head" },
      el("span", { class: "kb-col-titulo", text: rotulo }),
      el("span", { class: "kb-col-count", text: String(demandas.length) }),
    ),
  );
  const lista = el("div", { class: "kb-col-lista", "data-status": status });
  // Arraste para reordenar prioridade DENTRO da coluna.
  lista.addEventListener("dragover", (e) => { e.preventDefault(); });
  lista.addEventListener("drop", (e) => { e.preventDefault(); onDrop(e, lista); });
  for (const d of demandas) lista.append(cardEl(d));
  col.append(lista);
  return col;
}

function cardEl(d) {
  const proj = projetos.find((p) => p.id === d.project_id);
  const alerta = STATUS_ALERTA.has(d.status);
  const card = el("div", {
    class: "kb-card" + (alerta ? " alerta" : ""),
    draggable: "true",
    "data-id": String(d.id),
    onclick: () => abrirCard(d.id),
  });
  card.addEventListener("dragstart", (e) => {
    e.dataTransfer.setData("text/plain", String(d.id));
    card.classList.add("arrastando");
  });
  card.addEventListener("dragend", () => card.classList.remove("arrastando"));

  card.append(el("div", { class: "proj", text: proj ? proj.nome : t("comum.projeto_n", { id: d.project_id }) }));
  card.append(el("div", { class: "title", text: `#${d.id} — ${d.titulo}` }));

  if (d.fases_total > 0) {
    const frac = d.fases_concluidas / d.fases_total;
    card.append(el("div", { class: "kb-prog" },
      el("div", { class: "kb-prog-bar", style: `width:${Math.round(frac * 100)}%` })));
    card.append(el("div", { class: "kb-prog-txt", text: t("kanban.fases", { feitas: d.fases_concluidas, total: d.fases_total }) }));
  }

  const meta = el("div", { class: "meta" }, pillStatus(d.status));
  if (d.motor) meta.append(el("span", { class: "pill", text: d.motor }));
  meta.append(el("span", { class: "pill", text: dinheiro(d.custo_usd) }));
  if (alerta) meta.append(el("span", { class: "pill alerta-pill", text: t("kanban.precisa") }));
  const sobre = cacheOverlaps[String(d.id)];
  if (sobre && sobre.length) {
    const arqs = [...new Set(sobre.flatMap((s) => s.arquivos))];
    meta.append(el("span", { class: "pill overlap-pill", title: t("kanban.arquivos_comum", { arquivos: arqs.join(", ") }),
      text: tn("kanban.sobrepoe", sobre.length) }));
  }
  card.append(meta);
  return card;
}

// onDrop recalcula a nova ordem dos cards da coluna e persiste a prioridade.
async function onDrop(e, lista) {
  const id = e.dataTransfer.getData("text/plain");
  const arrastado = lista.parentElement.querySelector(`.kb-card[data-id="${id}"]`) ||
    document.querySelector(`.kb-card[data-id="${id}"]`);
  if (!arrastado) return;

  // Descobre a posição de soltura pelo card mais próximo do cursor.
  const cards = [...lista.querySelectorAll(".kb-card:not(.arrastando)")];
  let antes = null;
  for (const c of cards) {
    const r = c.getBoundingClientRect();
    if (e.clientY < r.top + r.height / 2) { antes = c; break; }
  }
  if (antes) lista.insertBefore(arrastado, antes);
  else lista.append(arrastado);

  const ids = [...lista.querySelectorAll(".kb-card")].map((c) => Number(c.dataset.id));
  try {
    await api.reordenarDemandas(ids);
  } catch (err) {
    bannerErro(t("kanban.falha_reordenar", { erro: err.message }));
    recarregar();
  }
}

// assinarEventos abre o SSE global e reagenda um recarregamento do board a cada
// evento relevante (mudança de status, conclusão, integração, conflito…).
function assinarEventos() {
  desmontarKanban();
  // O stream renova o token e reabre sozinho (api.abrirStream).
  sseEventos = api.streamEventos({
    eventos: {
      evento: () => {
        // Debounce: agrupa rajadas de eventos num único refresh.
        if (recarregarTimer) clearTimeout(recarregarTimer);
        recarregarTimer = setTimeout(() => { recarregarTimer = null; recarregar(); }, 300);
      },
    },
    // Ao reconectar, o board pode ter perdido eventos: recarrega.
    onopen: () => recarregar(),
  });
}
