// Tela "Projetos" — CRUD de projetos (Fase 1c) + edição do override de config do
// projeto (Fase 1e) com herança explícita do global, e "Ver config efetiva".

import { api } from "./api.js";
import { el, limpar, toast, bannerErro, mdEditor } from "./ui.js";
import { camposDoEscopo, campoEntrada, listasDinamicas, jsonParaTexto, textoParaJSON, preservarDesconhecidas } from "./config-fields.js";
import { GRUPOS_EVENTOS, resolverEventos } from "./notify-events.js";
import { t } from "./i18n.js";
import { escolherPasta } from "./pasta-picker.js";

let projetos = [];
let selecionadoID = null;

// rotuloValor traduz um valor vindo da API (ex.: modo_integracao, origem) pelo
// catálogo (prefixo + valor); sem chave correspondente, exibe o valor cru.
function rotuloValor(prefixo, valor) {
  const chave = prefixo + valor;
  const texto = t(chave);
  return texto === chave ? valor : texto;
}

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
    bannerErro(t("projetos.falha_carregar", { erro: e.message }));
    return;
  }
  bannerErro("");
  const lista = limpar(document.getElementById("lista-projetos"));
  if (projetos.length === 0) {
    lista.append(el("p", { class: "sub", text: t("projetos.nenhum") }));
    return;
  }
  for (const p of projetos) {
    const item = el("div", { class: "list-item" + (p.id === selecionadoID ? " sel" : ""), onclick: () => selecionar(p.id) },
      el("b", { text: p.nome }),
      el("div", { class: "path", text: t("projetos.pasta_branch", { pasta: p.pasta, branch: p.branch_principal }) }),
      el("div", { class: "meta" },
        p.ativo ? el("span", { class: "pill" }, el("span", { class: "dot dot-good" }), t("projetos.ativo"))
                : el("span", { class: "pill" }, el("span", { class: "dot dot-muted" }), t("projetos.inativo")),
        el("span", { class: "pill", text: rotuloValor("projetos.modo.", p.modo_integracao) }),
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
    bannerErro(t("projetos.falha_obter", { erro: e.message }));
    return;
  }
  renderEdicao(p);
}

function limparPainel() {
  const painel = limpar(document.getElementById("painel-projeto"));
  painel.append(el("p", { class: "sub", style: "margin:0", text: t("view.projetos.selecione") }));
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
  const slug = el("input", { value: p ? p.slug : "", placeholder: t("projetos.ph_slug") });
  const branch = el("input", { value: p ? p.branch_principal : "main" });
  const pasta = el("input", { value: p ? p.pasta : "", placeholder: t("projetos.ph_pasta") });
  // Seletor de pasta: navega o disco DO SERVIDOR (a pasta do projeto vive lá,
  // não na máquina do navegador) e preenche o campo com o caminho escolhido.
  const btnProcurar = el("button", { class: "btn ghost sm", type: "button", text: t("projetos.procurar_pasta") });
  btnProcurar.onclick = async () => {
    const escolhida = await escolherPasta(pasta.value.trim());
    if (escolhida) pasta.value = escolhida;
  };
  const modo = el("select", {},
    el("option", { value: "merge_request", selected: !p || p.modo_integracao === "merge_request" }, t("projetos.modo.merge_request")),
    el("option", { value: "merge_local", selected: p && p.modo_integracao === "merge_local" }, t("projetos.modo.merge_local")),
  );
  const url = el("input", { value: p ? p.url_plataforma : "", placeholder: t("projetos.ph_url_plataforma") });
  const addDirs = el("textarea", { placeholder: t("projetos.ph_add_dirs") }, p && p.add_dirs ? p.add_dirs.join("\n") : "");
  const ativo = el("input", { type: "checkbox" });
  ativo.checked = p ? p.ativo : true;

  const node = el("div", { class: "form" },
    el("div", { class: "row" },
      el("div", {}, el("label", {}, t("projetos.nome")), nome),
      el("div", {}, el("label", {}, t("projetos.branch_principal")), branch),
    ),
    el("div", {}, el("label", {}, t("projetos.slug") + " ", el("span", { class: "opt" }, t("projetos.opcional"))), slug),
    el("div", {}, el("label", {}, t("projetos.pasta")),
      el("div", { class: "campo-pasta" }, pasta, btnProcurar),
      el("div", { class: "hint", text: t("projetos.pasta_hint") })),
    el("div", { class: "row" },
      el("div", {}, el("label", {}, t("projetos.modo_integracao")), modo),
      el("div", {}, el("label", {}, t("projetos.url_plataforma") + " ", el("span", { class: "opt" }, t("projetos.url_plataforma_opt"))), url),
    ),
    el("div", {}, el("label", {}, t("projetos.add_dirs")), addDirs,
      el("div", { class: "hint", text: t("projetos.add_dirs_hint") })),
    el("label", { style: "display:flex;align-items:center;gap:8px;font-weight:600" }, ativo, t("projetos.projeto_ativo")),
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
    if (!v.nome) throw t("projetos.nome_obrigatorio");
    if (!v.pasta) throw t("projetos.pasta_obrigatoria");
    return v;
  }
  return { node, ler };
}

function renderEdicao(p) {
  const painel = limpar(document.getElementById("painel-projeto"));
  const criando = p == null;
  painel.append(el("h3", {}, criando ? t("projetos.novo") : t("projetos.titulo_parametros", { nome: p.nome })));

  const core = camposCore(p);
  painel.append(core.node);

  const acoes = el("div", { class: "form acoes", style: "margin-top:14px" });
  const btnSalvar = el("button", { class: "btn" }, criando ? t("projetos.cadastrar") : t("projetos.salvar"));
  btnSalvar.onclick = () => salvarCore(p, core, btnSalvar);
  acoes.append(btnSalvar);
  painel.append(acoes);

  if (!criando) {
    painel.append(el("div", { style: "border-top:1px solid var(--border);margin:20px 0 4px" }));
    painel.append(el("h3", {}, t("projetos.sec_acesso") + " ", el("small", {}, t("projetos.sec_acesso_sub"))));
    renderAcesso(painel, p);

    painel.append(el("div", { style: "border-top:1px solid var(--border);margin:20px 0 4px" }));
    painel.append(el("h3", {}, t("projetos.sec_overview") + " ", el("small", {}, t("projetos.sec_overview_sub"))));
    renderOverview(painel, p);

    painel.append(el("div", { style: "border-top:1px solid var(--border);margin:20px 0 4px" }));
    painel.append(el("h3", {}, t("projetos.sec_parametros") + " ", el("small", {}, t("projetos.sec_parametros_sub"))));
    renderOverride(painel, p);

    painel.append(el("div", { style: "border-top:1px solid var(--border);margin:20px 0 4px" }));
    painel.append(el("h3", {}, t("projetos.sec_notificacoes") + " ", el("small", {}, t("projetos.sec_notificacoes_sub"))));
    renderNotificacoes(painel, p);
  }
}

// renderNotificacoes desenha a seleção de notificações do projeto. Por padrão o
// projeto usa os eventos padrão do sistema; ao personalizar, escolhe evento a
// evento quais notificam. Os canais/tokens são sempre os globais (definidos em
// Configurações) — aqui só se decide QUAIS eventos deste projeto disparam.
async function renderNotificacoes(painel, p) {
  let override, global;
  try {
    [override, global] = await Promise.all([api.obterConfigProjeto(p.id), api.obterConfigGlobal()]);
  } catch (e) {
    painel.append(el("p", { class: "sub", text: t("projetos.notif_falha_carregar", { erro: e.message }) }));
    return;
  }
  const ov = (override && override["notificacoes"]) || {};
  const temOverride = Object.prototype.hasOwnProperty.call(override || {}, "notificacoes");
  const personalizar = temOverride && ov.usar_padrao === false;

  // Base = padrão do sistema (para preencher os eventos ainda não personalizados).
  const base = resolverEventos(((global && global["notificacoes"]) || {}).eventos);
  const eventos = resolverEventos(ov.eventos, base);

  const form = el("div", { class: "form" });

  const chkPersonalizar = el("input", { type: "checkbox" });
  chkPersonalizar.checked = personalizar;
  form.append(el("label", { class: "notif-modo" }, chkPersonalizar,
    el("span", {}, el("b", {}, t("projetos.notif_personalizar")),
      el("div", { class: "hint", style: "margin:2px 0 0", text: t("projetos.notif_personalizar_hint") }))));

  const evtCtl = new Map();
  const box = el("div", { class: "notif-projeto-eventos" });
  for (const g of GRUPOS_EVENTOS) {
    const grid = el("div", { class: "notif-eventos" });
    for (const ev of g.itens) {
      const chk = el("input", { type: "checkbox" });
      chk.checked = eventos[ev.tipo];
      evtCtl.set(ev.tipo, chk);
      grid.append(el("label", { class: "notif-evento" }, chk, ev.rotulo));
    }
    box.append(el("div", { class: "notif-grupo" }, el("div", { class: "notif-grupo-tit", text: g.grupo }), grid));
  }
  form.append(box);

  const sync = () => {
    box.hidden = !chkPersonalizar.checked;
  };
  chkPersonalizar.addEventListener("change", sync);
  sync();

  const btn = el("button", { class: "btn", text: t("projetos.notif_salvar") });
  btn.onclick = () => salvarNotificacoesProjeto(p, chkPersonalizar, evtCtl, btn);
  form.append(el("div", { class: "acoes" }, btn));
  painel.append(form);
}

async function salvarNotificacoesProjeto(p, chkPersonalizar, evtCtl, btn) {
  bannerErro("");
  btn.disabled = true;
  try {
    // read-modify-write do override do projeto: preserva as demais chaves.
    const atual = (await api.obterConfigProjeto(p.id)) || {};
    if (chkPersonalizar.checked) {
      const eventos = {};
      for (const [tipo, chk] of evtCtl) eventos[tipo] = chk.checked;
      atual["notificacoes"] = { usar_padrao: false, eventos };
    } else {
      // Volta ao padrão do sistema: remove o override de notificações.
      delete atual["notificacoes"];
    }
    await api.definirConfigProjeto(p.id, atual);
    toast(chkPersonalizar.checked ? t("projetos.notif_salvas") : t("projetos.notif_padrao"), "ok");
  } catch (e) {
    bannerErro(t("projetos.notif_falha_salvar", { erro: e.message }));
    toast(t("projetos.falha_salvar"), "err");
  } finally {
    btn.disabled = false;
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
    painel.append(el("p", { class: "sub", text: t("projetos.acesso_falha_carregar", { erro: e.message }) }));
    return;
  }

  const gruposSel = new Set((acesso.grupos || []).map((g) => g.id));
  const usuariosSel = new Set((acesso.usuarios || []).map((u) => u.id));
  const dispGrupos = (acesso.disponiveis && acesso.disponiveis.grupos) || [];
  const dispUsuarios = (acesso.disponiveis && acesso.disponiveis.usuarios) || [];

  const status = el("div", { class: "hint" });
  const repintarStatus = () => {
    status.textContent = gruposSel.size + usuariosSel.size === 0
      ? t("projetos.acesso_sem_restricao")
      : t("projetos.acesso_restrito");
  };

  // chips toggle (mesmo padrão dos papéis em Usuários).
  const chipsDe = (itens, selecionados) => {
    const box = el("div", { class: "papel-chips" });
    const repintar = () => {
      limpar(box);
      if (itens.length === 0) {
        box.append(el("span", { class: "sub", text: t("projetos.nenhum_cadastrado") }));
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

  const btnSalvar = el("button", { class: "btn", text: t("projetos.acesso_salvar") });
  btnSalvar.onclick = async () => {
    btnSalvar.disabled = true;
    try {
      const salvo = await api.definirAcessoProjeto(p.id, {
        grupos: [...gruposSel], usuarios: [...usuariosSel],
      });
      toast(salvo.restrito ? t("projetos.acesso_restrito_salvo") : t("projetos.acesso_liberado"), "ok");
    } catch (e) {
      bannerErro(t("projetos.acesso_falha_salvar", { erro: e.message }));
      toast(t("projetos.falha_salvar"), "err");
    } finally {
      btnSalvar.disabled = false;
    }
  };

  painel.append(el("div", { class: "form" },
    el("div", {}, el("label", {}, t("projetos.grupos_usuarios")), chipsDe(dispGrupos, gruposSel)),
    el("div", {}, el("label", {}, t("projetos.usuarios") + " ", el("span", { class: "opt" }, t("projetos.usuarios_opt"))), chipsDe(dispUsuarios, usuariosSel)),
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
    placeholder: t("projetos.overview_ph"),
    valor: p.overview_md || "",
    // quem já tem overview quase sempre quer lê-lo, não editá-lo.
    abrirEmPreview: !!(p.overview_md || "").trim(),
  });
  const info = el("div", { class: "hint", text: p.overview_em
    ? t("projetos.overview_atualizado", { em: p.overview_em })
    : t("projetos.overview_vazio") });

  const btnSalvar = el("button", { class: "btn", text: t("projetos.overview_salvar") });
  btnSalvar.onclick = async () => {
    btnSalvar.disabled = true;
    try {
      await api.salvarOverview(p.id, ed.ta.value);
      toast(t("projetos.overview_salvo"), "ok");
    } catch (e) {
      bannerErro(t("projetos.overview_falha_salvar", { erro: e.message }));
    } finally {
      btnSalvar.disabled = false;
    }
  };

  const btnGerar = el("button", { class: "btn ghost", text: t("projetos.overview_gerar") });
  btnGerar.onclick = async () => {
    btnGerar.disabled = true;
    try {
      await api.gerarOverview(p.id);
      toast(t("projetos.overview_gerando"), "ok");
    } catch (e) {
      bannerErro(t("projetos.overview_falha_gerar", { erro: e.message }));
    } finally {
      btnGerar.disabled = false;
    }
  };

  const btnRecarregar = el("button", { class: "btn ghost", text: t("projetos.recarregar") });
  btnRecarregar.onclick = async () => {
    try {
      const atual = await api.obterProjeto(p.id);
      renderEdicao(atual);
    } catch (e) {
      bannerErro(t("projetos.falha_recarregar", { erro: e.message }));
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
      toast(t("projetos.cadastrado"), "ok");
      selecionadoID = criado.id;
      await recarregarLista();
      const completo = await api.obterProjeto(criado.id);
      renderEdicao(completo);
    } else {
      await api.atualizarProjeto(p.id, corpo);
      toast(t("projetos.salvo"), "ok");
      await recarregarLista();
      const completo = await api.obterProjeto(p.id);
      renderEdicao(completo);
    }
  } catch (e) {
    bannerErro(t("projetos.falha_salvar_projeto", { erro: e.message }));
    toast(t("projetos.falha_salvar"), "err");
  } finally {
    btn.disabled = false;
  }
}

// renderOverride desenha os campos de config do projeto com herança explícita:
// cada campo tem um checkbox "herdar do global". Herdado → não persiste (cai no
// global); desmarcado → persiste como override. O rótulo da opção vazia mostra
// o valor que vem do global (ou o padrão do sistema), para a herança ser um
// valor visível e não uma incógnita.
async function renderOverride(painel, p) {
  let override, desconhecidas;
  try {
    override = (await api.obterConfigProjeto(p.id)) || {};
  } catch (e) {
    painel.append(el("p", { class: "sub", text: t("projetos.override_falha_carregar", { erro: e.message }) }));
    return;
  }
  desconhecidas = preservarDesconhecidas(override);
  const dinamicas = await listasDinamicas();
  // A config global só serve para rotular a herança de cada campo; falhar aqui
  // não impede editar (o rótulo cai no padrão do sistema).
  let cfgGlobal = {};
  try {
    cfgGlobal = (await api.obterConfigGlobal()) || {};
  } catch { /* sem valor global exibido */ }

  const form = el("div", { class: "form" });
  const campos = camposDoEscopo("project");
  const controles = new Map();

  let grupoAtual = null;
  for (const c of campos) {
    if (c.grupo && c.grupo !== grupoAtual) {
      grupoAtual = c.grupo;
      form.append(el("h3", { class: "form-sub", text: c.grupo }));
    }
    const temOverride = Object.prototype.hasOwnProperty.call(override, c.chave);
    const valorTexto = jsonParaTexto(override[c.chave], c.tipo);
    const entrada = campoEntrada(c, valorTexto, { vazio: rotuloHerdado(c, cfgGlobal), dinamicas });
    const chkHerda = el("input", { type: "checkbox" });
    chkHerda.checked = !temOverride;

    const campo = el("div", { class: "cfg-field" + (chkHerda.checked ? " herdado" : "") },
      el("div", { class: "cfg-head" },
        el("label", {}, c.rotulo),
        el("label", { class: "cfg-herda" }, chkHerda, t("projetos.herdar_global")),
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

  const btnSalvar = el("button", { class: "btn" }, t("projetos.override_salvar"));
  const btnEfetiva = el("button", { class: "btn ghost", type: "button" }, t("projetos.efetiva_ver"));
  const boxEfetiva = el("div", {});
  btnSalvar.onclick = () => salvarOverride(p, controles, desconhecidas, btnSalvar, boxEfetiva);
  btnEfetiva.onclick = () => verEfetiva(p, btnEfetiva, boxEfetiva);
  form.append(el("div", { class: "acoes" }, btnSalvar, btnEfetiva));
  form.append(boxEfetiva);
  painel.append(form);
}

// rotuloHerdado descreve, para um campo, de onde vem o valor quando o projeto
// não sobrescreve: o valor da config global, quando ela define a chave, ou o
// padrão do sistema declarado no campo.
function rotuloHerdado(c, cfgGlobal) {
  const doGlobal = jsonParaTexto(cfgGlobal[c.chave], c.tipo);
  if (doGlobal !== "") return t("config.opcao_herdado", { valor: doGlobal.split("\n").join(" · ") });
  if (c.padrao) return t("config.opcao_herdado_padrao", { valor: c.padrao });
  return t("config.opcao_nao_definido");
}

async function salvarOverride(p, controles, desconhecidas, btn, boxEfetiva) {
  const entradas = { ...desconhecidas };
  for (const [chave, ctl] of controles) {
    if (ctl.chkHerda.checked) continue; // herdado: não persiste
    // Campo deixado em branco (ou na opção "herdar") equivale a herdar — só
    // "lines" persiste vazio, porque lista vazia é um override com sentido
    // ("este projeto não roda gates").
    if (ctl.tipo !== "lines" && ctl.entrada.value.trim() === "") continue;
    const r = textoParaJSON(ctl.entrada.value, ctl.tipo);
    if (ctl.tipo === "number" && !r.ok) {
      bannerErro(t("projetos.override_numero", { rotulo: ctl.rotulo }));
      return;
    }
    entradas[chave] = r.valor;
  }
  bannerErro("");
  btn.disabled = true;
  try {
    await api.definirConfigProjeto(p.id, entradas);
    toast(t("projetos.override_salvos"), "ok");
    if (!boxEfetiva.firstChild) return;
    await verEfetiva(p, null, boxEfetiva, true); // atualiza a efetiva se estava aberta
  } catch (e) {
    bannerErro(t("projetos.override_falha_salvar", { erro: e.message }));
    toast(t("projetos.falha_salvar"), "err");
  } finally {
    btn.disabled = false;
  }
}

async function verEfetiva(p, btn, box, forcar) {
  if (!forcar && box.firstChild) { // toggle: já aberta → fecha
    limpar(box);
    if (btn) btn.textContent = t("projetos.efetiva_ver");
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
        el("td", {}, el("span", { class: `pill origem origem-${v.origem}`, text: rotuloValor("projetos.origem.", v.origem) })),
      );
    });
    const conteudo = linhas.length
      ? el("table", { class: "plain" },
          el("thead", {}, el("tr", {}, el("th", {}, t("projetos.efetiva_chave")), el("th", {}, t("projetos.efetiva_valor")), el("th", {}, t("projetos.efetiva_origem")))),
          el("tbody", {}, ...linhas))
      : el("p", { class: "sub", style: "margin:0", text: t("projetos.efetiva_vazia") });
    box.append(el("div", { class: "efetiva" },
      el("div", { class: "hint", style: "margin:0 0 8px", text: t("projetos.efetiva_hint") }),
      conteudo));
    if (btn) btn.textContent = t("projetos.efetiva_ocultar");
  } catch (e) {
    bannerErro(t("projetos.efetiva_falha", { erro: e.message }));
  } finally {
    if (btn) btn.disabled = false;
  }
}
