// Telas "Usuários" e "Papéis" (RBAC). Usuários gerencia as pessoas (com papéis
// vinculados); Papéis gerencia os conjuntos de permissões do catálogo. São duas
// views separadas que compartilham este módulo. Só acessíveis a quem tem
// usuarios.gerir — o menu já esconde para os demais e o servidor barra por
// permissão.

import { api } from "./api.js";
import { el, limpar, toast, bannerErro } from "./ui.js";
import { usuarioAtual } from "./auth.js";

// Cache do que é carregado a cada montagem (para montar seletores sem refetch).
let papeis = [];
let permissoes = [];
let usuarios = [];
let gruposUsuarios = [];

// montarUsuarios monta a view Usuários. Papéis e grupos de usuários também são
// carregados aqui porque o form de usuário oferece os vínculos.
export async function montarUsuarios() {
  try {
    [papeis, usuarios, gruposUsuarios] = await Promise.all([
      api.listarPapeis(),
      api.listarUsuarios(),
      api.listarGruposUsuarios().catch(() => []),
    ]);
  } catch (e) {
    bannerErro("Falha ao carregar usuários: " + e.message);
    return;
  }
  bannerErro("");
  renderListaUsuarios();
  document.getElementById("btn-novo-usuario").onclick = () => abrirUsuario(null);
}

// montarPapeis monta a view Papéis (lista + catálogo de permissões).
export async function montarPapeis() {
  try {
    [permissoes, papeis] = await Promise.all([
      api.listarPermissoes(),
      api.listarPapeis(),
    ]);
  } catch (e) {
    bannerErro("Falha ao carregar papéis: " + e.message);
    return;
  }
  bannerErro("");
  renderListaPapeis();
  document.getElementById("btn-novo-papel").onclick = () => abrirPapel(null);
}

// ---------- Usuários ----------

function renderListaUsuarios() {
  const lista = limpar(document.getElementById("lista-usuarios"));
  if (usuarios.length === 0) {
    lista.append(el("p", { class: "sub", style: "margin:0", text: "Nenhum usuário." }));
    return;
  }
  for (const u of usuarios) {
    const nomesPapeis = (u.papeis || []).map((p) => p.nome).join(", ") || "sem papéis";
    const resumo = u.grupo_nome ? nomesPapeis + " · grupo " + u.grupo_nome : nomesPapeis;
    lista.append(el("div", { class: "list-item" + (u.ativo ? "" : " usuario-inativo"), onclick: () => abrirUsuario(u) },
      el("div", {},
        el("div", { style: "font-weight:600", text: u.nome }),
        el("div", { class: "path", text: u.email }),
        el("div", { class: "hint", text: resumo }),
      ),
      u.ativo ? null : el("span", { class: "pill", text: "inativo" }),
    ));
  }
}

// abrirUsuario mostra o formulário de criação (u=null) ou edição de um usuário.
function abrirUsuario(u) {
  const painel = limpar(document.getElementById("painel-usuario"));
  const novo = !u;
  const eu = usuarioAtual();
  const souEu = u && eu && u.id === eu.id;

  const inpNome = el("input", { type: "text", value: novo ? "" : u.nome });
  const inpEmail = el("input", { type: "email", value: novo ? "" : u.email });
  const inpSenha = el("input", { type: "password", placeholder: novo ? "senha inicial" : "(deixe em branco para manter)" });
  const chkAtivo = el("input", { type: "checkbox" });
  if (novo || u.ativo) chkAtivo.checked = true;

  // Seleção de papéis (chips toggle).
  const selecionados = new Set(novo ? [] : (u.papeis || []).map((p) => p.id));
  const chips = el("div", { class: "papel-chips" });
  const repintarChips = () => {
    limpar(chips);
    for (const p of papeis) {
      const on = selecionados.has(p.id);
      chips.append(el("span", { class: "papel-chip" + (on ? " on" : ""), text: p.nome,
        onclick: () => { on ? selecionados.delete(p.id) : selecionados.add(p.id); repintarChips(); } }));
    }
  };
  repintarChips();

  // Grupo de usuários (consultas): define o motor/modelo das consultas do
  // usuário. Opcional — sem grupo, vale o padrão dos motores.
  const selGrupo = el("select", {},
    el("option", { value: "" }, "sem grupo (padrão)"),
    ...gruposUsuarios.map((g) =>
      el("option", { value: g.id, selected: !novo && u.grupo_id === g.id }, g.nome)),
  );

  const form = el("div", { class: "form" },
    rotulado("Nome", inpNome),
    rotulado("E-mail (login)", inpEmail),
    rotulado(novo ? "Senha" : "Nova senha", inpSenha),
    el("div", {}, el("label", { text: "Papéis" }), chips),
    el("div", {}, el("label", { text: "Grupo de usuários (consultas)" }), selGrupo,
      el("div", { class: "hint", text: "Define o motor/modelo das Consultas deste usuário. Gerencie os grupos na tela Grupos de usuários." })),
    el("label", { class: "cfg-herda" }, chkAtivo, "Usuário ativo"),
  );

  const acoes = el("div", { class: "acoes" });
  const btnSalvar = el("button", { class: "btn", text: novo ? "Criar usuário" : "Salvar" });
  btnSalvar.onclick = () => salvarUsuario(u, {
    nome: inpNome.value.trim(), email: inpEmail.value.trim(), senha: inpSenha.value,
    ativo: chkAtivo.checked, papeis: [...selecionados],
    grupo_id: selGrupo.value ? Number(selGrupo.value) : null,
  }, btnSalvar);
  acoes.append(btnSalvar);
  if (!novo && !souEu) {
    acoes.append(el("button", { class: "btn danger", text: "Excluir", onclick: () => excluirUsuario(u) }));
  }
  form.append(acoes);

  painel.append(el("h3", { text: novo ? "Novo usuário" : u.nome + (souEu ? " (você)" : "") }), form);
}

async function salvarUsuario(u, dados, btn) {
  if (!dados.email) { bannerErro("Informe o e-mail."); return; }
  if (!u && !dados.senha) { bannerErro("Informe a senha inicial."); return; }
  bannerErro("");
  btn.disabled = true;
  try {
    if (u) {
      await api.atualizarUsuario(u.id, { nome: dados.nome, email: dados.email, ativo: dados.ativo, papeis: dados.papeis, grupo_id: dados.grupo_id });
      if (dados.senha) await api.resetarSenha(u.id, dados.senha);
    } else {
      await api.criarUsuario({ nome: dados.nome, email: dados.email, senha: dados.senha, ativo: dados.ativo, papeis: dados.papeis, grupo_id: dados.grupo_id });
    }
    toast("Usuário salvo.", "ok");
    await montarUsuarios();
  } catch (e) {
    bannerErro("Falha ao salvar usuário: " + e.message);
    toast("Falha ao salvar.", "err");
  } finally {
    btn.disabled = false;
  }
}

async function excluirUsuario(u) {
  if (!confirm(`Excluir o usuário ${u.email}? Esta ação não pode ser desfeita.`)) return;
  try {
    await api.excluirUsuario(u.id);
    toast("Usuário excluído.", "ok");
    await montarUsuarios();
    limpar(document.getElementById("painel-usuario")).append(
      el("p", { class: "sub", style: "margin:0", text: "Selecione um usuário à esquerda ou cadastre um novo." }));
  } catch (e) {
    bannerErro("Falha ao excluir: " + e.message);
  }
}

// ---------- Papéis ----------

function renderListaPapeis() {
  const lista = limpar(document.getElementById("lista-papeis"));
  if (papeis.length === 0) {
    lista.append(el("p", { class: "sub", style: "margin:0", text: "Nenhum papel." }));
    return;
  }
  for (const p of papeis) {
    const resumo = p.permissoes && p.permissoes.includes("*")
      ? "acesso total"
      : `${(p.permissoes || []).length} permissã(o/es)`;
    lista.append(el("div", { class: "list-item", onclick: () => abrirPapel(p) },
      el("div", {},
        el("div", { style: "font-weight:600" }, p.nome, p.sistema ? el("span", { class: "pill", style: "margin-left:8px", text: "sistema" }) : null),
        p.descricao ? el("div", { class: "path", text: p.descricao }) : null,
        el("div", { class: "hint", text: resumo }),
      ),
    ));
  }
}

// abrirPapel mostra o formulário de criação (p=null) ou edição de um papel.
// Papéis de sistema (ex.: admin) são somente leitura.
function abrirPapel(p) {
  const painel = limpar(document.getElementById("painel-papel"));
  const novo = !p;
  const sistema = !novo && p.sistema;

  const inpNome = el("input", { type: "text", value: novo ? "" : p.nome, disabled: sistema });
  const inpDesc = el("input", { type: "text", value: novo ? "" : (p.descricao || ""), disabled: sistema });

  const selecionadas = new Set(novo ? [] : (p.permissoes || []));
  const listaPerm = el("div", { class: "perm-lista" });
  for (const perm of permissoes) {
    const chk = el("input", { type: "checkbox", disabled: sistema });
    if (selecionadas.has(perm.chave)) chk.checked = true;
    chk.addEventListener("change", () => { chk.checked ? selecionadas.add(perm.chave) : selecionadas.delete(perm.chave); });
    listaPerm.append(el("label", { class: "perm-item" }, chk,
      el("div", {}, el("div", { class: "rot", text: perm.rotulo }), el("div", { class: "desc", text: perm.descricao }))));
  }

  const form = el("div", { class: "form" },
    rotulado("Nome", inpNome),
    rotulado("Descrição", inpDesc),
    el("div", {}, el("label", { text: "Permissões" }), listaPerm),
  );

  if (sistema) {
    form.append(el("div", { class: "hint", text: "Papel de sistema: não pode ser editado nem removido (concede acesso total)." }));
  } else {
    const acoes = el("div", { class: "acoes" });
    const btnSalvar = el("button", { class: "btn", text: novo ? "Criar papel" : "Salvar" });
    btnSalvar.onclick = () => salvarPapel(p, { nome: inpNome.value.trim(), descricao: inpDesc.value.trim(), permissoes: [...selecionadas] }, btnSalvar);
    acoes.append(btnSalvar);
    if (!novo) acoes.append(el("button", { class: "btn danger", text: "Excluir", onclick: () => excluirPapel(p) }));
    form.append(acoes);
  }

  painel.append(el("h3", { text: novo ? "Novo papel" : p.nome }), form);
}

async function salvarPapel(p, dados, btn) {
  if (!dados.nome) { bannerErro("Informe o nome do papel."); return; }
  bannerErro("");
  btn.disabled = true;
  try {
    if (p) await api.atualizarPapel(p.id, dados);
    else await api.criarPapel(dados);
    toast("Papel salvo.", "ok");
    await montarPapeis();
  } catch (e) {
    bannerErro("Falha ao salvar papel: " + e.message);
    toast("Falha ao salvar.", "err");
  } finally {
    btn.disabled = false;
  }
}

async function excluirPapel(p) {
  if (!confirm(`Excluir o papel "${p.nome}"? Usuários vinculados perdem essas permissões.`)) return;
  try {
    await api.excluirPapel(p.id);
    toast("Papel excluído.", "ok");
    await montarPapeis();
    limpar(document.getElementById("painel-papel")).append(
      el("p", { class: "sub", style: "margin:0", text: "Selecione um papel à esquerda ou crie um novo." }));
  } catch (e) {
    bannerErro("Falha ao excluir papel: " + e.message);
  }
}

// rotulado embrulha um input com seu label (padrão .form).
function rotulado(rotulo, input) {
  return el("div", {}, el("label", { text: rotulo }), input);
}
