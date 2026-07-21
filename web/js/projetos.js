// Tela "Projetos" — CRUD de projetos (Fase 1c) + edição do override de config do
// projeto (Fase 1e) com herança explícita do global, e "Ver config efetiva".

import { api } from "./api.js";
import { el, limpar, toast, bannerErro, mdEditor } from "./ui.js";
import { camposDoEscopo, jsonParaTexto, textoParaJSON, preservarDesconhecidas } from "./config-fields.js";

let projetos = [];
let selecionadoID = null;

export async function montarProjetos() {
  await recarregarLista();
  document.getElementById("btn-novo-projeto").onclick = () => abrirNovo();
  if (selecionadoID != null) {
    const p = projetos.find((x) => x.id === selecionadoID);
    if (p) renderEdicao(p);
    else limparPainel();
  }
}

async function recarregarLista() {
  try {
    projetos = (await api.listarProjetos()) || [];
  } catch (e) {
    bannerErro("Falha ao carregar projetos: " + e.message);
    return;
  }
  bannerErro("");
  const lista = limpar(document.getElementById("lista-projetos"));
  if (projetos.length === 0) {
    lista.append(el("p", { class: "sub", text: "Nenhum projeto cadastrado ainda." }));
    return;
  }
  for (const p of projetos) {
    const item = el("div", { class: "list-item" + (p.id === selecionadoID ? " sel" : ""), onclick: () => selecionar(p.id) },
      el("b", { text: p.nome }),
      el("div", { class: "path", text: `${p.pasta} · branch ${p.branch_principal}` }),
      el("div", { class: "meta" },
        p.ativo ? el("span", { class: "pill" }, el("span", { class: "dot dot-good" }), "ativo")
                : el("span", { class: "pill" }, el("span", { class: "dot dot-muted" }), "inativo"),
        el("span", { class: "pill", text: p.modo_integracao }),
      ),
    );
    lista.append(item);
  }
}

async function selecionar(id) {
  selecionadoID = id;
  await recarregarLista();
  let p;
  try {
    p = await api.obterProjeto(id);
  } catch (e) {
    bannerErro("Falha ao obter projeto: " + e.message);
    return;
  }
  renderEdicao(p);
}

function limparPainel() {
  const painel = limpar(document.getElementById("painel-projeto"));
  painel.append(el("p", { class: "sub", style: "margin:0", text: "Selecione um projeto à esquerda ou cadastre um novo." }));
}

function abrirNovo() {
  selecionadoID = null;
  recarregarLista();
  renderEdicao(null);
}

// camposCore monta os campos principais do projeto e devolve {node, ler} onde
// ler() valida e devolve o corpo do projeto (ou lança string de erro).
function camposCore(p) {
  const nome = el("input", { value: p ? p.nome : "" });
  const slug = el("input", { value: p ? p.slug : "", placeholder: "derivado do nome se em branco" });
  const branch = el("input", { value: p ? p.branch_principal : "main" });
  const pasta = el("input", { value: p ? p.pasta : "", placeholder: "C:\\Projetos\\meu-repo" });
  const modo = el("select", {},
    el("option", { value: "merge_request", selected: !p || p.modo_integracao === "merge_request" }, "merge_request"),
    el("option", { value: "merge_local", selected: p && p.modo_integracao === "merge_local" }, "merge_local"),
  );
  const url = el("input", { value: p ? p.url_plataforma : "", placeholder: "https://gitlab.empresa.com/grupo/repo" });
  const addDirs = el("textarea", { placeholder: "um caminho por linha" }, p && p.add_dirs ? p.add_dirs.join("\n") : "");
  const ativo = el("input", { type: "checkbox" });
  ativo.checked = p ? p.ativo : true;

  const node = el("div", { class: "form" },
    el("div", { class: "row" },
      el("div", {}, el("label", {}, "Nome"), nome),
      el("div", {}, el("label", {}, "Branch principal"), branch),
    ),
    el("div", {}, el("label", {}, "Slug ", el("span", { class: "opt" }, "(opcional)")), slug),
    el("div", {}, el("label", {}, "Pasta do projeto"), pasta,
      el("div", { class: "hint", text: "Precisa ser um repositório git. Os worktrees das demandas são criados fora desta pasta, em %PRAXIS_HOME%\\worktrees." })),
    el("div", { class: "row" },
      el("div", {}, el("label", {}, "Modo de integração"), modo),
      el("div", {}, el("label", {}, "URL da plataforma ", el("span", { class: "opt" }, "(para abrir MR)")), url),
    ),
    el("div", {}, el("label", {}, "Diretórios adicionais (add_dirs)"), addDirs,
      el("div", { class: "hint", text: "Repositórios extras que o agente pode editar. Um caminho por linha." })),
    el("label", { style: "display:flex;align-items:center;gap:8px;font-weight:600" }, ativo, "Projeto ativo"),
  );

  function ler() {
    const v = {
      nome: nome.value.trim(),
      slug: slug.value.trim(),
      branch_principal: branch.value.trim(),
      pasta: pasta.value.trim(),
      modo_integracao: modo.value,
      url_plataforma: url.value.trim(),
      add_dirs: addDirs.value.split("\n").map((l) => l.trim()).filter((l) => l !== ""),
      ativo: ativo.checked,
    };
    if (!v.nome) throw "Nome é obrigatório.";
    if (!v.pasta) throw "Pasta é obrigatória.";
    return v;
  }
  return { node, ler };
}

function renderEdicao(p) {
  const painel = limpar(document.getElementById("painel-projeto"));
  const criando = p == null;
  painel.append(el("h3", {}, criando ? "Novo projeto" : `${p.nome} — parâmetros`));

  const core = camposCore(p);
  painel.append(core.node);

  const acoes = el("div", { class: "form acoes", style: "margin-top:14px" });
  const btnSalvar = el("button", { class: "btn" }, criando ? "Cadastrar" : "Salvar");
  btnSalvar.onclick = () => salvarCore(p, core, btnSalvar);
  acoes.append(btnSalvar);
  painel.append(acoes);

  if (!criando) {
    painel.append(el("div", { style: "border-top:1px solid var(--border);margin:20px 0 4px" }));
    painel.append(el("h3", {}, "Acesso ", el("small", {}, "quem enxerga este projeto")));
    renderAcesso(painel, p);

    painel.append(el("div", { style: "border-top:1px solid var(--border);margin:20px 0 4px" }));
    painel.append(el("h3", {}, "Overview do repositório ", el("small", {}, "contexto das consultas")));
    renderOverview(painel, p);

    painel.append(el("div", { style: "border-top:1px solid var(--border);margin:20px 0 4px" }));
    painel.append(el("h3", {}, "Parâmetros ", el("small", {}, "override do global")));
    renderOverride(painel, p);
  }
}

// renderAcesso desenha a ACL de visibilidade do projeto: chips de grupos de
// usuários e de usuários liberados. Nada selecionado = projeto aberto a todos os
// usuários autenticados; com seleção, só os liberados (além de administradores e
// de quem gerencia projetos) enxergam o projeto, suas demandas e consultas.
async function renderAcesso(painel, p) {
  let acesso;
  try {
    acesso = await api.obterAcessoProjeto(p.id);
  } catch (e) {
    painel.append(el("p", { class: "sub", text: "Falha ao carregar o acesso: " + e.message }));
    return;
  }

  const gruposSel = new Set((acesso.grupos || []).map((g) => g.id));
  const usuariosSel = new Set((acesso.usuarios || []).map((u) => u.id));
  const dispGrupos = (acesso.disponiveis && acesso.disponiveis.grupos) || [];
  const dispUsuarios = (acesso.disponiveis && acesso.disponiveis.usuarios) || [];

  const status = el("div", { class: "hint" });
  const repintarStatus = () => {
    status.textContent = gruposSel.size + usuariosSel.size === 0
      ? "Sem restrição: todos os usuários autenticados enxergam este projeto."
      : "Restrito: só os grupos/usuários selecionados (e administradores ou quem gerencia projetos) enxergam este projeto, suas demandas e consultas.";
  };

  // chips toggle (mesmo padrão dos papéis em Usuários).
  const chipsDe = (itens, selecionados) => {
    const box = el("div", { class: "papel-chips" });
    const repintar = () => {
      limpar(box);
      if (itens.length === 0) {
        box.append(el("span", { class: "sub", text: "nenhum cadastrado" }));
        return;
      }
      for (const it of itens) {
        const on = selecionados.has(it.id);
        box.append(el("span", { class: "papel-chip" + (on ? " on" : ""), text: it.nome,
          onclick: () => { on ? selecionados.delete(it.id) : selecionados.add(it.id); repintar(); repintarStatus(); } }));
      }
    };
    repintar();
    return box;
  };

  repintarStatus();

  const btnSalvar = el("button", { class: "btn", text: "Salvar acesso" });
  btnSalvar.onclick = async () => {
    btnSalvar.disabled = true;
    try {
      const salvo = await api.definirAcessoProjeto(p.id, {
        grupos: [...gruposSel], usuarios: [...usuariosSel],
      });
      toast(salvo.restrito ? "Acesso restrito salvo." : "Acesso liberado para todos.", "ok");
    } catch (e) {
      bannerErro("Falha ao salvar o acesso: " + e.message);
      toast("Falha ao salvar.", "err");
    } finally {
      btnSalvar.disabled = false;
    }
  };

  painel.append(el("div", { class: "form" },
    el("div", {}, el("label", {}, "Grupos de usuários"), chipsDe(dispGrupos, gruposSel)),
    el("div", {}, el("label", {}, "Usuários ", el("span", { class: "opt" }, "(acesso individual)")), chipsDe(dispUsuarios, usuariosSel)),
    status,
    el("div", { class: "acoes" }, btnSalvar),
  ));
}

// renderOverview desenha a edição do overview do repositório: texto de negócio
// (sem código) que orienta o consultor da feature de Consultas. Pode ser escrito
// à mão ou gerado pelo harness em background.
function renderOverview(painel, p) {
  const ed = mdEditor({
    rows: 10,
    placeholder: "Objetivo do sistema, domínio, principais módulos, fluxos de negócio… (markdown, sem código)",
    valor: p.overview_md || "",
    // quem já tem overview quase sempre quer lê-lo, não editá-lo.
    abrirEmPreview: !!(p.overview_md || "").trim(),
  });
  const info = el("div", { class: "hint", text: p.overview_em
    ? "Última atualização: " + p.overview_em
    : "Ainda sem overview — as consultas deste projeto terão menos contexto." });

  const btnSalvar = el("button", { class: "btn", text: "Salvar overview" });
  btnSalvar.onclick = async () => {
    btnSalvar.disabled = true;
    try {
      await api.salvarOverview(p.id, ed.ta.value);
      toast("Overview salvo.", "ok");
    } catch (e) {
      bannerErro("Falha ao salvar overview: " + e.message);
    } finally {
      btnSalvar.disabled = false;
    }
  };

  const btnGerar = el("button", { class: "btn ghost", text: "Gerar com o Praxis" });
  btnGerar.onclick = async () => {
    btnGerar.disabled = true;
    try {
      await api.gerarOverview(p.id);
      toast("Gerando overview em background — leva alguns minutos. Use \"Recarregar\" para ver o resultado.", "ok");
    } catch (e) {
      bannerErro("Falha ao disparar a geração: " + e.message);
    } finally {
      btnGerar.disabled = false;
    }
  };

  const btnRecarregar = el("button", { class: "btn ghost", text: "Recarregar" });
  btnRecarregar.onclick = async () => {
    try {
      const atual = await api.obterProjeto(p.id);
      renderEdicao(atual);
    } catch (e) {
      bannerErro("Falha ao recarregar: " + e.message);
    }
  };

  painel.append(el("div", { class: "form" },
    el("div", {}, ed.no, info),
    el("div", { class: "acoes" }, btnSalvar, btnGerar, btnRecarregar),
  ));
}

async function salvarCore(p, core, btn) {
  let corpo;
  try {
    corpo = core.ler();
  } catch (msg) {
    bannerErro(String(msg));
    return;
  }
  bannerErro("");
  btn.disabled = true;
  try {
    if (p == null) {
      const criado = await api.criarProjeto(corpo);
      toast("Projeto cadastrado.", "ok");
      selecionadoID = criado.id;
      await recarregarLista();
      const completo = await api.obterProjeto(criado.id);
      renderEdicao(completo);
    } else {
      await api.atualizarProjeto(p.id, corpo);
      toast("Projeto salvo.", "ok");
      await recarregarLista();
      const completo = await api.obterProjeto(p.id);
      renderEdicao(completo);
    }
  } catch (e) {
    bannerErro("Falha ao salvar projeto: " + e.message);
    toast("Falha ao salvar.", "err");
  } finally {
    btn.disabled = false;
  }
}

// renderOverride desenha os campos de config do projeto com herança explícita:
// cada campo tem um checkbox "herdar do global". Herdado → não persiste (cai no
// global); desmarcado → persiste como override.
async function renderOverride(painel, p) {
  let override, desconhecidas;
  try {
    override = (await api.obterConfigProjeto(p.id)) || {};
  } catch (e) {
    painel.append(el("p", { class: "sub", text: "Falha ao carregar overrides: " + e.message }));
    return;
  }
  desconhecidas = preservarDesconhecidas(override);

  const form = el("div", { class: "form" });
  const campos = camposDoEscopo("project");
  const controles = new Map();

  for (const c of campos) {
    const temOverride = Object.prototype.hasOwnProperty.call(override, c.chave);
    const valorTexto = jsonParaTexto(override[c.chave], c.tipo);
    const entrada = c.tipo === "lines"
      ? el("textarea", {}, valorTexto)
      : el("input", { type: c.tipo === "number" ? "number" : "text", step: c.tipo === "number" ? "any" : null, value: valorTexto });
    const chkHerda = el("input", { type: "checkbox" });
    chkHerda.checked = !temOverride;

    const campo = el("div", { class: "cfg-field" + (chkHerda.checked ? " herdado" : "") },
      el("div", { class: "cfg-head" },
        el("label", {}, c.rotulo),
        el("label", { class: "cfg-herda" }, chkHerda, "herdar do global"),
      ),
      entrada,
      c.hint ? el("div", { class: "hint", text: c.hint }) : null,
    );
    const sync = () => {
      entrada.disabled = chkHerda.checked;
      campo.classList.toggle("herdado", chkHerda.checked);
    };
    chkHerda.addEventListener("change", sync);
    sync();
    controles.set(c.chave, { chkHerda, entrada, tipo: c.tipo, rotulo: c.rotulo });
    form.append(campo);
  }

  const btnSalvar = el("button", { class: "btn" }, "Salvar overrides");
  const btnEfetiva = el("button", { class: "btn ghost", type: "button" }, "Ver config efetiva");
  const boxEfetiva = el("div", {});
  btnSalvar.onclick = () => salvarOverride(p, controles, desconhecidas, btnSalvar, boxEfetiva);
  btnEfetiva.onclick = () => verEfetiva(p, btnEfetiva, boxEfetiva);
  form.append(el("div", { class: "acoes" }, btnSalvar, btnEfetiva));
  form.append(boxEfetiva);
  painel.append(form);
}

async function salvarOverride(p, controles, desconhecidas, btn, boxEfetiva) {
  const entradas = { ...desconhecidas };
  for (const [chave, ctl] of controles) {
    if (ctl.chkHerda.checked) continue; // herdado: não persiste
    const r = textoParaJSON(ctl.entrada.value, ctl.tipo);
    if (ctl.tipo === "number" && !r.ok) {
      bannerErro(`"${ctl.rotulo}" deve ser um número (ou marque "herdar do global").`);
      return;
    }
    entradas[chave] = r.valor;
  }
  bannerErro("");
  btn.disabled = true;
  try {
    await api.definirConfigProjeto(p.id, entradas);
    toast("Overrides salvos.", "ok");
    if (!boxEfetiva.firstChild) return;
    await verEfetiva(p, null, boxEfetiva, true); // atualiza a efetiva se estava aberta
  } catch (e) {
    bannerErro("Falha ao salvar overrides: " + e.message);
    toast("Falha ao salvar.", "err");
  } finally {
    btn.disabled = false;
  }
}

async function verEfetiva(p, btn, box, forcar) {
  if (!forcar && box.firstChild) { // toggle: já aberta → fecha
    limpar(box);
    if (btn) btn.textContent = "Ver config efetiva";
    return;
  }
  if (btn) btn.disabled = true;
  try {
    const efetiva = await api.configEfetiva(p.id);
    limpar(box);
    const linhas = Object.keys(efetiva).sort().map((k) => {
      const v = efetiva[k];
      return el("tr", {},
        el("td", { text: k }),
        el("td", { text: JSON.stringify(v.valor) }),
        el("td", {}, el("span", { class: `pill origem origem-${v.origem}`, text: v.origem })),
      );
    });
    const conteudo = linhas.length
      ? el("table", { class: "plain" },
          el("thead", {}, el("tr", {}, el("th", {}, "chave"), el("th", {}, "valor"), el("th", {}, "origem"))),
          el("tbody", {}, ...linhas))
      : el("p", { class: "sub", style: "margin:0", text: "Nenhuma chave de config definida (tudo no default do sistema)." });
    box.append(el("div", { class: "efetiva" },
      el("div", { class: "hint", style: "margin:0 0 8px", text: "Resolução global × override deste projeto (origem indica de onde veio cada chave)." }),
      conteudo));
    if (btn) btn.textContent = "Ocultar config efetiva";
  } catch (e) {
    bannerErro("Falha ao obter config efetiva: " + e.message);
  } finally {
    if (btn) btn.disabled = false;
  }
}
