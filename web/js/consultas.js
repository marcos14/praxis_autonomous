// Tela "Consultas" — chat de análise de código para produto/suporte. O usuário
// escolhe um projeto ou um grupo de repositórios e conversa com o consultor,
// que lê o código em modo somente leitura e responde em linguagem de negócio
// (nunca código). Enquanto o consultor "pensa", um SSE sanitizado mostra o
// progresso ("lendo arquivo…") e um poll de fallback detecta o fim do turno.

import { api } from "./api.js";
import { el, limpar, toast, bannerErro, renderMarkdown, autoCrescer, mdEditor } from "./ui.js";
import { t } from "./i18n.js";

let consultas = [];
let selecionadaID = null;
let esProgresso = null; // EventSource do progresso do turno em andamento
let pollTimer = null;   // fallback: relê a consulta enquanto status=pensando

// PAPEIS mapeia o papel de uma fala ao rótulo e à classe visual da bolha
// (mesmo padrão do chat da demanda).
const PAPEIS = {
  user: [t("consultas.papel_voce"), "user"],
  consultor: [t("consultas.papel_consultor"), "agent"],
  sistema: ["", "sys"],
};

export async function montarConsultas() {
  document.getElementById("btn-nova-consulta").onclick = () => renderNova();
  await recarregarLista();
  if (selecionadaID != null) {
    const c = consultas.find((x) => x.id === selecionadaID);
    if (c) await abrirConsulta(c.id);
    else limparPainel();
  }
}

// desmontarConsultas fecha o SSE e o poll ao sair da view.
export function desmontarConsultas() {
  pararAcompanhamento();
}

function pararAcompanhamento() {
  if (esProgresso) { esProgresso.close(); esProgresso = null; }
  if (pollTimer) { clearTimeout(pollTimer); pollTimer = null; }
}

async function recarregarLista() {
  try {
    consultas = (await api.listarConsultas()) || [];
  } catch (e) {
    bannerErro(t("consultas.falha_carregar", { erro: e.message }));
    return;
  }
  bannerErro("");
  const lista = limpar(document.getElementById("lista-consultas"));
  if (consultas.length === 0) {
    lista.append(el("p", { class: "sub", text: t("consultas.nenhuma") }));
    return;
  }
  for (const c of consultas) {
    const alvo = c.grupo_nome ? t("consultas.grupo", { nome: c.grupo_nome }) : c.projeto_nome || "";
    lista.append(el("div", {
      class: "list-item" + (c.id === selecionadaID ? " sel" : ""),
      onclick: () => abrirConsulta(c.id),
    },
      el("b", { text: c.titulo || t("consultas.titulo_n", { id: c.id }) }),
      el("div", { class: "path", text: alvo }),
      el("div", { class: "meta" },
        pillStatusConsulta(c.status),
        c.custo_usd > 0 ? el("span", { class: "pill", text: t("consultas.custo", { valor: c.custo_usd.toFixed(2) }) }) : null,
      ),
    ));
  }
}

function pillStatusConsulta(status) {
  const mapa = {
    ociosa: ["dot-good", t("status.pronta")],
    pensando: ["dot-warn", t("consultas.status_pensando")],
    falhou: ["dot-crit", t("status.falhou")],
  };
  const [dot, rotulo] = mapa[status] || ["dot-muted", status];
  return el("span", { class: "pill" }, el("span", { class: "dot " + dot }), rotulo);
}

function limparPainel() {
  pararAcompanhamento();
  const painel = limpar(document.getElementById("painel-consulta"));
  painel.append(el("p", { class: "sub", style: "margin:0", text: t("view.consultas.selecione") }));
}

// ---------- nova consulta ----------

async function renderNova() {
  pararAcompanhamento();
  selecionadaID = null;
  await recarregarLista();
  const painel = limpar(document.getElementById("painel-consulta"));
  painel.append(el("h3", {}, t("consultas.nova_titulo")));

  let grupos = [], projetos = [];
  try {
    [grupos, projetos] = await Promise.all([
      api.listarGrupos().catch(() => []),
      api.listarProjetos(),
    ]);
  } catch (e) {
    painel.append(el("p", { class: "sub", text: t("consultas.falha_projetos", { erro: e.message }) }));
    return;
  }
  grupos = (grupos || []).filter((g) => g.ativo);
  projetos = (projetos || []).filter((p) => p.ativo);
  if (grupos.length === 0 && projetos.length === 0) {
    painel.append(el("p", { class: "sub", text: t("consultas.sem_projetos") }));
    return;
  }

  // Alvo unificado: grupos primeiro (soluções completas), depois projetos.
  const sel = el("select", {});
  for (const g of grupos) sel.append(el("option", { value: "g:" + g.id }, t("consultas.grupo", { nome: g.nome })));
  for (const p of projetos) sel.append(el("option", { value: "p:" + p.id }, p.nome));

  const ed = mdEditor({
    rows: 5,
    placeholder: t("consultas.ph_pergunta"),
  });
  const btn = el("button", { class: "btn", text: t("consultas.iniciar") });
  btn.onclick = async () => {
    const mensagem = ed.ta.value.trim();
    if (!mensagem) { bannerErro(t("consultas.escreva_pergunta")); return; }
    bannerErro("");
    btn.disabled = true;
    try {
      const [tipo, id] = sel.value.split(":");
      const corpo = { mensagem };
      if (tipo === "g") corpo.group_id = Number(id);
      else corpo.project_id = Number(id);
      const criada = await api.criarConsulta(corpo);
      toast(t("consultas.iniciada"), "ok");
      selecionadaID = criada.id;
      await recarregarLista();
      await abrirConsulta(criada.id);
    } catch (e) {
      bannerErro(t("consultas.falha_iniciar", { erro: e.message }));
      btn.disabled = false;
    }
  };

  painel.append(el("div", { class: "form" },
    el("div", {}, el("label", {}, t("consultas.projeto_ou_solucao")), sel,
      el("div", { class: "hint", text: t("consultas.hint_grupo") })),
    el("div", {}, el("label", {}, t("consultas.sua_pergunta")), ed.no,
      el("div", { class: "hint", text: t("consultas.hint_pergunta") })),
    el("div", { class: "acoes" }, btn),
  ));
  ed.ta.focus();
}

// ---------- conversa ----------

async function abrirConsulta(id) {
  pararAcompanhamento();
  selecionadaID = id;
  await recarregarLista();

  let cons;
  try {
    cons = await api.obterConsulta(id);
  } catch (e) {
    bannerErro(t("consultas.falha_abrir", { erro: e.message }));
    return;
  }

  const painel = limpar(document.getElementById("painel-consulta"));
  const alvo = cons.grupo_nome ? t("consultas.grupo", { nome: cons.grupo_nome }) : cons.projeto_nome || "";
  const cab = el("div", { style: "display:flex;align-items:center;justify-content:space-between;gap:10px" },
    el("h3", { style: "margin:0", text: cons.titulo || t("consultas.titulo_n", { id: cons.id }) }),
    el("button", {
      class: "btn ghost sm", text: t("consultas.excluir"),
      onclick: async () => {
        if (!confirm(t("consultas.confirmar_excluir"))) return;
        try {
          await api.excluirConsulta(cons.id);
          toast(t("consultas.excluida"), "ok");
          selecionadaID = null;
          limparPainel();
          await recarregarLista();
        } catch (e) {
          bannerErro(t("consultas.falha_excluir", { erro: e.message }));
        }
      },
    }),
  );
  const sub = el("p", { class: "sub", style: "margin:4px 0 10px", text: alvo +
    (cons.custo_usd > 0 ? " · " + t("consultas.custo_acumulado", { valor: cons.custo_usd.toFixed(2) }) : "") });

  const box = el("div", { class: "chat" });
  const progresso = el("div", { class: "hint", hidden: true });
  const inp = el("textarea", { rows: "1",
    placeholder: t("consultas.ph_resposta") });
  const ajustarAltura = autoCrescer(inp);
  const btn = el("button", { class: "btn", text: t("consultas.enviar") });
  painel.append(cab, sub, box, progresso, el("div", { class: "chat-input" }, inp, btn));

  async function recarregarChat() {
    let msgs;
    try {
      msgs = (await api.listarChatConsulta(cons.id)) || [];
    } catch (e) {
      limpar(box).append(el("p", { class: "vazio", text: t("consultas.falha_conversa", { erro: e.message }) }));
      return;
    }
    limpar(box);
    if (msgs.length === 0) {
      box.append(el("p", { class: "vazio", text: t("consultas.sem_mensagens") }));
    }
    for (const m of msgs) box.append(bolha(m));
    if (cons.status === "pensando") {
      box.append(el("div", { class: "msg sys", text: t("consultas.analisando_aviso") }));
    }
    box.scrollTop = box.scrollHeight;
  }

  function aplicarEstado() {
    const pensando = cons.status === "pensando";
    inp.disabled = pensando;
    btn.disabled = pensando;
    progresso.hidden = !pensando;
    if (pensando) {
      progresso.textContent = t("consultas.status_pensando");
      acompanharProgresso();
      agendarPoll();
    } else {
      pararAcompanhamento();
    }
  }

  // acompanharProgresso abre o SSE sanitizado e mostra o "detalhe" de cada passo
  // ("lendo baixa.go…"). O stream nunca traz conteúdo do código.
  function acompanharProgresso() {
    if (esProgresso) return;
    try {
      esProgresso = new EventSource(api.urlProgressoConsulta(cons.id));
      esProgresso.onmessage = (ev) => {
        try {
          const d = JSON.parse(ev.data);
          if (d.acao === "concluindo") {
            progresso.textContent = t("consultas.concluindo");
          } else if (d.detalhe) {
            progresso.textContent = d.detalhe + "…";
          }
        } catch { /* linha desconhecida: ignora */ }
      };
      esProgresso.onerror = () => { /* o poll de fallback cobre a queda do SSE */ };
    } catch { /* sem EventSource: o poll cobre */ }
  }

  // agendarPoll relê a consulta a cada 5s enquanto pensando; ao mudar o status,
  // re-renderiza a conversa inteira (resposta nova + custo atualizado).
  function agendarPoll() {
    if (pollTimer) return;
    const tick = async () => {
      pollTimer = null;
      if (selecionadaID !== cons.id) return;
      let atual;
      try {
        atual = await api.obterConsulta(cons.id);
      } catch {
        pollTimer = setTimeout(tick, 5000);
        return;
      }
      if (atual.status !== "pensando") {
        await abrirConsulta(cons.id);
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
      await api.enviarChatConsulta(cons.id, texto);
      inp.value = "";
      ajustarAltura();
      await abrirConsulta(cons.id); // re-renderiza já em modo "pensando"
    } catch (e) {
      if (e.status === 409) {
        bannerErro(t("consultas.aguarde"));
        await abrirConsulta(cons.id);
        return;
      }
      bannerErro(t("consultas.falha_enviar", { erro: e.message }));
      btn.disabled = false;
    }
  }
  btn.addEventListener("click", enviar);
  inp.addEventListener("keydown", (e) => {
    if (e.key === "Enter" && !e.shiftKey) { e.preventDefault(); enviar(); }
  });

  await recarregarChat();
  aplicarEstado();
  if (cons.status === "falhou" && cons.erro) {
    bannerErro(t("consultas.turno_falhou", { erro: cons.erro }));
  }
}

// bolha renderiza uma fala como bolha de chat. O conteúdo é sempre textContent
// (nunca innerHTML) — o texto do consultor já vem sanitizado do backend, e aqui
// nem HTML é interpretado.
function bolha(m) {
  const [rotulo, classe] = PAPEIS[m.papel] || [m.papel, "sys"];
  if (classe === "sys") {
    return el("div", { class: "msg sys", text: m.conteudo || rotulo });
  }
  let extra = null;
  try {
    const meta = typeof m.meta === "string" ? JSON.parse(m.meta) : m.meta;
    if (meta && meta.tipo === "perguntas") {
      extra = el("div", { class: "hint", text: t("consultas.responda_abaixo") });
    } else if (meta && meta.redigido > 0) {
      extra = el("div", { class: "hint", text: t("consultas.redigido") });
    }
  } catch { /* meta inválido: segue sem extras */ }
  return el("div", { class: "msg " + classe },
    rotulo ? el("div", { class: "who", text: rotulo }) : null,
    el("div", { class: "txt md" }, ...renderMarkdown(m.conteudo || "")),
    extra,
  );
}
