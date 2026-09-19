// Tela "Minha conta": dados do usuário logado, troca de senha, idioma e as
// sessões ativas (dispositivos conectados), com "encerrar" por sessão e
// "encerrar as outras". Acessível a qualquer usuário autenticado — as rotas
// /auth/* só operam sobre o próprio principal.

import { api } from "./api.js";
import { el, limpar, toast } from "./ui.js";
import * as auth from "./auth.js";
import { t, tn, seletorIdioma } from "./i18n.js";
import { quando } from "./demandas.js";

let listaSessoes = null;

export async function montarConta() {
  const painel = limpar(document.getElementById("painel-conta"));
  const u = auth.usuarioAtual();
  if (!u) return;
  painel.append(secaoDados(u), secaoSenha(), secaoIdioma(), await secaoSessoes());
}

// secao é um painel com título e subtítulo opcional.
function secao(titulo, sub, ...filhos) {
  return el("div", { class: "panel conta-secao" },
    el("h3", { text: titulo }),
    sub ? el("p", { class: "sub", text: sub }) : null,
    ...filhos);
}

function linha(rotulo, valor) {
  return el("div", { class: "conta-linha" }, el("span", { class: "rotulo", text: rotulo }), el("span", { text: valor }));
}

function campo(rotulo, input) {
  return el("div", {}, el("label", { text: rotulo }), input);
}

// ---------- dados ----------

function secaoDados(u) {
  const papeis = (u.papeis || []).map((p) => p.nome).filter(Boolean).join(", ") || "—";
  return secao(t("conta.dados"), null,
    linha(t("conta.nome"), u.nome || ""),
    linha(t("conta.email"), u.email || ""),
    linha(t("conta.papeis"), papeis),
    linha(t("conta.grupo"), u.grupo_nome || t("conta.sem_grupo")),
  );
}

// ---------- senha ----------

function secaoSenha() {
  const atual = el("input", { type: "password", autocomplete: "current-password" });
  const nova = el("input", { type: "password", autocomplete: "new-password" });
  const confirmar = el("input", { type: "password", autocomplete: "new-password" });
  const erro = el("div", { class: "banner banner-erro", hidden: true });
  const mostrarErro = (msg) => { erro.textContent = msg; erro.hidden = !msg; };
  const btn = el("button", { class: "btn", text: t("conta.senha_trocar") });
  btn.onclick = async () => {
    mostrarErro("");
    if (nova.value !== confirmar.value) {
      mostrarErro(t("conta.senha_nao_confere"));
      return;
    }
    btn.disabled = true;
    try {
      await auth.trocarSenha(atual.value, nova.value);
      toast(t("conta.senha_ok"));
      atual.value = nova.value = confirmar.value = "";
      // O servidor derrubou as outras sessões: reflete na lista.
      await recarregarSessoes();
    } catch (e) {
      mostrarErro(e && e.message ? e.message : t("auth.falha"));
    } finally {
      btn.disabled = false;
    }
  };
  [atual, nova, confirmar].forEach((i) =>
    i.addEventListener("keydown", (ev) => { if (ev.key === "Enter") btn.click(); }));
  return secao(t("conta.senha"), t("conta.senha_sub"), erro,
    el("div", { class: "form" },
      campo(t("conta.senha_atual"), atual),
      campo(t("conta.senha_nova"), nova),
      campo(t("conta.senha_confirmar"), confirmar),
      btn));
}

// ---------- idioma ----------

function secaoIdioma() {
  return secao(t("conta.idioma"), t("conta.idioma_sub"), seletorIdioma(() => auth.tokenAtual(), "sel-idioma"));
}

// ---------- sessões ----------

async function secaoSessoes() {
  listaSessoes = el("div", { class: "conta-sessoes" });
  const btnOutras = el("button", { class: "btn ghost sm", text: t("conta.sessoes_encerrar_outras") });
  btnOutras.onclick = async () => {
    btnOutras.disabled = true;
    try {
      const r = await api.encerrarOutrasSessoes();
      toast(tn("conta.sessoes_encerradas", (r && r.revogadas) || 0));
      await recarregarSessoes();
    } catch (e) {
      toast(e.message, "err");
    } finally {
      btnOutras.disabled = false;
    }
  };
  await recarregarSessoes();
  return secao(t("conta.sessoes"), t("conta.sessoes_sub"), listaSessoes, btnOutras);
}

async function recarregarSessoes() {
  if (!listaSessoes) return;
  let sessoes;
  try {
    sessoes = (await api.listarSessoes()) || [];
  } catch (e) {
    limpar(listaSessoes).append(el("p", { class: "sub", text: t("comum.falha_carregar", { erro: e.message }) }));
    return;
  }
  limpar(listaSessoes);
  if (sessoes.length === 0) {
    listaSessoes.append(el("p", { class: "sub", text: t("conta.sessao_nenhuma") }));
    return;
  }
  for (const s of sessoes) listaSessoes.append(itemSessao(s));
}

function itemSessao(s) {
  const encerrar = s.atual ? null : el("button", { class: "btn ghost sm", text: t("conta.sessao_encerrar") });
  if (encerrar) {
    encerrar.onclick = async () => {
      encerrar.disabled = true;
      try {
        await api.encerrarSessao(s.id);
        await recarregarSessoes();
      } catch (e) {
        toast(e.message, "err");
        encerrar.disabled = false;
      }
    };
  }
  return el("div", { class: "list-item conta-sessao" },
    el("div", { class: "conta-sessao-info" },
      el("b", { text: resumirUA(s.user_agent) }),
      el("div", { class: "path", text: t("conta.sessao_criada", { quando: quando(s.criado_em) }) }),
      el("div", { class: "meta" },
        s.atual ? el("span", { class: "pill" }, el("span", { class: "dot dot-good" }), t("conta.sessao_atual")) : null,
        el("span", { class: "pill", text: t("conta.sessao_ip", { ip: s.ip || "?" }) }),
        el("span", { class: "pill", text: t("conta.sessao_ultimo_uso", { quando: quando(s.ultimo_uso) }) }),
      ),
    ),
    encerrar,
  );
}

// resumirUA reduz um user-agent a "navegador · sistema" (heurística simples;
// serve para o usuário reconhecer o dispositivo).
function resumirUA(ua) {
  ua = ua || "";
  const nav = /Edg\//.test(ua) ? "Edge"
    : /OPR\//.test(ua) ? "Opera"
    : /Firefox\//.test(ua) ? "Firefox"
    : /Chrome\//.test(ua) ? "Chrome"
    : /Safari\//.test(ua) ? "Safari"
    : "";
  const so = /Windows/.test(ua) ? "Windows"
    : /Android/.test(ua) ? "Android"
    : /iPhone|iPad/.test(ua) ? "iOS"
    : /Mac OS/.test(ua) ? "macOS"
    : /Linux/.test(ua) ? "Linux"
    : "";
  const resumo = [nav, so].filter(Boolean).join(" · ");
  if (resumo) return resumo;
  return ua ? ua.slice(0, 60) : t("conta.sessao_desconhecida");
}
