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
// títulos (#..######), listas (ordenadas, não ordenadas, aninhadas e de tarefa),
// blocos de código (```), tabelas GFM, citações (>), régua (---), **negrito**,
// *itálico*, ~~tachado~~, `código` e [links](url). Tudo via textContent (nunca
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

    // régua horizontal (---, *** ou ___). Antes da lista: "-" sem espaço não é item.
    if (/^\s*(-{3,}|\*{3,}|_{3,})\s*$/.test(linha)) {
      nos.push(el("hr"));
      i++;
      continue;
    }

    // título (# .. ######).
    const titulo = linha.match(/^(#{1,6})\s+(.*)$/);
    if (titulo) {
      nos.push(el("h" + titulo[1].length, {}, ...inline(titulo[2])));
      i++;
      continue;
    }

    // citação (>): junta as linhas consecutivas e renderiza o miolo recursivamente.
    if (/^\s*>/.test(linha)) {
      const buf = [];
      while (i < linhas.length && /^\s*>/.test(linhas[i])) {
        buf.push(linhas[i].replace(/^\s*> ?/, ""));
        i++;
      }
      nos.push(el("blockquote", {}, ...renderMarkdown(buf.join("\n"))));
      continue;
    }

    // tabela GFM: linha de cabeçalho com | seguida da linha separadora |---|---|.
    if (linha.includes("|") && ehSepTabela(linhas[i + 1])) {
      const cab = celulasTabela(linha);
      const alin = celulasTabela(linhas[i + 1]).map((c) => {
        const e = c.startsWith(":"), d = c.endsWith(":");
        return e && d ? "center" : (d ? "right" : "");
      });
      const estilo = (j) => alin[j] ? `text-align:${alin[j]}` : null;
      const thead = el("thead", {}, el("tr", {},
        ...cab.map((c, j) => el("th", { style: estilo(j) }, ...inline(c)))));
      const tbody = el("tbody");
      i += 2;
      while (i < linhas.length && linhas[i].trim() !== "" && linhas[i].includes("|")) {
        const cs = celulasTabela(linhas[i]);
        tbody.append(el("tr", {},
          ...cab.map((_, j) => el("td", { style: estilo(j) }, ...inline(cs[j] || "")))));
        i++;
      }
      nos.push(el("table", {}, thead, tbody));
      continue;
    }

    // lista (ordenada, não ordenada, aninhada por indentação, tarefas - [ ]).
    if (RE_ITEM.test(linha)) {
      const r = parseLista(linhas, i);
      nos.push(r.no);
      i = r.fim;
      continue;
    }

    // parágrafo (linhas consecutivas até uma em branco / novo bloco); quebras de
    // linha simples viram <br>, como no GFM de comentários — natural em chat.
    const buf = [];
    while (i < linhas.length && linhas[i].trim() !== "" &&
           !/^\s*```/.test(linhas[i]) && !/^#{1,6}\s/.test(linhas[i]) &&
           !RE_ITEM.test(linhas[i]) && !/^\s*>/.test(linhas[i]) &&
           !/^\s*(-{3,}|\*{3,}|_{3,})\s*$/.test(linhas[i]) &&
           !(linhas[i].includes("|") && ehSepTabela(linhas[i + 1]))) {
      buf.push(linhas[i]);
      i++;
    }
    const p = el("p", {}, ...inline(buf[0]));
    for (let k = 1; k < buf.length; k++) p.append(el("br"), ...inline(buf[k]));
    nos.push(p);
  }
  return nos;
}

// RE_ITEM reconhece um item de lista: indentação + marcador (-, *, + ou "1."/"1)").
const RE_ITEM = /^(\s*)(?:(\d+)[.)]|[-*+])\s+(.*)$/;

// parseLista consome itens a partir de linhas[i] e devolve { no, fim }. Itens
// mais indentados (2+ espaços) viram sub-lista do item anterior, recursivamente.
function parseLista(linhas, i) {
  const primeiro = linhas[i].match(RE_ITEM);
  const indent = (s) => s.replace(/\t/g, "  ").length;
  const base = indent(primeiro[1]);
  const ordenada = primeiro[2] !== undefined;
  const cont = el(ordenada ? "ol" : "ul");
  if (ordenada && Number(primeiro[2]) !== 1) cont.setAttribute("start", primeiro[2]);
  let li = null;
  while (i < linhas.length) {
    const m = linhas[i].match(RE_ITEM);
    if (!m) break;
    const ind = indent(m[1]);
    if (ind < base) break; // devolve ao nível de cima
    if (ind >= base + 2 && li) {
      const sub = parseLista(linhas, i);
      li.append(sub.no);
      i = sub.fim;
      continue;
    }
    if ((m[2] !== undefined) !== ordenada) break; // trocou ol<->ul: nova lista
    // item de tarefa: - [ ] pendente / - [x] feito (checkbox só de leitura).
    const tarefa = m[3].match(/^\[([ xX])\]\s+(.*)$/);
    if (tarefa) {
      const chk = el("input", { type: "checkbox", disabled: true });
      chk.checked = tarefa[1] !== " ";
      li = el("li", { class: "task" }, chk, " ", ...inline(tarefa[2]));
    } else {
      li = el("li", {}, ...inline(m[3]));
    }
    cont.append(li);
    i++;
  }
  return { no: cont, fim: i };
}

// ehSepTabela informa se a linha é o separador de tabela GFM (| --- | :---: |).
// Exige ao menos um "|" para não confundir com régua/texto solto.
function ehSepTabela(l) {
  if (!l || !l.includes("|") || !l.includes("-")) return false;
  const cs = celulasTabela(l);
  return cs.length > 0 && cs.every((c) => /^:?-+:?$/.test(c));
}

// celulasTabela divide uma linha de tabela nas células, ignorando os | das bordas.
function celulasTabela(l) {
  let s = l.trim();
  if (s.startsWith("|")) s = s.slice(1);
  if (s.endsWith("|")) s = s.slice(0, -1);
  return s.split("|").map((c) => c.trim());
}

// inline converte **negrito**, *itálico*, ~~tachado~~, `código`, [links](url) e
// URLs soltas de um trecho em nós DOM (o restante vira texto puro — sem
// innerHTML, evita injeção). Negrito/itálico/tachado/link renderizam o miolo
// recursivamente (ex.: link com trecho em negrito).
function inline(texto) {
  const nos = [];
  const re = /(`([^`]+)`)|(\[([^\]]+)\]\(([^)\s]+)\))|(\*\*(.+?)\*\*)|(~~(.+?)~~)|(\*([^*]+)\*)|(https?:\/\/[^\s<>()]+)/g;
  let ultimo = 0, m;
  while ((m = re.exec(texto)) !== null) {
    if (m.index > ultimo) nos.push(document.createTextNode(texto.slice(ultimo, m.index)));
    if (m[2] !== undefined) nos.push(el("code", { text: m[2] }));
    else if (m[4] !== undefined) nos.push(linkSeguro(m[4], m[5]));
    else if (m[7] !== undefined) nos.push(el("b", {}, ...inline(m[7])));
    else if (m[9] !== undefined) nos.push(el("del", {}, ...inline(m[9])));
    else if (m[11] !== undefined) nos.push(el("i", {}, ...inline(m[11])));
    else if (m[12] !== undefined) nos.push(linkSeguro(m[12], m[12]));
    ultimo = m.index + m[0].length;
  }
  if (ultimo < texto.length) nos.push(document.createTextNode(texto.slice(ultimo)));
  return nos;
}

// linkSeguro cria um <a> apenas para destinos seguros (http/https, âncora ou
// caminho relativo); qualquer outro esquema (javascript:, data:…) vira só texto.
// Quando o rótulo é a própria URL (autolink), vai como texto puro — reprocessá-lo
// com inline() o reconheceria como URL de novo, em recursão infinita.
function linkSeguro(texto, url) {
  const filhos = texto === url ? [document.createTextNode(texto)] : inline(texto);
  if (!/^(https?:\/\/|#|\/)/i.test(url)) return el("span", {}, ...filhos);
  const a = el("a", { href: url }, ...filhos);
  if (/^https?:/i.test(url)) {
    a.target = "_blank";
    a.rel = "noopener noreferrer";
  }
  return a;
}

// autoCrescer faz um <textarea> crescer com o conteúdo (até maxPx) — para os
// campos de chat. Devolve a função de sincronização, útil após limpar o valor.
export function autoCrescer(ta, maxPx = 140) {
  const sync = () => {
    ta.style.height = "auto";
    ta.style.height = Math.min(ta.scrollHeight, maxPx) + "px";
  };
  ta.addEventListener("input", sync);
  return sync;
}

// mdEditor cria um editor de Markdown com abas Escrever/Visualizar e uma barra
// de formatação (o preview usa o próprio renderMarkdown, então o usuário vê
// exatamente o que os leitores verão). Devolve { no, ta }: `no` é o contêiner
// para inserir na tela; `ta` é o <textarea> (leia/escreva .value, .focus()).
// Com abrirEmPreview, começa na aba Visualizar — para conteúdo que se lê mais
// do que se edita (ex.: overview gerado pelo Praxis).
export function mdEditor({ placeholder = "", valor = "", rows = 10, abrirEmPreview = false } = {}) {
  const ta = el("textarea", { rows: String(rows), placeholder }, valor || "");
  const preview = el("div", { class: "preview md", hidden: true });

  // envolver aplica um marcador inline em volta da seleção (ou do placeholder).
  function envolver(antes, depois, ph) {
    const i = ta.selectionStart, f = ta.selectionEnd;
    const sel = ta.value.slice(i, f) || ph;
    ta.setRangeText(antes + sel + depois, i, f);
    ta.setSelectionRange(i + antes.length, i + antes.length + sel.length);
    ta.focus();
  }

  // prefixarLinhas expande a seleção para linhas inteiras e prefixa cada uma
  // (prefixo pode ser função do índice, para listas numeradas).
  function prefixarLinhas(prefixo) {
    const v = ta.value;
    const ini = v.lastIndexOf("\n", ta.selectionStart - 1) + 1;
    let fim = v.indexOf("\n", Math.max(ta.selectionEnd, ini));
    if (fim === -1) fim = v.length;
    const linhas = v.slice(ini, fim).split("\n")
      .map((l, k) => (typeof prefixo === "function" ? prefixo(k) : prefixo) + l);
    const novo = linhas.join("\n");
    ta.setRangeText(novo, ini, fim);
    ta.setSelectionRange(ini, ini + novo.length);
    ta.focus();
  }

  // inserirBloco insere um trecho em linha própria na posição do cursor.
  function inserirBloco(texto) {
    const i = ta.selectionStart;
    const pre = i === 0 || ta.value[i - 1] === "\n" ? "" : "\n\n";
    ta.setRangeText(pre + texto + "\n", i, ta.selectionEnd, "end");
    ta.focus();
  }

  function inserirLink() {
    const i = ta.selectionStart, f = ta.selectionEnd;
    const sel = ta.value.slice(i, f) || "texto";
    ta.setRangeText(`[${sel}](https://)`, i, f);
    const u = i + sel.length + 3; // depois de "[sel]("
    ta.setSelectionRange(u, u + "https://".length);
    ta.focus();
  }

  function codigo() {
    const sel = ta.value.slice(ta.selectionStart, ta.selectionEnd);
    if (sel.includes("\n")) envolver("```\n", "\n```", "código");
    else envolver("`", "`", "código");
  }

  const FERRAMENTAS = [
    ["B", "Negrito (Ctrl+B)", () => envolver("**", "**", "negrito")],
    ["I", "Itálico (Ctrl+I)", () => envolver("*", "*", "itálico")],
    ["<>", "Código", codigo],
    ["H", "Título", () => prefixarLinhas("## ")],
    ["•", "Lista", () => prefixarLinhas("- ")],
    ["1.", "Lista numerada", () => prefixarLinhas((k) => `${k + 1}. `)],
    ["❝", "Citação", () => prefixarLinhas("> ")],
    ["🔗", "Link", inserirLink],
    ["⊞", "Tabela", () => inserirBloco("| Coluna | Coluna |\n|---|---|\n|  |  |")],
  ];
  const tools = el("div", { class: "md-editor-tools" },
    ...FERRAMENTAS.map(([rotulo, dica, acao]) =>
      el("button", { type: "button", title: dica, text: rotulo, onclick: acao })));

  const tabEscrever = el("button", { type: "button", class: "active", text: "Escrever" });
  const tabVisualizar = el("button", { type: "button", text: "Visualizar" });
  function mostrar(edicao) {
    tabEscrever.classList.toggle("active", edicao);
    tabVisualizar.classList.toggle("active", !edicao);
    ta.hidden = !edicao;
    preview.hidden = edicao;
    tools.hidden = !edicao;
    if (edicao) {
      ta.focus();
      return;
    }
    limpar(preview);
    if (ta.value.trim()) preview.append(...renderMarkdown(ta.value));
    else preview.append(el("p", { class: "vazio", text: "Nada para visualizar ainda." }));
  }
  tabEscrever.addEventListener("click", () => mostrar(true));
  tabVisualizar.addEventListener("click", () => mostrar(false));

  ta.addEventListener("keydown", (e) => {
    if (!(e.ctrlKey || e.metaKey)) return;
    const k = e.key.toLowerCase();
    if (k === "b") { e.preventDefault(); envolver("**", "**", "negrito"); }
    else if (k === "i") { e.preventDefault(); envolver("*", "*", "itálico"); }
  });

  const no = el("div", { class: "md-editor" },
    el("div", { class: "md-editor-bar" },
      el("div", { class: "md-editor-tabs" }, tabEscrever, tabVisualizar),
      tools),
    ta, preview);
  if (abrirEmPreview) mostrar(false);
  return { no, ta };
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
