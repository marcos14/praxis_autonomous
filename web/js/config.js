// Tela "Configurações globais" — edita config_entries do escopo global (Fase
// 1e). PUT é full replace: recompomos o mapa completo a cada salvar, incluindo
// as chaves desconhecidas preservadas.

import { api } from "./api.js";
import { el, limpar, toast, bannerErro } from "./ui.js";
import { temPermissao } from "./auth.js";
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

  await montarTokens();
}

// ---------- Tokens de API (Fase 5a) ----------

const PAPEIS_TOKEN = ["leitor", "operador", "admin"];

async function montarTokens() {
  const painel = document.getElementById("painel-tokens");
  if (!painel) return;
  // Gestão de tokens de API é parte de "usuários e acessos": só quem tem
  // usuarios.gerir vê essa seção (o servidor também barra por permissão). Esconde
  // o painel e seus dois títulos (h2 + p) para os demais.
  const podeGerir = temPermissao("usuarios.gerir");
  painel.hidden = !podeGerir;
  let ant = painel.previousElementSibling, escondidos = 0;
  while (ant && escondidos < 2) { ant.hidden = !podeGerir; ant = ant.previousElementSibling; escondidos++; }
  if (!podeGerir) return;
  limpar(painel);

  const inpNome = el("input", { type: "text", placeholder: "nome (ex.: sistema de chamados)" });
  const selPapel = el("select", {}, ...PAPEIS_TOKEN.map((p) => el("option", { value: p, text: p })));
  selPapel.value = "operador";
  const btnNovo = el("button", { class: "btn sm", text: "Gerar token" });
  const form = el("div", { class: "token-form" }, inpNome, selPapel, btnNovo);
  const lista = el("div", { id: "lista-tokens", style: "margin-top:14px" });
  painel.append(form, lista);

  btnNovo.addEventListener("click", async () => {
    const nome = inpNome.value.trim();
    if (!nome) { bannerErro("Informe um nome para o token."); return; }
    btnNovo.disabled = true;
    try {
      const tok = await api.criarToken(nome, selPapel.value);
      inpNome.value = "";
      painel.querySelector(".token-novo")?.remove();
      painel.insertBefore(el("div", { class: "banner banner-ok token-novo" },
        el("div", { text: "Token criado — copie agora, não será mostrado de novo:" }),
        el("code", { class: "token-valor", text: tok.token }),
      ), lista);
      await recarregarTokens();
    } catch (e) {
      bannerErro("Falha ao criar token: " + e.message);
    } finally {
      btnNovo.disabled = false;
    }
  });

  await recarregarTokens();
}

async function recarregarTokens() {
  const lista = document.getElementById("lista-tokens");
  if (!lista) return;
  limpar(lista);
  let tokens;
  try {
    tokens = (await api.listarTokens()) || [];
  } catch (e) {
    lista.append(el("p", { class: "sub", text: "Falha ao listar tokens: " + e.message }));
    return;
  }
  if (tokens.length === 0) {
    lista.append(el("p", { class: "sub", style: "margin:0", text: "Nenhum token criado." }));
    return;
  }
  for (const t of tokens) {
    const revogado = !!t.revogado_em;
    const row = el("div", { class: "token-row" + (revogado ? " revogado" : "") },
      el("div", {},
        el("span", { class: "token-nome", text: t.nome }),
        el("span", { class: "pill", style: "margin-left:8px", text: t.papel }),
        revogado ? el("span", { class: "pill", style: "margin-left:6px;color:var(--muted)", text: "revogado" }) : null,
      ),
      revogado ? null : el("button", { class: "btn sm danger", text: "Revogar",
        onclick: () => revogar(t.id) }),
    );
    lista.append(row);
  }
}

async function revogar(id) {
  try {
    await api.revogarToken(id);
    await recarregarTokens();
  } catch (e) {
    bannerErro("Falha ao revogar: " + e.message);
  }
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
