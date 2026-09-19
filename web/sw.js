// Service worker do Praxis (M3/M4 do PLANO_INTERNET). Servido por GET /sw.js
// (internal/api/web.go), que injeta antes deste arquivo:
//   const VERSAO = "<versão do binário>";   // nome do cache: um por versão
//   const SHELL = ["/", "/app.css", ...];   // assets embutidos a pré-cachear
//
// Estratégia:
//   - /api/, /ide/, /healthz, /cert e o próprio /sw.js NUNCA são interceptados
//     (preserva SSE, autenticação, o proxy do VS Code e a atualização do SW);
//   - navegações: rede primeiro, cai no index.html do cache quando offline;
//   - estáticos do shell: cache primeiro, revalidando em segundo plano.
// Uma versão nova do binário gera um cache novo; o antigo é apagado no activate.
//
// Web Push (M4): o evento `push` traz o payload cifrado pelo servidor
// ({id, titulo, detalhe, rota, tag}); se há uma janela do Praxis em foco a
// notificação não é mostrada (o toast da aba já cobre); senão vira notificação
// do sistema. `notificationclick` foca uma janela existente e manda a rota
// (postMessage), ou abre uma janela nova já na rota.

const CACHE = "praxis-" + VERSAO;

self.addEventListener("install", (e) => {
  e.waitUntil(
    caches.open(CACHE)
      .then((c) => c.addAll(SHELL))
      .then(() => self.skipWaiting()),
  );
});

self.addEventListener("activate", (e) => {
  e.waitUntil(
    caches.keys()
      .then((chaves) => Promise.all(
        chaves.filter((k) => k.startsWith("praxis-") && k !== CACHE).map((k) => caches.delete(k))))
      .then(() => self.clients.claim()),
  );
});

// naoInterceptar lista o que passa direto para a rede.
function naoInterceptar(url) {
  const p = url.pathname;
  return p.startsWith("/api/") || p.startsWith("/ide/") || p === "/healthz" || p === "/cert" || p === "/sw.js";
}

self.addEventListener("fetch", (e) => {
  const req = e.request;
  if (req.method !== "GET") return;
  const url = new URL(req.url);
  if (url.origin !== self.location.origin || naoInterceptar(url)) return;

  if (req.mode === "navigate") {
    // Rede primeiro: a página sempre reflete o binário atual; offline, o shell.
    e.respondWith(fetch(req).catch(() => caches.match("/")));
    return;
  }

  // Estáticos: cache primeiro, atualizando em segundo plano.
  e.respondWith(
    caches.match(req).then((emCache) => {
      const rede = fetch(req).then((resp) => {
        if (resp && resp.ok) {
          const copia = resp.clone();
          caches.open(CACHE).then((c) => c.put(req, copia));
        }
        return resp;
      }).catch(() => emCache);
      return emCache || rede;
    }),
  );
});

// ---------- Web Push (M4) ----------

// janelas devolve as janelas do Praxis abertas (inclusive não controladas).
function janelas() {
  return self.clients.matchAll({ type: "window", includeUncontrolled: true });
}

self.addEventListener("push", (e) => {
  let dados = {};
  try { dados = e.data ? e.data.json() : {}; } catch { dados = { titulo: e.data ? e.data.text() : "Praxis" }; }
  const titulo = dados.titulo || "Praxis";
  e.waitUntil(janelas().then((lista) => {
    // Uma janela em foco já mostrou o toast pelo SSE: não duplica no sistema.
    if (lista.some((c) => c.focused)) return;
    return self.registration.showNotification(titulo, {
      body: dados.detalhe || "",
      tag: dados.tag || dados.rota || "praxis",
      icon: "/icons/icon-192.png",
      badge: "/icons/icon-192.png",
      data: { rota: dados.rota || "", id: dados.id || 0 },
    });
  }));
});

self.addEventListener("notificationclick", (e) => {
  e.notification.close();
  const rota = (e.notification.data && e.notification.data.rota) || "";
  const id = (e.notification.data && e.notification.data.id) || 0;
  e.waitUntil(janelas().then((lista) => {
    const alvo = lista.find((c) => "focus" in c);
    if (alvo) {
      alvo.postMessage({ tipo: "rota", rota, id });
      return alvo.focus();
    }
    return self.clients.openWindow("/" + rota);
  }));
});
