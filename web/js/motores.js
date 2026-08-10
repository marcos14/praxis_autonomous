// Tela "Motores" — CRUD de motores e contas (Fase 1d). A ordem da lista é a
// prioridade (fallback); reordenar chama PUT /engines/ordem. Ligar/desligar é um
// PUT do motor. Cada motor abre um painel de detalhes com contas.

import { api } from "./api.js";
import { t } from "./i18n.js";
import { el, limpar, toast, bannerErro } from "./ui.js";

let motores = [];
let editandoID = null; // motor com painel de detalhes aberto (ou "novo")

export async function montarMotores() {
  await recarregar();
  document.getElementById("btn-novo-motor").onclick = () => {
    editandoID = "novo";
    renderPainel(null);
  };
  document.getElementById("btn-detectar-motores").onclick = () => detectar();
  document.getElementById("btn-uso-motores").onclick = () => alternarUso();
  if (editandoID != null && editandoID !== "novo") {
    const m = motores.find((x) => x.id === editandoID);
    if (m) renderPainel(m);
  }
}

async function recarregar() {
  try {
    motores = (await api.listarMotores()) || [];
  } catch (e) {
    bannerErro("Falha ao carregar motores: " + e.message);
    return;
  }
  bannerErro("");
  const lista = limpar(document.getElementById("lista-motores"));
  if (motores.length === 0) {
    lista.append(el("p", { class: "sub", text: t("motores.nenhum") }));
    return;
  }
  motores.forEach((m, i) => lista.append(linhaMotor(m, i)));
}

// rotuloVis traduz a visibilidade (chave estática por valor — o teste de i18n
// exige chaves literais em t()).
function rotuloVis(v) {
  if (v === "privada") return t("vis.privada");
  if (v === "grupo") return t("vis.grupo");
  return t("vis.publica");
}

function linhaMotor(m, i) {
  const nContas = (m.contas || []).length;
  const det = [
    m.modelo_exec ? `modelo ${m.modelo_exec} (exec)` : t("motores.det_sem_modelo"),
    m.modelo_analise ? `${m.modelo_analise} (análise)` : null,
    m.modelo_consulta ? `${m.modelo_consulta} (consultas)` : null,
    m.budget_fase_usd > 0 ? `budget US$ ${m.budget_fase_usd.toFixed(2)}/fase` : t("motores.det_sem_budget"),
    m.timeout_min > 0 ? `timeout ${m.timeout_min}min` : null,
    `${nContas} ${nContas === 1 ? "perfil" : "perfis"}`,
    m.fallback === false ? t("motores.det_fora_fallback") : null,
    m.visibilidade && m.visibilidade !== "publica"
      ? rotuloVis(m.visibilidade) + (m.dono_nome ? ` (${m.dono_nome})` : "")
      : null,
  ].filter(Boolean).join(" · ");

  const sw = el("button", { class: "switch" + (m.ativo ? " on" : ""), title: m.ativo ? t("motores.ativo") : t("motores.inativo"),
    onclick: () => toggleAtivo(m) });
  const subir = el("button", { title: t("motores.subir_prioridade"), disabled: i === 0, onclick: () => mover(i, i - 1) }, "▲");
  const descer = el("button", { title: t("motores.descer_prioridade"), disabled: i === motores.length - 1, onclick: () => mover(i, i + 1) }, "▼");

  return el("div", { class: "motor-row" },
    el("div", { class: "ord" }, subir, descer),
    el("span", { class: "pill", text: `${i + 1}º` }),
    el("span", { class: "nm", text: m.nome }),
    el("span", { class: "det", text: det }),
    el("button", { class: "btn sm ghost", onclick: () => { editandoID = m.id; renderPainel(m); } }, t("motores.editar")),
    sw,
  );
}

async function toggleAtivo(m) {
  try {
    await api.atualizarMotor(m.id, { ativo: !m.ativo });
    await recarregar();
    if (editandoID === m.id) {
      const atual = motores.find((x) => x.id === m.id);
      if (atual) renderPainel(atual);
    }
  } catch (e) {
    bannerErro("Falha ao ligar/desligar motor: " + e.message);
  }
}

async function mover(de, para) {
  if (para < 0 || para >= motores.length) return;
  const ids = motores.map((m) => m.id);
  const [x] = ids.splice(de, 1);
  ids.splice(para, 0, x);
  try {
    await api.reordenarMotores(ids);
    toast(t("motores.prioridade_atualizada"), "ok");
    await recarregar();
  } catch (e) {
    bannerErro("Falha ao reordenar: " + e.message);
  }
}

// ---------------------------------------------------------------------------
// Painel t("motores.uso_titulo"): consumo do Praxis por motor/perfil (hoje, 7 dias,
// total) + última leitura de franquia do vendor feita pelo monitor periódico.
// Enquanto o painel está aberto, atualiza sozinho a cada 60s.

let usoTimer = null;

function alternarUso() {
  const painel = document.getElementById("painel-uso");
  if (!painel.hidden) {
    fecharUso(painel);
    return;
  }
  painel.hidden = false;
  limpar(painel).append(el("p", { class: "sub", text: t("motores.uso_carregando") }));
  carregarUso(painel);
}

function fecharUso(painel) {
  if (usoTimer) { clearInterval(usoTimer); usoTimer = null; }
  painel.hidden = true;
  limpar(painel);
}

async function carregarUso(painel) {
  let dados;
  try {
    dados = await api.usoMotores();
  } catch (e) {
    limpar(painel).append(el("p", { class: "sub", text: "Falha ao carregar o uso: " + e.message }));
    return;
  }
  renderUso(painel, dados);
  if (!usoTimer) {
    usoTimer = setInterval(() => {
      if (painel.hidden || !painel.isConnected) { fecharUso(painel); return; }
      carregarUso(painel);
    }, 60_000);
  }
}

function renderUso(painel, dados) {
  limpar(painel);
  const cab = el("div", { style: "display:flex;align-items:center;gap:8px;flex-wrap:wrap" },
    el("h3", { style: "margin:0" }, t("motores.uso_titulo")),
    el("span", { class: "sub", style: "margin:0", text: `franquia verificada a cada ${dados.intervalo_min}min (ajustável em Configurações)` }),
    el("button", { class: "btn sm ghost", style: "margin-left:auto", onclick: () => carregarUso(painel) }, t("motores.atualizar")),
    el("button", { class: "btn sm ghost", onclick: () => fecharUso(painel) }, t("motores.fechar")),
  );
  painel.append(cab);

  const motoresAtivos = dados.motores || [];
  if (motoresAtivos.length === 0) {
    painel.append(el("p", { class: "sub", text: t("motores.uso_nenhum_ativo") }));
    return;
  }
  for (const m of motoresAtivos) {
    painel.append(el("div", { style: "margin:12px 0 4px;display:flex;align-items:center;gap:8px;flex-wrap:wrap" },
      el("b", { text: m.nome }),
      m.fallback === false ? el("span", { class: "pill", text: t("motores.pill_fora_fallback") }) : null,
    ));
    const corpo = el("tbody", {});
    const perfis = m.perfis || [];
    if (perfis.length === 0 && !m.uso_sem_perfil) {
      corpo.append(el("tr", {}, el("td", { colspan: "5", class: "sub", text: t("motores.uso_nenhum_perfil") })));
    }
    for (const p of perfis) {
      corpo.append(el("tr", {},
        el("td", { text: p.alias }),
        el("td", {}, celulaUsoPraxis(p.uso_praxis && p.uso_praxis.hoje)),
        el("td", {}, celulaUsoPraxis(p.uso_praxis && p.uso_praxis.ultimos_7_dias)),
        el("td", {}, celulaUsoPraxis(p.uso_praxis && p.uso_praxis.total)),
        el("td", {}, celulaFranquia(p.franquia)),
      ));
    }
    if (m.uso_sem_perfil) {
      corpo.append(el("tr", {},
        el("td", { class: "sub", text: t("motores.uso_sem_perfil") }),
        el("td", {}, celulaUsoPraxis(m.uso_sem_perfil.hoje)),
        el("td", {}, celulaUsoPraxis(m.uso_sem_perfil.ultimos_7_dias)),
        el("td", {}, celulaUsoPraxis(m.uso_sem_perfil.total)),
        el("td", { class: "sub", text: "—" }),
      ));
    }
    painel.append(el("table", { class: "plain" },
      el("thead", {}, el("tr", {},
        el("th", {}, t("motores.col_perfil")), el("th", {}, t("motores.col_hoje")), el("th", {}, t("motores.col_7dias")),
        el("th", {}, t("motores.col_total")), el("th", {}, t("motores.col_franquia")))),
      corpo));
  }
  painel.append(el("div", { class: "hint", text: t("motores.uso_hint") }));
}

function celulaUsoPraxis(j) {
  if (!j || !j.execucoes) return el("span", { class: "sub", style: "margin:0", text: "—" });
  const custo = j.custo_usd > 0 ? ` · US$ ${j.custo_usd.toFixed(2)}` : "";
  const tokens = (j.tokens_in || j.tokens_out) ? ` · ${fmtTokens(j.tokens_in + j.tokens_out)} tok` : "";
  return el("span", { text: `${j.execucoes} exec${custo}${tokens}` });
}

function fmtTokens(n) {
  if (n >= 1e6) return (n / 1e6).toFixed(1) + "M";
  if (n >= 1e3) return (n / 1e3).toFixed(1) + "k";
  return String(n);
}

function celulaFranquia(f) {
  if (!f) return el("span", { class: "sub", style: "margin:0", text: t("motores.franquia_aguardando") });
  const quando = f.verificado_em ? new Date(f.verificado_em) : null;
  const hhmm = quando ? quando.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" }) : "";
  if (!f.disponivel) {
    return el("span", { class: "sub", style: "margin:0", title: hhmm ? `verificado às ${hhmm}` : "", text: f.mensagem || t("motores.indisponivel") });
  }
  const linhas = (f.janelas || []).map((j) => {
    const pct = Math.max(0, Math.min(100, j.usado_pct));
    const barra = el("div", { style: "background:var(--border);border-radius:4px;height:6px;width:120px;overflow:hidden" },
      el("div", { style: `height:100%;width:${pct}%;border-radius:4px;background:${pct >= 90 ? "var(--err, #d33)" : pct >= 70 ? "orange" : "var(--ok, #2a2)"}` }));
    const reset = j.reset_em ? ` · reset ${new Date(j.reset_em).toLocaleString([], { day: "2-digit", month: "2-digit", hour: "2-digit", minute: "2-digit" })}` : "";
    return el("div", { style: "display:flex;align-items:center;gap:6px;margin:2px 0" },
      el("span", { class: "sub", style: "margin:0;min-width:52px", text: j.rotulo }),
      barra,
      el("span", { class: "sub", style: "margin:0", text: `${pct.toFixed(0)}%${reset}` }));
  });
  const cont = el("div", { title: hhmm ? `verificado às ${hhmm}` : "" }, ...linhas);
  return linhas.length ? cont : el("span", { class: "sub", style: "margin:0", text: t("motores.franquia_sem_janelas") });
}

// detectar consulta o servidor sobre quais harnesses estão instalados e como o
// ambiente está configurado, e mostra sugestões de cadastro num painel.
async function detectar() {
  const painel = document.getElementById("painel-deteccao");
  painel.hidden = false;
  limpar(painel);
  painel.append(el("p", { class: "sub", text: t("motores.det_analisando") }));
  let sugestoes;
  try {
    sugestoes = (await api.detectarMotores()) || [];
  } catch (e) {
    limpar(painel);
    painel.append(el("p", { class: "sub", text: "Falha ao detectar motores: " + e.message }));
    return;
  }
  renderDeteccao(painel, sugestoes);
}

function renderDeteccao(painel, sugestoes) {
  limpar(painel);
  painel.append(el("h3", {}, "Detecção no ambiente ",
    el("small", {}, t("motores.det_subtitulo"))));

  const pendentes = sugestoes.filter((s) => s.instalado && !s.ja_cadastrado);
  const cabecalho = el("div", { style: "display:flex;align-items:center;gap:8px;margin-bottom:12px;flex-wrap:wrap" });
  const btnFechar = el("button", { class: "btn sm ghost", onclick: () => { painel.hidden = true; limpar(painel); } }, t("motores.fechar"));
  if (pendentes.length > 0) {
    const btnTodos = el("button", { class: "btn sm" }, `Cadastrar detectados (${pendentes.length})`);
    btnTodos.onclick = () => autocadastrar(btnTodos);
    cabecalho.append(btnTodos);
  }
  cabecalho.append(btnFechar);
  painel.append(cabecalho);

  for (const s of sugestoes) {
    painel.append(cardSugestao(s));
  }
}

function cardSugestao(s) {
  const dot = s.ja_cadastrado ? "dot-good" : s.instalado ? "dot-blue" : "dot-muted";
  const estado = s.ja_cadastrado ? t("motores.det_ja_cadastrado") : s.instalado ? t("motores.det_detectado") : t("motores.det_nao_instalado");
  const modelos = [
    s.modelo_exec ? `exec ${s.modelo_exec}` : null,
    s.modelo_analise ? `análise ${s.modelo_analise}` : null,
    s.modelo_consulta ? `consultas ${s.modelo_consulta}` : null,
  ].filter(Boolean).join(" · ") || t("motores.det_modelo_harness");

  const vars = (s.variaveis || []).filter((v) => v.definida);
  const varsTxt = vars.length
    ? vars.map((v) => v.sensivel ? `${v.nome}=••••` : `${v.nome}=${v.valor}`).join("  ")
    : t("motores.det_sem_vars");

  const contasTxt = (s.contas || []).length
    ? "contas do ambiente: " + s.contas.map((c) => `${c.alias} → ${c.config_dir}`).join(", ")
    : null;

  const linhas = [
    el("div", { style: "display:flex;align-items:center;gap:8px;flex-wrap:wrap" },
      el("span", { class: "pill" }, el("span", { class: "dot " + dot }), " " + estado),
      el("b", { text: s.nome }),
      s.caminho_cli ? el("span", { class: "sub", style: "margin:0", text: s.caminho_cli }) : null,
    ),
    el("div", { class: "sub", style: "margin:6px 0 0", text: "modelos sugeridos: " + modelos }),
    el("div", { class: "sub", style: "margin:2px 0 0", text: t("motores.det_vars", { vars: varsTxt }) }),
    contasTxt ? el("div", { class: "sub", style: "margin:2px 0 0", text: contasTxt }) : null,
    s.observacao ? el("div", { class: "sub", style: "margin:2px 0 0", text: s.observacao }) : null,
  ].filter(Boolean);

  if (s.instalado && !s.ja_cadastrado) {
    const btn = el("button", { class: "btn sm", style: "margin-top:8px" }, t("motores.det_cadastrar_este"));
    btn.onclick = () => cadastrarSugestao(s, btn);
    linhas.push(btn);
  }

  // Instalador (Fase D): CLI ausente ganha "Instalar" — o servidor baixa o
  // binário oficial do vendor para PRAXIS_HOME/tools/harness; CLI presente
  // ganha "Atualizar CLI" (re-download; o binário atual vira .old).
  const instalavel = ["claude", "codex", "opencode"].includes((s.nome || "").toLowerCase());
  if (instalavel && !s.instalado) {
    const btn = el("button", { class: "btn sm", style: "margin-top:8px" }, t("motores.inst_instalar"));
    btn.onclick = () => instalarHarness(s.nome, btn);
    linhas.push(btn);
  } else if (instalavel && s.instalado) {
    const btn = el("button", { class: "btn sm ghost", style: "margin-top:8px" }, t("motores.inst_atualizar"));
    btn.onclick = () => instalarHarness(s.nome, btn);
    linhas.push(btn);
  }

  return el("div", { class: "motor-row", style: "display:block" }, ...linhas);
}

// instalarHarness dispara o download do CLI oficial e acompanha o job até o
// fim, refletindo o progresso no próprio botão.
async function instalarHarness(vendor, btn) {
  const rotulo = btn.textContent;
  btn.disabled = true;
  bannerErro("");
  try {
    const aceite = await api.instalarHarness(vendor);
    for (;;) {
      const job = await api.obterInstalacao(aceite.job_id);
      if (job.status === "erro") throw new Error(job.detalhe || "download falhou");
      if (job.status === "concluido") {
        const versao = (job.info && job.info.versao) ? " " + job.info.versao : "";
        toast(t("motores.inst_ok", { vendor: vendor + versao }), "ok");
        break;
      }
      btn.textContent = job.detalhe || t("motores.inst_instalando");
      await new Promise((r) => setTimeout(r, 1200));
    }
    await detectar();
  } catch (e) {
    bannerErro(t("motores.inst_falha", { erro: e.message }));
    btn.disabled = false;
    btn.textContent = rotulo;
  }
}

async function cadastrarSugestao(s, btn) {
  if (btn) btn.disabled = true;
  bannerErro("");
  try {
    const criado = await api.criarMotor({
      nome: s.nome,
      modelo_exec: s.modelo_exec,
      modelo_analise: s.modelo_analise,
      modelo_consulta: s.modelo_consulta,
      budget_fase_usd: s.budget_fase_usd,
      timeout_min: s.timeout_min,
      params: {},
    });
    for (const c of s.contas || []) {
      try {
        await api.criarConta(criado.id, { alias: c.alias, config_dir: c.config_dir });
      } catch { /* conta duplicada/ inválida: ignora, o motor já foi criado */ }
    }
    toast(`Motor ${s.nome} cadastrado.`, "ok");
    await recarregar();
    await detectar();
  } catch (e) {
    bannerErro("Falha ao cadastrar motor: " + e.message);
    if (btn) btn.disabled = false;
  }
}

async function autocadastrar(btn) {
  if (btn) btn.disabled = true;
  bannerErro("");
  try {
    const criados = (await api.autocadastrarMotores()) || [];
    toast(criados.length ? `${criados.length} motor(es) cadastrado(s).` : t("motores.nada_novo"), "ok");
    await recarregar();
    await detectar();
  } catch (e) {
    bannerErro("Falha ao auto-cadastrar motores: " + e.message);
    if (btn) btn.disabled = false;
  }
}

function renderPainel(m) {
  const painel = document.getElementById("painel-motor");
  painel.hidden = false;
  limpar(painel);
  const criando = m == null;
  painel.append(el("h3", {}, criando ? t("motores.novo_motor") : `${m.nome} — detalhes`));

  const nome = el("input", { value: m ? m.nome : "", placeholder: "claude / codex / opencode" });
  const modeloExec = el("input", { value: m ? m.modelo_exec : "" });
  const modeloAnalise = el("input", { value: m ? m.modelo_analise : "" });
  const modeloConsulta = el("input", { value: m ? (m.modelo_consulta || "") : "", placeholder: t("motores.ph_modelo_consulta") });
  const budget = el("input", { type: "number", step: "0.5", value: m ? m.budget_fase_usd : 0 });
  const timeout = el("input", { type: "number", value: m ? m.timeout_min : 0 });
  const fallback = el("input", { type: "checkbox" });
  fallback.checked = criando ? true : m.fallback !== false;
  const params = el("textarea", {}, m && m.params ? prettyJSON(m.params) : "{}");
  // Visibilidade (Fase A): pública (todos), privada (só quem cadastrou) ou do
  // grupo. Só é enviada quando muda — enviar sempre redefiniria a ACL.
  const visInicial = m && m.visibilidade ? m.visibilidade : "publica";
  const visibilidade = el("select", {},
    el("option", { value: "publica", selected: visInicial === "publica" }, t("vis.publica")),
    el("option", { value: "privada", selected: visInicial === "privada" }, t("vis.privada")),
    el("option", { value: "grupo", selected: visInicial === "grupo" }, t("vis.grupo")),
  );

  const form = el("div", { class: "form" },
    el("div", {}, el("label", {}, t("motores.nome")), nome),
    el("div", { class: "row" },
      el("div", {}, el("label", {}, t("motores.modelo_exec")), modeloExec),
      el("div", {}, el("label", {}, t("motores.modelo_analise")), modeloAnalise),
    ),
    el("div", {}, el("label", {}, t("motores.modelo_consulta")), modeloConsulta,
      el("div", { class: "hint", text: t("motores.hint_modelo_consulta") })),
    el("div", { class: "row" },
      el("div", {}, el("label", {}, t("motores.budget_fase")), budget),
      el("div", {}, el("label", {}, t("motores.timeout_fase")), timeout),
    ),
    el("div", {},
      el("label", { style: "display:flex;align-items:center;gap:8px;cursor:pointer" }, fallback, t("motores.participa_fallback")),
      el("div", { class: "hint", text: t("motores.hint_fallback") })),
    el("div", {}, el("label", {}, t("motores.visibilidade")), visibilidade,
      el("div", { class: "hint", text: t("motores.vis_hint") })),
    el("div", {}, el("label", {}, "Params ", el("span", { class: "opt" }, t("motores.params_json"))), params,
      el("div", { class: "hint", text: t("motores.hint_params") })),
  );

  const btn = el("button", { class: "btn", style: "width:fit-content" }, criando ? t("motores.cadastrar") : t("configx.salvar"));
  btn.onclick = () => salvar(m, { nome, modeloExec, modeloAnalise, modeloConsulta, budget, timeout, fallback, params, visibilidade, visInicial }, btn);
  form.append(btn);
  painel.append(form);

  if (!criando) {
    painel.append(el("div", { style: "border-top:1px solid var(--border);margin:20px 0 8px" }));
    const variavel = m.nome.toLowerCase() === "codex" ? "CODEX_HOME" : m.nome.toLowerCase() === "claude" ? "CLAUDE_CONFIG_DIR" : "config_dir";
    painel.append(el("h3", {}, "Perfis ", el("small", {}, variavel)));
    renderContas(painel, m);
  }
}

function prettyJSON(raw) {
  try { return JSON.stringify(raw, null, 2); } catch { return "{}"; }
}

async function salvar(m, campos, btn) {
  const nome = campos.nome.value.trim();
  if (!nome) { bannerErro(t("motores.nome_obrigatorio")); return; }
  let params;
  try {
    params = JSON.parse(campos.params.value || "{}");
    if (params === null || typeof params !== "object" || Array.isArray(params)) throw new Error(t("motores.params_objeto"));
  } catch (e) {
    bannerErro(t("motores.params_invalido", { erro: e.message }));
    return;
  }
  const corpo = {
    nome,
    modelo_exec: campos.modeloExec.value.trim(),
    modelo_analise: campos.modeloAnalise.value.trim(),
    modelo_consulta: campos.modeloConsulta.value.trim(),
    budget_fase_usd: Number(campos.budget.value) || 0,
    timeout_min: Number(campos.timeout.value) || 0,
    fallback: campos.fallback.checked,
    params,
  };
  if (campos.visibilidade.value !== campos.visInicial) {
    corpo.visibilidade = campos.visibilidade.value;
    if (campos.visibilidade.value === "grupo" && m && m.grupo_id) corpo.grupo_id = m.grupo_id;
  }
  bannerErro("");
  btn.disabled = true;
  try {
    if (m == null) {
      const criado = await api.criarMotor(corpo);
      toast(t("motores.motor_cadastrado"), "ok");
      editandoID = criado.id;
      await recarregar();
      renderPainel(motores.find((x) => x.id === criado.id) || criado);
    } else {
      await api.atualizarMotor(m.id, corpo);
      toast(t("motores.motor_salvo"), "ok");
      await recarregar();
      renderPainel(motores.find((x) => x.id === m.id));
    }
  } catch (e) {
    bannerErro("Falha ao salvar motor: " + e.message);
    toast(t("configx.falha_salvar_toast"), "err");
  } finally {
    btn.disabled = false;
  }
}

function renderContas(painel, m) {
  const contas = m.contas || [];
  const suportaLogin = ["claude", "codex"].includes((m.nome || "").trim().toLowerCase());
  const corpo = el("tbody", {});
  if (contas.length === 0) {
    corpo.append(el("tr", {}, el("td", { colspan: "5", class: "sub", text: t("motores.sem_perfis") })));
  }
  for (const c of contas) {
    const sw = el("button", { class: "switch" + (c.ativo ? " on" : ""), onclick: () => toggleConta(m, c) });
    const estado = el("span", { class: "sub", style: "margin:0", text: suportaLogin ? t("motores.login_nao_verificado") : t("motores.indisponivel") });
    const acoes = el("div", { class: "acoes", style: "margin:0;gap:5px;flex-wrap:wrap" });
    if (suportaLogin) {
      let btnLogin;
      const btnVerificar = el("button", { class: "btn sm ghost", onclick: () => verificarLogin(m, c, estado, btnVerificar, btnLogin) }, t("motores.verificar"));
      btnLogin = el("button", { class: "btn sm", onclick: () => iniciarLogin(m, c, estado, acoes, btnLogin) }, t("motores.entrar_navegador"));
      acoes.append(btnVerificar, btnLogin);
      queueMicrotask(() => verificarLogin(m, c, estado, btnVerificar, btnLogin));
    }
    acoes.append(el("button", { class: "btn sm ghost", onclick: () => removerConta(m, c) }, t("demandas.remover")));
    corpo.append(el("tr", {},
      el("td", { text: c.alias }),
      el("td", { text: c.config_dir || "—" }),
      el("td", {}, sw),
      el("td", {}, estado),
      el("td", {}, acoes),
    ));
  }
  const tabela = el("table", { class: "plain" },
    el("thead", {}, el("tr", {}, el("th", {}, t("motores.col_perfil")), el("th", {}, t("motores.col_diretorio")), el("th", {}, t("motores.col_ativo")), el("th", {}, t("motores.col_login")), el("th", {}))),
    corpo);

  const alias = el("input", { placeholder: t("motores.ph_alias") });
  const configDir = el("input", { placeholder: t("motores.ph_config_dir") });
  const btnAdd = el("button", { class: "btn sm" }, t("motores.adicionar_perfil"));
  btnAdd.onclick = () => adicionarConta(m, alias, configDir, btnAdd);

  painel.append(el("div", { class: "form" },
    tabela,
    el("div", { class: "row" }, el("div", {}, alias), el("div", {}, configDir)),
    el("div", { class: "acoes" }, btnAdd),
    el("div", { class: "hint", text: suportaLogin
      ? t("motores.hint_perfis_login")
      : t("motores.hint_perfis_sem_login") }),
  ));
}

async function verificarLogin(m, c, estado, btn, btnLogin) {
  if (btn) btn.disabled = true;
  estado.textContent = t("motores.login_verificando");
  try {
    const d = await api.estadoAuthMotor(m.id, c.id);
    if (d.autenticado) {
      estado.textContent = t("motores.login_autenticado") + (d.metodo ? ` (${d.metodo})` : "");
      if (btnLogin) btnLogin.textContent = t("motores.trocar_login");
    } else if (d.estado === t("motores.login_deslogado")) {
      estado.textContent = t("motores.login_deslogado");
      if (btnLogin) btnLogin.textContent = t("motores.entrar_navegador");
    } else {
      estado.textContent = d.mensagem || t("motores.login_desconhecido");
    }
  } catch {
    estado.textContent = t("motores.login_falha_verificar");
  } finally {
    if (btn) btn.disabled = false;
  }
}

async function iniciarLogin(m, c, estado, acoes, btn) {
  // Nada é aberto automaticamente: a URL só serve no navegador de quem pediu o
  // login (muitas vezes outra máquina que não o servidor), então a tela exibe o
  // link e o campo para colar o código, e o usuário conduz o restante.
  btn.disabled = true;
  estado.textContent = t("motores.login_iniciando");
  const detalhe = el("div", { style: "width:100%;min-width:260px" });
  acoes.append(detalhe);
  try {
    let sessao = await api.iniciarLoginMotor(m.id, c.id);
    let versaoRenderizada = "";
    while (detalhe.isConnected) {
      // Re-renderiza só quando a sessão muda; render incondicional apagaria o
      // código que o usuário está colando no campo.
      const versao = [sessao.estado, sessao.url, sessao.codigo, sessao.requer_codigo, sessao.mensagem].join(" ");
      if (versao !== versaoRenderizada) {
        renderSessaoLogin(sessao, detalhe);
        versaoRenderizada = versao;
      }
      estado.textContent = rotuloSessaoLogin(sessao);
      if (["concluido", t("demandas.erro"), "cancelado", "expirado"].includes(sessao.estado)) {
        if (sessao.estado === "concluido") {
          toast(`Perfil ${c.alias} autenticado.`, "ok");
          await verificarLogin(m, c, estado, null, btn);
        }
        break;
      }
      await new Promise((resolve) => setTimeout(resolve, 1000));
      sessao = await api.obterLoginMotor(sessao.id);
    }
  } catch (e) {
    estado.textContent = t("motores.login_indisponivel");
    detalhe.replaceChildren(el("span", { class: "sub", text: e.message }));
  } finally {
    btn.disabled = false;
  }
}

function rotuloSessaoLogin(sessao) {
  const rotulos = {
    iniciando: t("motores.sessao_iniciando"),
    aguardando_navegador: t("motores.sessao_aguardando"),
    concluido: t("motores.login_autenticado"),
    erro: t("motores.sessao_erro"),
    cancelado: t("motores.sessao_cancelado"),
    expirado: t("motores.sessao_expirado"),
  };
  return rotulos[sessao.estado] || sessao.estado;
}

function renderSessaoLogin(sessao, alvo) {
  limpar(alvo);
  alvo.append(el("div", { class: "sub", style: "margin:4px 0", text: sessao.mensagem || rotuloSessaoLogin(sessao) }));
  if (sessao.url) {
    const abrir = el("a", { href: sessao.url, target: "_blank", rel: "noopener noreferrer", text: t("motores.abrir_pagina_auth") });
    const copiar = el("button", { class: "btn sm ghost", onclick: async () => {
      try { await navigator.clipboard.writeText(sessao.url); toast(t("motores.url_copiada"), "ok"); } catch { toast(t("motores.copie_url_menu"), "err"); }
    } }, t("motores.copiar_url"));
    alvo.append(el("div", { class: "acoes", style: "margin:5px 0" }, abrir, copiar));
  }
  if (sessao.codigo) {
    const codigo = el("code", { style: "font-size:1.05em", text: sessao.codigo });
    const copiar = el("button", { class: "btn sm ghost", onclick: async () => {
      try { await navigator.clipboard.writeText(sessao.codigo); toast(t("motores.codigo_copiado"), "ok"); } catch { toast(t("motores.copie_codigo"), "err"); }
    } }, t("motores.copiar_codigo"));
    alvo.append(el("div", { class: "acoes", style: "margin:5px 0" }, codigo, copiar));
  }
  if (sessao.requer_codigo && !["concluido", t("demandas.erro"), "cancelado", "expirado"].includes(sessao.estado)) {
    const entrada = el("input", { placeholder: t("motores.ph_codigo"), autocomplete: "off" });
    const enviar = el("button", { class: "btn sm", onclick: async () => {
      if (!entrada.value.trim()) return;
      enviar.disabled = true;
      try {
        await api.enviarCodigoLoginMotor(sessao.id, entrada.value.trim());
        entrada.value = "";
        toast(t("motores.codigo_enviado"), "ok");
      } catch (e) {
        toast(e.message, "err");
      } finally { enviar.disabled = false; }
    } }, t("motores.enviar_codigo"));
    alvo.append(el("div", { class: "acoes", style: "margin:5px 0" }, entrada, enviar));
  }
  if (!["concluido", t("demandas.erro"), "cancelado", "expirado"].includes(sessao.estado)) {
    alvo.append(el("button", { class: "btn sm ghost", onclick: async () => {
      try { await api.cancelarLoginMotor(sessao.id); } catch (e) { toast(e.message, "err"); }
    } }, t("motores.cancelar_login")));
  }
}

async function adicionarConta(m, alias, configDir, btn) {
  const a = alias.value.trim();
  if (!a) { bannerErro(t("motores.alias_obrigatorio")); return; }
  bannerErro("");
  btn.disabled = true;
  try {
    await api.criarConta(m.id, { alias: a, config_dir: configDir.value.trim() });
    toast(t("motores.perfil_adicionado"), "ok");
    await recarregar();
    renderPainel(motores.find((x) => x.id === m.id));
  } catch (e) {
    bannerErro("Falha ao adicionar perfil: " + e.message);
  } finally {
    btn.disabled = false;
  }
}

async function toggleConta(m, c) {
  try {
    await api.atualizarConta(m.id, c.id, { ativo: !c.ativo });
    await recarregar();
    renderPainel(motores.find((x) => x.id === m.id));
  } catch (e) {
    bannerErro("Falha ao alterar conta: " + e.message);
  }
}

async function removerConta(m, c) {
  try {
    await api.removerConta(m.id, c.id);
    toast(t("motores.perfil_removido"), "ok");
    await recarregar();
    renderPainel(motores.find((x) => x.id === m.id));
  } catch (e) {
    bannerErro("Falha ao remover perfil: " + e.message);
  }
}
