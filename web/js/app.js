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
import { montarPlanejamentos, desmontarPlanejamentos } from "./planejamentos.js";
import { montarProjetos } from "./projetos.js";
import { montarGrupos } from "./grupos.js";
import { montarMotores } from "./motores.js";
import { montarConfig } from "./config.js";
import { montarKanban, desmontarKanban } from "./kanban.js";
import { montarHome, desmontarHome } from "./home.js";
import { montarManual } from "./manual.js";
import { montarUsuarios, montarPapeis } from "./usuarios.js";
import { montarGruposUsuarios } from "./gusuarios.js";
import { montarConta } from "./conta.js";
import { registrarServiceWorker, botaoInstalarApp } from "./pwa.js";
import { separarRota } from "./rota.js";
import { iniciarNotificacoes, pararNotificacoes, botaoSino, ligarSinoTopbar } from "./notificacoes.js";
import { bannerErro, el, limpar } from "./ui.js";
import * as auth from "./auth.js";
import { t, aplicarTraducoes, seletorIdioma, adotarIdiomaDoUsuario } from "./i18n.js";

// views mapeia o nome da view à sua função de montagem (chamada a cada exibição,
// para refletir o estado atual do banco).
const views = {
  home: montarHome,
  kanban: montarKanban,
  demandas: montarDemandas,
  nova: montarNovaDemanda,
  consultas: montarConsultas,
  planejamentos: montarPlanejamentos,
  projetos: montarProjetos,
  grupos: montarGrupos,
  motores: montarMotores,
  config: montarConfig,
  usuarios: montarUsuarios,
  papeis: montarPapeis,
  gusuarios: montarGruposUsuarios,
  conta: montarConta,
  manual: montarManual,
};

// desmontar mapeia (opcionalmente) o nome da view à sua função de limpeza,
// chamada ao SAIR da view (ex.: fechar o SSE do kanban).
const desmontar = {
  kanban: desmontarKanban,
  home: desmontarHome,
  consultas: desmontarConsultas,
  planejamentos: desmontarPlanejamentos,
};

// permView mapeia a view à permissão exigida para acessá-la (ausente = livre a
// qualquer usuário autenticado). Espelha os data-perm da sidebar e protege a
// navegação direta por hash.
const permView = {
  nova: "demandas.criar",
  consultas: "consultas.usar",
  planejamentos: "planejamentos.usar",
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
// caem em "home". Aceita "view/id" (M4): o id vai ao montar da view, que abre o
// item ("#consultas/7", "#planejamentos/3", "#demandas/12").
async function irPara(destino) {
  let { view: nome, id } = separarRota(destino);
  if (!nomesValidos.has(nome)) { nome = "home"; id = ""; }
  if (permView[nome] && !auth.temPermissao(permView[nome])) nome = "home";
  if (viewAtual && viewAtual !== nome && desmontar[viewAtual]) {
    try { desmontar[viewAtual](); } catch { /* ignora falha de limpeza */ }
  }
  viewAtual = nome;
  document.querySelectorAll(".view").forEach((v) => v.classList.remove("active"));
  document.getElementById("view-" + nome).classList.add("active");
  document.querySelectorAll(".nav-item").forEach((n) =>
    n.classList.toggle("active", n.dataset.view === nome));
  abrirMenu(false);
  fecharPaineis();
  bannerErro("");
  window.scrollTo(0, 0);
  try {
    await views[nome](id);
  } catch (e) {
    bannerErro(t("shell.erro_montar", { erro: e && e.message ? e.message : e }));
  }
}

// irParaHash navega para uma view via URL: quando o hash já é o alvo (re-clique
// na view atual), monta direto; senão troca o hash e deixa o listener de
// hashchange chamar irPara — assim a montagem acontece UMA única vez por
// navegação.
function irParaHash(destino) {
  const { view } = separarRota(destino);
  const alvo = nomesValidos.has(view) ? destino.replace(/^#/, "") : "home";
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
    const rotulo = online ? t("foot.online") : t("foot.banco_indisponivel");
    foot.innerHTML = `v${h.versao || "dev"}<br><span style="color:${cor}">●</span> ${rotulo}`;
  } catch {
    foot.innerHTML = `<span style="color:var(--critical)">●</span> ${t("foot.offline")}`;
  }
}

// aplicarPermissoes esconde os itens de menu que o usuário não pode acessar e
// preenche o rodapé com o usuário logado + botão Sair.
function aplicarPermissoes() {
  document.querySelectorAll(".nav-item[data-perm]").forEach((n) => {
    n.hidden = !auth.temPermissao(n.dataset.perm);
  });
  // Um grupo de menu sem nenhum item visível some por inteiro (título incluso).
  document.querySelectorAll(".nav-group").forEach((g) => {
    g.hidden = !g.querySelector(".nav-item:not([hidden])");
  });
  const u = auth.usuarioAtual();
  const box = document.getElementById("nav-user");
  if (box && u) {
    limpar(box);
    box.append(
      el("div", { class: "quem", title: t("nav.conta"), onclick: () => irParaHash("conta") },
        el("b", { text: u.nome || u.email }), el("span", { text: u.email })),
      el("div", { class: "nav-user-acoes" },
        el("button", { class: "btn ghost sm", text: t("nav.conta"), onclick: () => irParaHash("conta") }),
        botaoSino(),
        botaoInstalarApp(),
        el("button", { class: "btn ghost sm", text: t("nav.sair"), onclick: () => sair() }),
      ),
      seletorIdioma(() => auth.tokenAtual()),
    );
    box.hidden = false;
  }
}

// ---------- Portão de autenticação ----------

// mostrarPortao exibe o overlay de login (ou de criação do primeiro admin). O
// card é montado ANTES de tornar o overlay visível — assim, mesmo que
// auth.status() demore, nunca se vê o overlay vazio.
//
// No boot (reauth == null) esconde a app até haver sessão. Ao REAUTENTICAR
// (a sessão caiu com a app aberta: revogada, expirada no servidor) a app fica
// como está por baixo do overlay — DOM, texto digitado e rolagem sobrevivem —
// e, se quem volta é o mesmo usuário, tudo continua de onde parou.
async function mostrarPortao(reauth = null) {
  if (!reauth) document.querySelector(".app").style.display = "none";
  const overlay = document.getElementById("auth-overlay");

  let setupNecessario = false;
  if (!reauth) {
    try {
      const st = await auth.status();
      setupNecessario = !!(st && st.setup_necessario);
    } catch { /* sem backend: mostra login mesmo assim */ }
  }

  renderAuthCard(setupNecessario ? "setup" : "login", setupNecessario, reauth);
  overlay.hidden = false;
}

// renderAuthCard desenha o formulário de login ou de setup dentro do card.
// reauth ({ anterior: usuário que estava logado } ou null) muda a mensagem,
// pré-preenche o e-mail e decide o que fazer depois de logar.
function renderAuthCard(modo, setupNecessario, reauth = null) {
  const card = document.getElementById("auth-card");
  limpar(card);
  const erro = el("div", { class: "banner banner-erro", hidden: true });

  const inpNome = el("input", { type: "text", placeholder: t("auth.ph_nome"), autocomplete: "name" });
  const inpEmail = el("input", { type: "email", placeholder: t("auth.ph_email"), autocomplete: "username" });
  const inpSenha = el("input", { type: "password", placeholder: t("auth.ph_senha"), autocomplete: modo === "setup" ? "new-password" : "current-password" });
  const anterior = reauth && reauth.anterior;
  if (anterior && anterior.email) inpEmail.value = anterior.email;

  const mostrarErro = (msg) => { erro.textContent = msg; erro.hidden = !msg; };

  const btn = el("button", { class: "btn" }, modo === "setup" ? t("auth.criar_admin") : t("auth.entrar"));
  const submeter = async () => {
    mostrarErro("");
    btn.disabled = true;
    try {
      if (modo === "setup") {
        await auth.setup(inpNome.value.trim(), inpEmail.value.trim(), inpSenha.value);
      } else {
        await auth.login(inpEmail.value.trim(), inpSenha.value);
      }
      if (!reauth) {
        entrarNaApp();
      } else if (anterior && auth.usuarioAtual() && auth.usuarioAtual().id === anterior.id) {
        retomarApp();
      } else {
        location.reload(); // outra pessoa entrou: começa do zero, sem estado alheio
      }
    } catch (e) {
      // Se o setup falhou porque a instalação já tem admin (outra aba/instância
      // criou), troca para a tela de login em vez de deixar o usuário preso no
      // formulário de criação.
      if (modo === "setup" && e && e.codigo === "setup_concluido") {
        renderAuthCard("login", false);
        return;
      }
      mostrarErro(e && e.message ? e.message : t("auth.falha"));
      btn.disabled = false;
    }
  };
  btn.addEventListener("click", submeter);
  [inpNome, inpEmail, inpSenha].forEach((i) =>
    i.addEventListener("keydown", (ev) => { if (ev.key === "Enter") submeter(); }));

  const form = el("div", { class: "form" });
  if (modo === "setup") form.append(rotulado(t("auth.nome"), inpNome));
  form.append(rotulado(t("auth.email"), inpEmail), rotulado(t("auth.senha"), inpSenha), btn);

  let sub = t("auth.sub_login");
  if (modo === "setup") sub = t("auth.sub_setup");
  else if (reauth) sub = t("auth.sub_reautenticar");
  card.append(
    el("h2", { text: modo === "setup" ? t("auth.titulo_setup") : "Praxis Autonomous" }),
    el("p", { class: "sub", text: sub }),
    erro,
    form,
    el("div", { class: "auth-idioma" }, seletorIdioma(() => "")),
  );

  // Alternância login/setup só faz sentido quando o setup NÃO é obrigatório
  // (já há admin) — aí o link não aparece. Mantido simples: sem alternância.
  if (modo === "setup") inpNome.focus();
  else if (inpEmail.value) inpSenha.focus();
  else inpEmail.focus();
}

// retomarApp fecha o portão após reautenticar com o MESMO usuário: a view atual
// continua como estava (nada é remontado); só as permissões do menu são
// reaplicadas. Os streams reabrem sozinhos ao ver o token novo (api.abrirStream)
// e os polls de fundo voltam a funcionar na próxima chamada.
function retomarApp() {
  document.getElementById("auth-overlay").hidden = true;
  document.querySelector(".app").style.display = "";
  aplicarPermissoes();
}

// sair revoga a sessão no servidor (apaga o cookie) e recarrega a página para
// começar do zero — fecha streams, limpa o estado das views e cai no portão.
let saindo = false;
async function sair() {
  saindo = true;
  pararNotificacoes();
  try {
    await auth.logout();
  } finally {
    location.reload();
  }
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
  // Sino + stream de notificações do usuário (M4); best-effort, não bloqueia.
  ligarSinoTopbar();
  iniciarNotificacoes();
  irParaHash(location.hash.slice(1) || "home");
}

// ---------- Celular: gaveta do menu e barra superior (M3) ----------

// abrirMenu abre/fecha a gaveta da sidebar (só tem efeito visual no celular;
// no desktop a sidebar é fixa e as classes não mudam nada).
function abrirMenu(aberto) {
  document.querySelector(".sidebar").classList.toggle("aberta", aberto);
  document.getElementById("sidebar-backdrop").classList.toggle("aberta", aberto);
}

// atualizarTopbar mostra o título da view ativa na barra superior e o botão
// "voltar" quando uma tela de duas colunas está mostrando o painel.
function atualizarTopbar() {
  const titulo = document.getElementById("topbar-titulo");
  const h1 = document.querySelector(".view.active h1.page");
  if (titulo) titulo.textContent = h1 ? h1.textContent : "Praxis";
  const aberto = document.querySelector(".view.active .two-col.painel-aberto");
  document.getElementById("btn-voltar").hidden = !aberto;
}

// ehCelular informa se o layout móvel está ativo (mesmo breakpoint do CSS).
function ehCelular() {
  return window.matchMedia("(max-width: 768px)").matches;
}

// Telas de duas colunas (consultas, planejamentos, projetos, usuários…) viram
// páginas no celular: tocar num item da lista — ou no "+ Novo" — mostra o
// painel; "←" na barra superior volta à lista. No desktop nada muda.
function mostrarPainel(twoCol, aberto) {
  twoCol.classList.toggle("painel-aberto", aberto);
  atualizarTopbar();
  if (aberto) window.scrollTo(0, 0);
}

function fecharPaineis() {
  document.querySelectorAll(".two-col.painel-aberto").forEach((tc) => tc.classList.remove("painel-aberto"));
  atualizarTopbar();
}

// ---------- Servidor indisponível ----------

let tentativasIndisponivel = 0;
let timerIndisponivel = null;

// mostrarIndisponivel cobre a app com um aviso quando o servidor não responde
// no boot (rede, reinício, 5xx). A sessão (cookie) NÃO é descartada: tenta de
// novo sozinho — 5, 10, 20 e depois a cada 30 s — ou quando o usuário pedir.
function mostrarIndisponivel() {
  document.querySelector(".app").style.display = "none";
  const overlay = document.getElementById("auth-overlay");
  const card = limpar(document.getElementById("auth-card"));
  let segundos = Math.min(30, 5 * 2 ** tentativasIndisponivel);
  tentativasIndisponivel++;

  const contagem = el("p", { class: "sub" });
  const atualizar = () => { contagem.textContent = t("auth.indisponivel_retentando", { segundos }); };
  const tentar = () => {
    clearInterval(timerIndisponivel);
    timerIndisponivel = null;
    resolverSessao();
  };
  atualizar();
  card.append(
    el("h2", { text: "Praxis Autonomous" }),
    el("p", { class: "sub", text: t("auth.indisponivel") }),
    contagem,
    el("div", { class: "form" }, el("button", { class: "btn", text: t("auth.tentar_agora"), onclick: tentar })),
  );
  overlay.hidden = false;

  clearInterval(timerIndisponivel);
  timerIndisponivel = setInterval(() => {
    segundos--;
    if (segundos <= 0) tentar();
    else atualizar();
  }, 1000);
}

// resolverSessao reidrata a sessão pelo cookie (POST /auth/refresh) e entra na
// app. Sem sessão → portão de login/setup. Servidor fora do ar → aviso com nova
// tentativa automática (a sessão sobrevive a um reinício do servidor).
async function resolverSessao() {
  let u;
  try {
    u = await auth.carregarSessao();
  } catch {
    mostrarIndisponivel();
    return;
  }
  tentativasIndisponivel = 0;
  if (!u) {
    await mostrarPortao();
    return;
  }
  // Preferência de idioma do usuário (servidor) difere do ativo → recarrega uma
  // vez para reavaliar os módulos no idioma certo.
  if (u.idioma && adotarIdiomaDoUsuario(u.idioma)) return;
  entrarNaApp();
}

// ---------- Boot ----------

async function iniciar() {
  // Esconde a app enquanto a sessão é resolvida, evitando um "flash" da interface
  // antes do portão de login/criação de admin aparecer.
  document.querySelector(".app").style.display = "none";

  // Traduz os textos estáticos do index.html (data-i18n) para o idioma ativo.
  aplicarTraducoes(document);

  // PWA: service worker (shell offline, base das notificações do M4).
  registrarServiceWorker();

  // A sessão caiu com a app aberta (401 definitivo: sessão revogada ou expirada
  // no servidor) → portão POR CIMA da app, sem recarregar — o que estava na
  // tela, inclusive texto digitado, continua lá. "Sair" (botão) é diferente:
  // revoga no servidor e recarrega; o callback não deve se sobrepor a isso.
  auth.aoDeslogar((anterior) => {
    if (saindo) return;
    mostrarPortao({ anterior });
  });

  document.querySelectorAll(".nav-item").forEach((btn) =>
    btn.addEventListener("click", () => irParaHash(btn.dataset.view)));
  window.addEventListener("hashchange", () => irPara(location.hash.slice(1)));

  // Celular: hambúrguer abre a gaveta; tocar fora fecha; "←" volta do painel
  // para a lista; tocar num item (ou "+ Novo") da coluna da lista abre o painel.
  document.getElementById("btn-menu").addEventListener("click", () => abrirMenu(true));
  document.getElementById("sidebar-backdrop").addEventListener("click", () => abrirMenu(false));
  document.getElementById("btn-voltar").addEventListener("click", () => fecharPaineis());
  document.addEventListener("click", (e) => {
    if (!ehCelular()) return;
    const coluna = e.target.closest(".two-col > :first-child");
    if (!coluna || !e.target.closest(".list-item, .dem-item, .btn")) return;
    mostrarPainel(coluna.parentElement, true);
  });

  await resolverSessao();
}

if (document.readyState === "loading") {
  document.addEventListener("DOMContentLoaded", iniciar);
} else {
  iniciar();
}
