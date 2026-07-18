// Store de sessão do usuário (JWT). Guarda o token em localStorage e o usuário
// atual (com as permissões efetivas) em memória. É deliberadamente independente
// de api.js — faz seu próprio fetch para as rotas /auth/* — para evitar ciclo de
// import (api.js importa este módulo para anexar o Bearer e tratar 401).

const CHAVE_TOKEN = "praxis_token";

let _token = localStorage.getItem(CHAVE_TOKEN) || "";
let _usuario = null; // { id, nome, email, permissoes:[], papeis:[] }
let _aoDeslogar = null; // callback registrado pela shell para exibir o login
let _deslogando = false; // trava o callback de logout para disparar UMA vez só

// tokenAtual devolve o JWT guardado (string vazia se não logado).
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

function guardarToken(t) {
  _token = t || "";
  if (_token) localStorage.setItem(CHAVE_TOKEN, _token);
  else localStorage.removeItem(CHAVE_TOKEN);
}

// reqAuth faz uma requisição às rotas de autenticação, anexando o Bearer quando
// há token. Lança Error com a mensagem do backend em status >= 400.
async function reqAuth(metodo, caminho, corpo) {
  const opts = { method: metodo, headers: {} };
  if (_token) opts.headers["Authorization"] = "Bearer " + _token;
  if (corpo !== undefined) {
    opts.headers["Content-Type"] = "application/json";
    opts.body = JSON.stringify(corpo);
  }
  const resp = await fetch(caminho, opts);
  const texto = await resp.text();
  let dados = null;
  if (texto) {
    try { dados = JSON.parse(texto); } catch { /* corpo não-JSON */ }
  }
  if (!resp.ok) {
    const e = dados && dados.erro;
    const err = new Error((e && e.mensagem) || `erro ${resp.status}`);
    err.status = resp.status;
    err.codigo = e && e.codigo;
    throw err;
  }
  return dados;
}

// status informa se ainda é preciso criar o primeiro admin (setup) ou já dá para
// logar.
export async function status() {
  return reqAuth("GET", "/api/v1/auth/status");
}

// login autentica e guarda a sessão. Devolve o usuário.
export async function login(email, senha) {
  const r = await reqAuth("POST", "/api/v1/auth/login", { email, senha });
  guardarToken(r.token);
  _usuario = r.usuario;
  return _usuario;
}

// setup cria o primeiro admin e já loga. Devolve o usuário.
export async function setup(nome, email, senha) {
  const r = await reqAuth("POST", "/api/v1/auth/setup", { nome, email, senha });
  guardarToken(r.token);
  _usuario = r.usuario;
  return _usuario;
}

// carregarSessao tenta reidratar a sessão a partir do token guardado (GET
// /auth/me). Token inválido/expirado → limpa e devolve null.
export async function carregarSessao() {
  if (!_token) return null;
  try {
    _usuario = await reqAuth("GET", "/api/v1/auth/me");
    return _usuario;
  } catch {
    guardarToken("");
    _usuario = null;
    return null;
  }
}

// trocarSenha muda a própria senha (exige a atual).
export async function trocarSenha(atual, nova) {
  return reqAuth("PUT", "/api/v1/auth/senha", { atual, nova });
}

// logout limpa a sessão e dispara o callback registrado (a shell exibe o login).
// O callback (tipicamente location.reload) é disparado UMA única vez: várias
// requisições autenticadas podem receber 401 em paralelo (ex.: a Home dispara
// metrics/activity/pendencias juntas) e cada uma chama logout — sem a trava,
// isso empilharia vários location.reload(), causando um loop de recarregamento e
// o "flash" de tela/card vazio durante o portão de login.
export function logout() {
  guardarToken("");
  _usuario = null;
  if (_deslogando) return;
  _deslogando = true;
  if (_aoDeslogar) _aoDeslogar();
}

// aoDeslogar registra o callback chamado quando a sessão cai (logout manual ou
// 401 vindo da API).
export function aoDeslogar(cb) {
  _aoDeslogar = cb;
}
