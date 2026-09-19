// Store de sessão do usuário. O JWT de acesso vive SÓ em memória (nunca em
// localStorage): quem persiste a sessão é o cookie HttpOnly `praxis_sessao`,
// que o navegador envia a /api/v1/auth/refresh para o servidor emitir um JWT
// novo. É deliberadamente independente de api.js — faz seu próprio fetch para
// as rotas /auth/* — para evitar ciclo de import (api.js importa este módulo
// para anexar o Bearer e renovar em 401).
//
// Ciclo de vida:
//   boot        → carregarSessao(): POST /auth/refresh (cookie) → usuário ou null;
//   em uso      → renovação proativa em ~80% da validade (expira_em) e, se a
//                 API responder 401, renovação sob demanda (api.js);
//   sessão caiu → sessaoCaiu(): limpa e avisa a shell (portão de login);
//   Sair        → logout(): POST /auth/logout (revoga a sessão) + sessaoCaiu().

import { idiomaAtivo } from "./i18n.js";

// Versões anteriores guardavam o JWT em localStorage; descarta o resíduo.
try { localStorage.removeItem("praxis_token"); } catch { /* sem storage */ }

let _token = "";
let _expiraEm = 0;      // epoch ms do vencimento do JWT (0 = desconhecido)
let _usuario = null;    // { id, nome, email, permissoes:[], papeis:[], idioma, grupo_id }
let _aoDeslogar = null; // callback registrado pela shell para exibir o login
let _deslogando = false; // trava o callback para disparar UMA vez só
let _renovando = null;  // promessa compartilhada da renovação em curso
let _timerRenovacao = null;

// ErroAuth é o erro das rotas /auth/*: status 0 = rede; 401 = sessão inválida;
// demais = resposta do servidor (com o código/mensagem do envelope de erro).
export class ErroAuth extends Error {
  constructor(status, codigo, mensagem) {
    super(mensagem || `erro ${status}`);
    this.name = "ErroAuth";
    this.status = status;
    this.codigo = codigo || "";
  }
}

// sessaoInvalida informa se o erro significa "não há sessão" (401) — em
// oposição a uma falha transitória (rede, 5xx), que NÃO deve derrubar nada.
export function sessaoInvalida(err) {
  return !!err && err.status === 401;
}

// tokenAtual devolve o JWT em memória (string vazia se não logado).
export function tokenAtual() {
  return _token;
}

// usuarioAtual devolve o usuário logado (ou null).
export function usuarioAtual() {
  return _usuario;
}

// temPermissao informa se o usuário atual pode fazer algo. O curinga "*" (admin)
// libera tudo; "visualizar" é implícito a qualquer usuário autenticado.
export function temPermissao(p) {
  if (!_usuario) return false;
  if (p === "visualizar") return true;
  const perms = _usuario.permissoes || [];
  return perms.includes("*") || perms.includes(p);
}

// reqAuth faz uma requisição às rotas de autenticação, anexando o Bearer quando
// há token (o cookie da sessão vai sozinho: mesma origem). Lança ErroAuth.
async function reqAuth(metodo, caminho, corpo) {
  const opts = { method: metodo, headers: { "X-Praxis-Idioma": idiomaAtivo() } };
  if (_token) opts.headers["Authorization"] = "Bearer " + _token;
  if (corpo !== undefined) {
    opts.headers["Content-Type"] = "application/json";
    opts.body = JSON.stringify(corpo);
  }
  let resp;
  try {
    resp = await fetch(caminho, opts);
  } catch (e) {
    throw new ErroAuth(0, "rede", e && e.message ? e.message : "falha de rede");
  }
  const texto = await resp.text();
  let dados = null;
  if (texto) {
    try { dados = JSON.parse(texto); } catch { /* corpo não-JSON */ }
  }
  if (!resp.ok) {
    const e = dados && dados.erro;
    throw new ErroAuth(resp.status, e && e.codigo, (e && e.mensagem) || `erro ${resp.status}`);
  }
  return dados;
}

// adotarSessao guarda token + usuário de uma resposta de login/setup/refresh
// e agenda a renovação proativa a partir do expira_em informado pelo servidor.
function adotarSessao(r) {
  _token = (r && r.token) || "";
  _usuario = (r && r.usuario) || null;
  _expiraEm = r && r.expira_em ? Date.parse(r.expira_em) || 0 : 0;
  _deslogando = false;
  agendarRenovacao();
}

function limparSessao() {
  _token = "";
  _usuario = null;
  _expiraEm = 0;
  clearTimeout(_timerRenovacao);
  _timerRenovacao = null;
}

// agendarRenovacao marca a renovação proativa para ~80% da validade restante
// (mínimo 5 s). Timers de aba em segundo plano podem atrasar; o 401 tratado em
// api.js e o visibilitychange abaixo cobrem esse atraso.
function agendarRenovacao() {
  clearTimeout(_timerRenovacao);
  _timerRenovacao = null;
  if (!_expiraEm) return;
  const espera = Math.max(5000, (_expiraEm - Date.now()) * 0.8);
  _timerRenovacao = setTimeout(() => {
    // 401 aqui já derrubou a sessão; falha transitória fica para o próximo 401
    // de uma chamada normal (que renova sob demanda).
    renovar().catch(() => {});
  }, espera);
}

// Aba voltando ao primeiro plano (PWA/celular congelam timers) com o token
// perto de vencer ou vencido: renova já, antes da primeira chamada levar 401.
document.addEventListener("visibilitychange", () => {
  if (document.visibilityState !== "visible" || !_token || !_expiraEm) return;
  if (_expiraEm - Date.now() < 60_000) renovar().catch(() => {});
});

// renovar pede um JWT novo ao servidor pela sessão do cookie. Single-flight:
// chamadas concorrentes (várias requisições recebendo 401 ao mesmo tempo, o
// timer, o visibilitychange) compartilham UMA requisição. Resolve com o
// usuário; rejeita com ErroAuth 401 quando a sessão não existe mais (o chamador
// decide derrubar) ou com status 0/5xx numa falha transitória.
export function renovar() {
  if (_renovando) return _renovando;
  _renovando = (async () => {
    try {
      const r = await reqAuth("POST", "/api/v1/auth/refresh");
      adotarSessao(r);
      return _usuario;
    } finally {
      _renovando = null;
    }
  })();
  return _renovando;
}

// status informa se ainda é preciso criar o primeiro admin (setup) ou já dá para
// logar.
export async function status() {
  return reqAuth("GET", "/api/v1/auth/status");
}

// login autentica e adota a sessão (o servidor grava o cookie). Devolve o usuário.
export async function login(email, senha) {
  const r = await reqAuth("POST", "/api/v1/auth/login", { email, senha });
  adotarSessao(r);
  return _usuario;
}

// setup cria o primeiro admin e já loga. Devolve o usuário.
export async function setup(nome, email, senha) {
  const r = await reqAuth("POST", "/api/v1/auth/setup", { nome, email, senha });
  adotarSessao(r);
  return _usuario;
}

// carregarSessao reidrata a sessão no boot pelo cookie. Devolve o usuário, ou
// null quando não há sessão (401). Falha transitória (rede, 5xx) é PROPAGADA:
// a shell mostra "servidor indisponível" e tenta de novo — um servidor fora do
// ar nunca apaga uma sessão válida.
export async function carregarSessao() {
  try {
    return await renovar();
  } catch (e) {
    if (sessaoInvalida(e)) {
      limparSessao();
      return null;
    }
    throw e;
  }
}

// trocarSenha muda a própria senha (exige a atual). O servidor derruba as
// OUTRAS sessões do usuário; esta continua.
export async function trocarSenha(atual, nova) {
  return reqAuth("PUT", "/api/v1/auth/senha", { atual, nova });
}

// sessaoCaiu derruba a sessão local e dispara o callback registrado UMA única
// vez: várias requisições autenticadas podem receber 401 em paralelo (ex.: a
// Home dispara metrics/activity/pendencias juntas) e cada uma chega aqui. O
// callback recebe o usuário que estava logado (a shell pré-preenche o e-mail e
// sabe se quem voltou é a mesma pessoa).
export function sessaoCaiu() {
  const anterior = _usuario;
  limparSessao();
  if (_deslogando) return;
  _deslogando = true;
  if (_aoDeslogar) _aoDeslogar(anterior);
}

// logout é o "Sair": revoga a sessão no servidor (que apaga o cookie) e derruba
// a local. Best-effort no servidor — sessão já inválida ou servidor fora do ar
// não impedem sair localmente.
export async function logout() {
  try {
    await reqAuth("POST", "/api/v1/auth/logout");
  } catch { /* derruba localmente mesmo assim */ }
  sessaoCaiu();
}

// aoDeslogar registra o callback chamado quando a sessão cai (Sair ou 401
// definitivo vindo da API).
export function aoDeslogar(cb) {
  _aoDeslogar = cb;
}
