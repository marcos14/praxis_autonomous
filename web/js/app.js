// Ponto de entrada do frontend. Faz a navegação entre as telas (shell) e
// dispara a montagem de cada view ao ser exibida. Sem framework: só ES modules.
//
// Antes de montar a shell, exige uma sessão: sem usuário logado exibe o portão de
// autenticação (login, ou criação do primeiro admin quando a instalação é nova).
// A navegação e os itens de menu respeitam as permissões do usuário — o servidor
// continua sendo a autoridade; aqui é só experiência (esconder o que não pode).

import { montarDemandas } from "./demandas.js";
import { montarNovaDemanda } from "./nova.js";
import { montarConsultas, desmontarConsultas } from "./consultas.js";
import { montarProjetos } from "./projetos.js";
import { montarGrupos } from "./grupos.js";
import { montarMotores } from "./motores.js";
import { montarConfig } from "./config.js";
import { montarKanban, desmontarKanban } from "./kanban.js";
import { montarHome, desmontarHome } from "./home.js";
import { montarManual } from "./manual.js";
import { montarUsuarios, montarPapeis } from "./usuarios.js";
import { montarGruposUsuarios } from "./gusuarios.js";
import { bannerErro, el, limpar } from "./ui.js";
import * as auth from "./auth.js";

// views mapeia o nome da view à sua função de montagem (chamada a cada exibição,
// para refletir o estado atual do banco).
const views = {
  home: montarHome,
  kanban: montarKanban,
  demandas: montarDemandas,
  nova: montarNovaDemanda,
  consultas: montarConsultas,
  projetos: montarProjetos,
  grupos: montarGrupos,
  motores: montarMotores,
  config: montarConfig,
  usuarios: montarUsuarios,
  papeis: montarPapeis,
  gusuarios: montarGruposUsuarios,
  manual: montarManual,
};

// desmontar mapeia (opcionalmente) o nome da view à sua função de limpeza,
// chamada ao SAIR da view (ex.: fechar o SSE do kanban).
const desmontar = {
  kanban: desmontarKanban,
  home: desmontarHome,
  consultas: desmontarConsultas,
};

// permView mapeia a view à permissão exigida para acessá-la (ausente = livre a
// qualquer usuário autenticado). Espelha os data-perm da sidebar e protege a
// navegação direta por hash.
const permView = {
  nova: "demandas.criar",
  consultas: "consultas.usar",
  projetos: "projetos.gerir",
  grupos: "projetos.gerir",
  motores: "config.gerir",
  config: "config.gerir",
  usuarios: "usuarios.gerir",
  papeis: "usuarios.gerir",
  gusuarios: "usuarios.gerir",
};

const nomesValidos = new Set(Object.keys(views));
let viewAtual = "";

// irPara ativa a view pedida: alterna as seções, destaca o item do menu, limpa o
// banner de erro e (re)monta o conteúdo. Views desconhecidas — ou sem permissão —
// caem em "home".
async function irPara(nome) {
  if (!nomesValidos.has(nome)) nome = "home";
  if (permView[nome] && !auth.temPermissao(permView[nome])) nome = "home";
  if (viewAtual && viewAtual !== nome && desmontar[viewAtual]) {
    try { desmontar[viewAtual](); } catch { /* ignora falha de limpeza */ }
  }
  viewAtual = nome;
  document.querySelectorAll(".view").forEach((v) => v.classList.remove("active"));
  document.getElementById("view-" + nome).classList.add("active");
  document.querySelectorAll(".nav-item").forEach((n) =>
    n.classList.toggle("active", n.dataset.view === nome));
  bannerErro("");
  window.scrollTo(0, 0);
  try {
    await views[nome]();
  } catch (e) {
    bannerErro("Erro ao montar a tela: " + (e && e.message ? e.message : e));
  }
}

// irParaHash navega para uma view via URL: quando o hash já é o alvo (re-clique
// na view atual), monta direto; senão troca o hash e deixa o listener de
// hashchange chamar irPara — assim a montagem acontece UMA única vez por
// navegação.
function irParaHash(nome) {
  const alvo = nomesValidos.has(nome) ? nome : "home";
  if (location.hash.slice(1) === alvo) irPara(alvo);
  else location.hash = alvo;
}

// atualizarRodape mostra a versão e o estado do serviço/banco no rodapé do menu.
async function atualizarRodape() {
  const foot = document.getElementById("nav-foot");
  try {
    const resp = await fetch("/healthz");
    const h = await resp.json();
    const online = resp.ok && h.banco !== undefined ? h.banco === "ok" : resp.ok;
    const cor = online ? "var(--good)" : "var(--critical)";
    const rotulo = online ? "online" : "banco indisponível";
    foot.innerHTML = `v${h.versao || "dev"}<br><span style="color:${cor}">●</span> ${rotulo}`;
  } catch {
    foot.innerHTML = `<span style="color:var(--critical)">●</span> offline`;
  }
}

// aplicarPermissoes esconde os itens de menu que o usuário não pode acessar e
// preenche o rodapé com o usuário logado + botão Sair.
function aplicarPermissoes() {
  document.querySelectorAll(".nav-item[data-perm]").forEach((n) => {
    n.hidden = !auth.temPermissao(n.dataset.perm);
  });
  const u = auth.usuarioAtual();
  const box = document.getElementById("nav-user");
  if (box && u) {
    limpar(box);
    box.append(
      el("div", { class: "quem" }, el("b", { text: u.nome || u.email }), el("span", { text: u.email })),
      el("button", { class: "btn ghost sm", text: "Sair", onclick: () => auth.logout() }),
    );
    box.hidden = false;
  }
}

// ---------- Portão de autenticação ----------

// mostrarPortao exibe o overlay de login (ou de criação do primeiro admin) e
// esconde a app até haver sessão. O card é montado ANTES de tornar o overlay
// visível — assim, mesmo que auth.status() demore, nunca se vê o overlay vazio
// (o "card em branco" que aparecia enquanto se aguardava a resposta).
async function mostrarPortao() {
  document.querySelector(".app").style.display = "none";
  const overlay = document.getElementById("auth-overlay");

  let setupNecessario = false;
  try {
    const st = await auth.status();
    setupNecessario = !!(st && st.setup_necessario);
  } catch { /* sem backend: mostra login mesmo assim */ }

  renderAuthCard(setupNecessario ? "setup" : "login", setupNecessario);
  overlay.hidden = false;
}

// renderAuthCard desenha o formulário de login ou de setup dentro do card.
function renderAuthCard(modo, setupNecessario) {
  const card = document.getElementById("auth-card");
  limpar(card);
  const erro = el("div", { class: "banner banner-erro", hidden: true });

  const inpNome = el("input", { type: "text", placeholder: "Seu nome", autocomplete: "name" });
  const inpEmail = el("input", { type: "email", placeholder: "e-mail", autocomplete: "username" });
  const inpSenha = el("input", { type: "password", placeholder: "senha", autocomplete: modo === "setup" ? "new-password" : "current-password" });

  const mostrarErro = (msg) => { erro.textContent = msg; erro.hidden = !msg; };

  const btn = el("button", { class: "btn" }, modo === "setup" ? "Criar admin e entrar" : "Entrar");
  const submeter = async () => {
    mostrarErro("");
    btn.disabled = true;
    try {
      if (modo === "setup") {
        await auth.setup(inpNome.value.trim(), inpEmail.value.trim(), inpSenha.value);
      } else {
        await auth.login(inpEmail.value.trim(), inpSenha.value);
      }
      entrarNaApp();
    } catch (e) {
      // Se o setup falhou porque a instalação já tem admin (outra aba/instância
      // criou), troca para a tela de login em vez de deixar o usuário preso no
      // formulário de criação.
      if (modo === "setup" && e && e.codigo === "setup_concluido") {
        renderAuthCard("login", false);
        return;
      }
      mostrarErro(e && e.message ? e.message : "falha ao autenticar");
      btn.disabled = false;
    }
  };
  btn.addEventListener("click", submeter);
  [inpNome, inpEmail, inpSenha].forEach((i) =>
    i.addEventListener("keydown", (ev) => { if (ev.key === "Enter") submeter(); }));

  const form = el("div", { class: "form" });
  if (modo === "setup") form.append(rotulado("Nome", inpNome));
  form.append(rotulado("E-mail", inpEmail), rotulado("Senha", inpSenha), btn);

  card.append(
    el("h2", { text: modo === "setup" ? "Bem-vindo ao Praxis" : "Praxis Autonomous" }),
    el("p", { class: "sub", text: modo === "setup"
      ? "Nenhum usuário ainda. Crie o administrador inicial desta instalação."
      : "Entre com seu e-mail e senha." }),
    erro,
    form,
  );

  // Alternância login/setup só faz sentido quando o setup NÃO é obrigatório
  // (já há admin) — aí o link não aparece. Mantido simples: sem alternância.
  (modo === "setup" ? inpNome : inpEmail).focus();
}

// rotulado embrulha um input com seu label (padrão .form).
function rotulado(rotulo, input) {
  return el("div", {}, el("label", { text: rotulo }), input);
}

// entrarNaApp esconde o portão, revela a app, aplica permissões e navega.
function entrarNaApp() {
  document.getElementById("auth-overlay").hidden = true;
  document.querySelector(".app").style.display = "";
  aplicarPermissoes();
  atualizarRodape();
  irParaHash(location.hash.slice(1) || "home");
}

// ---------- Boot ----------

async function iniciar() {
  // Esconde a app enquanto a sessão é resolvida, evitando um "flash" da interface
  // antes do portão de login/criação de admin aparecer.
  document.querySelector(".app").style.display = "none";

  // Sair derruba a sessão: recarrega para reinicializar tudo (fecha SSEs, limpa
  // estado das views) e cair de novo no portão.
  auth.aoDeslogar(() => location.reload());

  document.querySelectorAll(".nav-item").forEach((btn) =>
    btn.addEventListener("click", () => irParaHash(btn.dataset.view)));
  window.addEventListener("hashchange", () => irPara(location.hash.slice(1)));

  const u = await auth.carregarSessao();
  if (!u) {
    await mostrarPortao();
    return;
  }
  entrarNaApp();
}

if (document.readyState === "loading") {
  document.addEventListener("DOMContentLoaded", iniciar);
} else {
  iniciar();
}
