// Tela "Nova demanda" (Fase 3a): escolhe o projeto e cola o PRD. A demanda nasce
// como conversa (status "recebida"), com o PRD como a primeira mensagem do chat.
// Ao criar, navega para Demandas e abre o card já na aba Chat/PRD.

import { api } from "./api.js";
import { el, limpar, bannerErro, toast, mdEditor } from "./ui.js";
import { abrirCard } from "./demandas.js";

export async function montarNovaDemanda() {
  const cont = limpar(document.getElementById("painel-nova"));

  let projetos = [];
  try {
    projetos = (await api.listarProjetos()) || [];
  } catch (e) {
    cont.append(el("p", { class: "sub", text: "Falha ao carregar projetos: " + e.message }));
    return;
  }
  if (projetos.length === 0) {
    cont.append(el("p", { class: "sub" },
      "Cadastre um projeto antes de criar uma demanda. Vá em ",
      el("a", { href: "#projetos", text: "Projetos" }), "."));
    return;
  }

  const selProj = el("select", {},
    ...projetos.map((p) => el("option", { value: String(p.id), text: p.nome })));
  const inpTitulo = el("input", { type: "text", placeholder: "Opcional — se vazio, usamos a primeira linha do PRD" });
  const inpBranch = el("input", { type: "text", placeholder: "Opcional — ex.: painel-home (vira praxis/painel-home)" });
  const edPRD = mdEditor({ placeholder: "Cole aqui o PRD ou descreva o chamado…", rows: 10 });
  const btn = el("button", { class: "btn", text: "Criar demanda" });

  const form = el("div", { class: "form" },
    el("div", {}, el("label", { text: "Projeto" }), selProj),
    el("div", {}, el("label", { text: "Título" }), inpTitulo),
    el("div", {}, el("label", { text: "Branch" }), inpBranch,
      el("p", { class: "sub", style: "margin:4px 0 0",
        text: "Em branco, geramos praxis/d<id>-<título>. O prefixo praxis/ é sempre adicionado." })),
    el("div", {}, el("label", { text: "PRD / descrição do chamado" }), edPRD.no),
    el("div", { class: "acoes" }, btn),
  );
  cont.append(form);

  async function criar() {
    const prd = edPRD.ta.value.trim();
    if (!prd) {
      bannerErro("Cole o PRD ou descreva o chamado antes de criar a demanda.");
      return;
    }
    btn.disabled = true;
    try {
      const d = await api.criarDemandaChat(selProj.value, {
        titulo: inpTitulo.value.trim(),
        branch: inpBranch.value.trim(),
        prd,
      });
      bannerErro("");
      toast("Demanda #" + d.id + " criada.", "ok");
      // limpa o formulário e abre o card da demanda recém-criada.
      inpTitulo.value = "";
      inpBranch.value = "";
      edPRD.ta.value = "";
      location.hash = "demandas";
      await abrirCard(d.id);
    } catch (e) {
      bannerErro("Falha ao criar a demanda: " + e.message);
    } finally {
      btn.disabled = false;
    }
  }

  btn.addEventListener("click", criar);
}
