// Tela "Configurações globais" — edita config_entries do escopo global (Fase
// 1e). PUT é full replace: recompomos o mapa completo a cada salvar, incluindo
// as chaves desconhecidas preservadas.

import { api } from "./api.js";
import { el, limpar, toast, bannerErro } from "./ui.js";
import { camposDoEscopo, jsonParaTexto, textoParaJSON, preservarDesconhecidas } from "./config-fields.js";

let desconhecidas = {}; // chaves fora da whitelist, preservadas no save

export async function montarConfig() {
  const painel = document.getElementById("painel-config");
  limpar(painel);
  painel.append(el("p", { class: "sub", style: "margin:0", text: "Carregando…" }));

  let cfg;
  try {
    cfg = await api.obterConfigGlobal();
  } catch (e) {
    bannerErro("Falha ao carregar config global: " + e.message);
    return;
  }
  bannerErro("");
  desconhecidas = preservarDesconhecidas(cfg);

  limpar(painel);
  const form = el("div", { class: "form" });
  const campos = camposDoEscopo("global");
  const inputs = new Map();

  for (const c of campos) {
    const valorTexto = jsonParaTexto(cfg[c.chave], c.tipo);
    const entrada = c.tipo === "lines"
      ? el("textarea", { id: `g-${c.chave}` }, valorTexto)
      : el("input", { id: `g-${c.chave}`, type: c.tipo === "number" ? "number" : "text",
                      step: c.tipo === "number" ? "any" : null, value: valorTexto });
    inputs.set(c.chave, entrada);
    form.append(el("div", {},
      el("label", {}, c.rotulo),
      entrada,
      c.hint ? el("div", { class: "hint", text: c.hint }) : null,
    ));
  }

  const btn = el("button", { class: "btn", style: "width:fit-content", onclick: () => salvar(inputs, campos, btn) }, "Salvar");
  form.append(el("div", { class: "hint", text: "Chaves em branco não são gravadas (a config efetiva de cada projeto cai no default do sistema)." }));
  form.append(btn);
  painel.append(form);
}

async function salvar(inputs, campos, btn) {
  const entradas = { ...desconhecidas };
  for (const c of campos) {
    const texto = inputs.get(c.chave).value;
    // Campo vazio → chave ausente (não grava). Para number, exige valor válido.
    if (c.tipo === "number") {
      if (texto.trim() === "") continue;
      const r = textoParaJSON(texto, c.tipo);
      if (!r.ok) { bannerErro(`"${c.rotulo}" deve ser um número.`); return; }
      entradas[c.chave] = r.valor;
      continue;
    }
    if (c.tipo === "lines") {
      const r = textoParaJSON(texto, c.tipo);
      if (r.valor.length > 0) entradas[c.chave] = r.valor;
      continue;
    }
    if (texto.trim() !== "") entradas[c.chave] = texto.trim();
  }

  bannerErro("");
  btn.disabled = true;
  try {
    await api.definirConfigGlobal(entradas);
    toast("Configuração global salva.", "ok");
    await montarConfig();
  } catch (e) {
    bannerErro("Falha ao salvar: " + e.message);
    toast("Falha ao salvar.", "err");
  } finally {
    btn.disabled = false;
  }
}
