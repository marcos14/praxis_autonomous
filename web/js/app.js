// Ponto de entrada do frontend. Faz a navegação entre as telas (shell) e
// dispara a montagem de cada view ao ser exibida. Sem framework: só ES modules.

import { montarDemandas } from "./demandas.js";
import { montarNovaDemanda } from "./nova.js";
import { montarProjetos } from "./projetos.js";
import { montarMotores } from "./motores.js";
import { montarConfig } from "./config.js";
import { montarKanban, desmontarKanban } from "./kanban.js";
import { bannerErro } from "./ui.js";

// views mapeia o nome da view à sua função de montagem (chamada a cada exibição,
// para refletir o estado atual do banco).
const views = {
  kanban: montarKanban,
  demandas: montarDemandas,
  nova: montarNovaDemanda,
  projetos: montarProjetos,
  motores: montarMotores,
  config: montarConfig,
};

// desmontar mapeia (opcionalmente) o nome da view à sua função de limpeza,
// chamada ao SAIR da view (ex.: fechar o SSE do kanban).
const desmontar = {
  kanban: desmontarKanban,
};

const nomesValidos = new Set(Object.keys(views));
let viewAtual = "";

// irPara ativa a view pedida: alterna as seções, destaca o item do menu, limpa o
// banner de erro e (re)monta o conteúdo. Views desconhecidas caem em "home".
async function irPara(nome) {
  if (!nomesValidos.has(nome)) nome = "kanban";
  if (viewAtual && viewAtual !== nome && desmontar[viewAtual]) {
    try { desmontar[viewAtual](); } catch { /* ignora falha de limpeza */ }
  }
  viewAtual = nome;
  document.querySelectorAll(".view").forEach((v) => v.classList.remove("active"));
  document.getElementById("view-" + nome).classList.add("active");
  document.querySelectorAll(".nav-item").forEach((n) =>
    n.classList.toggle("active", n.dataset.view === nome));
  if (location.hash.slice(1) !== nome) location.hash = nome;
  bannerErro("");
  window.scrollTo(0, 0);
  try {
    await views[nome]();
  } catch (e) {
    bannerErro("Erro ao montar a tela: " + (e && e.message ? e.message : e));
  }
}

// atualizarRodape mostra a versão e o estado do serviço/banco no rodapé do menu.
async function atualizarRodape() {
  const foot = document.getElementById("nav-foot");
  try {
    const resp = await fetch("/healthz");
    const h = await resp.json();
    const online = resp.ok && h.banco !== undefined ? h.banco === "ok" : resp.ok;
    const cor = online ? "var(--good)" : "var(--critical)";
    const rotulo = online ? "online" : "banco indisponível";
    foot.innerHTML = `v${h.versao || "dev"}<br><span style="color:${cor}">●</span> ${rotulo}`;
  } catch {
    foot.innerHTML = `<span style="color:var(--critical)">●</span> offline`;
  }
}

function iniciar() {
  document.querySelectorAll(".nav-item").forEach((btn) =>
    btn.addEventListener("click", () => irPara(btn.dataset.view)));
  window.addEventListener("hashchange", () => irPara(location.hash.slice(1)));
  atualizarRodape();
  irPara(location.hash.slice(1) || "kanban");
}

if (document.readyState === "loading") {
  document.addEventListener("DOMContentLoaded", iniciar);
} else {
  iniciar();
}
