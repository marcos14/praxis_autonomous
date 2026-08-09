// Tela "Nova demanda" (Fase 3a): escolhe o projeto e cola o PRD. A demanda nasce
// como conversa (status "recebida"), com o PRD como a primeira mensagem do chat.
// Ao criar, navega para Demandas e abre o card já na aba Chat/PRD.

import { api } from "./api.js";
import { el, limpar, bannerErro, toast, mdEditor } from "./ui.js";
import { abrirCard } from "./demandas.js";
import * as auth from "./auth.js";
import { t } from "./i18n.js";

export async function montarNovaDemanda() {
  const cont = limpar(document.getElementById("painel-nova"));

  let projetos = [];
  try {
    projetos = (await api.listarProjetos()) || [];
  } catch (e) {
    cont.append(el("p", { class: "sub", text: t("nova.falha_projetos", { erro: e.message }) }));
    return;
  }
  if (projetos.length === 0) {
    cont.append(el("p", { class: "sub" },
      t("nova.cadastre_antes"),
      el("a", { href: "#projetos", text: t("nav.projetos") }), "."));
    return;
  }

  const selProj = el("select", {},
    ...projetos.map((p) => el("option", { value: String(p.id), text: p.nome })));
  const inpTitulo = el("input", { type: "text", placeholder: t("nova.ph_titulo") });
  const inpBranch = el("input", { type: "text", placeholder: t("nova.ph_branch") });
  const edPRD = mdEditor({ placeholder: t("nova.ph_prd"), rows: 10 });
  const btn = el("button", { class: "btn", text: t("nova.criar") });

  const form = el("div", { class: "form" },
    el("div", {}, el("label", { text: t("nova.projeto") }), selProj),
    el("div", {}, el("label", { text: t("nova.titulo") }), inpTitulo),
    el("div", {}, el("label", { text: t("nova.branch") }), inpBranch,
      el("p", { class: "sub", style: "margin:4px 0 0",
        text: t("nova.hint_branch") })),
    el("div", {}, el("label", { text: t("nova.prd") }), edPRD.no),
    el("div", { class: "acoes" }, btn),
  );
  cont.append(form);

  // Atalho para o caminho planejado: quem tem acesso a Planejamentos pode
  // lapidar o PRD com o estrategista e criar a demanda de lá (com vínculo e
  // revisão registrados) — o fluxo de handoff vive naquela tela.
  if (auth.temPermissao("planejamentos.usar")) {
    cont.append(el("p", { class: "sub", style: "margin-top:12px" },
      t("nova.plan_antes"),
      el("a", { href: "#planejamentos", text: t("nova.plan_link") }),
      t("nova.plan_depois")));
  }

  async function criar() {
    const prd = edPRD.ta.value.trim();
    if (!prd) {
      bannerErro(t("nova.cole_prd"));
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
      toast(t("nova.criada", { id: d.id }), "ok");
      // limpa o formulário e abre o card da demanda recém-criada.
      inpTitulo.value = "";
      inpBranch.value = "";
      edPRD.ta.value = "";
      location.hash = "demandas";
      await abrirCard(d.id);
    } catch (e) {
      bannerErro(t("nova.falha_criar", { erro: e.message }));
    } finally {
      btn.disabled = false;
    }
  }

  btn.addEventListener("click", criar);
}
