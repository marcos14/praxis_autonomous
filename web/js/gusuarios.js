// Tela t("projetos.grupos_usuarios") — cada grupo define o motor/modelo usados nas
// CONSULTAS dos seus membros (rigor menor que análise/execução: a consulta só
// explica comportamento). O vínculo usuário↔grupo é feito na tela Usuários.

import { api } from "./api.js";
import { t } from "./i18n.js";
import { el, limpar, toast, bannerErro } from "./ui.js";

let grupos = [];
let motores = [];
let selID = null;

export async function montarGruposUsuarios() {
  try {
    [grupos, motores] = await Promise.all([
      api.listarGruposUsuarios(),
      api.listarMotores().catch(() => []),
    ]);
  } catch (e) {
    bannerErro(t("gusuarios.falha_carregar", { erro: e.message }));
    return;
  }
  bannerErro("");
  renderLista();
  document.getElementById("btn-novo-gusuario").onclick = () => renderForm(null);
  if (selID != null) {
    const g = grupos.find((x) => x.id === selID);
    if (g) renderForm(g);
    else limparPainel();
  }
}

function limparPainel() {
  selID = null;
  limpar(document.getElementById("painel-gusuario"))
    .append(el("p", { class: "sub", style: "margin:0", text: t("view.gusuarios.selecione") }));
}

function nomeMotor(engineID) {
  if (engineID == null) return t("gusuarios.motor_padrao");
  const m = motores.find((x) => x.id === engineID);
  return m ? m.nome : "motor #" + engineID;
}

function renderLista() {
  const lista = limpar(document.getElementById("lista-gusuarios"));
  if ((grupos || []).length === 0) {
    lista.append(el("p", { class: "sub", text: t("gusuarios.nenhum") }));
    return;
  }
  for (const g of grupos) {
    const detalhe = [nomeMotor(g.engine_id), g.modelo ? "modelo " + g.modelo : t("gusuarios.modelo_do_motor")].join(" · ");
    lista.append(el("div", {
      class: "list-item" + (g.id === selID ? " sel" : ""),
      onclick: () => { selID = g.id; renderLista(); renderForm(g); },
    },
      el("b", { text: g.nome }),
      el("div", { class: "path", text: detalhe }),
      el("div", { class: "hint", text: (g.usuarios || []).join(", ") || t("gusuarios.sem_usuarios") }),
    ));
  }
}

function renderForm(g) {
  const painel = limpar(document.getElementById("painel-gusuario"));
  const criando = g == null;
  if (criando) selID = null;
  painel.append(el("h3", {}, criando ? t("gusuarios.novo") : `${g.nome} — grupo`));

  const nome = el("input", { value: g ? g.nome : "" });
  const descricao = el("textarea", { rows: "2", placeholder: t("gusuarios.ph_descricao") }, g ? g.descricao : "");

  const selMotor = el("select", {},
    el("option", { value: "" }, t("gusuarios.motor_padrao_opcao")),
    ...motores.map((m) => el("option", { value: m.id, selected: g && g.engine_id === m.id }, m.nome)),
  );
  const modelo = el("input", { value: g ? g.modelo : "", placeholder: t("gusuarios.ph_modelo") });

  const btnSalvar = el("button", { class: "btn", text: criando ? t("grupos.criar") : t("configx.salvar") });
  btnSalvar.onclick = async () => {
    if (!nome.value.trim()) { bannerErro(t("grupos.nome_obrigatorio")); return; }
    bannerErro("");
    btnSalvar.disabled = true;
    const corpo = {
      nome: nome.value.trim(),
      descricao: descricao.value.trim(),
      engine_id: selMotor.value ? Number(selMotor.value) : null,
      modelo: modelo.value.trim(),
    };
    try {
      if (criando) {
        const criado = await api.criarGrupoUsuarios(corpo);
        selID = criado.id;
        toast(t("grupos.criado"), "ok");
      } else {
        await api.atualizarGrupoUsuarios(g.id, corpo);
        toast(t("grupos.salvo"), "ok");
      }
      await montarGruposUsuarios();
    } catch (e) {
      bannerErro("Falha ao salvar grupo: " + e.message);
    } finally {
      btnSalvar.disabled = false;
    }
  };

  const acoes = el("div", { class: "acoes" }, btnSalvar);
  if (!criando) {
    acoes.append(el("button", { class: "btn ghost", text: t("consultas.excluir"), onclick: async () => {
      if (!confirm(`Excluir o grupo "${g.nome}"? Os usuários vinculados voltam ao motor padrão das consultas.`)) return;
      try {
        await api.excluirGrupoUsuarios(g.id);
        toast(t("grupos.excluido"), "ok");
        limparPainel();
        await montarGruposUsuarios();
      } catch (e) {
        bannerErro("Falha ao excluir: " + e.message);
      }
    } }));
  }

  painel.append(el("div", { class: "form" },
    el("div", {}, el("label", {}, t("motores.nome")), nome),
    el("div", {}, el("label", {}, "Descrição ", el("span", { class: "opt" }, t("configx.opcional"))), descricao),
    el("div", { class: "row" },
      el("div", {}, el("label", {}, t("gusuarios.motor_consultas")), selMotor),
      el("div", {}, el("label", {}, t("gusuarios.modelo_consultas")), modelo),
    ),
    el("div", { class: "hint", text: t("gusuarios.hint_precedencia") }),
    acoes,
  ));
}
