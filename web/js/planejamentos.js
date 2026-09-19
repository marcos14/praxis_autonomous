// Tela "Planejamentos" — sessões iterativas com o estrategista (especialista de
// produto/arquitetura): o usuário descreve a necessidade, escolhe o foco (PRD,
// ADRs ou ambos) e o nível visual (documento, apresentação ou protótipo), e o
// estrategista lê o código e lapida os documentos a cada turno da conversa. Os
// documentos (.md versionados) e os artefatos (.html autocontidos, abertos em
// nova aba sandbox) ficam em abas ao lado da conversa; um botão cria a demanda
// a partir do PRD pronto.

import { api } from "./api.js";
import { t } from "./i18n.js";
import { el, limpar, toast, bannerErro, renderMarkdown, autoCrescer, mdEditor } from "./ui.js";
import { abrirCard, setProjetos, pillStatus } from "./demandas.js";
import { fixarRota } from "./rota.js";
import {
  seletorVisibilidade, campoVisibilidade, lembrarVisibilidade,
  pillVisibilidade, pillAutor, controleVisibilidade, filtroEscopo, paramEscopo,
} from "./visibilidade.js";

let planejamentos = [];
let selecionadoID = null;
let esProgresso = null; // EventSource do progresso do turno em andamento
let pollTimer = null;   // fallback: relê o planejamento enquanto status=pensando

const PAPEIS = {
  user: [t("consultas.papel_voce"), "user"],
  estrategista: [t("planejamentos.papel_estrategista"), "agent"],
  sistema: ["", "sys"],
};

const FOCOS = { prd: t("planejamentos.foco_prd"), adr: t("planejamentos.foco_adr"), ambos: t("planejamentos.foco_ambos") };
const NIVEIS = { documento: t("planejamentos.nivel_documento"), apresentacao: t("planejamentos.nivel_apresentacao"), prototipo: t("planejamentos.nivel_prototipo") };

// escopoAtual é o filtro Todos · Meus · Do grupo da lista (lembrado por tela).
let escopoAtual = "todos";

function montarFiltroEscopo() {
  const lista = document.getElementById("lista-planejamentos");
  let cont = document.getElementById("filtro-planejamentos");
  if (!cont) {
    cont = el("div", { id: "filtro-planejamentos", class: "filtros" });
    lista.before(cont);
  }
  const f = filtroEscopo("planejamentos", (e) => { escopoAtual = e; recarregarLista(); });
  escopoAtual = f.valor();
  limpar(cont).append(f.no);
}

// id (opcional) vem da rota "#planejamentos/3".
export async function montarPlanejamentos(id) {
  document.getElementById("btn-novo-planejamento").onclick = () => renderNovo();
  montarFiltroEscopo();
  if (id) { await abrirPlanejamento(Number(id)); return; }
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
    planejamentos = (await api.listarPlanejamentos({ escopo: paramEscopo(escopoAtual) })) || [];
  } catch (e) {
    bannerErro("Falha ao carregar planejamentos: " + e.message);
    return;
  }
  bannerErro("");
  const lista = limpar(document.getElementById("lista-planejamentos"));
  if (planejamentos.length === 0) {
    lista.append(el("p", { class: "sub", text: t("planejamentos.nenhum") }));
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
        pillVisibilidade(p),
        pillAutor(p),
        el("span", { class: "pill", text: FOCOS[p.foco] || p.foco }),
        p.demandas_criadas > 0 ? el("span", { class: "pill" }, el("span", { class: "dot dot-done" }),
          p.demandas_criadas === 1 ? "1 demanda" : `${p.demandas_criadas} demandas`) : null,
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
    ocioso: ["dot-good", t("planejamentos.status_pronto")],
    pensando: ["dot-warn", t("planejamentos.status_planejando")],
    falhou: ["dot-crit", "falhou"],
  };
  const [dot, rotulo] = mapa[status] || ["dot-muted", status];
  return el("span", { class: "pill" }, el("span", { class: "dot " + dot }), rotulo);
}

function limparPainel() {
  pararAcompanhamento();
  const painel = limpar(document.getElementById("painel-planejamento"));
  painel.append(el("p", { class: "sub", style: "margin:0", text: t("view.planejamentos.selecione") }));
}

// ---------- novo planejamento ----------

async function renderNovo() {
  pararAcompanhamento();
  selecionadoID = null;
  await recarregarLista();
  const painel = limpar(document.getElementById("painel-planejamento"));
  painel.append(el("h3", {}, t("planejamentos.novo_titulo")));

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
    painel.append(el("p", { class: "sub", text: t("consultas.sem_projetos") }));
    return;
  }

  const sel = el("select", {});
  for (const g of grupos) sel.append(el("option", { value: "g:" + g.id }, "Grupo: " + g.nome));
  for (const p of projetos) sel.append(el("option", { value: "p:" + p.id }, p.nome));

  const selFoco = el("select", {},
    el("option", { value: "prd" }, t("planejamentos.op_prd")),
    el("option", { value: "adr" }, t("planejamentos.op_adr")),
    el("option", { value: "ambos" }, t("planejamentos.foco_ambos")),
  );
  const selNivel = el("select", {},
    el("option", { value: "apresentacao" }, t("planejamentos.op_apresentacao")),
    el("option", { value: "documento" }, t("planejamentos.op_documento")),
    el("option", { value: "prototipo" }, t("planejamentos.op_prototipo")),
  );

  const ed = mdEditor({
    rows: 5,
    placeholder: t("planejamentos.ph_necessidade"),
  });

  // Referências opcionais anexadas já na criação (transcrição de reunião, ADR
  // de outro projeto…): o planejamento nasce sem disparar o turno, os arquivos
  // sobem e só então o estrategista roda — assim o 1º turno já as enxerga.
  const inputAnexos = el("input", {
    type: "file", multiple: true, hidden: true,
    accept: ".md,.txt,.csv,.json,.pdf,.html,.xml,.tx2,.png,.jpg,.jpeg,.webp",
  });
  const listaAnexos = el("span", { class: "hint", text: t("planejamentos.nenhum_arquivo") });
  const btnAnexos = el("button", { class: "btn ghost sm", text: t("planejamentos.selecionar_arquivos") });
  btnAnexos.onclick = (ev) => { ev.preventDefault(); inputAnexos.click(); };
  inputAnexos.onchange = () => {
    const nomes = [...inputAnexos.files].map((f) => f.name);
    listaAnexos.textContent = nomes.length ? nomes.join(" · ") : t("planejamentos.nenhum_arquivo");
  };

  const selVis = seletorVisibilidade();
  const btn = el("button", { class: "btn", text: t("planejamentos.iniciar") });
  btn.onclick = async () => {
    const mensagem = ed.ta.value.trim();
    if (!mensagem) { bannerErro(t("planejamentos.descreva")); return; }
    bannerErro("");
    btn.disabled = true;
    const anexos = [...inputAnexos.files];
    try {
      const [tipo, id] = sel.value.split(":");
      const corpo = { mensagem, foco: selFoco.value, nivel_visual: selNivel.value, visibilidade: selVis.value };
      if (tipo === "g") corpo.group_id = Number(id);
      else corpo.project_id = Number(id);
      if (anexos.length > 0) corpo.anexos_pendentes = true;

      const criado = await api.criarPlanejamento(corpo);
      lembrarVisibilidade(selVis.value);
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
      toast(t("planejamentos.iniciado"), "ok");
      selecionadoID = criado.id;
      await recarregarLista();
      await abrirPlanejamento(criado.id);
    } catch (e) {
      bannerErro("Falha ao iniciar planejamento: " + e.message);
      btn.disabled = false;
    }
  };

  painel.append(el("div", { class: "form" },
    el("div", {}, el("label", {}, t("consultas.projeto_ou_solucao")), sel,
      el("div", { class: "hint", text: t("planejamentos.hint_grupo") })),
    el("div", { class: "row" },
      el("div", {}, el("label", {}, t("planejamentos.o_que_produzir")), selFoco),
      el("div", {}, el("label", {}, t("planejamentos.nivel_visual")), selNivel),
    ),
    el("div", {}, el("label", {}, t("planejamentos.a_necessidade")), ed.no,
      el("div", { class: "hint", text: t("planejamentos.hint_necessidade") })),
    el("div", {}, el("label", {}, "Referências ", el("span", { class: "opt", text: t("configx.opcional") })),
      el("div", { style: "display:flex;align-items:center;gap:10px" }, btnAnexos, inputAnexos, listaAnexos),
      el("div", { class: "hint", text: t("planejamentos.hint_anexos") })),
    campoVisibilidade(selVis),
    el("div", { class: "acoes" }, btn),
  ));
  ed.ta.focus();
}

// ---------- planejamento aberto ----------

async function abrirPlanejamento(id, abaInicial) {
  pararAcompanhamento();
  selecionadoID = id;
  fixarRota("planejamentos", id);
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
    el("div", { style: "display:flex;gap:6px;align-items:center" },
      // Quem enxerga (dono ou admin); a lista reflete a mudança.
      controleVisibilidade(plan, api.definirVisibilidadePlanejamento, () => recarregarLista()),
      botaoCriarDemanda(plan),
      el("button", {
        class: "btn ghost sm", text: t("consultas.excluir"),
        onclick: async () => {
          if (!confirm(t("planejamentos.confirmar_excluir"))) return;
          try {
            await api.excluirPlanejamento(plan.id);
            toast(t("planejamentos.excluido"), "ok");
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
      toast(t("planejamentos.prefs_atualizadas"), "ok");
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
      el("span", { class: "hint", text: t("planejamentos.rotulo_produzir") }), selFoco,
      el("span", { class: "hint", text: t("planejamentos.rotulo_visual") }), selNivel,
    ),
    el("div", { class: "hint", style: "margin-top:4px",
      text: t("planejamentos.hint_etapas") }),
  );

  // Abas: Conversa | Documentos | Artefatos | Referências.
  const corpoConversa = el("div", { class: "tab-body active" });
  const corpoDocs = el("div", { class: "tab-body" });
  const corpoArts = el("div", { class: "tab-body" });
  const corpoRefs = el("div", { class: "tab-body" });
  const abas = [
    [t("planejamentos.aba_conversa"), corpoConversa, null],
    [t("planejamentos.aba_documentos"), corpoDocs, () => renderDocumentos(corpoDocs, plan)],
    [t("planejamentos.aba_artefatos"), corpoArts, () => renderArtefatos(corpoArts, plan)],
    [t("planejamentos.aba_referencias"), corpoRefs, () => renderReferencias(corpoRefs, plan)],
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
    placeholder: t("planejamentos.ph_resposta") });
  const ajustarAltura = autoCrescer(inp);
  const btn = el("button", { class: "btn", text: t("consultas.enviar") });

  // 📎 anexa referências direto da conversa (fica ativo mesmo com turno em voo:
  // o anexo vale a partir do turno seguinte).
  const inputClip = el("input", {
    type: "file", multiple: true, hidden: true,
    accept: ".md,.txt,.csv,.json,.pdf,.html,.xml,.tx2,.png,.jpg,.jpeg,.webp",
  });
  const btnClip = el("button", { class: "btn ghost", text: "📎", title: t("planejamentos.title_anexar") });
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
      box.append(el("p", { class: "vazio", text: t("consultas.sem_mensagens") }));
    }
    for (const m of msgs) box.append(bolha(m, plan));
    if (plan.status === "pensando") {
      box.append(el("div", { class: "msg sys", text: t("planejamentos.pensando_aviso") }));
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
      progresso.textContent = t("planejamentos.trabalhando");
      acompanharProgresso();
      agendarPoll();
    } else {
      pararAcompanhamento();
    }
  }

  function acompanharProgresso() {
    if (esProgresso) return;
    try {
      // O stream renova o token e reabre sozinho (api.abrirStream).
      esProgresso = api.streamProgressoPlanejamento(plan.id, {
        onmessage: (ev) => {
          try {
            const d = JSON.parse(ev.data);
            if (d.acao === "concluindo") {
              progresso.textContent = t("planejamentos.concluindo");
            } else if (d.detalhe) {
              progresso.textContent = d.detalhe + "…";
            }
          } catch { /* linha desconhecida: ignora */ }
        },
      });
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
        bannerErro(t("planejamentos.aguarde"));
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
      bannerErro(t("planejamentos.turno_falhou", { erro: plan.erro }));
    }
    const btnRetry = el("button", { class: "btn sm", text: t("planejamentos.tentar_novamente") });
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
      el("span", { class: "hint", style: "margin-left:8px", text: t("planejamentos.hint_reprocessar") }));
  }
}

// botaoCriarDemanda monta o botão de handoff do cabeçalho: sem demanda ainda,
// t("nova.criar"); com demandas geradas, "Demandas (N)". Ambos abrem o diálogo
// de handoff (lista + drift + criação).
function botaoCriarDemanda(plan) {
  if (plan.demandas_criadas > 0) {
    return el("button", {
      class: "btn ghost sm", text: `Demandas (${plan.demandas_criadas}) ⧉`,
      title: t("planejamentos.title_demandas"),
      onclick: () => abrirDialogoDemandas(plan),
    });
  }
  return el("button", {
    class: "btn good sm", text: t("nova.criar"),
    onclick: () => abrirDialogoDemandas(plan),
  });
}

// DEMANDA_ENCERRADA são os status de demanda que não representam trabalho em
// andamento — o aviso de sobreposição só vale para demandas fora deste conjunto.
const DEMANDA_ENCERRADA = new Set(["concluida", "integrada", "cancelada"]);

// abrirDialogoDemandas é o diálogo de handoff: mostra as demandas já geradas
// (com o estado e a revisão entregue), o drift dos documentos desde a última
// entrega e o formulário de criação de uma nova demanda completa.
async function abrirDialogoDemandas(plan) {
  let info;
  try {
    info = (await api.listarDemandasPlanejamento(plan.id)) || { demandas: [] };
  } catch (e) {
    bannerErro("Falha ao carregar as demandas do planejamento: " + e.message);
    return;
  }
  const vinculos = info.demandas || [];

  let membros = [];
  if (plan.group_id) {
    try {
      membros = ((await api.obterGrupo(plan.group_id)).membros) || [];
    } catch (e) {
      bannerErro("Falha ao carregar o grupo: " + e.message);
      return;
    }
    if (membros.length === 0) { bannerErro(t("planejamentos.grupo_sem_repos")); return; }
  }

  const overlay = el("div", { class: "overlay open" });
  const fechar = () => overlay.remove();
  overlay.addEventListener("click", (ev) => { if (ev.target === overlay) fechar(); });

  const corpo = el("div", { class: "form", style: "padding:0 22px 22px" });

  // --- demandas já geradas + drift ---
  if (vinculos.length > 0) {
    const lista = el("div");
    for (const v of vinculos) {
      lista.append(el("div", {
        class: "list-item", title: t("planejamentos.title_abrir_demanda"),
        onclick: () => { fechar(); abrirDemandaModal(v.demand_id); },
      },
        el("b", { text: `Demanda #${v.demand_id}` + (v.demanda_titulo ? " — " + v.demanda_titulo : "") }),
        el("div", { class: "meta" },
          pillStatus(v.demanda_status),
          el("span", { class: "pill", text: v.tipo }),
          el("span", { class: "pill", text: rotuloRevEntregue(v) }),
        ),
      ));
    }
    corpo.append(el("div", {}, el("label", {}, t("planejamentos.demandas_geradas")), lista));

    corpo.append(el("div", { class: "hint", text: descreverDrift(vinculos, info) }));

    const ativas = vinculos.filter((v) => !DEMANDA_ENCERRADA.has(v.demanda_status));
    if (ativas.length > 0) {
      corpo.append(el("div", { class: "banner banner-warn", style: "display:block;position:static" },
        `⚠ A demanda #${ativas[ativas.length - 1].demand_id} ainda está ativa. Criar outra demanda do mesmo plano pode gerar trabalho sobreposto — o Kanban sinaliza quando duas demandas tocam os mesmos arquivos.`));
    }
  }

  // --- criação de nova demanda (completa) ---
  const camposNova = el("div", {});
  let selMembro = null;
  if (plan.group_id) {
    selMembro = el("select", {},
      ...membros.map((m) => el("option", { value: String(m.project_id) }, m.nome || String(m.project_id))));
    camposNova.append(el("label", {}, t("planejamentos.repo_recebe")), selMembro);
  }
  const btnCriar = el("button", {
    class: "btn good",
    text: vinculos.length > 0 ? t("planejamentos.criar_nova_completa") : t("nova.criar"),
  });
  btnCriar.onclick = async () => {
    btnCriar.disabled = true;
    const corpoReq = {};
    if (selMembro) corpoReq.project_id = Number(selMembro.value);
    try {
      const dem = await api.criarDemandaDePlanejamento(plan.id, corpoReq);
      toast(`Demanda #${dem.id} criada — o analista já está lendo o PRD.`, "ok");
      fechar();
      await abrirPlanejamento(plan.id);
      await abrirDemandaModal(dem.id); // abre o card da demanda por cima
    } catch (e) {
      bannerErro("Falha ao criar demanda: " + e.message);
      btnCriar.disabled = false;
    }
  };
  camposNova.append(
    el("div", { class: "hint", style: "margin:6px 0",
      text: t("planejamentos.hint_nova_completa") }),
    el("div", { class: "acoes" }, btnCriar),
  );
  corpo.append(el("div", {}, el("label", {}, vinculos.length > 0 ? t("planejamentos.nova_demanda") : t("planejamentos.criar_do_documento")), camposNova));

  overlay.append(el("div", { class: "modal", style: "max-width:640px" },
    el("div", { class: "modal-head" },
      el("div", { class: "row1" },
        el("h2", { text: plan.titulo || `Planejamento #${plan.id}` }),
        el("button", { class: "modal-close", text: "✕", onclick: fechar }),
      ),
      el("p", { class: "sub", style: "margin:4px 0 14px", text: t("planejamentos.demandas_deste") }),
    ),
    corpo,
  ));
  document.body.append(overlay);
}

// rotuloRevEntregue formata a revisão entregue no handoff (0 = vínculo antigo,
// anterior ao registro de revisões).
function rotuloRevEntregue(v) {
  const partes = [];
  if (v.prd_rev > 0) partes.push("PRD rev " + v.prd_rev);
  if (v.adrs_rev > 0) partes.push("ADRs rev " + v.adrs_rev);
  if (partes.length === 0) return t("planejamentos.rev_nao_registrada");
  return partes.join(" · ");
}

// descreverDrift compara a última entrega com a revisão atual dos documentos.
function descreverDrift(vinculos, info) {
  const ult = vinculos[vinculos.length - 1];
  if (ult.prd_rev === 0 && ult.adrs_rev === 0) {
    return t("planejamentos.drift_nao_registrado");
  }
  const mudancas = [];
  if (info.prd_rev_atual > ult.prd_rev) {
    mudancas.push(`o PRD está na revisão ${info.prd_rev_atual} (a demanda #${ult.demand_id} recebeu a ${ult.prd_rev})`);
  }
  if (info.adrs_rev_atual > ult.adrs_rev) {
    mudancas.push(`os ADRs estão na revisão ${info.adrs_rev_atual} (a demanda #${ult.demand_id} recebeu a ${ult.adrs_rev})`);
  }
  if (mudancas.length === 0) {
    return t("planejamentos.drift_sem_mudancas");
  }
  return t("planejamentos.drift", { mudancas: mudancas.join("; ") });
}

// ---------- aba Documentos ----------

const NOMES_DOCS = { "prd.md": t("planejamentos.foco_prd"), "adrs.md": t("planejamentos.foco_adr") };

async function renderDocumentos(corpo, plan) {
  limpar(corpo).append(el("p", { class: "vazio", text: t("planejamentos.carregando_docs") }));
  let docs;
  try {
    docs = (await api.listarDocumentosPlanejamento(plan.id)) || [];
  } catch (e) {
    limpar(corpo).append(el("p", { class: "vazio", text: "Falha ao carregar documentos: " + e.message }));
    return;
  }
  limpar(corpo);
  if (docs.length === 0) {
    corpo.append(el("p", { class: "vazio", text: t("planejamentos.sem_docs") }));
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
      bannerErro(t("planejamentos.falha_revisao", { erro: e.message }));
    }
  };
  const btnBaixar = el("button", {
    class: "btn ghost sm", text: t("planejamentos.baixar_md"), title: t("planejamentos.title_baixar_rev"),
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
  limpar(corpo).append(el("p", { class: "vazio", text: t("planejamentos.carregando_arts") }));
  let arts;
  try {
    arts = (await api.listarArtefatosPlanejamento(plan.id)) || [];
  } catch (e) {
    limpar(corpo).append(el("p", { class: "vazio", text: "Falha ao carregar artefatos: " + e.message }));
    return;
  }
  limpar(corpo);
  if (arts.length === 0) {
    corpo.append(el("p", { class: "vazio", text: t("planejamentos.sem_arts") }));
    return;
  }
  for (const a of arts) {
    corpo.append(el("div", { class: "list-item", style: "cursor:default" },
      el("div", { style: "display:flex;align-items:center;justify-content:space-between;gap:10px" },
        el("b", { text: a.titulo || a.arquivo }),
        el("div", { style: "display:flex;gap:6px" },
          el("button", {
            class: "btn ghost sm", text: t("planejamentos.baixar"),
            onclick: () => baixarURL(api.urlDownloadArtefatoPlanejamento(plan.id, a.arquivo)),
          }),
          el("button", {
            class: "btn sm", text: t("planejamentos.abrir"),
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
  corpo.append(el("p", { class: "hint", text: t("planejamentos.hint_sandbox") }));
}

// ---------- aba Referências ----------

// renderReferencias lista os documentos de apoio anexados (ADRs de outros
// projetos, transcrições de reunião…) e permite anexar novos — o próximo turno
// do estrategista já os enxerga.
async function renderReferencias(corpo, plan) {
  limpar(corpo).append(el("p", { class: "vazio", text: t("planejamentos.carregando_refs") }));
  let refs;
  try {
    refs = (await api.listarReferenciasPlanejamento(plan.id)) || [];
  } catch (e) {
    limpar(corpo).append(el("p", { class: "vazio", text: t("planejamentos.falha_refs", { erro: e.message }) }));
    return;
  }
  limpar(corpo);

  const inputArquivos = el("input", {
    type: "file", multiple: true, hidden: true,
    accept: ".md,.txt,.csv,.json,.pdf,.html,.xml,.tx2,.png,.jpg,.jpeg,.webp",
  });
  const btnAnexar = el("button", { class: "btn", text: t("planejamentos.anexar_arquivos") });
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
    el("span", { class: "hint", text: t("planejamentos.hint_formatos") }),
  ));

  if (refs.length === 0) {
    corpo.append(el("p", { class: "vazio", text: t("planejamentos.sem_refs") }));
    return;
  }
  for (const ref of refs) {
    corpo.append(el("div", { class: "list-item", style: "cursor:default" },
      el("div", { style: "display:flex;align-items:center;justify-content:space-between;gap:10px" },
        el("b", { text: ref.arquivo }),
        el("div", { style: "display:flex;gap:6px" },
          el("button", {
            class: "btn ghost sm", text: t("planejamentos.baixar"),
            onclick: () => baixarURL(api.urlReferenciaPlanejamento(plan.id, ref.arquivo)),
          }),
          el("button", {
            class: "btn ghost sm", text: t("consultas.excluir"),
            onclick: async () => {
              if (!confirm(`Excluir a referência ${ref.arquivo}?`)) return;
              try {
                await api.excluirReferenciaPlanejamento(plan.id, ref.arquivo);
                toast(t("planejamentos.ref_excluida"), "ok");
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
  corpo.append(el("p", { class: "hint", text: t("planejamentos.hint_refs_leitura") }));
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
      extras.push(el("div", { class: "hint", text: t("planejamentos.responda_abaixo") }));
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
