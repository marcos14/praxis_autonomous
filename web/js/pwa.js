// PWA (M3 do PLANO_INTERNET): registro do service worker, aviso de versão
// nova, botão "Instalar app" e a dica de instalação no iOS (que não dispara
// beforeinstallprompt). O SW só registra em contexto seguro (https ou
// localhost) — fora disso o navegador nem o aceita.

import { el, toast } from "./ui.js";
import { t } from "./i18n.js";

let promptInstalacao = null; // evento beforeinstallprompt guardado para o clique
let botaoInstalar = null;

// registrarServiceWorker registra /sw.js e avisa quando uma versão nova do
// binário instalou um SW novo (a página atual ainda usa a anterior).
export function registrarServiceWorker() {
  if (!("serviceWorker" in navigator) || !window.isSecureContext) return;
  navigator.serviceWorker.register("/sw.js").then((reg) => {
    reg.addEventListener("updatefound", () => {
      const novo = reg.installing;
      if (!novo) return;
      novo.addEventListener("statechange", () => {
        if (novo.state === "installed" && navigator.serviceWorker.controller) {
          toast(t("pwa.nova_versao"), "ok");
        }
      });
    });
  }).catch(() => { /* sem SW: a app funciona igual, só não instala */ });
}

// instalado informa se a página roda como app instalado (modo standalone).
export function instalado() {
  return window.matchMedia("(display-mode: standalone)").matches || navigator.standalone === true;
}

function ehIOS() {
  return /iPhone|iPad|iPod/.test(navigator.userAgent) ||
    (navigator.platform === "MacIntel" && navigator.maxTouchPoints > 1);
}

window.addEventListener("beforeinstallprompt", (e) => {
  e.preventDefault();
  promptInstalacao = e;
  if (botaoInstalar) botaoInstalar.hidden = false;
});

window.addEventListener("appinstalled", () => {
  promptInstalacao = null;
  if (botaoInstalar) botaoInstalar.hidden = true;
  toast(t("pwa.instalado"), "ok");
});

// botaoInstalarApp devolve o botão "Instalar app" para o rodapé do menu: null
// quando já está instalado; no iOS mostra a dica (Compartilhar → Adicionar à
// Tela de Início); nos demais fica oculto até o navegador oferecer a
// instalação (beforeinstallprompt).
export function botaoInstalarApp() {
  if (instalado()) return null;
  if (ehIOS()) {
    return el("button", { class: "btn ghost sm", text: t("pwa.instalar"), onclick: () => alert(t("pwa.dica_ios")) });
  }
  botaoInstalar = el("button", {
    class: "btn ghost sm", text: t("pwa.instalar"), hidden: !promptInstalacao,
    onclick: async () => {
      if (!promptInstalacao) return;
      promptInstalacao.prompt();
      const escolha = await promptInstalacao.userChoice;
      if (escolha && escolha.outcome === "accepted") botaoInstalar.hidden = true;
      promptInstalacao = null;
    },
  });
  return botaoInstalar;
}
