// Harness de teste: roda o renderMarkdown real de web/js/ui.js fora do
// navegador, com um stub mínimo de DOM, e falha (exit 1) se algum conteúdo
// quebrar o parser. Recebe os arquivos a validar como argumentos; o corpo
// testado é o arquivo SEM a primeira linha ("# Título"), como o servidor envia.
//
// Existe porque um manual com CRLF derrubava a tela inteira (o \r sobrevive ao
// split e desancora os padrões terminados em $), e nenhum teste pegava isso.
function novoNo(tag) {
  return {
    tag, children: [], attrs: {}, _text: "",
    get textContent() { return this._text; },
    set textContent(v) { this._text = String(v); },
    set innerHTML(v) { this._text = String(v); },
    setAttribute(k, v) { this.attrs[k] = v; },
    addEventListener() {},
    append(...cs) { this.children.push(...cs); },
    replaceChildren() { this.children = []; },
    nodeType: 1,
  };
}
global.document = {
  createElement: novoNo,
  createTextNode: (t) => ({ nodeType: 3, textContent: String(t) }),
  getElementById: () => null,
  documentElement: { lang: "" },
};
Object.defineProperty(global, "navigator", { value: { languages: ["pt-BR"] }, configurable: true });
global.localStorage = { getItem: () => null, setItem: () => {} };
global.location = { reload: () => {} };
global.fetch = async () => ({ ok: false });

const [uiPath, ...arquivos] = process.argv.slice(2);
const { renderMarkdown } = await import(uiPath);
const fs = await import("node:fs");

let falhas = 0;

// Casos-limite fixos: cada um já quebrou (ou quase) o parser alguma vez.
const casos = {
  "titulo com CRLF": "## Título\r\n\r\ntexto\r\n",
  "só CR": "# A\rtexto\r",
  "vazio": "",
  "só espaços": "   \n\t\n",
  "lista sem fechar": "- item\n  - sub",
  "tabela sem corpo": "| a | b |\n|---|---|",
  "cerca aberta": "```js\nsem fechar",
  "citação aninhada": "> nível 1\n> > nível 2",
};
for (const [nome, md] of Object.entries(casos)) {
  try {
    renderMarkdown(md);
  } catch (e) {
    console.log(`CRASH caso "${nome}": ${e.message}`);
    falhas++;
  }
}

for (const arq of arquivos) {
  const md = fs.readFileSync(arq, "utf8");
  const corpo = md.split("\n").slice(1).join("\n").replace(/^\n+/, "");
  try {
    renderMarkdown(corpo);
  } catch (e) {
    console.log(`CRASH ${arq}: ${e.message}`);
    falhas++;
  }
}

if (falhas > 0) {
  console.log(`${falhas} conteúdo(s) quebram o renderMarkdown`);
  process.exit(1);
}
console.log(`renderMarkdown OK em ${Object.keys(casos).length} casos-limite e ${arquivos.length} arquivos`);
