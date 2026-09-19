// Caixa de entrada por usuário (M4 do PLANO_INTERNET): o sino com o contador
// de não lidas, o painel lateral com a lista, o toast quando uma notificação
// nova chega pelo SSE e — com a aba escondida e permissão concedida — a
// notificação do sistema operacional via service worker. Clicar numa
// notificação navega até o item (rota "#consultas/7" etc.) e a marca lida.

import { api } from "./api.js";
import { el, limpar } from "./ui.js";
import { t } from "./i18n.js";
import { quando } from "./demandas.js";

let stream = null;
let naoLidas = 0;
let ultimoID = 0;      // maior id conhecido: cursor do stream e dedupe nas reconexões
let itens = [];        // lista carregada no painel (mais recentes primeiro)
let painel = null;
let lista = null;
let prefs = { navegador: true };

// iniciarNotificacoes carrega o contador e a lista e abre o stream. Chamada
// ao entrar na app; idempotente (reinicia o stream se já havia um).
export async function iniciarNotificacoes() {
  pararNotificacoes();
  try {
    const p = await api.obterPreferencias();
    if (p) prefs = p;
  } catch { /* sem preferências: padrão (navegador ligado) */ }
  try {
    const r = await api.listarNotificacoes({ limite: 50 });
    itens = (r && r.itens) || [];
    naoLidas = (r && r.nao_lidas) || 0;
    ultimoID = itens.reduce((m, n) => Math.max(m, n.id), 0);
  } catch { /* offline no boot: o stream traz o que vier */ }
  atualizarBadge();
  if (painel && painel.classList.contains("aberto")) renderLista();
  stream = api.streamNotificacoes(ultimoID, {
    eventos: { notificacao: (ev) => receber(ev) },
  });
}

// pararNotificacoes fecha o stream (sair da app).
export function pararNotificacoes() {
  if (stream) { stream.close(); stream = null; }
}

// receber trata uma notificação nova do stream. As reconexões reenviam o que
// veio depois do cursor de abertura: ids já vistos são ignorados.
function receber(ev) {
  let n;
  try { n = JSON.parse(ev.data); } catch { return; }
  if (!n || !n.id || n.id <= ultimoID) return;
  ultimoID = n.id;
  itens.unshift(n);
  if (itens.length > 50) itens.length = 50;
  naoLidas++;
  atualizarBadge();
  if (painel && painel.classList.contains("aberto")) renderLista();
  if (prefs.navegador === false) return;
  if (document.hidden) notificarSistema(n);
  else toastNotificacao(n);
}

// toastNotificacao mostra o toast clicável (abre o item) por alguns segundos.
function toastNotificacao(n) {
  const box = el("div", { class: "toast ok clicavel", title: t("sino.abrir") },
    el("b", { text: n.titulo }),
    n.detalhe ? el("div", { class: "sub", text: n.detalhe }) : null);
  box.onclick = () => { box.remove(); abrirItem(n); };
  document.body.appendChild(box);
  setTimeout(() => box.remove(), 6000);
}

// notificarSistema pede ao service worker para mostrar a notificação do SO
// (a aba está em segundo plano). A tag agrupa as do mesmo item; o clique é
// tratado pelo SW (notificationclick), que foca a janela e manda a rota.
async function notificarSistema(n) {
  if (!("Notification" in window) || Notification.permission !== "granted" || !("serviceWorker" in navigator)) return;
  try {
    const reg = await navigator.serviceWorker.ready;
    await reg.showNotification(n.titulo, {
      body: n.detalhe || "",
      tag: n.rota || n.tipo,
      icon: "/icons/icon-192.png",
      badge: "/icons/icon-192.png",
      data: { rota: n.rota, id: n.id },
    });
  } catch { /* sem SW ativo: só o sino */ }
}

// abrirItem navega até o item da notificação e a marca lida.
export function abrirItem(n) {
  fecharPainel();
  if (!n.lida_em) marcarLida(n);
  if (n.rota) location.hash = n.rota;
}

async function marcarLida(n) {
  n.lida_em = new Date().toISOString();
  naoLidas = Math.max(0, naoLidas - 1);
  atualizarBadge();
  try { await api.marcarNotificacaoLida(n.id); } catch { /* best-effort */ }
}

// ---------- sino (botões) e badge ----------

// botaoSino devolve um botão-sino para a sidebar; o da topbar (index.html) é
// ligado por ligarSinoTopbar. Todos compartilham o mesmo badge.
export function botaoSino() {
  return el("button", { class: "btn ghost sm sino-btn", title: t("sino.titulo"), onclick: () => alternarPainel() },
    "🔔 ", el("span", { text: t("sino.titulo") }), el("span", { class: "sino-badge", hidden: true }));
}

export function ligarSinoTopbar() {
  const b = document.getElementById("btn-sino-topbar");
  if (b) b.onclick = () => alternarPainel();
}

function atualizarBadge() {
  document.querySelectorAll(".sino-badge").forEach((b) => {
    b.textContent = naoLidas > 99 ? "99+" : String(naoLidas);
    b.hidden = naoLidas === 0;
  });
  document.title = (naoLidas > 0 ? `(${naoLidas}) ` : "") + "Praxis Autonomous";
}

// ---------- painel ----------

function garantirPainel() {
  if (painel) return;
  lista = el("div", { class: "sino-lista" });
  const btnTodas = el("button", { class: "btn ghost sm", text: t("sino.marcar_todas") });
  btnTodas.onclick = async () => {
    btnTodas.disabled = true;
    try {
      await api.marcarTodasLidas();
      const agora = new Date().toISOString();
      for (const n of itens) if (!n.lida_em) n.lida_em = agora;
      naoLidas = 0;
      atualizarBadge();
      renderLista();
    } catch { /* best-effort */ } finally {
      btnTodas.disabled = false;
    }
  };
  painel = el("aside", { class: "sino-painel", role: "dialog", "aria-label": t("sino.titulo") },
    el("div", { class: "sino-cab" },
      el("h3", { text: t("sino.titulo") }),
      btnTodas,
      el("button", { class: "topbar-btn sino-fechar", title: t("sino.fechar"), text: "✕", onclick: () => fecharPainel() }),
    ),
    lista);
  document.body.append(painel);
  document.addEventListener("keydown", (e) => { if (e.key === "Escape") fecharPainel(); });
}

async function alternarPainel() {
  garantirPainel();
  if (painel.classList.contains("aberto")) { fecharPainel(); return; }
  painel.classList.add("aberto");
  renderLista();
  try {
    const r = await api.listarNotificacoes({ limite: 50 });
    itens = (r && r.itens) || [];
    naoLidas = (r && r.nao_lidas) || 0;
    ultimoID = Math.max(ultimoID, ...itens.map((n) => n.id));
    atualizarBadge();
    renderLista();
  } catch (e) {
    limpar(lista).append(el("p", { class: "sub", text: t("sino.falha_carregar", { erro: e.message }) }));
  }
}

function fecharPainel() {
  if (painel) painel.classList.remove("aberto");
}

function renderLista() {
  if (!lista) return;
  limpar(lista);
  if (itens.length === 0) {
    lista.append(el("p", { class: "sub", text: t("sino.nenhuma") }));
    return;
  }
  for (const n of itens) {
    lista.append(el("div", { class: "sino-item" + (n.lida_em ? "" : " nao-lida"), onclick: () => abrirItem(n) },
      el("b", { text: n.titulo }),
      n.detalhe ? el("div", { class: "path", text: n.detalhe }) : null,
      el("div", { class: "meta" }, el("span", { class: "pill", text: quando(n.criado_em) })),
    ));
  }
}
