// Web Push neste dispositivo (M4 do PLANO_INTERNET): pede permissão, assina no
// serviço de push do navegador com a chave VAPID pública da instância e
// registra a assinatura no servidor; "desativar" faz o caminho inverso. Exige
// service worker ativo (contexto seguro: https ou localhost).

import { api } from "./api.js";

// suportado informa se este navegador consegue receber Web Push.
export function suportado() {
  return "serviceWorker" in navigator && "PushManager" in window && "Notification" in window && window.isSecureContext;
}

// estado devolve { suportado, permissao, assinado, endpoint }.
export async function estado() {
  if (!suportado()) return { suportado: false, permissao: "unsupported", assinado: false, endpoint: "" };
  let sub = null;
  try {
    const reg = await navigator.serviceWorker.ready;
    sub = await reg.pushManager.getSubscription();
  } catch { /* SW ainda não registrado */ }
  return { suportado: true, permissao: Notification.permission, assinado: !!sub, endpoint: sub ? sub.endpoint : "" };
}

// chaveParaBytes converte a chave VAPID (base64url) no Uint8Array que
// pushManager.subscribe exige como applicationServerKey.
function chaveParaBytes(b64url) {
  const padding = "=".repeat((4 - (b64url.length % 4)) % 4);
  const b64 = (b64url + padding).replace(/-/g, "+").replace(/_/g, "/");
  const bin = atob(b64);
  const bytes = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i);
  return bytes;
}

// ativar pede a permissão, assina e registra no servidor. Lança Error com a
// causa ("permissao_negada", ou a mensagem da API/navegador).
export async function ativar() {
  if (!suportado()) throw new Error("unsupported");
  const permissao = await Notification.requestPermission();
  if (permissao !== "granted") throw new Error("permissao_negada");
  const reg = await navigator.serviceWorker.ready;
  let sub = await reg.pushManager.getSubscription();
  if (!sub) {
    const { chave } = await api.chavePush();
    sub = await reg.pushManager.subscribe({ userVisibleOnly: true, applicationServerKey: chaveParaBytes(chave) });
  }
  // (Re)registra sempre: o servidor faz upsert pelo endpoint e zera as falhas.
  await api.assinarPush(sub.toJSON());
  return sub.endpoint;
}

// desativar cancela no servidor e no navegador (best-effort dos dois lados).
export async function desativar() {
  if (!suportado()) return;
  const reg = await navigator.serviceWorker.ready;
  const sub = await reg.pushManager.getSubscription();
  if (!sub) return;
  try { await api.cancelarPush(sub.endpoint); } catch { /* servidor limpa em 404/410 depois */ }
  try { await sub.unsubscribe(); } catch { /* já cancelada */ }
}
