// Tela "Grupos de repositórios" — agrupa projetos de uma mesma solução (N:N)
// para as consultas multi-repo. A ordem dos membros importa: o primeiro é o
// repositório principal (cwd do harness); os demais entram como contexto extra.

import { api } from "./api.js";
import { el, limpar, toast, bannerErro } from "./ui.js";
import { t } from "./i18n.js";

let grupos = [];
let projetos = [];
let grupoSelID = null;

export async function montarGrupos() {
  try {
    projetos = ((await api.listarProjetos()) || []).filter((p) => p.ativo);
  } catch (e) {
    bannerErro(t("grupos.falha_carregar_projetos", { erro: e.message }));
    return;
  }
  document.getElementById("btn-novo-grupo").onclick = () => renderGrupo(null);
  await recarregarGrupos();
  if (grupoSelID != null) {
    const g = grupos.find((x) => x.id === grupoSelID);
    if (g) renderGrupo(g);
    else limparPainel();
  }
}

function limparPainel() {
  grupoSelID = null;
  limpar(document.getElementById("painel-grupo"))
    .append(el("p", { class: "sub", style: "margin:0", text: t("view.grupos.selecione") }));
}

async function recarregarGrupos() {
  try {
    grupos = (await api.listarGrupos()) || [];
  } catch (e) {
    bannerErro(t("grupos.falha_carregar", { erro: e.message }));
    return;
  }
  bannerErro("");
  const lista = limpar(document.getElementById("lista-grupos"));
  if (grupos.length === 0) {
    lista.append(el("p", { class: "sub", text: t("grupos.nenhum") }));
    return;
  }
  for (const g of grupos) {
    lista.append(el("div", {
      class: "list-item" + (g.id === grupoSelID ? " sel" : ""),
      onclick: () => { grupoSelID = g.id; recarregarGrupos(); renderGrupo(g); },
    },
      el("b", { text: g.nome }),
      el("div", { class: "path", text: (g.membros || []).map((m) => m.nome).join(" + ") || t("grupos.sem_repos") }),
      el("div", { class: "meta" },
        g.ativo ? el("span", { class: "pill" }, el("span", { class: "dot dot-good" }), t("grupos.ativo"))
                : el("span", { class: "pill" }, el("span", { class: "dot dot-muted" }), t("grupos.inativo")),
      ),
    ));
  }
}

// renderGrupo desenha o form de criação/edição de um grupo. A ordem dos membros
// importa: o PRIMEIRO é o repositório principal (▲/▼ reordenam).
function renderGrupo(g) {
  const painel = limpar(document.getElementById("painel-grupo"));
  const criando = g == null;
  if (criando) grupoSelID = null;
  painel.append(el("h3", {}, criando ? t("grupos.novo") : t("grupos.titulo", { nome: g.nome })));

  const nome = el("input", { value: g ? g.nome : "" });
  const descricao = el("textarea", { rows: "3",
    placeholder: t("grupos.ph_descricao") },
    g ? g.descricao : "");
  const ativo = el("input", { type: "checkbox" });
  ativo.checked = g ? g.ativo : true;

  // membros: lista ordenada de project_ids; o restante dos projetos pode ser
  // acrescentado por um select.
  let membros = g ? (g.membros || []).map((m) => m.project_id) : [];
  const boxMembros = el("div", {});

  function nomeProjeto(id) {
    const p = projetos.find((x) => x.id === id);
    return p ? p.nome : t("grupos.projeto_n", { id });
  }

  function renderMembros() {
    limpar(boxMembros);
    if (membros.length === 0) {
      boxMembros.append(el("p", { class: "sub", style: "margin:6px 0", text: t("grupos.sem_membros") }));
    }
    membros.forEach((pid, i) => {
      boxMembros.append(el("div", { class: "list-item", style: "display:flex;align-items:center;gap:8px" },
        el("b", { style: "flex:1", text: nomeProjeto(pid) + (i === 0 ? "  " + t("grupos.principal") : "") }),
        el("button", { class: "btn ghost sm", text: "▲", disabled: i === 0, onclick: () => {
          [membros[i - 1], membros[i]] = [membros[i], membros[i - 1]]; renderMembros();
        } }),
        el("button", { class: "btn ghost sm", text: "▼", disabled: i === membros.length - 1, onclick: () => {
          [membros[i + 1], membros[i]] = [membros[i], membros[i + 1]]; renderMembros();
        } }),
        el("button", { class: "btn ghost sm", text: "✕", onclick: () => {
          membros = membros.filter((x) => x !== pid); renderMembros();
        } }),
      ));
    });
    const fora = projetos.filter((p) => !membros.includes(p.id));
    if (fora.length > 0) {
      const sel = el("select", {}, ...fora.map((p) => el("option", { value: p.id }, p.nome)));
      const btnAdd = el("button", { class: "btn ghost sm", text: t("grupos.adicionar"), onclick: () => {
        membros.push(Number(sel.value)); renderMembros();
      } });
      boxMembros.append(el("div", { style: "display:flex;gap:8px;margin-top:8px" }, sel, btnAdd));
    }
  }
  renderMembros();

  const btnSalvar = el("button", { class: "btn", text: criando ? t("grupos.criar") : t("grupos.salvar") });
  btnSalvar.onclick = async () => {
    if (!nome.value.trim()) { bannerErro(t("grupos.nome_obrigatorio")); return; }
    if (membros.length === 0) { bannerErro(t("grupos.membro_obrigatorio")); return; }
    bannerErro("");
    btnSalvar.disabled = true;
    const corpo = { nome: nome.value.trim(), descricao: descricao.value.trim(),
      project_ids: membros, ativo: ativo.checked };
    try {
      if (criando) {
        const criado = await api.criarGrupo(corpo);
        grupoSelID = criado.id;
        toast(t("grupos.criado"), "ok");
      } else {
        await api.atualizarGrupo(g.id, corpo);
        toast(t("grupos.salvo"), "ok");
      }
      await recarregarGrupos();
      const atual = grupos.find((x) => x.id === grupoSelID);
      if (atual) renderGrupo(atual);
    } catch (e) {
      bannerErro(t("grupos.falha_salvar", { erro: e.message }));
    } finally {
      btnSalvar.disabled = false;
    }
  };

  const acoes = el("div", { class: "acoes" }, btnSalvar);
  if (!criando) {
    acoes.append(el("button", { class: "btn ghost", text: t("grupos.excluir"), onclick: async () => {
      if (!confirm(t("grupos.confirmar_exclusao"))) return;
      try {
        await api.excluirGrupo(g.id);
        toast(t("grupos.excluido"), "ok");
        grupoSelID = null;
        await recarregarGrupos();
        limparPainel();
      } catch (e) {
        bannerErro(t("grupos.falha_excluir", { erro: e.message }));
      }
    } }));
  }

  painel.append(el("div", { class: "form" },
    el("div", {}, el("label", {}, t("grupos.nome")), nome),
    el("div", {}, el("label", {}, t("grupos.descricao") + " ", el("span", { class: "opt" }, t("grupos.descricao_opt"))), descricao),
    el("div", {}, el("label", {}, t("grupos.repositorios")), boxMembros,
      el("div", { class: "hint", text: t("grupos.repositorios_hint") })),
    el("label", { style: "display:flex;align-items:center;gap:8px;font-weight:600" }, ativo, t("grupos.grupo_ativo")),
    acoes,
  ));
}
