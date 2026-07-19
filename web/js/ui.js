// Helpers de DOM e feedback ao usuário — sem dependências externas.

// el cria um elemento com atributos e filhos. Atributos especiais: `class`,
// `html` (innerHTML), `text` (textContent) e handlers `onX` (ex.: onclick).
// Filhos podem ser nós ou strings (viram texto).
export function el(tag, attrs = {}, ...filhos) {
  const n = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs)) {
    if (v == null || v === false) continue;
    if (k === "class") n.className = v;
    else if (k === "html") n.innerHTML = v;
    else if (k === "text") n.textContent = v;
    else if (k.startsWith("on") && typeof v === "function") n.addEventListener(k.slice(2), v);
    else if (v === true) n.setAttribute(k, "");
    else n.setAttribute(k, v);
  }
  for (const f of filhos) {
    if (f == null) continue;
    n.append(f.nodeType ? f : document.createTextNode(String(f)));
  }
  return n;
}

// limpar remove todos os filhos de um nó e o devolve.
export function limpar(n) {
  n.replaceChildren();
  return n;
}

// toast mostra uma notificação efêmera no canto. tipo ∈ {"ok","err",""}.
export function toast(msg, tipo = "ok") {
  const t = el("div", { class: `toast ${tipo}`, text: msg });
  document.body.appendChild(t);
  setTimeout(() => t.remove(), 3200);
}

// renderMarkdown converte um texto Markdown num array de nós DOM. Suporta
// títulos (#..######), listas (ordenadas e não), blocos de código (```),
// **negrito**, *itálico* e `código` inline. Tudo via textContent (nunca
// innerHTML), então é seguro contra injeção mesmo com conteúdo não confiável.
export function renderMarkdown(md) {
  const nos = [];
  const linhas = String(md || "").split("\n");
  let i = 0;
  while (i < linhas.length) {
    const linha = linhas[i];
    if (linha.trim() === "") { i++; continue; }

    // bloco de código cercado por ```.
    const cerca = linha.match(/^\s*```(\w*)\s*$/);
    if (cerca) {
      const buf = [];
      i++;
      while (i < linhas.length && !/^\s*```\s*$/.test(linhas[i])) {
        buf.push(linhas[i]);
        i++;
      }
      if (i < linhas.length) i++; // consome a cerca de fechamento
      nos.push(el("pre", {}, el("code", { text: buf.join("\n") })));
      continue;
    }

    // título (# .. ######).
    const titulo = linha.match(/^(#{1,6})\s+(.*)$/);
    if (titulo) {
      nos.push(el("h" + titulo[1].length, {}, ...inline(titulo[2])));
      i++;
      continue;
    }

    // lista não ordenada (- ) ou ordenada (1. ).
    const ordenada = /^\s*\d+\.\s/.test(linha);
    const naoOrdenada = /^\s*[-*]\s/.test(linha);
    if (ordenada || naoOrdenada) {
      const itens = [];
      while (i < linhas.length && (/^\s*\d+\.\s/.test(linhas[i]) || /^\s*[-*]\s/.test(linhas[i]))) {
        const texto = linhas[i].replace(/^\s*(\d+\.|[-*])\s/, "");
        itens.push(el("li", {}, ...inline(texto)));
        i++;
      }
      nos.push(el(ordenada ? "ol" : "ul", {}, ...itens));
      continue;
    }

    // parágrafo (linhas consecutivas até uma em branco / novo bloco).
    const buf = [];
    while (i < linhas.length && linhas[i].trim() !== "" &&
           !/^\s*```/.test(linhas[i]) && !/^#{1,6}\s/.test(linhas[i]) &&
           !/^\s*\d+\.\s/.test(linhas[i]) && !/^\s*[-*]\s/.test(linhas[i])) {
      buf.push(linhas[i]);
      i++;
    }
    nos.push(el("p", {}, ...inline(buf.join(" "))));
  }
  return nos;
}

// inline converte **negrito**, *itálico* e `código` de um trecho em nós DOM (o
// restante vira texto puro — sem innerHTML, evita injeção).
function inline(texto) {
  const nos = [];
  const re = /(\*\*([^*]+)\*\*|\*([^*]+)\*|`([^`]+)`)/g;
  let ultimo = 0, m;
  while ((m = re.exec(texto)) !== null) {
    if (m.index > ultimo) nos.push(document.createTextNode(texto.slice(ultimo, m.index)));
    if (m[2] !== undefined) nos.push(el("b", { text: m[2] }));
    else if (m[3] !== undefined) nos.push(el("i", { text: m[3] }));
    else if (m[4] !== undefined) nos.push(el("code", { text: m[4] }));
    ultimo = m.index + m[0].length;
  }
  if (ultimo < texto.length) nos.push(document.createTextNode(texto.slice(ultimo)));
  return nos;
}

// bannerErro exibe (ou esconde, com msg vazia) o banner de erro global no topo.
export function bannerErro(msg) {
  const b = document.getElementById("banner-erro");
  if (!b) return;
  if (msg) {
    b.textContent = msg;
    b.hidden = false;
  } else {
    b.textContent = "";
    b.hidden = true;
  }
}
