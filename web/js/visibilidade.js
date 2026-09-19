// Visibilidade por dono (M2 do PLANO_INTERNET), compartilhada por consultas,
// planejamentos e demandas: o seletor dos formulários de criação, a pill das
// listas, a pill do autor, o filtro de escopo (Todos · Meus · Do grupo) e o
// controle de alteração no painel/card. O servidor é a autoridade — aqui é só
// experiência (mostrar o que faz sentido, lembrar a última escolha).

import { el, toast } from "./ui.js";
import { t } from "./i18n.js";
import { usuarioAtual, temPermissao } from "./auth.js";

export const VISIBILIDADES = ["privada", "grupo", "publica"];
const ICONES = { privada: "🔒", grupo: "👥", publica: "🌐" };
// Chaves literais: o teste de paridade do i18n varre as chamadas de tradução
// e não entende chaves montadas por concatenação.
const ROTULOS = { privada: t("vis.privada"), grupo: t("vis.grupo"), publica: t("vis.publica") };
const DICAS = { privada: t("vis.hint_privada"), grupo: t("vis.hint_grupo"), publica: t("vis.hint_publica") };
const ROTULOS_ESCOPO = { todos: t("escopo.todos"), meus: t("escopo.meus"), grupo: t("escopo.grupo") };

// rotuloVisibilidade devolve o texto traduzido de uma visibilidade.
export function rotuloVisibilidade(v) {
  return ROTULOS[v] || v;
}

// temGrupo informa se o usuário logado pertence a um grupo de usuários — sem
// grupo, "grupo" equivale a "privada" (só ele vê), então a opção é desabilitada.
function temGrupo() {
  const u = usuarioAtual();
  return !!(u && u.grupo_id);
}

const CHAVE_ULTIMA = "praxis_visibilidade";

// ultimaVisibilidade é a escolha lembrada do último formulário (default privada).
export function ultimaVisibilidade() {
  try {
    const v = localStorage.getItem(CHAVE_ULTIMA);
    return VISIBILIDADES.includes(v) ? v : "privada";
  } catch {
    return "privada";
  }
}

export function lembrarVisibilidade(v) {
  try { localStorage.setItem(CHAVE_ULTIMA, v); } catch { /* sem storage */ }
}

// seletorVisibilidade cria o <select> com as três opções; "grupo" fica
// desabilitada (com a explicação) quando o usuário não tem grupo.
export function seletorVisibilidade(valor = ultimaVisibilidade()) {
  const sel = el("select", {});
  for (const v of VISIBILIDADES) {
    const o = el("option", { value: v, text: ICONES[v] + " " + rotuloVisibilidade(v) });
    if (v === "grupo" && !temGrupo()) {
      o.disabled = true;
      o.textContent += " — " + t("vis.sem_grupo");
    }
    sel.append(o);
  }
  sel.value = valor === "grupo" && !temGrupo() ? "privada" : valor;
  return sel;
}

// campoVisibilidade embrulha o seletor no padrão .form (rótulo + hint).
export function campoVisibilidade(sel) {
  return el("div", {}, el("label", { text: t("vis.rotulo") }), sel,
    el("div", { class: "hint", text: t("vis.hint") }));
}

// pillVisibilidade é a pill 🔒/👥/🌐 de um item (null se o item não traz o campo).
export function pillVisibilidade(item) {
  const v = item && item.visibilidade;
  if (!VISIBILIDADES.includes(v)) return null;
  return el("span", { class: "pill vis-" + v, title: DICAS[v],
    text: ICONES[v] + " " + rotuloVisibilidade(v) });
}

// pillAutor mostra "por Fulano" quando o item é de outra pessoa.
export function pillAutor(item) {
  if (!item || !item.criado_por_nome) return null;
  const u = usuarioAtual();
  if (u && item.criado_por === u.id) return null;
  return el("span", { class: "pill", text: t("vis.por", { nome: item.criado_por_nome }) });
}

// podeAlterarVisibilidade: o criador ou um admin (mesma regra do servidor).
export function podeAlterarVisibilidade(item) {
  const u = usuarioAtual();
  if (!u) return false;
  if (temPermissao("*")) return true;
  return !!item && item.criado_por === u.id;
}

// controleVisibilidade é o <select> inline do painel/card que altera quem
// enxerga o item via `definir(id, valor)`. null quando o usuário não pode.
export function controleVisibilidade(item, definir, aoMudar) {
  if (!podeAlterarVisibilidade(item)) return null;
  const sel = seletorVisibilidade(item.visibilidade || "privada");
  sel.className = "sel-vis";
  sel.title = t("vis.alterar");
  sel.onchange = async () => {
    const anterior = item.visibilidade;
    try {
      await definir(item.id, sel.value);
      item.visibilidade = sel.value;
      toast(t("vis.alterada", { v: rotuloVisibilidade(sel.value) }), "ok");
      if (aoMudar) aoMudar(sel.value);
    } catch (e) {
      sel.value = anterior || "privada";
      toast(e.message, "err");
    }
  };
  return sel;
}

// ---------- escopo das listagens ----------

export const ESCOPOS = ["todos", "meus", "grupo"];

// escopoSalvo devolve o escopo lembrado da tela (default todos).
export function escopoSalvo(tela) {
  try {
    const v = localStorage.getItem("praxis_escopo_" + tela);
    return ESCOPOS.includes(v) ? v : "todos";
  } catch {
    return "todos";
  }
}

// filtroEscopo cria o controle segmentado "Todos · Meus · Do grupo" da tela,
// persistindo a escolha; aoMudar(escopo) é chamado a cada troca. Devolve
// { no, valor() }. Para enviar ao servidor, use paramEscopo(valor()).
export function filtroEscopo(tela, aoMudar) {
  let atual = escopoSalvo(tela);
  const cont = el("div", { class: "filtro-escopo", role: "group" });
  const botoes = [];
  for (const e of ESCOPOS) {
    const b = el("button", { type: "button", class: "escopo" + (e === atual ? " active" : ""), text: ROTULOS_ESCOPO[e] });
    if (e === "grupo" && !temGrupo()) {
      b.disabled = true;
      b.title = t("vis.sem_grupo");
    }
    b.onclick = () => {
      atual = e;
      try { localStorage.setItem("praxis_escopo_" + tela, e); } catch { /* sem storage */ }
      botoes.forEach((x) => x.classList.remove("active"));
      b.classList.add("active");
      aoMudar(e);
    };
    botoes.push(b);
    cont.append(b);
  }
  return { no: cont, valor: () => atual };
}

// paramEscopo converte o escopo da UI no valor de ?escopo= (todos = ausente).
export function paramEscopo(escopo) {
  return escopo && escopo !== "todos" ? escopo : "";
}
