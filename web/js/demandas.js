// Tela "Demandas" (Fase 2h): lista as demandas e abre o card (modal) com as abas
// Fases (leitura), Log ao vivo (SSE do .jsonl) e Eventos. Não é o kanban (colunas,
// arrastar, filtros ricos) — isso é o M4; aqui é a porta de entrada para o card.

import { api } from "./api.js";
import { el, limpar, bannerErro, toast, renderMarkdown } from "./ui.js";

let projetos = [];
let filtro = { project: "", status: "" };

// setProjetos permite a outras telas (kanban, Home) preencher a lista de
// projetos usada pelo card modal antes de chamar abrirCard, para o card exibir
// o nome do projeto sem depender de a tela Demandas ter sido montada.
export function setProjetos(lista) {
  projetos = lista || [];
}

// STATUS_KANBAN é a ordem canônica das colunas do kanban e da pill de status,
// reexportada para as outras telas (kanban/Home) manterem os mesmos rótulos.
export { STATUS };

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

export function pillStatus(status) {
  const [rotulo, dot] = STATUS[status] || [status, "dot-muted"];
  return el("span", { class: "pill" }, el("span", { class: "dot " + dot }), rotulo);
}

// dinheiro formata um valor USD como "US$ 1,50" (pt-BR).
export function dinheiro(v) {
  return "US$ " + Number(v || 0).toFixed(2).replace(".", ",");
}

// quando formata um timestamp ISO para leitura (pt-BR); devolve o cru se falhar.
export function quando(iso) {
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

let sseAtual = null;       // EventSource do log ao vivo (aba Log), reiniciado a cada render
let sseCardEventos = null; // EventSource global que dispara o refresh do card aberto
let cardAberto = null;     // { id, overlay, assinatura } do card atualmente aberto

function fecharCard(overlay) {
  if (sseAtual) { sseAtual.close(); sseAtual = null; }
  if (sseCardEventos) { sseCardEventos.close(); sseCardEventos = null; }
  if (cardAberto && cardAberto.overlay === overlay) cardAberto = null;
  overlay.remove();
}

// chaveAba normaliza o rótulo de uma aba para uma chave estável (sem a contagem
// entre parênteses: "Perguntas (3)" → "Perguntas"), usada para preservar a aba
// ativa quando o card é re-renderizado ao vivo.
function chaveAba(nome) { return nome.replace(/\s*\(.*\)\s*$/, "").trim(); }

// assinaturaCard resume o estado VISÍVEL do card; quando muda, o card é
// re-renderizado ao vivo (status, branch, custo, nº de perguntas e as fases).
function assinaturaCard(dados, perguntas) {
  const fases = (dados.fases || [])
    .map((f) => `${f.codigo}:${f.status}:${f.custo_usd}:${f.tentativas}`).join("|");
  return [dados.status, dados.branch || "", dados.custo_usd, dados.erro || "",
    (perguntas || []).length, fases].join("~");
}

// abrirCard abre o modal da demanda. Exportado para a tela "Nova demanda" abrir o
// card da demanda recém-criada. O conteúdo se ATUALIZA SOZINHO enquanto aberto
// (SSE global de eventos): não é preciso fechar e reabrir para ver o progresso.
export async function abrirCard(id) {
  let dados;
  try {
    dados = await api.obterDemanda(id);
  } catch (e) {
    bannerErro("Falha ao abrir a demanda: " + e.message);
    return;
  }
  const overlay = el("div", { class: "overlay open" });
  overlay.addEventListener("click", (ev) => { if (ev.target === overlay) fecharCard(overlay); });
  document.body.append(overlay);
  cardAberto = { id, overlay, assinatura: "" };
  await renderConteudoCard(overlay, id, dados, null, null);
  assinarEventosCard(id, overlay);
}

// renderConteudoCard (re)constrói o conteúdo do modal DENTRO do overlay, sem
// destruir o backdrop (evita flicker no refresh ao vivo). abaPreferida (chave de
// aba) mantém a aba ativa entre atualizações; nula = escolha padrão pelo status.
// perguntasPre evita uma segunda busca quando o chamador já as carregou.
async function renderConteudoCard(overlay, id, dados, abaPreferida, perguntasPre) {
  if (sseAtual) { sseAtual.close(); sseAtual = null; } // reinicia o log no rebuild
  const proj = projetos.find((p) => p.id === dados.project_id);

  const corpoChat = el("div", { class: "tab-body", id: "tb-chat" });
  const corpoFases = el("div", { class: "tab-body", id: "tb-fases" });
  const corpoLog = el("div", { class: "tab-body", id: "tb-log" });
  const corpoEventos = el("div", { class: "tab-body", id: "tb-eventos" });
  const corpoIntegr = el("div", { class: "tab-body", id: "tb-integr" });
  const corpoDiff = el("div", { class: "tab-body", id: "tb-diff" });

  renderFases(corpoFases, dados, overlay);

  // Aba Perguntas (Fase 3b): só aparece quando o analista já gerou perguntas.
  let perguntas = perguntasPre;
  if (perguntas == null) {
    try { perguntas = (await api.listarPerguntas(id)) || []; } catch { perguntas = []; }
  }
  const temPerguntas = perguntas.length > 0;

  const abas = [];
  let corpoPerg = null;
  if (temPerguntas) {
    corpoPerg = el("div", { class: "tab-body", id: "tb-perg" });
    renderPerguntas(corpoPerg, dados, perguntas, overlay);
    abas.push([`Perguntas (${perguntas.length})`, corpoPerg, null]);
  }
  abas.push(["Chat / PRD", corpoChat, () => ativarChat(corpoChat, id)]);
  abas.push(["Plano & Fases", corpoFases, null]);
  // Aba Integração (Fases 4c/4d): só quando a branch já foi criada de fato — ou
  // seja, quando existe a worktree (preenchida na preparação) ou a demanda já foi
  // integrada. Não basta ter `branch`: o dev pode ter nomeado a branch na criação
  // antes de a execução criá-la no git.
  const temIntegracao = !!dados.worktree_path || dados.status === "integrada";
  if (temIntegracao) {
    abas.push(["Integração", corpoIntegr, () => ativarIntegracao(corpoIntegr, id, overlay)]);
  }
  // Aba Diff: mostra o que a demanda alterou no git (todas as fases ou uma só).
  // Só faz sentido enquanto a branch existe no servidor (worktree presente);
  // após integrar, a branch é removida e o diff não fica mais disponível.
  const temDiff = !!dados.worktree_path;
  if (temDiff) {
    abas.push(["Diff", corpoDiff, () => ativarDiff(corpoDiff, id, dados)]);
  }
  abas.push(["Log ao vivo", corpoLog, () => ativarLog(corpoLog, id)]);
  abas.push(["Eventos", corpoEventos, () => ativarEventos(corpoEventos, id)]);

  // Aba ativa: preserva a preferida (refresh ao vivo); senão escolhe pelo status
  // (aguardando_aprovacao → Plano & Fases; conflito/concluída → Integração).
  let idxAtiva = -1;
  if (abaPreferida) idxAtiva = abas.findIndex(([nome]) => chaveAba(nome) === abaPreferida);
  if (idxAtiva < 0) {
    idxAtiva = 0;
    if (dados.status === "aguardando_aprovacao") {
      const i = abas.findIndex(([nome]) => nome === "Plano & Fases");
      if (i >= 0) idxAtiva = i;
    } else if (temIntegracao && (dados.status === "conflito" || dados.status === "concluida")) {
      const i = abas.findIndex(([nome]) => nome === "Integração");
      if (i >= 0) idxAtiva = i;
    }
  }
  abas[idxAtiva][1].classList.add("active");
  if (abas[idxAtiva][2]) abas[idxAtiva][2]();

  const tabs = el("div", { class: "tabs" });
  abas.forEach(([nome, corpo, ativar], i) => {
    const btn = el("button", { class: "tab" + (i === idxAtiva ? " active" : ""), text: nome, onclick: () => {
      tabs.querySelectorAll(".tab").forEach((t) => t.classList.remove("active"));
      overlay.querySelectorAll(".tab-body").forEach((b) => b.classList.remove("active"));
      btn.classList.add("active");
      corpo.classList.add("active");
      if (ativar) ativar();
    } });
    btn.dataset.aba = chaveAba(nome);
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
    corpoPerg, corpoChat, corpoFases, corpoIntegr, corpoDiff, corpoLog, corpoEventos,
  );

  overlay.querySelector(".modal")?.remove(); // troca o conteúdo mantendo o backdrop
  overlay.append(modal);
  if (cardAberto && cardAberto.overlay === overlay) {
    cardAberto.assinatura = assinaturaCard(dados, perguntas);
  }
  // Badge de sobreposição (Fase 5c): busca best-effort; se houver, insere um
  // aviso com as demandas que tocam os mesmos arquivos.
  mostrarSobreposicao(modal, id);
}

// assinarEventosCard escuta o SSE global e agenda a atualização do card quando um
// evento da PRÓPRIA demanda chega (debounce). Fechado em fecharCard.
function assinarEventosCard(id, overlay) {
  if (sseCardEventos) { sseCardEventos.close(); sseCardEventos = null; }
  const es = new EventSource(api.urlEventos());
  sseCardEventos = es;
  let timer = null;
  es.addEventListener("evento", (e) => {
    let ev;
    try { ev = JSON.parse(e.data); } catch { return; }
    if (Number(ev.demand_id) !== Number(id)) return;
    if (timer) clearTimeout(timer);
    timer = setTimeout(() => atualizarCardSeMudou(id, overlay), 350);
  });
  es.onerror = () => { /* o navegador reconecta sozinho */ };
}

// atualizarCardSeMudou re-busca a demanda e re-renderiza o card se o estado
// visível mudou — preservando a aba ativa. Não mexe no DOM enquanto o usuário
// digita num campo do card (reagenda, para não perder o texto).
async function atualizarCardSeMudou(id, overlay) {
  if (!cardAberto || cardAberto.overlay !== overlay || !overlay.isConnected) return;
  if (estaEditando(overlay)) {
    setTimeout(() => atualizarCardSeMudou(id, overlay), 1500);
    return;
  }
  let dados;
  try { dados = await api.obterDemanda(id); } catch { return; }
  let perguntas = [];
  try { perguntas = (await api.listarPerguntas(id)) || []; } catch { perguntas = []; }
  if (!cardAberto || cardAberto.overlay !== overlay) return;
  if (assinaturaCard(dados, perguntas) === cardAberto.assinatura) return;
  const abaAtiva = overlay.querySelector(".tab.active")?.dataset.aba || null;
  await renderConteudoCard(overlay, id, dados, abaAtiva, perguntas);
}

// estaEditando informa se o foco está num campo de texto dentro do card (para não
// re-renderizar por baixo do usuário enquanto ele digita).
function estaEditando(overlay) {
  const ae = document.activeElement;
  return !!ae && overlay.contains(ae) && (ae.tagName === "INPUT" || ae.tagName === "TEXTAREA");
}

// mostrarSobreposicao consulta as sobreposições da demanda e, se houver, injeta
// um banner logo abaixo do cabeçalho do card, listando as demandas em comum.
async function mostrarSobreposicao(modal, id) {
  let sobre;
  try {
    sobre = (await api.overlapDemanda(id)) || [];
  } catch {
    return;
  }
  if (!sobre.length) return;
  const banner = el("div", { class: "banner banner-warn", style: "margin:0 22px 12px" },
    el("div", { text: `⚠ Sobreposição de arquivos com ${sobre.length} outra(s) demanda(s):` }));
  for (const s of sobre) {
    banner.append(el("div", { class: "overlap-linha" },
      el("b", { text: `#${s.demand_id} — ${s.titulo}` }),
      el("span", { class: "overlap-arqs", text: " · " + s.arquivos.join(", ") }),
    ));
  }
  const head = modal.querySelector(".modal-head");
  head.insertAdjacentElement("afterend", banner);
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
    el("div", { class: "txt md" }, ...renderMarkdown(m.conteudo || "")),
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

// ---------- aba Perguntas (Fase 3b) ----------

// IMPACTO_DOT mapeia o impacto de uma pergunta à classe do "dot" da pill.
const IMPACTO_DOT = { alto: "dot-crit", medio: "dot-warn", baixo: "dot-muted" };

// renderPerguntas monta a aba Perguntas: quando a demanda está aguardando
// respostas, cada pergunta vira um formulário (chips de sugestão ou texto livre) e
// há o botão "Responder tudo e gerar plano"; nos demais estados é só leitura
// (mostra a resposta dada ou a sugestão).
function renderPerguntas(cont, dados, perguntas, overlay) {
  limpar(cont);
  const editavel = dados.status === "aguardando_respostas";
  cont.append(el("div", { class: "banner banner-info", text: editavel
    ? "O analista leu o código e gerou as perguntas abaixo. Sugestões já vêm preenchidas — confirme ou ajuste e gere o plano."
    : "Perguntas do analista para esta demanda." }));

  const estado = {}; // id da pergunta → valor da resposta corrente (modo editável)

  perguntas.forEach((q, i) => {
    const item = el("div", { class: "q-item" + (!editavel && q.resposta ? " answered" : "") });
    if (q.impacto) {
      const dot = IMPACTO_DOT[q.impacto] || "dot-muted";
      const rot = q.impacto === "alto" ? "alto impacto" : q.impacto;
      item.append(el("span", { class: "imp pill" }, el("span", { class: "dot " + dot }), rot));
    }
    item.append(el("div", { class: "q", text: `${i + 1}. ${q.pergunta}` }));
    if (q.contexto) item.append(el("div", { class: "ctx", text: q.contexto }));

    const temOpcoes = q.opcoes && q.opcoes.length > 0;
    if (editavel) {
      estado[q.id] = q.resposta || q.sugestao || "";
      const answer = el("div", { class: "answer" });
      if (temOpcoes && q.tipo !== "texto") {
        const chips = [];
        q.opcoes.forEach((op) => {
          const rotulo = op + (op === q.sugestao ? " (sugestão)" : "");
          const chip = el("button", { class: "chip" + (op === estado[q.id] ? " sel" : ""), text: rotulo,
            onclick: () => { estado[q.id] = op; chips.forEach((c) => c.classList.remove("sel")); chip.classList.add("sel"); } });
          chips.push(chip);
          answer.append(chip);
        });
      } else {
        const inp = el("input", { type: "text", value: estado[q.id] });
        inp.addEventListener("input", () => { estado[q.id] = inp.value; });
        answer.append(inp);
      }
      item.append(answer);
    } else if (q.resposta) {
      item.append(el("div", { class: "resp" }, el("b", { text: "✓ Respondida: " }), q.resposta));
    } else {
      item.append(el("div", { class: "resp", text: q.sugestao ? "Sugestão do analista: " + q.sugestao : "Sem resposta." }));
    }
    cont.append(item);
  });

  if (editavel) {
    const btn = el("button", { class: "btn", text: "Responder tudo e gerar plano →" });
    btn.addEventListener("click", async () => {
      btn.disabled = true;
      const respostas = perguntas.map((q) => ({ id: q.id, resposta: (estado[q.id] || "").trim() }));
      try {
        await api.responderPerguntas(dados.id, respostas);
      } catch (e) {
        bannerErro("Falha ao responder: " + e.message);
        btn.disabled = false;
        return;
      }
      bannerErro("");
      fecharCard(overlay);
      await recarregarLista();
      await abrirCard(dados.id);
    });
    cont.append(el("div", { class: "perguntas-acoes" }, btn));
  }
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

// ---------- aba Integração (Fases 4c/4d) ----------

// ativarIntegracao mostra o estado de fechamento da demanda: branch, commits,
// commits não publicados (+ publicar), preview de conflito com a main e, no modo
// merge_request, o link para abrir o MR. No modo merge_local, o botão Integrar
// faz o merge na main; em qualquer modo, "Atualizar branch" traz a main para a
// branch (Fase 4d).
async function ativarIntegracao(cont, id, overlay) {
  limpar(cont).append(el("p", { class: "sub", text: "Carregando integração…" }));
  let mp;
  try {
    mp = await api.mergePreview(id);
  } catch (e) {
    limpar(cont).append(el("div", { class: "banner banner-erro", text: "Falha ao carregar integração: " + e.message }));
    return;
  }
  limpar(cont);

  cont.append(el("div", { class: "integr-head" },
    el("div", {},
      el("div", { class: "integr-branch", text: mp.branch }),
      el("div", { class: "sub", style: "margin:2px 0 0", text: `alvo: ${mp.base} · modo: ${mp.modo_integracao}` }),
      mp.worktree_path ? el("div", { class: "sub wt-linha", style: "margin:6px 0 0;display:flex;align-items:center;gap:8px;flex-wrap:wrap" },
        el("span", { text: "worktree (servidor):" }),
        el("code", { class: "wt-path", text: mp.worktree_path }),
        el("button", { class: "btn sm ghost", text: "copiar",
          onclick: async () => {
            try { await navigator.clipboard.writeText(mp.worktree_path); toast("Caminho copiado.", "ok"); }
            catch { toast("Não foi possível copiar.", "err"); }
          } }),
        el("button", { class: "btn sm", text: "Editar código ⧉",
          title: "Abre o VS Code no navegador, direto neste worktree (exige demanda pausada, falhada, em conflito ou encerrada).",
          onclick: (ev) => abrirIDEWeb(id, mp.worktree_path, ev.currentTarget) }),
        linkVSCodeLocal(mp.worktree_path),
      ) : null,
    ),
  ));

  // Preview de conflito.
  if (mp.aviso) {
    cont.append(el("div", { class: "banner banner-info", text: mp.aviso }));
  } else if (mp.limpo) {
    cont.append(el("div", { class: "banner banner-ok", text: "✓ Sem conflitos com a main — pronto para integrar." }));
  } else {
    const box = el("div", { class: "banner banner-erro" },
      el("div", { text: `⚠ Conflito com a main em ${mp.conflitos.length} arquivo(s):` }));
    for (const f of mp.conflitos) box.append(el("div", { class: "conf-file", text: f }));
    cont.append(box);
  }

  // Commits não publicados (alerta 2f).
  if (mp.commits_nao_publicados > 0) {
    cont.append(el("div", { class: "banner banner-info", style: "display:flex;align-items:center;gap:10px;justify-content:space-between" },
      el("span", { text: `${mp.commits_nao_publicados} commit(s) não publicado(s).` }),
      el("button", { class: "btn sm", text: "Publicar branch",
        onclick: () => executarAcaoIntegr(id, "publicar_branch", cont, overlay) })));
  }

  // Botões de fechamento — só quando a demanda está em estado integrável
  // (concluída ou em conflito). O backend também valida (409 estado_invalido);
  // esconder aqui evita o clique prematuro que mesclaria trabalho incompleto.
  const integravel = ["concluida", "conflito"].includes(mp.status);
  const agendada = AGENDAVEIS.includes(mp.status);
  const acoes = el("div", { class: "integr-acoes" });
  if (integravel && mp.modo_integracao === "merge_request" && mp.url_mr) {
    acoes.append(el("a", { class: "btn", href: mp.url_mr, target: "_blank", rel: "noopener", text: "Abrir Merge Request ↗" }));
  }
  if (integravel && mp.modo_integracao === "merge_local") {
    acoes.append(el("button", { class: "btn good", text: "Integrar na main",
      onclick: () => executarAcaoIntegr(id, "integrar", cont, overlay) }));
  }
  if (!agendada && !mp.ja_integrada && mp.worktree_path) {
    acoes.append(el("button", { class: "btn ghost", text: "Atualizar branch (trazer main)",
      onclick: () => executarAcaoIntegr(id, "atualizar_branch", cont, overlay) }));
  }
  if (!integravel && !mp.ja_integrada) {
    acoes.append(el("span", { class: "sub", text: "A integração fica disponível quando a demanda concluir." }));
  }
  cont.append(acoes);

  // Lista de commits.
  cont.append(el("h3", { style: "margin:18px 0 8px", text: `Commits (${mp.commits.length})` }));
  if (mp.commits.length === 0) {
    cont.append(el("p", { class: "sub", text: "Nenhum commit à frente da main." }));
  } else {
    const lista = el("div", { class: "commits" });
    for (const c of mp.commits) {
      lista.append(el("div", { class: "commit-row" },
        el("span", { class: "commit-hash", text: c.hash }),
        el("span", { class: "commit-msg", text: c.assunto }),
      ));
    }
    cont.append(lista);
  }
}

// ---------- IDE web (edição manual do worktree) ----------

// abrirIDEWeb cria/renova a sessão do IDE (cookie de /ide/*) e abre o VS Code
// Web no worktree da demanda. A aba é aberta JÁ no clique (contra bloqueio de
// popup); enquanto o serve-web sobe, o proxy serve uma página "preparando" que
// recarrega sozinha preservando o ?folder=.
async function abrirIDEWeb(id, worktree, btn) {
  const rotulo = btn.textContent;
  btn.disabled = true;
  btn.textContent = "Abrindo…";
  const aba = window.open("", "praxis-ide");
  try {
    await api.sessaoIDE(id); // valida estado/permissão, emite o cookie e sobe o serve-web
    // O valor de ?folder= PRECISA começar com "/" (o workbench só monta a URI
    // remota para caminhos com "/" inicial; "C:/…" viraria scheme de URI).
    let caminho = worktree.replace(/\\/g, "/");
    if (!caminho.startsWith("/")) caminho = "/" + caminho;
    const url = "/ide/?folder=" + encodeURIComponent(caminho);
    if (aba && !aba.closed) aba.location = url;
    else window.open(url, "praxis-ide");
  } catch (e) {
    if (aba && !aba.closed) aba.close();
    toast("IDE: " + e.message, "err");
  } finally {
    btn.disabled = false;
    btn.textContent = rotulo;
  }
}

// linkVSCodeLocal devolve o link vscode://file/… para abrir o worktree no VS
// Code instalado — só quando o navegador acessa o Praxis por loopback, único
// caso em que o caminho do servidor existe nesta máquina. (Um túnel SSH também
// parece loopback; nesse caso o link abre um caminho inexistente — use o
// "Editar código" web.)
function linkVSCodeLocal(worktree) {
  if (!["localhost", "127.0.0.1", "[::1]"].includes(location.hostname)) return null;
  return el("a", { class: "btn sm ghost", text: "Abrir no VS Code local",
    href: "vscode://file/" + encodeURI(worktree.replace(/\\/g, "/")),
    title: "Requer o Praxis rodando NESTA máquina e o VS Code instalado." });
}

// executarAcaoIntegr dispara uma ação de integração e recarrega o card.
async function executarAcaoIntegr(id, acao, cont, overlay) {
  cont.querySelectorAll("button").forEach((b) => (b.disabled = true));
  try {
    await api.acaoDemanda(id, acao);
  } catch (e) {
    bannerErro(`Falha ao ${acao}: ${e.message}`);
    cont.querySelectorAll("button").forEach((b) => (b.disabled = false));
    return;
  }
  bannerErro("");
  fecharCard(overlay);
  await recarregarLista();
  await abrirCard(id);
}

// ---------- aba Diff ----------

// ativarDiff mostra o diff do git da demanda: por padrão tudo o que a branch
// alterou (base...branch) e, com o seletor de fase, apenas os commits de uma
// fase. As fases sem commit (pendentes) devolvem um diff vazio.
async function ativarDiff(cont, id, dados) {
  limpar(cont);
  let faseSel = "";

  const sel = el("select", { class: "diff-fase",
    onchange: (e) => { faseSel = e.target.value; carregar(); } },
    el("option", { value: "", text: "Todas as alterações" }));
  for (const f of dados.fases || []) {
    sel.append(el("option", { value: f.codigo, text: `Fase ${f.codigo} — ${f.titulo}` }));
  }

  const corpo = el("div", { class: "diff-view" });
  cont.append(
    el("div", { class: "diff-head" },
      el("span", { class: "sub", text: "Filtrar por fase:" }),
      sel,
    ),
    corpo,
  );

  async function carregar() {
    limpar(corpo).append(el("p", { class: "sub", text: "Carregando diff…" }));
    let resp;
    try {
      resp = await api.diffDemanda(id, faseSel);
    } catch (e) {
      limpar(corpo).append(el("div", { class: "banner banner-erro", text: "Falha ao carregar o diff: " + e.message }));
      return;
    }
    renderDiffTexto(corpo, resp.diff);
  }

  await carregar();
}

// renderDiffTexto renderiza um diff unified do git como um <pre> com as linhas
// coloridas (adições, remoções, cabeçalhos de arquivo e de trecho @@).
function renderDiffTexto(corpo, texto) {
  limpar(corpo);
  if (!texto || !texto.trim()) {
    corpo.append(el("p", { class: "vazio", text: "Nenhuma alteração para exibir." }));
    return;
  }
  const pre = el("pre", { class: "diff" });
  for (const linha of texto.split("\n")) {
    let cls = "d-ctx";
    if (linha.startsWith("diff --git") || linha.startsWith("index ") ||
        linha.startsWith("--- ") || linha.startsWith("+++ ") ||
        linha.startsWith("new file") || linha.startsWith("deleted file") ||
        linha.startsWith("rename ") || linha.startsWith("similarity ")) {
      cls = "d-meta";
    } else if (linha.startsWith("@@")) {
      cls = "d-hunk";
    } else if (linha.startsWith("+")) {
      cls = "d-add";
    } else if (linha.startsWith("-")) {
      cls = "d-del";
    }
    pre.append(el("span", { class: "d-line " + cls, text: linha + "\n" }));
  }
  corpo.append(pre);
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

function renderFases(cont, dados, overlay) {
  // Quando a demanda aguarda aprovação, a aba vira o editor do plano (Fase 3c):
  // editar/reordenar/remover/exigir humano + aprovar/rejeitar. Nos demais estados
  // é leitura (progresso das fases).
  if (dados.status === "aguardando_aprovacao") {
    renderFasesEditavel(cont, dados, overlay);
    return;
  }

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

// renderFasesEditavel monta o editor do plano para uma demanda aguardando
// aprovação: uma linha por fase (código, título, dependências, "exige humano"),
// mover para cima/baixo, remover, adicionar; e as ações Salvar / Aprovar /
// Rejeitar (com comentário). A edição é local até "Salvar alterações"; Aprovar
// exige salvar antes (o backend valida o conjunto atual).
function renderFasesEditavel(cont, dados, overlay) {
  limpar(cont);
  cont.append(el("div", { class: "banner banner-info",
    text: "O planejador gerou o plano abaixo. Ajuste as fases (título, código, dependências, exige humano), salve e aprove para executar — ou rejeite com um comentário para replanejar." }));

  // estado local editável: cópia rasa das fases.
  const fases = (dados.fases || []).map((f) => ({
    codigo: f.codigo || "",
    titulo: f.titulo || "",
    depende_de: (f.depende_de || []).slice(),
    requer_humano: !!f.requer_humano,
    gate_extra: f.gate_extra || "",
    modelo: f.modelo || "",
    observacao: f.observacao || "",
  }));

  const lista = el("div", { class: "fases-edit" });
  cont.append(lista);

  function redesenhar() {
    limpar(lista);
    if (fases.length === 0) {
      lista.append(el("p", { class: "vazio", text: "Sem fases. Adicione ao menos uma antes de aprovar." }));
    }
    fases.forEach((f, i) => {
      const inCodigo = el("input", { class: "f-cod", type: "text", value: f.codigo, placeholder: "cód" });
      inCodigo.addEventListener("input", () => { f.codigo = inCodigo.value; });
      const inTitulo = el("input", { class: "f-tit", type: "text", value: f.titulo, placeholder: "título da fase" });
      inTitulo.addEventListener("input", () => { f.titulo = inTitulo.value; });
      const inDep = el("input", { class: "f-dep", type: "text", value: f.depende_de.join(", "), placeholder: "depende de (ex.: 1, 2a)" });
      inDep.addEventListener("input", () => {
        f.depende_de = inDep.value.split(",").map((s) => s.trim()).filter(Boolean);
      });
      const chkHum = el("input", { type: "checkbox" });
      chkHum.checked = f.requer_humano;
      chkHum.addEventListener("change", () => { f.requer_humano = chkHum.checked; });

      const btnUp = el("button", { class: "btn sm ghost", text: "↑", title: "subir",
        onclick: () => { if (i > 0) { [fases[i - 1], fases[i]] = [fases[i], fases[i - 1]]; redesenhar(); } } });
      const btnDown = el("button", { class: "btn sm ghost", text: "↓", title: "descer",
        onclick: () => { if (i < fases.length - 1) { [fases[i + 1], fases[i]] = [fases[i], fases[i + 1]]; redesenhar(); } } });
      const btnDel = el("button", { class: "btn sm danger", text: "✕", title: "remover",
        onclick: () => { fases.splice(i, 1); redesenhar(); } });

      lista.append(el("div", { class: "fase-edit-row" },
        inCodigo, inTitulo, inDep,
        el("label", { class: "f-hum", title: "exige humano" }, chkHum, "✋"),
        el("div", { class: "f-btns" }, btnUp, btnDown, btnDel),
      ));
    });
  }
  redesenhar();

  const btnAdd = el("button", { class: "btn sm ghost", text: "+ Adicionar fase",
    onclick: () => { fases.push({ codigo: "", titulo: "", depende_de: [], requer_humano: false, gate_extra: "", modelo: "", observacao: "" }); redesenhar(); } });
  cont.append(el("div", { class: "fases-edit-add" }, btnAdd));

  if (dados.plano_md) {
    cont.append(el("div", { class: "plano-md", text: dados.plano_md }));
  }

  // ações do plano.
  const reabrir = async () => { fecharCard(overlay); await recarregarLista(); await abrirCard(dados.id); };
  const payloadFases = () => fases.map((f) => ({
    codigo: f.codigo.trim(), titulo: f.titulo.trim(), depende_de: f.depende_de,
    requer_humano: f.requer_humano, gate_extra: f.gate_extra, modelo: f.modelo, observacao: f.observacao,
  }));

  const btnSalvar = el("button", { class: "btn ghost", text: "Salvar alterações" });
  btnSalvar.addEventListener("click", async () => {
    btnSalvar.disabled = true;
    try {
      await api.editarFases(dados.id, payloadFases());
    } catch (e) {
      bannerErro("Falha ao salvar as fases: " + e.message);
      btnSalvar.disabled = false;
      return;
    }
    bannerErro("");
    await reabrir();
  });

  const btnAprovar = el("button", { class: "btn good", text: "Aprovar e executar →" });
  btnAprovar.addEventListener("click", async () => {
    btnAprovar.disabled = true;
    try {
      // salva o estado atual antes de aprovar (o backend valida o conjunto persistido).
      await api.editarFases(dados.id, payloadFases());
      await api.aprovarPlano(dados.id);
    } catch (e) {
      bannerErro("Falha ao aprovar: " + e.message);
      btnAprovar.disabled = false;
      return;
    }
    bannerErro("");
    await reabrir();
  });

  const btnRejeitar = el("button", { class: "btn danger", text: "Rejeitar…" });
  btnRejeitar.addEventListener("click", async () => {
    const comentario = (prompt("O que ajustar no plano? (o planejador vai refazê-lo com base neste comentário)") || "").trim();
    if (!comentario) return;
    btnRejeitar.disabled = true;
    try {
      await api.rejeitarPlano(dados.id, comentario);
    } catch (e) {
      bannerErro("Falha ao rejeitar: " + e.message);
      btnRejeitar.disabled = false;
      return;
    }
    bannerErro("");
    await reabrir();
  });

  cont.append(el("div", { class: "perguntas-acoes" }, btnSalvar, btnAprovar, btnRejeitar));
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
