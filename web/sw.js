// Service worker do Praxis (M3 do PLANO_INTERNET). Servido por GET /sw.js
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
// Os handlers de push/notificationclick entram no M4.

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
