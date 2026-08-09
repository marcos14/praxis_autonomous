// Tela "Configurações globais" — edita config_entries do escopo global (Fase
// 1e). PUT é full replace: recompomos o mapa completo a cada salvar, incluindo
// as chaves desconhecidas preservadas.

import { api } from "./api.js";
import { el, limpar, toast, bannerErro } from "./ui.js";
import { temPermissao } from "./auth.js";
import { camposDoEscopo, jsonParaTexto, textoParaJSON, preservarDesconhecidas } from "./config-fields.js";
import { GRUPOS_EVENTOS, CANAIS, resolverEventos } from "./notify-events.js";
import { t } from "./i18n.js";

let desconhecidas = {}; // chaves fora da whitelist, preservadas no save

export async function montarConfig() {
  const painel = document.getElementById("painel-config");
  limpar(painel);
  painel.append(el("p", { class: "sub", style: "margin:0", text: t("configx.carregando") }));

  let cfg;
  try {
    cfg = await api.obterConfigGlobal();
  } catch (e) {
    bannerErro(t("configx.falha_carregar", { erro: e.message }));
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

  const btn = el("button", { class: "btn", style: "width:fit-content", onclick: () => salvar(inputs, campos, btn) }, t("configx.salvar"));
  form.append(el("div", { class: "hint", text: t("configx.hint_branco") }));
  form.append(btn);
  painel.append(form);

  await montarNotificacoes();
  await montarTokens();
}

// ---------- Notificações externas ----------
//
// A config de notificações vive na chave global "notificacoes" (config_entries),
// um JSON com { canais, eventos, cabecalho } lido pelo despachante do backend
// (internal/notify). Aqui editamos os canais/tokens e os eventos notificados por
// padrão. O save é read-modify-write: relê a config global completa e troca só a
// chave "notificacoes", preservando as demais (parâmetros e chaves desconhecidas).

async function montarNotificacoes() {
  const painel = document.getElementById("painel-notificacoes");
  if (!painel) return;
  limpar(painel);

  let cfg;
  try {
    cfg = await api.obterConfigGlobal();
  } catch (e) {
    painel.append(el("p", { class: "sub", text: t("configx.falha_notif", { erro: e.message }) }));
    return;
  }
  const notif = cfg["notificacoes"] || {};
  const canaisSalvos = notif.canais || {};
  const eventos = resolverEventos(notif.eventos);

  const form = el("div", { class: "form" });

  const inpCab = el("input", { type: "text", value: notif.cabecalho || "", placeholder: t("configx.ph_cabecalho") });
  form.append(el("div", {},
    el("label", {}, t("configx.cabecalho") + " ", el("span", { class: "opt" }, t("configx.opcional"))),
    inpCab,
    el("div", { class: "hint", text: t("configx.hint_cabecalho") })));

  // Canais.
  const canalCtl = new Map();
  form.append(el("h3", { class: "notif-sub" }, t("configx.canais")));
  for (const canal of CANAIS) {
    const salvo = canaisSalvos[canal.chave] || {};
    const chkAtivo = el("input", { type: "checkbox" });
    chkAtivo.checked = !!salvo.ativo;
    const inputs = new Map();
    const campoNodes = [];
    for (const campo of canal.campos) {
      const inp = el("input", {
        type: campo.tipo === "password" ? "password" : "text",
        value: salvo[campo.chave] || "", placeholder: campo.placeholder || "", autocomplete: "off",
      });
      inputs.set(campo.chave, inp);
      campoNodes.push(el("div", {}, el("label", {}, campo.rotulo), inp));
    }
    const corpo = el("div", { class: "notif-canal-corpo" },
      ...campoNodes, canal.hint ? el("div", { class: "hint", text: canal.hint }) : null);
    const card = el("div", { class: "notif-canal" },
      el("label", { class: "notif-canal-head" }, chkAtivo, el("b", { text: canal.rotulo })),
      corpo);
    const sync = () => { corpo.hidden = !chkAtivo.checked; };
    chkAtivo.addEventListener("change", sync);
    sync();
    canalCtl.set(canal.chave, { chkAtivo, inputs });
    form.append(card);
  }

  // Eventos notificados por padrão.
  form.append(el("h3", { class: "notif-sub" }, t("configx.eventos_padrao")));
  form.append(el("div", { class: "hint", style: "margin-top:-6px", text: t("configx.hint_eventos") }));
  const evtCtl = new Map();
  for (const g of GRUPOS_EVENTOS) {
    const grid = el("div", { class: "notif-eventos" });
    for (const ev of g.itens) {
      const chk = el("input", { type: "checkbox" });
      chk.checked = eventos[ev.tipo];
      evtCtl.set(ev.tipo, chk);
      grid.append(el("label", { class: "notif-evento" }, chk, ev.rotulo));
    }
    form.append(el("div", { class: "notif-grupo" }, el("div", { class: "notif-grupo-tit", text: g.grupo }), grid));
  }

  const btn = el("button", { class: "btn", style: "width:fit-content" }, t("configx.salvar_notif"));
  btn.onclick = () => salvarNotificacoes(canalCtl, evtCtl, inpCab, btn);
  form.append(btn);
  painel.append(form);
}

async function salvarNotificacoes(canalCtl, evtCtl, inpCab, btn) {
  const canais = {};
  for (const [chave, ctl] of canalCtl) {
    const c = { ativo: ctl.chkAtivo.checked };
    for (const [k, inp] of ctl.inputs) {
      const v = inp.value.trim();
      if (v) c[k] = v;
    }
    canais[chave] = c;
  }
  const eventos = {};
  for (const [tipo, chk] of evtCtl) eventos[tipo] = chk.checked;
  const notificacoes = { canais, eventos };
  const cab = inpCab.value.trim();
  if (cab) notificacoes.cabecalho = cab;

  bannerErro("");
  btn.disabled = true;
  try {
    // read-modify-write: preserva as demais chaves da config global.
    const atual = await api.obterConfigGlobal();
    atual["notificacoes"] = notificacoes;
    await api.definirConfigGlobal(atual);
    toast(t("configx.notif_salvas"), "ok");
    await montarNotificacoes();
  } catch (e) {
    bannerErro(t("configx.falha_salvar_notif", { erro: e.message }));
    toast(t("configx.falha_salvar_toast"), "err");
  } finally {
    btn.disabled = false;
  }
}

// ---------- Tokens de API (Fase 5a) ----------

// PAPEIS_TOKEN são os VALORES enviados à API; o rótulo exibido é traduzido
// por rotuloPapel (configx.papel_*).
const PAPEIS_TOKEN = ["leitor", "operador", "admin"];

// rotuloPapel traduz o papel de um token para exibição; papel desconhecido
// aparece como veio da API.
function rotuloPapel(papel) {
  const chave = "configx.papel_" + papel;
  const rotulo = t(chave);
  return rotulo === chave ? papel : rotulo;
}

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

  const inpNome = el("input", { type: "text", placeholder: t("configx.ph_token_nome") });
  const selPapel = el("select", {}, ...PAPEIS_TOKEN.map((p) => el("option", { value: p, text: rotuloPapel(p) })));
  selPapel.value = "operador";
  const btnNovo = el("button", { class: "btn sm", text: t("configx.gerar_token") });
  const form = el("div", { class: "token-form" }, inpNome, selPapel, btnNovo);
  const lista = el("div", { id: "lista-tokens", style: "margin-top:14px" });
  painel.append(form, lista);

  btnNovo.addEventListener("click", async () => {
    const nome = inpNome.value.trim();
    if (!nome) { bannerErro(t("configx.informe_nome")); return; }
    btnNovo.disabled = true;
    try {
      const tok = await api.criarToken(nome, selPapel.value);
      inpNome.value = "";
      painel.querySelector(".token-novo")?.remove();
      painel.insertBefore(el("div", { class: "banner banner-ok token-novo" },
        el("div", { text: t("configx.token_criado") }),
        el("code", { class: "token-valor", text: tok.token }),
      ), lista);
      await recarregarTokens();
    } catch (e) {
      bannerErro(t("configx.falha_criar_token", { erro: e.message }));
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
    lista.append(el("p", { class: "sub", text: t("configx.falha_listar_tokens", { erro: e.message }) }));
    return;
  }
  if (tokens.length === 0) {
    lista.append(el("p", { class: "sub", style: "margin:0", text: t("configx.sem_tokens") }));
    return;
  }
  for (const tok of tokens) {
    const revogado = !!tok.revogado_em;
    const row = el("div", { class: "token-row" + (revogado ? " revogado" : "") },
      el("div", {},
        el("span", { class: "token-nome", text: tok.nome }),
        el("span", { class: "pill", style: "margin-left:8px", text: rotuloPapel(tok.papel) }),
        revogado ? el("span", { class: "pill", style: "margin-left:6px;color:var(--muted)", text: t("configx.revogado") }) : null,
      ),
      revogado ? null : el("button", { class: "btn sm danger", text: t("configx.revogar"),
        onclick: () => revogar(tok.id) }),
    );
    lista.append(row);
  }
}

async function revogar(id) {
  try {
    await api.revogarToken(id);
    await recarregarTokens();
  } catch (e) {
    bannerErro(t("configx.falha_revogar", { erro: e.message }));
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
      if (!r.ok) { bannerErro(t("configx.deve_numero", { campo: c.rotulo })); return; }
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
    toast(t("configx.salva"), "ok");
    await montarConfig();
  } catch (e) {
    bannerErro(t("configx.falha_salvar", { erro: e.message }));
    toast(t("configx.falha_salvar_toast"), "err");
  } finally {
    btn.disabled = false;
  }
}
