// Tela "Motores" — CRUD de motores e contas (Fase 1d). A ordem da lista é a
// prioridade (fallback); reordenar chama PUT /engines/ordem. Ligar/desligar é um
// PUT do motor. Cada motor abre um painel de detalhes com contas.

import { api } from "./api.js";
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
    lista.append(el("p", { class: "sub", text: "Nenhum motor cadastrado ainda." }));
    return;
  }
  motores.forEach((m, i) => lista.append(linhaMotor(m, i)));
}

function linhaMotor(m, i) {
  const nContas = (m.contas || []).length;
  const det = [
    m.modelo_exec ? `modelo ${m.modelo_exec} (exec)` : "sem modelo de execução",
    m.modelo_analise ? `${m.modelo_analise} (análise)` : null,
    m.modelo_consulta ? `${m.modelo_consulta} (consultas)` : null,
    m.budget_fase_usd > 0 ? `budget US$ ${m.budget_fase_usd.toFixed(2)}/fase` : "sem budget",
    m.timeout_min > 0 ? `timeout ${m.timeout_min}min` : null,
    `${nContas} ${nContas === 1 ? "perfil" : "perfis"}`,
    m.fallback === false ? "fora do fallback (uso manual)" : null,
  ].filter(Boolean).join(" · ");

  const sw = el("button", { class: "switch" + (m.ativo ? " on" : ""), title: m.ativo ? "ativo" : "inativo",
    onclick: () => toggleAtivo(m) });
  const subir = el("button", { title: "subir prioridade", disabled: i === 0, onclick: () => mover(i, i - 1) }, "▲");
  const descer = el("button", { title: "descer prioridade", disabled: i === motores.length - 1, onclick: () => mover(i, i + 1) }, "▼");

  return el("div", { class: "motor-row" },
    el("div", { class: "ord" }, subir, descer),
    el("span", { class: "pill", text: `${i + 1}º` }),
    el("span", { class: "nm", text: m.nome }),
    el("span", { class: "det", text: det }),
    el("button", { class: "btn sm ghost", onclick: () => { editandoID = m.id; renderPainel(m); } }, "Editar"),
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
    toast("Prioridade atualizada.", "ok");
    await recarregar();
  } catch (e) {
    bannerErro("Falha ao reordenar: " + e.message);
  }
}

// detectar consulta o servidor sobre quais harnesses estão instalados e como o
// ambiente está configurado, e mostra sugestões de cadastro num painel.
async function detectar() {
  const painel = document.getElementById("painel-deteccao");
  painel.hidden = false;
  limpar(painel);
  painel.append(el("p", { class: "sub", text: "Analisando o ambiente do servidor…" }));
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
    el("small", {}, "harness instalado + variáveis de ambiente")));

  const pendentes = sugestoes.filter((s) => s.instalado && !s.ja_cadastrado);
  const cabecalho = el("div", { style: "display:flex;align-items:center;gap:8px;margin-bottom:12px;flex-wrap:wrap" });
  const btnFechar = el("button", { class: "btn sm ghost", onclick: () => { painel.hidden = true; limpar(painel); } }, "Fechar");
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
  const estado = s.ja_cadastrado ? "já cadastrado" : s.instalado ? "detectado" : "não instalado";
  const modelos = [
    s.modelo_exec ? `exec ${s.modelo_exec}` : null,
    s.modelo_analise ? `análise ${s.modelo_analise}` : null,
    s.modelo_consulta ? `consultas ${s.modelo_consulta}` : null,
  ].filter(Boolean).join(" · ") || "usa o modelo configurado no próprio harness";

  const vars = (s.variaveis || []).filter((v) => v.definida);
  const varsTxt = vars.length
    ? vars.map((v) => v.sensivel ? `${v.nome}=••••` : `${v.nome}=${v.valor}`).join("  ")
    : "nenhuma variável relevante definida";

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
    el("div", { class: "sub", style: "margin:2px 0 0", text: "variáveis: " + varsTxt }),
    contasTxt ? el("div", { class: "sub", style: "margin:2px 0 0", text: contasTxt }) : null,
    s.observacao ? el("div", { class: "sub", style: "margin:2px 0 0", text: s.observacao }) : null,
  ].filter(Boolean);

  if (s.instalado && !s.ja_cadastrado) {
    const btn = el("button", { class: "btn sm", style: "margin-top:8px" }, "Cadastrar este");
    btn.onclick = () => cadastrarSugestao(s, btn);
    linhas.push(btn);
  }

  return el("div", { class: "motor-row", style: "display:block" }, ...linhas);
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
    toast(criados.length ? `${criados.length} motor(es) cadastrado(s).` : "Nada novo a cadastrar.", "ok");
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
  painel.append(el("h3", {}, criando ? "Novo motor" : `${m.nome} — detalhes`));

  const nome = el("input", { value: m ? m.nome : "", placeholder: "claude / codex / opencode" });
  const modeloExec = el("input", { value: m ? m.modelo_exec : "" });
  const modeloAnalise = el("input", { value: m ? m.modelo_analise : "" });
  const modeloConsulta = el("input", { value: m ? (m.modelo_consulta || "") : "", placeholder: "vazio = usa o de análise" });
  const budget = el("input", { type: "number", step: "0.5", value: m ? m.budget_fase_usd : 0 });
  const timeout = el("input", { type: "number", value: m ? m.timeout_min : 0 });
  const fallback = el("input", { type: "checkbox" });
  fallback.checked = criando ? true : m.fallback !== false;
  const params = el("textarea", {}, m && m.params ? prettyJSON(m.params) : "{}");

  const form = el("div", { class: "form" },
    el("div", {}, el("label", {}, "Nome"), nome),
    el("div", { class: "row" },
      el("div", {}, el("label", {}, "Modelo para execução"), modeloExec),
      el("div", {}, el("label", {}, "Modelo para análise/planejamento"), modeloAnalise),
    ),
    el("div", {}, el("label", {}, "Modelo para consultas"), modeloConsulta,
      el("div", { class: "hint", text: "Usado no chat de Consultas (produto/suporte). O rigor pode ser menor que o de análise/execução — pode ser um modelo mais leve/barato. Grupos de usuários podem sobrescrever." })),
    el("div", { class: "row" },
      el("div", {}, el("label", {}, "Budget por fase (US$)"), budget),
      el("div", {}, el("label", {}, "Timeout por fase (min)"), timeout),
    ),
    el("div", {},
      el("label", { style: "display:flex;align-items:center;gap:8px;cursor:pointer" }, fallback, "Participa do fallback automático"),
      el("div", { class: "hint", text: "Ligado, o motor entra na cadeia de troca automática quando a franquia esgota (todos os perfis do motor anterior são esgotados antes). Desligado, o motor só roda onde for escolhido manualmente — motor preferido do projeto ou motor do grupo de usuários nas consultas." })),
    el("div", {}, el("label", {}, "Params ", el("span", { class: "opt" }, "(JSON)")), params,
      el("div", { class: "hint", text: "Objeto JSON com parâmetros específicos do motor." })),
  );

  const btn = el("button", { class: "btn", style: "width:fit-content" }, criando ? "Cadastrar" : "Salvar");
  btn.onclick = () => salvar(m, { nome, modeloExec, modeloAnalise, modeloConsulta, budget, timeout, fallback, params }, btn);
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
  if (!nome) { bannerErro("Nome do motor é obrigatório."); return; }
  let params;
  try {
    params = JSON.parse(campos.params.value || "{}");
    if (params === null || typeof params !== "object" || Array.isArray(params)) throw new Error("params deve ser um objeto JSON");
  } catch (e) {
    bannerErro("Params inválido: " + e.message);
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
  bannerErro("");
  btn.disabled = true;
  try {
    if (m == null) {
      const criado = await api.criarMotor(corpo);
      toast("Motor cadastrado.", "ok");
      editandoID = criado.id;
      await recarregar();
      renderPainel(motores.find((x) => x.id === criado.id) || criado);
    } else {
      await api.atualizarMotor(m.id, corpo);
      toast("Motor salvo.", "ok");
      await recarregar();
      renderPainel(motores.find((x) => x.id === m.id));
    }
  } catch (e) {
    bannerErro("Falha ao salvar motor: " + e.message);
    toast("Falha ao salvar.", "err");
  } finally {
    btn.disabled = false;
  }
}

function renderContas(painel, m) {
  const contas = m.contas || [];
  const suportaLogin = ["claude", "codex"].includes((m.nome || "").trim().toLowerCase());
  const corpo = el("tbody", {});
  if (contas.length === 0) {
    corpo.append(el("tr", {}, el("td", { colspan: "5", class: "sub", text: "Nenhum perfil." })));
  }
  for (const c of contas) {
    const sw = el("button", { class: "switch" + (c.ativo ? " on" : ""), onclick: () => toggleConta(m, c) });
    const estado = el("span", { class: "sub", style: "margin:0", text: suportaLogin ? "não verificado" : "indisponível" });
    const acoes = el("div", { class: "acoes", style: "margin:0;gap:5px;flex-wrap:wrap" });
    if (suportaLogin) {
      let btnLogin;
      const btnVerificar = el("button", { class: "btn sm ghost", onclick: () => verificarLogin(m, c, estado, btnVerificar, btnLogin) }, "Verificar");
      btnLogin = el("button", { class: "btn sm", onclick: () => iniciarLogin(m, c, estado, acoes, btnLogin) }, "Entrar pelo navegador");
      acoes.append(btnVerificar, btnLogin);
      queueMicrotask(() => verificarLogin(m, c, estado, btnVerificar, btnLogin));
    }
    acoes.append(el("button", { class: "btn sm ghost", onclick: () => removerConta(m, c) }, "remover"));
    corpo.append(el("tr", {},
      el("td", { text: c.alias }),
      el("td", { text: c.config_dir || "—" }),
      el("td", {}, sw),
      el("td", {}, estado),
      el("td", {}, acoes),
    ));
  }
  const tabela = el("table", { class: "plain" },
    el("thead", {}, el("tr", {}, el("th", {}, "Perfil"), el("th", {}, "Diretório isolado"), el("th", {}, "Ativo"), el("th", {}, "Login"), el("th", {}))),
    corpo);

  const alias = el("input", { placeholder: "nome do perfil (ex.: principal)" });
  const configDir = el("input", { placeholder: "diretório opcional; vazio = gerenciado pelo Praxis" });
  const btnAdd = el("button", { class: "btn sm" }, "Adicionar perfil");
  btnAdd.onclick = () => adicionarConta(m, alias, configDir, btnAdd);

  painel.append(el("div", { class: "form" },
    tabela,
    el("div", { class: "row" }, el("div", {}, alias), el("div", {}, configDir)),
    el("div", { class: "acoes" }, btnAdd),
    el("div", { class: "hint", text: suportaLogin
      ? "Cada perfil mantém credenciais próprias. O Praxis distribui os fluxos de forma determinística entre os perfis ativos e aplica o mesmo diretório no login e nas execuções."
      : "Login assistido e isolamento completo estão disponíveis neste incremento apenas para Claude e Codex." }),
  ));
}

async function verificarLogin(m, c, estado, btn, btnLogin) {
  if (btn) btn.disabled = true;
  estado.textContent = "verificando…";
  try {
    const d = await api.estadoAuthMotor(m.id, c.id);
    if (d.autenticado) {
      estado.textContent = "autenticado" + (d.metodo ? ` (${d.metodo})` : "");
      if (btnLogin) btnLogin.textContent = "Trocar login";
    } else if (d.estado === "deslogado") {
      estado.textContent = "deslogado";
      if (btnLogin) btnLogin.textContent = "Entrar pelo navegador";
    } else {
      estado.textContent = d.mensagem || "estado desconhecido";
    }
  } catch {
    estado.textContent = "falha ao verificar";
  } finally {
    if (btn) btn.disabled = false;
  }
}

async function iniciarLogin(m, c, estado, acoes, btn) {
  // Abre a aba durante o clique para não ser bloqueada; ela só navega quando o
  // backend receber a URL emitida pelo vendor.
  let aba = null;
  try { aba = window.open("about:blank", "_blank"); } catch { /* o link também aparece na tela */ }
  btn.disabled = true;
  estado.textContent = "iniciando login…";
  const detalhe = el("div", { style: "width:100%;min-width:260px" });
  acoes.append(detalhe);
  try {
    let sessao = await api.iniciarLoginMotor(m.id, c.id);
    let abriuURL = false;
    while (detalhe.isConnected) {
      renderSessaoLogin(sessao, detalhe);
      if (sessao.url && !abriuURL) {
        abriuURL = true;
        if (aba && !aba.closed) {
          try { aba.location.href = sessao.url; aba.opener = null; } catch { /* usa o link visível */ }
        }
      }
      estado.textContent = rotuloSessaoLogin(sessao);
      if (["concluido", "erro", "cancelado", "expirado"].includes(sessao.estado)) {
        if (sessao.estado === "concluido") {
          toast(`Perfil ${c.alias} autenticado.`, "ok");
          await verificarLogin(m, c, estado, null, btn);
        } else if (aba && !aba.closed && !abriuURL) {
          aba.close();
        }
        break;
      }
      await new Promise((resolve) => setTimeout(resolve, 1000));
      sessao = await api.obterLoginMotor(sessao.id);
    }
  } catch (e) {
    estado.textContent = "login indisponível";
    detalhe.replaceChildren(el("span", { class: "sub", text: e.message }));
    if (aba && !aba.closed) aba.close();
  } finally {
    btn.disabled = false;
  }
}

function rotuloSessaoLogin(sessao) {
  const rotulos = {
    iniciando: "preparando login…",
    aguardando_navegador: "aguardando navegador",
    concluido: "autenticado",
    erro: "falha no login",
    cancelado: "login cancelado",
    expirado: "login expirado",
  };
  return rotulos[sessao.estado] || sessao.estado;
}

function renderSessaoLogin(sessao, alvo) {
  limpar(alvo);
  alvo.append(el("div", { class: "sub", style: "margin:4px 0", text: sessao.mensagem || rotuloSessaoLogin(sessao) }));
  if (sessao.url) {
    alvo.append(el("a", { href: sessao.url, target: "_blank", rel: "noopener noreferrer", text: "Abrir página de autenticação ↗" }));
  }
  if (sessao.codigo) {
    const codigo = el("code", { style: "font-size:1.05em", text: sessao.codigo });
    const copiar = el("button", { class: "btn sm ghost", onclick: async () => {
      try { await navigator.clipboard.writeText(sessao.codigo); toast("Código copiado.", "ok"); } catch { toast("Copie o código exibido.", "err"); }
    } }, "Copiar código");
    alvo.append(el("div", { class: "acoes", style: "margin:5px 0" }, codigo, copiar));
  }
  if (sessao.requer_codigo && !["concluido", "erro", "cancelado", "expirado"].includes(sessao.estado)) {
    const entrada = el("input", { placeholder: "cole aqui o código mostrado pelo Claude", autocomplete: "off" });
    const enviar = el("button", { class: "btn sm", onclick: async () => {
      if (!entrada.value.trim()) return;
      enviar.disabled = true;
      try {
        await api.enviarCodigoLoginMotor(sessao.id, entrada.value.trim());
        entrada.value = "";
        toast("Código enviado ao Claude.", "ok");
      } catch (e) {
        toast(e.message, "err");
      } finally { enviar.disabled = false; }
    } }, "Enviar código");
    alvo.append(el("div", { class: "acoes", style: "margin:5px 0" }, entrada, enviar));
  }
  if (!["concluido", "erro", "cancelado", "expirado"].includes(sessao.estado)) {
    alvo.append(el("button", { class: "btn sm ghost", onclick: async () => {
      try { await api.cancelarLoginMotor(sessao.id); } catch (e) { toast(e.message, "err"); }
    } }, "Cancelar login"));
  }
}

async function adicionarConta(m, alias, configDir, btn) {
  const a = alias.value.trim();
  if (!a) { bannerErro("Nome do perfil é obrigatório."); return; }
  bannerErro("");
  btn.disabled = true;
  try {
    await api.criarConta(m.id, { alias: a, config_dir: configDir.value.trim() });
    toast("Perfil adicionado.", "ok");
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
    toast("Perfil removido. O diretório e as credenciais foram preservados no servidor.", "ok");
    await recarregar();
    renderPainel(motores.find((x) => x.id === m.id));
  } catch (e) {
    bannerErro("Falha ao remover perfil: " + e.message);
  }
}
