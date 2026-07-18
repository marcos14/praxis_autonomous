// Tela "Grupos de usuários" — cada grupo define o motor/modelo usados nas
// CONSULTAS dos seus membros (rigor menor que análise/execução: a consulta só
// explica comportamento). O vínculo usuário↔grupo é feito na tela Usuários.

import { api } from "./api.js";
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
    bannerErro("Falha ao carregar grupos de usuários: " + e.message);
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
    .append(el("p", { class: "sub", style: "margin:0", text: "Selecione um grupo à esquerda ou crie um novo." }));
}

function nomeMotor(engineID) {
  if (engineID == null) return "motor padrão";
  const m = motores.find((x) => x.id === engineID);
  return m ? m.nome : "motor #" + engineID;
}

function renderLista() {
  const lista = limpar(document.getElementById("lista-gusuarios"));
  if ((grupos || []).length === 0) {
    lista.append(el("p", { class: "sub", text: "Nenhum grupo de usuários ainda." }));
    return;
  }
  for (const g of grupos) {
    const detalhe = [nomeMotor(g.engine_id), g.modelo ? "modelo " + g.modelo : "modelo do motor"].join(" · ");
    lista.append(el("div", {
      class: "list-item" + (g.id === selID ? " sel" : ""),
      onclick: () => { selID = g.id; renderLista(); renderForm(g); },
    },
      el("b", { text: g.nome }),
      el("div", { class: "path", text: detalhe }),
      el("div", { class: "hint", text: (g.usuarios || []).join(", ") || "sem usuários vinculados" }),
    ));
  }
}

function renderForm(g) {
  const painel = limpar(document.getElementById("painel-gusuario"));
  const criando = g == null;
  if (criando) selID = null;
  painel.append(el("h3", {}, criando ? "Novo grupo de usuários" : `${g.nome} — grupo`));

  const nome = el("input", { value: g ? g.nome : "" });
  const descricao = el("textarea", { rows: "2", placeholder: "ex.: time de suporte N1" }, g ? g.descricao : "");

  const selMotor = el("select", {},
    el("option", { value: "" }, "motor padrão (ordem de fallback)"),
    ...motores.map((m) => el("option", { value: m.id, selected: g && g.engine_id === m.id }, m.nome)),
  );
  const modelo = el("input", { value: g ? g.modelo : "", placeholder: "vazio = modelo de consultas do motor" });

  const btnSalvar = el("button", { class: "btn", text: criando ? "Criar grupo" : "Salvar" });
  btnSalvar.onclick = async () => {
    if (!nome.value.trim()) { bannerErro("Nome do grupo é obrigatório."); return; }
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
        toast("Grupo criado.", "ok");
      } else {
        await api.atualizarGrupoUsuarios(g.id, corpo);
        toast("Grupo salvo.", "ok");
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
    acoes.append(el("button", { class: "btn ghost", text: "Excluir", onclick: async () => {
      if (!confirm(`Excluir o grupo "${g.nome}"? Os usuários vinculados voltam ao motor padrão das consultas.`)) return;
      try {
        await api.excluirGrupoUsuarios(g.id);
        toast("Grupo excluído.", "ok");
        limparPainel();
        await montarGruposUsuarios();
      } catch (e) {
        bannerErro("Falha ao excluir: " + e.message);
      }
    } }));
  }

  painel.append(el("div", { class: "form" },
    el("div", {}, el("label", {}, "Nome"), nome),
    el("div", {}, el("label", {}, "Descrição ", el("span", { class: "opt" }, "(opcional)")), descricao),
    el("div", { class: "row" },
      el("div", {}, el("label", {}, "Motor das consultas"), selMotor),
      el("div", {}, el("label", {}, "Modelo das consultas"), modelo),
    ),
    el("div", { class: "hint", text: "Precedência do modelo: modelo do grupo → modelo de consultas do motor → modelo de análise. Membros são vinculados na tela Usuários." }),
    acoes,
  ));
}
