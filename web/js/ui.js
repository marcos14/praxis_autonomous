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
