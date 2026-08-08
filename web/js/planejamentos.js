// Tela "Planejamentos" — sessões iterativas com o estrategista (especialista de
// produto/arquitetura): o usuário descreve a necessidade, escolhe o foco (PRD,
// ADRs ou ambos) e o nível visual (documento, apresentação ou protótipo), e o
// estrategista lê o código e lapida os documentos a cada turno da conversa. Os
// documentos (.md versionados) e os artefatos (.html autocontidos, abertos em
// nova aba sandbox) ficam em abas ao lado da conversa; um botão cria a demanda
// a partir do PRD pronto.

import { api } from "./api.js";
import { el, limpar, toast, bannerErro, renderMarkdown, autoCrescer, mdEditor } from "./ui.js";
import { abrirCard, setProjetos } from "./demandas.js";

let planejamentos = [];
let selecionadoID = null;
let esProgresso = null; // EventSource do progresso do turno em andamento
let pollTimer = null;   // fallback: relê o planejamento enquanto status=pensando

const PAPEIS = {
  user: ["Você", "user"],
  estrategista: ["Praxis · Estrategista", "agent"],
  sistema: ["", "sys"],
};

const FOCOS = { prd: "PRD", adr: "ADRs", ambos: "PRD + ADRs" };
const NIVEIS = { documento: "Documento", apresentacao: "Apresentação", prototipo: "Protótipo" };

export async function montarPlanejamentos() {
  document.getElementById("btn-novo-planejamento").onclick = () => renderNovo();
  await recarregarLista();
  if (selecionadoID != null) {
    const p = planejamentos.find((x) => x.id === selecionadoID);
    if (p) await abrirPlanejamento(p.id);
    else limparPainel();
  }
}

// desmontarPlanejamentos fecha o SSE e o poll ao sair da view.
export function desmontarPlanejamentos() {
  pararAcompanhamento();
}

function pararAcompanhamento() {
  if (esProgresso) { esProgresso.close(); esProgresso = null; }
  if (pollTimer) { clearTimeout(pollTimer); pollTimer = null; }
}

async function recarregarLista() {
  try {
    planejamentos = (await api.listarPlanejamentos()) || [];
  } catch (e) {
    bannerErro("Falha ao carregar planejamentos: " + e.message);
    return;
  }
  bannerErro("");
  const lista = limpar(document.getElementById("lista-planejamentos"));
  if (planejamentos.length === 0) {
    lista.append(el("p", { class: "sub", text: "Nenhum planejamento ainda. Inicie um novo." }));
    return;
  }
  for (const p of planejamentos) {
    const alvo = p.grupo_nome ? "Grupo: " + p.grupo_nome : p.projeto_nome || "";
    lista.append(el("div", {
      class: "list-item" + (p.id === selecionadoID ? " sel" : ""),
      onclick: () => abrirPlanejamento(p.id),
    },
      el("b", { text: p.titulo || `Planejamento #${p.id}` }),
      el("div", { class: "path", text: alvo }),
      el("div", { class: "meta" },
        pillStatusPlanejamento(p.status),
        el("span", { class: "pill", text: FOCOS[p.foco] || p.foco }),
        p.demand_id ? el("span", { class: "pill" }, el("span", { class: "dot dot-done" }), `demanda #${p.demand_id}`) : null,
        p.custo_usd > 0 ? el("span", { class: "pill", text: "US$ " + p.custo_usd.toFixed(2) }) : null,
      ),
    ));
  }
}

// abrirDemandaModal abre o card da demanda por cima da tela atual (o modal de
// demandas é independente da view; só precisa dos projetos carregados).
async function abrirDemandaModal(id) {
  try {
    setProjetos((await api.listarProjetos()) || []);
  } catch {
    setProjetos([]);
  }
  await abrirCard(id);
}

// baixarTexto baixa um conteúdo textual como arquivo na máquina do usuário.
function baixarTexto(nome, conteudo) {
  const blob = new Blob([conteudo], { type: "text/markdown;charset=utf-8" });
  const url = URL.createObjectURL(blob);
  const a = el("a", { href: url });
  a.download = nome;
  a.click();
  URL.revokeObjectURL(url);
}

// baixarURL navega para uma URL que responde com Content-Disposition:
// attachment — o navegador baixa sem sair da página.
function baixarURL(url) {
  const a = el("a", { href: url });
  a.download = "";
  a.click();
}

function pillStatusPlanejamento(status) {
  const mapa = {
    ocioso: ["dot-good", "pronto"],
    pensando: ["dot-warn", "planejando…"],
    falhou: ["dot-crit", "falhou"],
  };
  const [dot, rotulo] = mapa[status] || ["dot-muted", status];
  return el("span", { class: "pill" }, el("span", { class: "dot " + dot }), rotulo);
}

function limparPainel() {
  pararAcompanhamento();
  const painel = limpar(document.getElementById("painel-planejamento"));
  painel.append(el("p", { class: "sub", style: "margin:0", text: "Selecione um planejamento à esquerda ou inicie um novo." }));
}

// ---------- novo planejamento ----------

async function renderNovo() {
  pararAcompanhamento();
  selecionadoID = null;
  await recarregarLista();
  const painel = limpar(document.getElementById("painel-planejamento"));
  painel.append(el("h3", {}, "Novo planejamento"));

  let grupos = [], projetos = [];
  try {
    [grupos, projetos] = await Promise.all([
      api.listarGrupos().catch(() => []),
      api.listarProjetos(),
    ]);
  } catch (e) {
    painel.append(el("p", { class: "sub", text: "Falha ao carregar projetos: " + e.message }));
    return;
  }
  grupos = (grupos || []).filter((g) => g.ativo);
  projetos = (projetos || []).filter((p) => p.ativo);
  if (grupos.length === 0 && projetos.length === 0) {
    painel.append(el("p", { class: "sub", text: "Nenhum projeto cadastrado. Peça a um administrador para cadastrar em Projetos." }));
    return;
  }

  const sel = el("select", {});
  for (const g of grupos) sel.append(el("option", { value: "g:" + g.id }, "Grupo: " + g.nome));
  for (const p of projetos) sel.append(el("option", { value: "p:" + p.id }, p.nome));

  const selFoco = el("select", {},
    el("option", { value: "prd" }, "PRD — visão de negócio"),
    el("option", { value: "adr" }, "ADRs — decisões arquiteturais"),
    el("option", { value: "ambos" }, "PRD + ADRs"),
  );
  const selNivel = el("select", {},
    el("option", { value: "apresentacao" }, "Apresentação — documento + infográficos/fluxogramas"),
    el("option", { value: "documento" }, "Documento — só o texto"),
    el("option", { value: "prototipo" }, "Protótipo — apresentação + simulação navegável das telas"),
  );

  const ed = mdEditor({
    rows: 5,
    placeholder: "Descreva a necessidade. Ex.: \"Precisamos de um portal de autoatendimento para segunda via de boletos\", \"Definir a arquitetura de integração com o novo gateway de pagamentos\".",
  });

  // Referências opcionais anexadas já na criação (transcrição de reunião, ADR
  // de outro projeto…): o planejamento nasce sem disparar o turno, os arquivos
  // sobem e só então o estrategista roda — assim o 1º turno já as enxerga.
  const inputAnexos = el("input", {
    type: "file", multiple: true, hidden: true,
    accept: ".md,.txt,.csv,.json,.pdf,.html,.png,.jpg,.jpeg,.webp",
  });
  const listaAnexos = el("span", { class: "hint", text: "nenhum arquivo selecionado" });
  const btnAnexos = el("button", { class: "btn ghost sm", text: "📎 Selecionar arquivos" });
  btnAnexos.onclick = (ev) => { ev.preventDefault(); inputAnexos.click(); };
  inputAnexos.onchange = () => {
    const nomes = [...inputAnexos.files].map((f) => f.name);
    listaAnexos.textContent = nomes.length ? nomes.join(" · ") : "nenhum arquivo selecionado";
  };

  const btn = el("button", { class: "btn", text: "Iniciar planejamento" });
  btn.onclick = async () => {
    const mensagem = ed.ta.value.trim();
    if (!mensagem) { bannerErro("Descreva a necessidade."); return; }
    bannerErro("");
    btn.disabled = true;
    const anexos = [...inputAnexos.files];
    try {
      const [tipo, id] = sel.value.split(":");
      const corpo = { mensagem, foco: selFoco.value, nivel_visual: selNivel.value };
      if (tipo === "g") corpo.group_id = Number(id);
      else corpo.project_id = Number(id);
      if (anexos.length > 0) corpo.anexos_pendentes = true;

      const criado = await api.criarPlanejamento(corpo);
      if (anexos.length > 0) {
        for (const arq of anexos) {
          try {
            await api.enviarReferenciaPlanejamento(criado.id, arq);
          } catch (e) {
            bannerErro(`Falha ao anexar ${arq.name}: ` + e.message + " — o planejamento segue sem este arquivo.");
          }
        }
        await api.dispararTurnoPlanejamento(criado.id);
      }
      toast("Planejamento iniciado — o estrategista está trabalhando.", "ok");
      selecionadoID = criado.id;
      await recarregarLista();
      await abrirPlanejamento(criado.id);
    } catch (e) {
      bannerErro("Falha ao iniciar planejamento: " + e.message);
      btn.disabled = false;
    }
  };

  painel.append(el("div", { class: "form" },
    el("div", {}, el("label", {}, "Projeto ou solução"), sel,
      el("div", { class: "hint", text: "Num grupo, o estrategista enxerga todos os repositórios da solução." })),
    el("div", { class: "row" },
      el("div", {}, el("label", {}, "O que produzir"), selFoco),
      el("div", {}, el("label", {}, "Nível visual"), selNivel),
    ),
    el("div", {}, el("label", {}, "A necessidade"), ed.no,
      el("div", { class: "hint", text: "Não precisa estar redondo: o estrategista lê o código e faz perguntas antes de fechar o documento. Você pode mudar o foco e o nível visual a qualquer momento." })),
    el("div", {}, el("label", {}, "Referências ", el("span", { class: "opt", text: "(opcional)" })),
      el("div", { style: "display:flex;align-items:center;gap:10px" }, btnAnexos, inputAnexos, listaAnexos),
      el("div", { class: "hint", text: "Anexe o material que o estrategista deve tomar como base: transcrição da reunião, ADR de outro projeto, rascunhos… (md, txt, csv, json, pdf, html ou imagem · até 15 MB cada)" })),
    el("div", { class: "acoes" }, btn),
  ));
  ed.ta.focus();
}

// ---------- planejamento aberto ----------

async function abrirPlanejamento(id, abaInicial) {
  pararAcompanhamento();
  selecionadoID = id;
  await recarregarLista();

  let plan;
  try {
    plan = await api.obterPlanejamento(id);
  } catch (e) {
    bannerErro("Falha ao abrir planejamento: " + e.message);
    return;
  }

  const painel = limpar(document.getElementById("painel-planejamento"));
  const alvo = plan.grupo_nome ? "Grupo: " + plan.grupo_nome : plan.projeto_nome || "";

  const cab = el("div", { style: "display:flex;align-items:center;justify-content:space-between;gap:10px" },
    el("h3", { style: "margin:0", text: plan.titulo || `Planejamento #${plan.id}` }),
    el("div", { style: "display:flex;gap:6px" },
      botaoCriarDemanda(plan),
      el("button", {
        class: "btn ghost sm", text: "Excluir",
        onclick: async () => {
          if (!confirm("Excluir este planejamento, com documentos e artefatos?")) return;
          try {
            await api.excluirPlanejamento(plan.id);
            toast("Planejamento excluído.", "ok");
            selecionadoID = null;
            limparPainel();
            await recarregarLista();
          } catch (e) {
            bannerErro("Falha ao excluir: " + e.message);
          }
        },
      }),
    ),
  );
  const sub = el("p", { class: "sub", style: "margin:4px 0 10px", text: alvo +
    (plan.custo_usd > 0 ? ` · custo acumulado US$ ${plan.custo_usd.toFixed(2)}` : "") });

  // Preferências ajustáveis entre turnos (o próximo turno usa os valores novos).
  const selFoco = el("select", {},
    ...Object.entries(FOCOS).map(([v, r]) => el("option", { value: v }, r)));
  selFoco.value = plan.foco;
  const selNivel = el("select", {},
    ...Object.entries(NIVEIS).map(([v, r]) => el("option", { value: v }, r)));
  selNivel.value = plan.nivel_visual;
  const aoAjustar = async () => {
    try {
      plan = await api.atualizarPlanejamento(plan.id, { foco: selFoco.value, nivel_visual: selNivel.value });
      toast("Preferências atualizadas — valem a partir do próximo turno.", "ok");
    } catch (e) {
      selFoco.value = plan.foco;
      selNivel.value = plan.nivel_visual;
      bannerErro("Falha ao ajustar: " + e.message);
    }
  };
  selFoco.onchange = aoAjustar;
  selNivel.onchange = aoAjustar;
  const prefs = el("div", { style: "margin-bottom:10px" },
    el("div", { class: "filtros" },
      el("span", { class: "hint", text: "Produzir:" }), selFoco,
      el("span", { class: "hint", text: "Visual:" }), selNivel,
    ),
    el("div", { class: "hint", style: "margin-top:4px",
      text: "Trabalho em etapas: o PO pode fechar o PRD e depois o arquiteto muda \"Produzir\" para PRD + ADRs e continua nesta mesma conversa — os documentos ficam." }),
  );

  // Abas: Conversa | Documentos | Artefatos | Referências.
  const corpoConversa = el("div", { class: "tab-body active" });
  const corpoDocs = el("div", { class: "tab-body" });
  const corpoArts = el("div", { class: "tab-body" });
  const corpoRefs = el("div", { class: "tab-body" });
  const abas = [
    ["Conversa", corpoConversa, null],
    ["Documentos", corpoDocs, () => renderDocumentos(corpoDocs, plan)],
    ["Artefatos", corpoArts, () => renderArtefatos(corpoArts, plan)],
    ["Referências", corpoRefs, () => renderReferencias(corpoRefs, plan)],
  ];
  const corpos = [corpoConversa, corpoDocs, corpoArts, corpoRefs];
  const barra = el("div", { class: "tabs" });
  const botoesAba = abas.map(([nome, corpo, ativar], i) => {
    const b = el("button", { class: "tab" + (i === 0 ? " active" : ""), text: nome });
    b.onclick = async () => {
      barra.querySelectorAll(".tab").forEach((t) => t.classList.remove("active"));
      corpos.forEach((c) => c.classList.remove("active"));
      b.classList.add("active");
      corpo.classList.add("active");
      if (ativar) await ativar();
    };
    barra.append(b);
    return b;
  });

  // --- aba Conversa ---
  const box = el("div", { class: "chat" });
  const progresso = el("div", { class: "hint", hidden: true });
  const acoesTurno = el("div", { style: "margin-top:8px", hidden: true });
  const inp = el("textarea", { rows: "1",
    placeholder: "Responder ao estrategista ou pedir ajustes no documento… (Shift+Enter quebra linha)" });
  const ajustarAltura = autoCrescer(inp);
  const btn = el("button", { class: "btn", text: "Enviar" });

  // 📎 anexa referências direto da conversa (fica ativo mesmo com turno em voo:
  // o anexo vale a partir do turno seguinte).
  const inputClip = el("input", {
    type: "file", multiple: true, hidden: true,
    accept: ".md,.txt,.csv,.json,.pdf,.html,.png,.jpg,.jpeg,.webp",
  });
  const btnClip = el("button", { class: "btn ghost", text: "📎", title: "Anexar referências (transcrições, ADRs, rascunhos…)" });
  btnClip.onclick = () => inputClip.click();
  inputClip.onchange = async () => {
    const arquivos = [...inputClip.files];
    if (arquivos.length === 0) return;
    let enviados = 0;
    for (const arq of arquivos) {
      try {
        await api.enviarReferenciaPlanejamento(plan.id, arq);
        enviados++;
      } catch (e) {
        bannerErro(`Falha ao anexar ${arq.name}: ` + e.message);
      }
    }
    inputClip.value = "";
    if (enviados > 0) {
      toast(`${enviados} referência(s) anexada(s) — cite-as na sua mensagem para orientar o estrategista.`, "ok");
    }
  };

  corpoConversa.append(box, progresso, acoesTurno,
    el("div", { class: "chat-input" }, btnClip, inputClip, inp, btn));

  painel.append(cab, sub, prefs, barra, corpoConversa, corpoDocs, corpoArts, corpoRefs);

  async function recarregarChat() {
    let msgs;
    try {
      msgs = (await api.listarChatPlanejamento(plan.id)) || [];
    } catch (e) {
      limpar(box).append(el("p", { class: "vazio", text: "Falha ao carregar a conversa: " + e.message }));
      return;
    }
    limpar(box);
    if (msgs.length === 0) {
      box.append(el("p", { class: "vazio", text: "Nenhuma mensagem ainda." }));
    }
    for (const m of msgs) box.append(bolha(m, plan));
    if (plan.status === "pensando") {
      box.append(el("div", { class: "msg sys", text: "O estrategista está lendo o código e trabalhando nos documentos — isso pode levar alguns minutos." }));
    }
    box.scrollTop = box.scrollHeight;
  }

  function aplicarEstado() {
    const pensando = plan.status === "pensando";
    inp.disabled = pensando;
    btn.disabled = pensando;
    selFoco.disabled = pensando;
    selNivel.disabled = pensando;
    progresso.hidden = !pensando;
    if (pensando) {
      progresso.textContent = "trabalhando…";
      acompanharProgresso();
      agendarPoll();
    } else {
      pararAcompanhamento();
    }
  }

  function acompanharProgresso() {
    if (esProgresso) return;
    try {
      esProgresso = new EventSource(api.urlProgressoPlanejamento(plan.id));
      esProgresso.onmessage = (ev) => {
        try {
          const d = JSON.parse(ev.data);
          if (d.acao === "concluindo") {
            progresso.textContent = "concluindo o turno…";
          } else if (d.detalhe) {
            progresso.textContent = d.detalhe + "…";
          }
        } catch { /* linha desconhecida: ignora */ }
      };
      esProgresso.onerror = () => { /* o poll de fallback cobre a queda do SSE */ };
    } catch { /* sem EventSource: o poll cobre */ }
  }

  function agendarPoll() {
    if (pollTimer) return;
    const tick = async () => {
      pollTimer = null;
      if (selecionadoID !== plan.id) return;
      let atual;
      try {
        atual = await api.obterPlanejamento(plan.id);
      } catch {
        pollTimer = setTimeout(tick, 5000);
        return;
      }
      if (atual.status !== "pensando") {
        await abrirPlanejamento(plan.id);
        return;
      }
      pollTimer = setTimeout(tick, 5000);
    };
    pollTimer = setTimeout(tick, 5000);
  }

  async function enviar() {
    const texto = inp.value.trim();
    if (!texto) return;
    btn.disabled = true;
    try {
      await api.enviarChatPlanejamento(plan.id, texto);
      inp.value = "";
      ajustarAltura();
      await abrirPlanejamento(plan.id); // re-renderiza já em modo "pensando"
    } catch (e) {
      if (e.status === 409) {
        bannerErro("O estrategista ainda está trabalhando — aguarde a resposta.");
        await abrirPlanejamento(plan.id);
        return;
      }
      bannerErro("Falha ao enviar: " + e.message);
      btn.disabled = false;
    }
  }
  btn.addEventListener("click", enviar);
  inp.addEventListener("keydown", (e) => {
    if (e.key === "Enter" && !e.shiftKey) { e.preventDefault(); enviar(); }
  });

  await recarregarChat();
  aplicarEstado();
  if (abaInicial) {
    const i = abas.findIndex(([nome]) => nome === abaInicial);
    if (i > 0) botoesAba[i].click();
  }
  if (plan.status === "falhou") {
    if (plan.erro) {
      bannerErro("Último turno falhou: " + plan.erro);
    }
    const btnRetry = el("button", { class: "btn sm", text: "↻ Tentar novamente" });
    btnRetry.onclick = async () => {
      btnRetry.disabled = true;
      try {
        await api.dispararTurnoPlanejamento(plan.id);
        await abrirPlanejamento(plan.id); // reabre já em modo "pensando"
      } catch (e) {
        bannerErro("Falha ao reprocessar: " + e.message);
        btnRetry.disabled = false;
      }
    };
    acoesTurno.hidden = false;
    acoesTurno.append(btnRetry,
      el("span", { class: "hint", style: "margin-left:8px", text: "Reprocessa a conversa atual, sem precisar reenviar a mensagem." }));
  }
}

// botaoCriarDemanda monta o botão de handoff: cria a demanda a partir do PRD
// mais recente (num grupo, pergunta antes qual repositório recebe a demanda).
// Quando a demanda já existe, vira um marcador com o número dela.
function botaoCriarDemanda(plan) {
  if (plan.demand_id) {
    return el("button", {
      class: "btn ghost sm", title: "Abrir o card da demanda",
      onclick: () => abrirDemandaModal(plan.demand_id),
    }, el("span", { class: "dot dot-done", style: "margin-right:6px" }), `Demanda #${plan.demand_id} ⧉`);
  }
  const b = el("button", { class: "btn good sm", text: "Criar demanda" });
  b.onclick = async () => {
    const corpo = {};
    if (plan.group_id) {
      let grupo;
      try {
        grupo = await api.obterGrupo(plan.group_id);
      } catch (e) {
        bannerErro("Falha ao carregar o grupo: " + e.message);
        return;
      }
      const membros = grupo.membros || [];
      if (membros.length === 0) { bannerErro("O grupo não tem repositórios."); return; }
      const nomes = membros.map((m, i) => `${i + 1} — ${m.nome || m.project_id}`).join("\n");
      const escolha = prompt("Qual repositório recebe a demanda?\n" + nomes, "1");
      if (escolha == null) return;
      const idx = Number(escolha) - 1;
      if (!(idx >= 0 && idx < membros.length)) { bannerErro("Escolha inválida."); return; }
      corpo.project_id = membros[idx].project_id;
    } else if (!confirm("Criar a demanda a partir do documento atual do planejamento?")) {
      return;
    }
    b.disabled = true;
    try {
      const dem = await api.criarDemandaDePlanejamento(plan.id, corpo);
      toast(`Demanda #${dem.id} criada — o analista já está lendo o PRD.`, "ok");
      await abrirPlanejamento(plan.id);
      await abrirDemandaModal(dem.id); // abre o card da demanda por cima
    } catch (e) {
      bannerErro("Falha ao criar demanda: " + e.message);
      b.disabled = false;
    }
  };
  return b;
}

// ---------- aba Documentos ----------

const NOMES_DOCS = { "prd.md": "PRD", "adrs.md": "ADRs" };

async function renderDocumentos(corpo, plan) {
  limpar(corpo).append(el("p", { class: "vazio", text: "Carregando documentos…" }));
  let docs;
  try {
    docs = (await api.listarDocumentosPlanejamento(plan.id)) || [];
  } catch (e) {
    limpar(corpo).append(el("p", { class: "vazio", text: "Falha ao carregar documentos: " + e.message }));
    return;
  }
  limpar(corpo);
  if (docs.length === 0) {
    corpo.append(el("p", { class: "vazio", text: "Nenhum documento ainda — o estrategista os cria conforme a conversa avança." }));
    return;
  }
  for (const doc of docs) corpo.append(cartaoDocumento(plan, doc));
}

// cartaoDocumento renderiza um documento com seletor de revisão (a mais recente
// primeiro) e o markdown renderizado.
function cartaoDocumento(plan, doc) {
  let conteudoAtual = doc.conteudo || "";
  let revisaoAtual = doc.revisao;
  const selRev = el("select", {});
  for (let r = doc.revisao; r >= 1; r--) {
    selRev.append(el("option", { value: String(r) }, r === doc.revisao ? `revisão ${r} (atual)` : `revisão ${r}`));
  }
  const corpoMD = el("div", { class: "md", style: "margin-top:8px" }, ...renderMarkdown(conteudoAtual));
  selRev.onchange = async () => {
    try {
      const d = await api.obterDocumentoPlanejamento(plan.id, doc.arquivo, Number(selRev.value));
      conteudoAtual = d.conteudo || "";
      revisaoAtual = d.revisao;
      limpar(corpoMD).append(...renderMarkdown(conteudoAtual));
    } catch (e) {
      bannerErro("Falha ao carregar a revisão: " + e.message);
    }
  };
  const btnBaixar = el("button", {
    class: "btn ghost sm", text: "Baixar .md", title: "Baixa a revisão exibida",
    onclick: () => baixarTexto(doc.arquivo.replace(/\.md$/, "") + `-rev${revisaoAtual}.md`, conteudoAtual),
  });
  return el("div", { class: "panel", style: "margin-bottom:12px" },
    el("div", { style: "display:flex;align-items:center;justify-content:space-between;gap:10px" },
      el("h3", { style: "margin:0" }, NOMES_DOCS[doc.arquivo] || doc.arquivo,
        el("small", { text: " " + doc.arquivo })),
      el("div", { style: "display:flex;gap:6px;align-items:center" }, selRev, btnBaixar),
    ),
    corpoMD,
  );
}

// ---------- aba Artefatos ----------

async function renderArtefatos(corpo, plan) {
  limpar(corpo).append(el("p", { class: "vazio", text: "Carregando artefatos…" }));
  let arts;
  try {
    arts = (await api.listarArtefatosPlanejamento(plan.id)) || [];
  } catch (e) {
    limpar(corpo).append(el("p", { class: "vazio", text: "Falha ao carregar artefatos: " + e.message }));
    return;
  }
  limpar(corpo);
  if (arts.length === 0) {
    corpo.append(el("p", { class: "vazio", text: "Nenhum artefato visual ainda. Nos níveis Apresentação e Protótipo, o estrategista os gera junto com o documento." }));
    return;
  }
  for (const a of arts) {
    corpo.append(el("div", { class: "list-item", style: "cursor:default" },
      el("div", { style: "display:flex;align-items:center;justify-content:space-between;gap:10px" },
        el("b", { text: a.titulo || a.arquivo }),
        el("div", { style: "display:flex;gap:6px" },
          el("button", {
            class: "btn ghost sm", text: "Baixar",
            onclick: () => baixarURL(api.urlDownloadArtefatoPlanejamento(plan.id, a.arquivo)),
          }),
          el("button", {
            class: "btn sm", text: "Abrir ⧉",
            onclick: () => window.open(api.urlArtefatoPlanejamento(plan.id, a.arquivo), "_blank", "noopener"),
          }),
        ),
      ),
      a.descricao ? el("div", { class: "path", text: a.descricao }) : null,
      el("div", { class: "meta" },
        el("span", { class: "pill", text: a.arquivo }),
        el("span", { class: "pill", text: "rev " + a.revisao }),
        el("span", { class: "pill", text: formatarTamanho(a.tamanho) }),
      ),
    ));
  }
  corpo.append(el("p", { class: "hint", text: "Os artefatos abrem em nova aba, isolados numa sandbox — eles não têm acesso à sua sessão do Praxis." }));
}

// ---------- aba Referências ----------

// renderReferencias lista os documentos de apoio anexados (ADRs de outros
// projetos, transcrições de reunião…) e permite anexar novos — o próximo turno
// do estrategista já os enxerga.
async function renderReferencias(corpo, plan) {
  limpar(corpo).append(el("p", { class: "vazio", text: "Carregando referências…" }));
  let refs;
  try {
    refs = (await api.listarReferenciasPlanejamento(plan.id)) || [];
  } catch (e) {
    limpar(corpo).append(el("p", { class: "vazio", text: "Falha ao carregar referências: " + e.message }));
    return;
  }
  limpar(corpo);

  const inputArquivos = el("input", {
    type: "file", multiple: true, hidden: true,
    accept: ".md,.txt,.csv,.json,.pdf,.html,.png,.jpg,.jpeg,.webp",
  });
  const btnAnexar = el("button", { class: "btn", text: "+ Anexar arquivos" });
  btnAnexar.onclick = () => inputArquivos.click();
  inputArquivos.onchange = async () => {
    const arquivos = [...inputArquivos.files];
    if (arquivos.length === 0) return;
    btnAnexar.disabled = true;
    let enviados = 0;
    for (const arq of arquivos) {
      try {
        await api.enviarReferenciaPlanejamento(plan.id, arq);
        enviados++;
      } catch (e) {
        bannerErro(`Falha ao anexar ${arq.name}: ` + e.message);
      }
    }
    if (enviados > 0) {
      toast(`${enviados} referência(s) anexada(s) — o estrategista as verá no próximo turno.`, "ok");
    }
    await renderReferencias(corpo, plan);
  };
  corpo.append(el("div", { style: "display:flex;align-items:center;gap:10px;margin-bottom:12px" },
    btnAnexar, inputArquivos,
    el("span", { class: "hint", text: "md, txt, csv, json, pdf, html ou imagem · até 15 MB cada" }),
  ));

  if (refs.length === 0) {
    corpo.append(el("p", { class: "vazio", text: "Nenhuma referência ainda. Anexe uma ADR de outro projeto, a transcrição de uma reunião ou qualquer material que o estrategista deva tomar como base." }));
    return;
  }
  for (const ref of refs) {
    corpo.append(el("div", { class: "list-item", style: "cursor:default" },
      el("div", { style: "display:flex;align-items:center;justify-content:space-between;gap:10px" },
        el("b", { text: ref.arquivo }),
        el("div", { style: "display:flex;gap:6px" },
          el("button", {
            class: "btn ghost sm", text: "Baixar",
            onclick: () => baixarURL(api.urlReferenciaPlanejamento(plan.id, ref.arquivo)),
          }),
          el("button", {
            class: "btn ghost sm", text: "Excluir",
            onclick: async () => {
              if (!confirm(`Excluir a referência ${ref.arquivo}?`)) return;
              try {
                await api.excluirReferenciaPlanejamento(plan.id, ref.arquivo);
                toast("Referência excluída.", "ok");
                await renderReferencias(corpo, plan);
              } catch (e) {
                bannerErro("Falha ao excluir: " + e.message);
              }
            },
          }),
        ),
      ),
      el("div", { class: "meta" },
        el("span", { class: "pill", text: formatarTamanho(ref.tamanho) }),
      ),
    ));
  }
  corpo.append(el("p", { class: "hint", text: "As referências ficam na pasta do planejamento como insumo somente leitura: o estrategista as consulta, mas nunca as altera." }));
}

function formatarTamanho(bytes) {
  if (!bytes) return "0 KB";
  if (bytes < 1024) return bytes + " B";
  if (bytes < 1024 * 1024) return (bytes / 1024).toFixed(0) + " KB";
  return (bytes / (1024 * 1024)).toFixed(1) + " MB";
}

// ---------- bolhas ----------

// bolha renderiza uma fala. No meta do estrategista vêm os documentos e
// artefatos tocados no turno — viram atalhos sob a mensagem.
function bolha(m, plan) {
  let [rotulo, classe] = PAPEIS[m.papel] || [m.papel, "sys"];
  if (classe === "sys") {
    return el("div", { class: "msg sys", text: m.conteudo || rotulo });
  }
  const extras = [];
  try {
    const meta = typeof m.meta === "string" ? JSON.parse(m.meta) : m.meta;
    // Chat colaborativo: a fala do usuário mostra QUEM falou (PO, arquiteto…).
    if (m.papel === "user" && meta && meta.autor) rotulo = meta.autor;
    if (meta && meta.tipo === "perguntas") {
      extras.push(el("div", { class: "hint", text: "Responda no campo abaixo para o estrategista continuar." }));
    }
    if (meta && Array.isArray(meta.documentos) && meta.documentos.length > 0) {
      extras.push(el("div", { class: "hint", text: "Documentos atualizados: " + meta.documentos.join(" · ") + " (aba Documentos)" }));
    }
    if (meta && Array.isArray(meta.artefatos) && meta.artefatos.length > 0) {
      const links = el("div", { style: "display:flex;gap:6px;flex-wrap:wrap;margin-top:6px" });
      for (const a of meta.artefatos) {
        if (!a || !a.arquivo) continue;
        links.append(el("button", {
          class: "btn ghost sm", text: (a.titulo || a.arquivo) + " ⧉",
          onclick: () => window.open(api.urlArtefatoPlanejamento(plan.id, a.arquivo), "_blank", "noopener"),
        }));
      }
      extras.push(links);
    }
  } catch { /* meta inválido: segue sem extras */ }
  return el("div", { class: "msg " + classe },
    rotulo ? el("div", { class: "who", text: rotulo }) : null,
    el("div", { class: "txt md" }, ...renderMarkdown(m.conteudo || "")),
    ...extras,
  );
}
