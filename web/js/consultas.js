// Tela "Consultas" — chat de análise de código para produto/suporte. O usuário
// escolhe um projeto ou um grupo de repositórios e conversa com o consultor,
// que lê o código em modo somente leitura e responde em linguagem de negócio
// (nunca código). Enquanto o consultor "pensa", um SSE sanitizado mostra o
// progresso ("lendo arquivo…") e um poll de fallback detecta o fim do turno.

import { api } from "./api.js";
import { el, limpar, toast, bannerErro } from "./ui.js";

let consultas = [];
let selecionadaID = null;
let esProgresso = null; // EventSource do progresso do turno em andamento
let pollTimer = null;   // fallback: relê a consulta enquanto status=pensando

// PAPEIS mapeia o papel de uma fala ao rótulo e à classe visual da bolha
// (mesmo padrão do chat da demanda).
const PAPEIS = {
  user: ["Você", "user"],
  consultor: ["Praxis · Consultor", "agent"],
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
    bannerErro("Falha ao carregar consultas: " + e.message);
    return;
  }
  bannerErro("");
  const lista = limpar(document.getElementById("lista-consultas"));
  if (consultas.length === 0) {
    lista.append(el("p", { class: "sub", text: "Nenhuma consulta ainda. Inicie uma nova." }));
    return;
  }
  for (const c of consultas) {
    const alvo = c.grupo_nome ? "Grupo: " + c.grupo_nome : c.projeto_nome || "";
    lista.append(el("div", {
      class: "list-item" + (c.id === selecionadaID ? " sel" : ""),
      onclick: () => abrirConsulta(c.id),
    },
      el("b", { text: c.titulo || `Consulta #${c.id}` }),
      el("div", { class: "path", text: alvo }),
      el("div", { class: "meta" },
        pillStatusConsulta(c.status),
        c.custo_usd > 0 ? el("span", { class: "pill", text: "US$ " + c.custo_usd.toFixed(2) }) : null,
      ),
    ));
  }
}

function pillStatusConsulta(status) {
  const mapa = {
    ociosa: ["dot-good", "pronta"],
    pensando: ["dot-warn", "analisando…"],
    falhou: ["dot-crit", "falhou"],
  };
  const [dot, rotulo] = mapa[status] || ["dot-muted", status];
  return el("span", { class: "pill" }, el("span", { class: "dot " + dot }), rotulo);
}

function limparPainel() {
  pararAcompanhamento();
  const painel = limpar(document.getElementById("painel-consulta"));
  painel.append(el("p", { class: "sub", style: "margin:0", text: "Selecione uma consulta à esquerda ou inicie uma nova." }));
}

// ---------- nova consulta ----------

async function renderNova() {
  pararAcompanhamento();
  selecionadaID = null;
  await recarregarLista();
  const painel = limpar(document.getElementById("painel-consulta"));
  painel.append(el("h3", {}, "Nova consulta"));

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

  // Alvo unificado: grupos primeiro (soluções completas), depois projetos.
  const sel = el("select", {});
  for (const g of grupos) sel.append(el("option", { value: "g:" + g.id }, "Grupo: " + g.nome));
  for (const p of projetos) sel.append(el("option", { value: "p:" + p.id }, p.nome));

  const txt = el("textarea", {
    rows: "5",
    placeholder: "O que você quer entender? Ex.: \"Como funciona a baixa de títulos?\", \"Preciso montar um PRD para melhorar a régua de cobrança\", \"Como implantar o Vulcano Chat para um cliente com duas filiais?\"",
  });
  const btn = el("button", { class: "btn", text: "Iniciar consulta" });
  btn.onclick = async () => {
    const mensagem = txt.value.trim();
    if (!mensagem) { bannerErro("Escreva a sua pergunta."); return; }
    bannerErro("");
    btn.disabled = true;
    try {
      const [tipo, id] = sel.value.split(":");
      const corpo = { mensagem };
      if (tipo === "g") corpo.group_id = Number(id);
      else corpo.project_id = Number(id);
      const criada = await api.criarConsulta(corpo);
      toast("Consulta iniciada — o consultor está analisando.", "ok");
      selecionadaID = criada.id;
      await recarregarLista();
      await abrirConsulta(criada.id);
    } catch (e) {
      bannerErro("Falha ao iniciar consulta: " + e.message);
      btn.disabled = false;
    }
  };

  painel.append(el("div", { class: "form" },
    el("div", {}, el("label", {}, "Projeto ou solução"), sel,
      el("div", { class: "hint", text: "Num grupo, o consultor enxerga todos os repositórios da solução." })),
    el("div", {}, el("label", {}, "Sua pergunta"), txt,
      el("div", { class: "hint", text: "Não precisa ser técnico: se faltar contexto, o consultor faz perguntas antes de responder." })),
    el("div", { class: "acoes" }, btn),
  ));
  txt.focus();
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
    bannerErro("Falha ao abrir consulta: " + e.message);
    return;
  }

  const painel = limpar(document.getElementById("painel-consulta"));
  const alvo = cons.grupo_nome ? "Grupo: " + cons.grupo_nome : cons.projeto_nome || "";
  const cab = el("div", { style: "display:flex;align-items:center;justify-content:space-between;gap:10px" },
    el("h3", { style: "margin:0", text: cons.titulo || `Consulta #${cons.id}` }),
    el("button", {
      class: "btn ghost sm", text: "Excluir",
      onclick: async () => {
        if (!confirm("Excluir esta consulta e toda a conversa?")) return;
        try {
          await api.excluirConsulta(cons.id);
          toast("Consulta excluída.", "ok");
          selecionadaID = null;
          limparPainel();
          await recarregarLista();
        } catch (e) {
          bannerErro("Falha ao excluir: " + e.message);
        }
      },
    }),
  );
  const sub = el("p", { class: "sub", style: "margin:4px 0 10px", text: alvo +
    (cons.custo_usd > 0 ? ` · custo acumulado US$ ${cons.custo_usd.toFixed(2)}` : "") });

  const box = el("div", { class: "chat" });
  const progresso = el("div", { class: "hint", hidden: true });
  const inp = el("input", { type: "text", placeholder: "Responder ao consultor ou fazer outra pergunta…" });
  const btn = el("button", { class: "btn", text: "Enviar" });
  painel.append(cab, sub, box, progresso, el("div", { class: "chat-input" }, inp, btn));

  async function recarregarChat() {
    let msgs;
    try {
      msgs = (await api.listarChatConsulta(cons.id)) || [];
    } catch (e) {
      limpar(box).append(el("p", { class: "vazio", text: "Falha ao carregar a conversa: " + e.message }));
      return;
    }
    limpar(box);
    if (msgs.length === 0) {
      box.append(el("p", { class: "vazio", text: "Nenhuma mensagem ainda." }));
    }
    for (const m of msgs) box.append(bolha(m));
    if (cons.status === "pensando") {
      box.append(el("div", { class: "msg sys", text: "O consultor está analisando o código — isso pode levar alguns minutos." }));
    }
    box.scrollTop = box.scrollHeight;
  }

  function aplicarEstado() {
    const pensando = cons.status === "pensando";
    inp.disabled = pensando;
    btn.disabled = pensando;
    progresso.hidden = !pensando;
    if (pensando) {
      progresso.textContent = "analisando…";
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
            progresso.textContent = "concluindo a resposta…";
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
      await abrirConsulta(cons.id); // re-renderiza já em modo "pensando"
    } catch (e) {
      if (e.status === 409) {
        bannerErro("O consultor ainda está analisando — aguarde a resposta.");
        await abrirConsulta(cons.id);
        return;
      }
      bannerErro("Falha ao enviar: " + e.message);
      btn.disabled = false;
    }
  }
  btn.addEventListener("click", enviar);
  inp.addEventListener("keydown", (e) => { if (e.key === "Enter") enviar(); });

  await recarregarChat();
  aplicarEstado();
  if (cons.status === "falhou" && cons.erro) {
    bannerErro("Último turno falhou: " + cons.erro + " — você pode reenviar a pergunta.");
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
      extra = el("div", { class: "hint", text: "Responda no campo abaixo para o consultor continuar." });
    } else if (meta && meta.redigido > 0) {
      extra = el("div", { class: "hint", text: "Trechos técnicos foram removidos pela política de segurança." });
    }
  } catch { /* meta inválido: segue sem extras */ }
  return el("div", { class: "msg " + classe },
    rotulo ? el("div", { class: "who", text: rotulo }) : null,
    el("div", { class: "txt", text: m.conteudo }),
    extra,
  );
}
