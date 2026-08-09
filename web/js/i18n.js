// i18n do frontend. O catálogo do idioma ativo é carregado com top-level await:
// qualquer módulo que importe este arquivo só é avaliado DEPOIS do catálogo
// pronto — então até constantes de módulo (ex.: o mapa STATUS) podem usar t().
// Sem framework: catálogo JSON plano (chave→texto) e interpolação {nome}.
//
// Resolução do idioma: preferência salva (localStorage, sincronizada com a
// preferência do usuário logado) → idioma do navegador → pt-BR. Trocar de
// idioma recarrega a página, reavaliando os módulos com o novo catálogo.

const CHAVE = "praxis_idioma";

// IDIOMAS lista os idiomas suportados (tag → rótulo no próprio idioma).
export const IDIOMAS = [
  ["pt-BR", "Português"],
  ["en", "English"],
  ["es", "Español"],
  ["zh-CN", "中文"],
];

// normalizar mapeia uma tag frouxa ("pt", "en-US", "zh-Hans") num idioma
// suportado; "" quando não casa.
function normalizar(tag) {
  const t = String(tag || "").toLowerCase();
  if (t.startsWith("pt")) return "pt-BR";
  if (t.startsWith("en")) return "en";
  if (t.startsWith("es")) return "es";
  if (t.startsWith("zh")) return "zh-CN";
  return "";
}

function detectar() {
  const salvo = normalizar(localStorage.getItem(CHAVE));
  if (salvo) return salvo;
  for (const l of navigator.languages || [navigator.language]) {
    const n = normalizar(l);
    if (n) return n;
  }
  return "pt-BR";
}

const idioma = detectar();
document.documentElement.lang = idioma;

let catalogo = {};
try {
  const resp = await fetch(`/locales/${idioma}.json`);
  if (resp.ok) catalogo = await resp.json();
} catch { /* sem catálogo: t() devolve as chaves — visível, não quebra a app */ }

// idiomaAtivo devolve a tag do idioma em uso (ex.: "pt-BR"). As requisições da
// API a enviam no header X-Praxis-Idioma para o servidor responder no idioma.
export function idiomaAtivo() {
  return idioma;
}

// t traduz uma chave do catálogo, interpolando {nome} com args.nome. Chave
// ausente devolve a própria chave (o teste de paridade do backend garante os
// catálogos completos; aqui é só rede de segurança visível).
export function t(chave, args) {
  let texto = catalogo[chave];
  if (texto === undefined) return chave;
  if (args) {
    for (const [k, v] of Object.entries(args)) {
      texto = texto.replaceAll("{" + k + "}", String(v));
    }
  }
  return texto;
}

// tn é o plural mínimo: usa chave+".one" para n === 1 e ".other" nos demais
// casos (pt/en/es; o catálogo zh repete o texto). n fica disponível como {n}.
export function tn(chave, n, args) {
  return t(chave + (n === 1 ? ".one" : ".other"), { ...(args || {}), n });
}

// aplicarTraducoes traduz os elementos estáticos do HTML marcados com
// data-i18n (textContent), data-i18n-placeholder e data-i18n-title.
export function aplicarTraducoes(raiz = document) {
  raiz.querySelectorAll("[data-i18n]").forEach((n) => { n.textContent = t(n.dataset.i18n); });
  raiz.querySelectorAll("[data-i18n-placeholder]").forEach((n) => { n.placeholder = t(n.dataset.i18nPlaceholder); });
  raiz.querySelectorAll("[data-i18n-title]").forEach((n) => { n.title = t(n.dataset.i18nTitle); });
}

// definirIdioma persiste a escolha e recarrega a página. Com tokenSessao, grava
// também a preferência do usuário no servidor (best-effort: a preferência local
// já vale mesmo se o PUT falhar).
export async function definirIdioma(novo, tokenSessao) {
  const n = normalizar(novo) || "pt-BR";
  if (n === idioma) return;
  localStorage.setItem(CHAVE, n);
  if (tokenSessao) {
    try {
      await fetch("/api/v1/auth/idioma", {
        method: "PUT",
        headers: {
          "Content-Type": "application/json",
          "Authorization": "Bearer " + tokenSessao,
          "X-Praxis-Idioma": n,
        },
        body: JSON.stringify({ idioma: n }),
      });
    } catch { /* segue com a preferência local */ }
  }
  location.reload();
}

// adotarIdiomaDoUsuario aplica a preferência vinda do servidor (login/sessão):
// se difere do idioma ativo, salva e recarrega UMA vez para reavaliar os
// módulos. Devolve true quando vai recarregar (a chamada deve interromper o boot).
export function adotarIdiomaDoUsuario(pref) {
  const n = normalizar(pref);
  if (!n || n === idioma) return false;
  localStorage.setItem(CHAVE, n);
  location.reload();
  return true;
}

// seletorIdioma cria o <select> de troca de idioma (tela de login e rodapé do
// menu). obterToken é chamado na troca para também persistir no servidor.
export function seletorIdioma(obterToken, classe = "sel-idioma") {
  const sel = document.createElement("select");
  sel.className = classe;
  sel.title = t("idioma.rotulo");
  for (const [tag, rotulo] of IDIOMAS) {
    const o = document.createElement("option");
    o.value = tag;
    o.textContent = rotulo;
    if (tag === idioma) o.selected = true;
    sel.append(o);
  }
  sel.addEventListener("change", () => {
    definirIdioma(sel.value, obterToken ? obterToken() : "");
  });
  return sel;
}
