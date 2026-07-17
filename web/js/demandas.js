// Tela "Demandas" (Fase 2h): lista as demandas e abre o card (modal) com as abas
// Fases (leitura), Log ao vivo (SSE do .jsonl) e Eventos. Não é o kanban (colunas,
// arrastar, filtros ricos) — isso é o M4; aqui é a porta de entrada para o card.

import { api } from "./api.js";
import { el, limpar, bannerErro } from "./ui.js";

let projetos = [];
let filtro = { project: "", status: "" };

// STATUS descreve os estados da demanda: rótulo amigável e classe do "dot" da pill.
const STATUS = {
  recebida: ["recebida", "dot-muted"],
  analisando: ["analisando", "dot-wait"],
  aguardando_respostas: ["aguardando respostas", "dot-wait"],
  planejando: ["planejando", "dot-wait"],
  aguardando_aprovacao: ["aguardando aprovação", "dot-wait"],
  pronta: ["pronta", "dot-blue"],
  executando: ["executando", "dot-exec"],
  concluida: ["concluída", "dot-done"],
  integrada: ["integrada", "dot-done"],
  pausada: ["pausada", "dot-wait"],
  aguardando_franquia: ["aguardando franquia", "dot-wait"],
  falhou: ["falhou", "dot-fail"],
  conflito: ["conflito", "dot-fail"],
  cancelada: ["cancelada", "dot-muted"],
};

function pillStatus(status) {
  const [rotulo, dot] = STATUS[status] || [status, "dot-muted"];
  return el("span", { class: "pill" }, el("span", { class: "dot " + dot }), rotulo);
}

// dinheiro formata um valor USD como "US$ 1,50" (pt-BR).
function dinheiro(v) {
  return "US$ " + Number(v || 0).toFixed(2).replace(".", ",");
}

// quando formata um timestamp ISO para leitura (pt-BR); devolve o cru se falhar.
function quando(iso) {
  if (!iso) return "";
  const d = new Date(iso);
  return isNaN(d) ? iso : d.toLocaleString("pt-BR");
}

export async function montarDemandas() {
  try {
    projetos = (await api.listarProjetos()) || [];
  } catch {
    projetos = [];
  }
  renderFiltros();
  await recarregarLista();
}

function renderFiltros() {
  const cont = limpar(document.getElementById("filtros-demandas"));

  const selProj = el("select", { onchange: (e) => { filtro.project = e.target.value; recarregarLista(); } },
    el("option", { value: "", text: "Todos os projetos" }));
  for (const p of projetos) {
    const o = el("option", { value: String(p.id), text: p.nome });
    if (String(p.id) === filtro.project) o.selected = true;
    selProj.append(o);
  }

  const selStatus = el("select", { onchange: (e) => { filtro.status = e.target.value; recarregarLista(); } },
    el("option", { value: "", text: "Todos os status" }));
  for (const s of Object.keys(STATUS)) {
    const o = el("option", { value: s, text: STATUS[s][0] });
    if (s === filtro.status) o.selected = true;
    selStatus.append(o);
  }

  cont.append(selProj, selStatus);
}

async function recarregarLista() {
  const lista = limpar(document.getElementById("lista-demandas"));
  let demandas;
  try {
    demandas = (await api.listarDemandas(filtro)) || [];
  } catch (e) {
    bannerErro("Falha ao carregar demandas: " + e.message);
    return;
  }
  bannerErro("");
  if (demandas.length === 0) {
    lista.append(el("p", { class: "sub", text: "Nenhuma demanda encontrada com os filtros atuais." }));
    return;
  }
  for (const d of demandas) {
    const proj = projetos.find((p) => p.id === d.project_id);
    const item = el("div", { class: "dem-item", onclick: () => abrirCard(d.id) },
      el("div", { class: "proj", text: (proj ? proj.nome : "projeto " + d.project_id) + (d.branch ? " · " + d.branch : "") }),
      el("div", { class: "title", text: `#${d.id} — ${d.titulo}` }),
      el("div", { class: "meta" },
        pillStatus(d.status),
        el("span", { class: "pill", text: dinheiro(d.custo_usd) + (d.budget_usd ? " de " + dinheiro(d.budget_usd) : "") }),
      ),
    );
    lista.append(item);
  }
}

// ---------- card (modal) ----------

let sseAtual = null; // EventSource em uso pelo card aberto (fechado ao sair)

function fecharCard(overlay) {
  if (sseAtual) { sseAtual.close(); sseAtual = null; }
  overlay.remove();
}

// abrirCard abre o modal da demanda. Exportado para a tela "Nova demanda" abrir o
// card da demanda recém-criada.
export async function abrirCard(id) {
  let dados;
  try {
    dados = await api.obterDemanda(id);
  } catch (e) {
    bannerErro("Falha ao abrir a demanda: " + e.message);
    return;
  }
  const proj = projetos.find((p) => p.id === dados.project_id);

  const overlay = el("div", { class: "overlay open" });
  overlay.addEventListener("click", (ev) => { if (ev.target === overlay) fecharCard(overlay); });

  const corpoChat = el("div", { class: "tab-body active", id: "tb-chat" });
  const corpoFases = el("div", { class: "tab-body", id: "tb-fases" });
  const corpoLog = el("div", { class: "tab-body", id: "tb-log" });
  const corpoEventos = el("div", { class: "tab-body", id: "tb-eventos" });

  renderFases(corpoFases, dados);
  ativarChat(corpoChat, id); // a aba Chat/PRD abre ativa: a demanda nasce como conversa

  const abas = [
    ["Chat / PRD", corpoChat, null],
    ["Fases", corpoFases, null],
    ["Log ao vivo", corpoLog, () => ativarLog(corpoLog, id)],
    ["Eventos", corpoEventos, () => ativarEventos(corpoEventos, id)],
  ];
  const tabs = el("div", { class: "tabs" });
  abas.forEach(([nome, corpo, ativar], i) => {
    const btn = el("button", { class: "tab" + (i === 0 ? " active" : ""), text: nome, onclick: () => {
      tabs.querySelectorAll(".tab").forEach((t) => t.classList.remove("active"));
      overlay.querySelectorAll(".tab-body").forEach((b) => b.classList.remove("active"));
      btn.classList.add("active");
      corpo.classList.add("active");
      if (ativar) ativar();
    } });
    tabs.append(btn);
  });

  const modal = el("div", { class: "modal" },
    el("div", { class: "modal-head" },
      el("div", { class: "row1" },
        el("div", {},
          el("div", { class: "proj", text: (proj ? proj.nome : "projeto " + dados.project_id) + " · #" + dados.id + (dados.branch ? " · " + dados.branch : "") }),
          el("h2", { text: dados.titulo }),
        ),
        el("button", { class: "modal-close", text: "✕", onclick: () => fecharCard(overlay) }),
      ),
      el("div", { class: "modal-actions" },
        pillStatus(dados.status),
        el("span", { class: "pill", text: dinheiro(dados.custo_usd) + (dados.budget_usd ? " de " + dinheiro(dados.budget_usd) : "") }),
        dados.erro ? el("span", { class: "pill", style: "color:var(--critical)", text: "erro" }) : null,
        ...botoesAcao(dados, overlay),
      ),
    ),
    tabs,
    corpoChat, corpoFases, corpoLog, corpoEventos,
  );
  overlay.append(modal);
  document.body.append(overlay);
}

// ---------- aba Chat/PRD (Fase 3a) ----------

// PAPEIS mapeia o papel de uma fala do chat ao rótulo e à classe visual da bolha.
const PAPEIS = {
  user: ["Você", "user"],
  analista: ["Praxis · Analista", "agent"],
  planejador: ["Praxis · Planejador", "agent"],
  sistema: ["", "sys"],
};

// bolha renderiza uma fala do chat como uma bolha (nó DOM).
function bolha(m) {
  const [rotulo, classe] = PAPEIS[m.papel] || [m.papel, "sys"];
  if (classe === "sys") {
    return el("div", { class: "msg sys", text: m.conteudo || rotulo });
  }
  return el("div", { class: "msg " + classe },
    rotulo ? el("div", { class: "who", text: rotulo }) : null,
    el("div", { class: "txt", text: m.conteudo }),
  );
}

// ativarChat monta a conversa da demanda: lista as falas (do PRD em diante) e
// oferece um campo para o usuário complementar a demanda (POST /chat).
async function ativarChat(cont, id) {
  limpar(cont);
  const box = el("div", { class: "chat" });
  const inp = el("input", { type: "text", placeholder: "Complementar a demanda…" });
  const btn = el("button", { class: "btn", text: "Enviar" });
  cont.append(box, el("div", { class: "chat-input" }, inp, btn));

  async function recarregar() {
    let msgs;
    try {
      msgs = (await api.listarChat(id)) || [];
    } catch (e) {
      limpar(box).append(el("p", { class: "vazio", text: "Falha ao carregar o chat: " + e.message }));
      return;
    }
    limpar(box);
    if (msgs.length === 0) {
      box.append(el("p", { class: "vazio", text: "Nenhuma mensagem ainda." }));
      return;
    }
    for (const m of msgs) box.append(bolha(m));
    box.scrollTop = box.scrollHeight;
  }

  async function enviar() {
    const texto = inp.value.trim();
    if (!texto) return;
    btn.disabled = true;
    try {
      await api.enviarChat(id, texto);
      inp.value = "";
      await recarregar();
    } catch (e) {
      bannerErro("Falha ao enviar: " + e.message);
    } finally {
      btn.disabled = false;
    }
  }
  btn.addEventListener("click", enviar);
  inp.addEventListener("keydown", (e) => { if (e.key === "Enter") enviar(); });

  await recarregar();
}

// ---------- ações de controle (Fase 2i) ----------

// AGENDAVEIS / TERMINAIS espelham a máquina de estados do backend para decidir
// quais botões de ação mostrar.
const AGENDAVEIS = ["pronta", "executando", "aguardando_franquia"];
const RETOMAVEIS = ["pausada", "aguardando_franquia"];
const TERMINAIS = ["concluida", "integrada", "cancelada", "falhou"];

// botoesAcao devolve os botões pausar/retomar/cancelar aplicáveis ao status atual.
function botoesAcao(dados, overlay) {
  const botoes = [];
  const add = (acao, rotulo, classe) =>
    botoes.push(el("button", { class: "btn sm " + classe, text: rotulo,
      onclick: () => executarAcao(dados.id, acao, overlay) }));

  if (AGENDAVEIS.includes(dados.status)) add("pausar", "Pausar", "ghost");
  if (RETOMAVEIS.includes(dados.status)) add("retomar", "Retomar", "good");
  if (!TERMINAIS.includes(dados.status)) add("cancelar", "Cancelar", "danger");
  return botoes;
}

// executarAcao dispara a ação na API e recarrega o card (e a lista de fundo) para
// refletir o novo status. Um erro (ex.: 409 transição inválida) vira banner.
async function executarAcao(id, acao, overlay) {
  try {
    await api.acaoDemanda(id, acao);
  } catch (e) {
    bannerErro(`Falha ao ${acao}: ${e.message}`);
    return;
  }
  bannerErro("");
  fecharCard(overlay);
  await recarregarLista();
  await abrirCard(id);
}

// ---------- aba Fases ----------

// iconeFase devolve o marcador de status de uma fase (nó DOM).
function iconeFase(f) {
  switch (f.status) {
    case "concluida": return el("span", { class: "st", style: "color:var(--good)", text: "✓" });
    case "executando": return el("span", { class: "st" }, el("span", { class: "spin" }));
    case "falhou": return el("span", { class: "st", style: "color:var(--critical)", text: "✗" });
    case "pausada": return el("span", { class: "st", style: "color:var(--warning)", text: "⏸" });
    default:
      return f.requer_humano
        ? el("span", { class: "st", style: "color:var(--warning)", text: "✋" })
        : el("span", { class: "st", style: "color:var(--muted)", text: "○" });
  }
}

function renderFases(cont, dados) {
  limpar(cont);
  const fases = dados.fases || [];
  if (fases.length === 0) {
    cont.append(el("p", { class: "vazio", text: "Esta demanda ainda não tem fases." }));
  }
  for (const f of fases) {
    const dep = (f.depende_de && f.depende_de.length) ? "dep: " + f.depende_de.join("+") : "";
    let custo = "";
    if (f.status === "concluida" || f.custo_usd) custo = dinheiro(f.custo_usd);
    else if (f.requer_humano) custo = "exige humano";
    else if (dep) custo = dep;
    const classe = f.status === "concluida" ? "done" : (f.status === "executando" ? "run" : "");
    cont.append(el("div", { class: "fase-row " + classe },
      iconeFase(f),
      el("span", { class: "nm", text: `${f.codigo}. ${f.titulo}` }),
      el("span", { class: "cost", text: custo }),
    ));
  }
  if (dados.plano_md) {
    cont.append(el("div", { class: "plano-md", text: dados.plano_md }));
  }
}

// ---------- aba Log ao vivo (SSE) ----------

function ativarLog(cont, id) {
  if (sseAtual) return; // já conectado enquanto o card está aberto
  limpar(cont);
  const box = el("div", { class: "log" });
  const foot = el("div", { class: "log-foot", text: "Conectando ao streaming ao vivo (SSE)…" });
  cont.append(box, foot);

  const empurrar = (no) => {
    if (!no) return;
    const perto = box.scrollHeight - box.scrollTop - box.clientHeight < 40;
    box.append(no);
    if (perto) box.scrollTop = box.scrollHeight;
  };

  const es = new EventSource(api.urlLogsDemanda(id));
  sseAtual = es;
  es.onopen = () => { foot.textContent = "Streaming ao vivo (SSE) · log completo salvo no banco."; };
  es.onmessage = (ev) => { formatarLinha(ev.data).forEach(empurrar); };
  es.addEventListener("exec", (ev) => empurrar(separadorExec(ev.data)));
  es.onerror = () => { foot.textContent = "Conexão do log interrompida — tentando reconectar…"; };
}

// separadorExec cria a linha que marca a troca de execução/etapa (evento `exec`).
function separadorExec(raw) {
  let m = {};
  try { m = JSON.parse(raw); } catch { /* ignora */ }
  const motor = m.engine ? m.engine + (m.modelo ? "/" + m.modelo : "") : "";
  const txt = `▶ ${m.operacao || "execução"}${motor ? " · " + motor : ""}`;
  return el("span", { class: "ln sep", text: txt });
}

// formatarLinha traduz uma linha crua do .jsonl (stream-json do motor) em nós de
// log legíveis. Reconhece as formas comuns (assistant/result) e cai num resumo
// cru para linhas desconhecidas, para nunca engolir conteúdo do log.
function formatarLinha(raw) {
  let ev;
  try { ev = JSON.parse(raw); } catch { return [el("span", { class: "ln muted", text: raw })]; }
  const nos = [];
  if (ev.type === "assistant" && ev.message && Array.isArray(ev.message.content)) {
    for (const c of ev.message.content) {
      if (c.type === "text" && c.text && c.text.trim()) {
        nos.push(el("span", { class: "ln", text: c.text.trim() }));
      } else if (c.type === "tool_use") {
        nos.push(el("span", { class: "ln" }, el("span", { class: "tool", text: "⏺ " + (c.name || "tool") })));
      }
    }
    return nos;
  }
  if (ev.type === "result") {
    if (ev.is_error) {
      nos.push(el("span", { class: "ln" }, el("span", { class: "err", text: "✗ " + (ev.subtype || "erro") })));
    } else {
      const r = (ev.result || "").trim();
      nos.push(el("span", { class: "ln" }, el("span", { class: "ok", text: "✓ concluído" }), r ? " — " + primeiraLinha(r) : ""));
    }
    return nos;
  }
  // system/user (init, tool_result) são ruído de acompanhamento — omitidos.
  if (ev.type === "system" || ev.type === "user") return [];
  return [el("span", { class: "ln muted", text: primeiraLinha(raw) })];
}

function primeiraLinha(s) {
  const l = String(s).split("\n")[0];
  return l.length > 240 ? l.slice(0, 240) + "…" : l;
}

// ---------- aba Eventos ----------

async function ativarEventos(cont, id) {
  limpar(cont);
  cont.append(el("p", { class: "vazio", text: "Carregando eventos…" }));
  let eventos;
  try {
    eventos = (await api.eventosDemanda(id)) || [];
  } catch (e) {
    limpar(cont).append(el("p", { class: "vazio", text: "Falha ao carregar eventos: " + e.message }));
    return;
  }
  limpar(cont);
  if (eventos.length === 0) {
    cont.append(el("p", { class: "vazio", text: "Nenhum evento registrado ainda." }));
    return;
  }
  for (const e of eventos) {
    cont.append(el("div", { class: "ev" },
      el("span", { class: "when", text: quando(e.criado_em) }),
      el("span", { class: "ev-tit" }, el("b", { text: e.titulo }), e.detalhe ? " — " + primeiraLinha(e.detalhe) : ""),
    ));
  }
}
