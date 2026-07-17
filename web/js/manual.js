// Tela "Manual" (Fase 5b): renderiza as seções do manual embutido servidas por
// GET /api/v1/manual (lista) e /api/v1/manual/{slug} (conteúdo markdown).

import { api } from "./api.js";
import { el, limpar, bannerErro } from "./ui.js";

export async function montarManual() {
  const nav = limpar(document.getElementById("manual-nav"));
  const corpo = limpar(document.getElementById("manual-corpo"));
  corpo.append(el("p", { class: "sub", text: "Carregando…" }));

  let secoes;
  try {
    secoes = (await api.listarManual()) || [];
  } catch (e) {
    bannerErro("Falha ao carregar o manual: " + e.message);
    return;
  }
  bannerErro("");
  limpar(corpo);

  if (secoes.length === 0) {
    corpo.append(el("p", { class: "sub", text: "Manual indisponível." }));
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

// renderMarkdown é um renderizador mínimo: parágrafos, listas (ordenadas e não),
// **negrito** e `código`. Suficiente para o manual embutido (conteúdo confiável).
function renderMarkdown(md) {
  const nos = [];
  const linhas = md.split("\n");
  let i = 0;
  while (i < linhas.length) {
    const linha = linhas[i];
    if (linha.trim() === "") { i++; continue; }

    // lista não ordenada (- ) ou ordenada (1. ).
    const ordenada = /^\d+\.\s/.test(linha);
    const naoOrdenada = /^[-*]\s/.test(linha);
    if (ordenada || naoOrdenada) {
      const itens = [];
      while (i < linhas.length && (/^\d+\.\s/.test(linhas[i]) || /^[-*]\s/.test(linhas[i]))) {
        const texto = linhas[i].replace(/^(\d+\.|[-*])\s/, "");
        itens.push(el("li", {}, ...inline(texto)));
        i++;
      }
      nos.push(el(ordenada ? "ol" : "ul", {}, ...itens));
      continue;
    }

    // parágrafo (linhas consecutivas até uma em branco).
    const buf = [];
    while (i < linhas.length && linhas[i].trim() !== "" &&
           !/^\d+\.\s/.test(linhas[i]) && !/^[-*]\s/.test(linhas[i])) {
      buf.push(linhas[i]);
      i++;
    }
    nos.push(el("p", {}, ...inline(buf.join(" "))));
  }
  return nos;
}

// inline converte **negrito** e `código` de um trecho em nós DOM (o restante vira
// texto puro — sem innerHTML, evita injeção mesmo com conteúdo confiável).
function inline(texto) {
  const nos = [];
  const re = /(\*\*([^*]+)\*\*|`([^`]+)`)/g;
  let ultimo = 0, m;
  while ((m = re.exec(texto)) !== null) {
    if (m.index > ultimo) nos.push(document.createTextNode(texto.slice(ultimo, m.index)));
    if (m[2] !== undefined) nos.push(el("b", { text: m[2] }));
    else if (m[3] !== undefined) nos.push(el("code", { text: m[3] }));
    ultimo = m.index + m[0].length;
  }
  if (ultimo < texto.length) nos.push(document.createTextNode(texto.slice(ultimo)));
  return nos;
}
