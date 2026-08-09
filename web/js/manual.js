// Tela "Manual" (Fase 5b): renderiza as seções do manual embutido servidas por
// GET /api/v1/manual (lista) e /api/v1/manual/{slug} (conteúdo markdown).

import { api } from "./api.js";
import { t } from "./i18n.js";
import { el, limpar, bannerErro, renderMarkdown } from "./ui.js";

export async function montarManual() {
  const nav = limpar(document.getElementById("manual-nav"));
  const corpo = limpar(document.getElementById("manual-corpo"));
  corpo.append(el("p", { class: "sub", text: t("configx.carregando") }));

  let secoes;
  try {
    secoes = (await api.listarManual()) || [];
  } catch (e) {
    bannerErro(t("manualx.falha_carregar", { erro: e.message }));
    return;
  }
  bannerErro("");
  limpar(corpo);

  if (secoes.length === 0) {
    corpo.append(el("p", { class: "sub", text: t("manualx.indisponivel") }));
    return;
  }

  // Índice lateral (âncoras) + render de todas as seções em sequência.
  for (const sec of secoes) {
    nav.append(el("a", { class: "manual-toc", href: "#manual", text: sec.titulo,
      onclick: (e) => { e.preventDefault(); document.getElementById("sec-" + sec.slug)?.scrollIntoView({ behavior: "smooth" }); } }));
  }

  for (const sec of secoes) {
    let dados;
    try {
      dados = await api.secaoManual(sec.slug);
    } catch {
      continue;
    }
    const bloco = el("div", { class: "manual-sec", id: "sec-" + sec.slug });
    bloco.append(el("h2", { text: dados.titulo }));
    bloco.append(...renderMarkdown(dados.conteudo || ""));
    corpo.append(bloco);
  }
}
