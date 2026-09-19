// Seção "Notificações" da tela Minha conta (M4): o que avisa na aba aberta e
// por push, quais eventos, e o botão de ativar/desativar o push NESTE
// dispositivo. As preferências valem para todos os dispositivos do usuário;
// a assinatura push é por dispositivo.

import { api } from "./api.js";
import { el, limpar, toast } from "./ui.js";
import { t } from "./i18n.js";
import { GRUPOS_EVENTOS, TODOS_EVENTOS } from "./notify-events.js";
import * as push from "./push.js";

// secaoNotificacoes monta a seção; `secao(titulo, sub, ...filhos)` vem da
// tela Minha conta para manter o mesmo visual.
export async function secaoNotificacoes(secao) {
  let prefs;
  try {
    prefs = await api.obterPreferencias();
  } catch (e) {
    return secao(t("conta.notif"), null, el("p", { class: "sub", text: t("comum.falha_carregar", { erro: e.message }) }));
  }
  const padrao = new Set(prefs.padrao_tipos || []);
  const eventos = { ...(prefs.eventos || {}) };

  const chkNavegador = el("input", { type: "checkbox" });
  chkNavegador.checked = prefs.navegador !== false;
  const chkPush = el("input", { type: "checkbox" });
  chkPush.checked = prefs.push !== false;

  // Catálogo de eventos em grupos, com o padrão do usuário pré-marcado.
  const caixas = {};
  const grupos = el("div", { class: "conta-eventos" });
  for (const g of GRUPOS_EVENTOS) {
    const bloco = el("div", { class: "conta-eventos-grupo" }, el("div", { class: "nav-group-title", text: g.grupo }));
    for (const ev of g.itens) {
      const c = el("input", { type: "checkbox" });
      c.checked = !!eventos[ev.tipo];
      caixas[ev.tipo] = c;
      bloco.append(el("label", { class: "conta-check" }, c, el("span", { text: ev.rotulo })));
    }
    grupos.append(bloco);
  }

  const btnPadrao = el("button", { class: "btn ghost sm", text: t("conta.notif_restaurar") });
  btnPadrao.onclick = () => {
    for (const ev of TODOS_EVENTOS) caixas[ev.tipo].checked = padrao.has(ev.tipo);
    chkNavegador.checked = true;
    chkPush.checked = true;
  };
  const btnSalvar = el("button", { class: "btn", text: t("conta.notif_salvar") });
  btnSalvar.onclick = async () => {
    btnSalvar.disabled = true;
    const mapa = {};
    for (const ev of TODOS_EVENTOS) mapa[ev.tipo] = caixas[ev.tipo].checked;
    try {
      await api.definirPreferencias({ navegador: chkNavegador.checked, push: chkPush.checked, eventos: mapa });
      toast(t("conta.notif_salvo"));
    } catch (e) {
      toast(e.message, "err");
    } finally {
      btnSalvar.disabled = false;
    }
  };

  // Push neste dispositivo.
  const dispositivo = el("div", { class: "conta-push" });
  await renderPush(dispositivo, prefs.assinaturas || 0);

  return secao(t("conta.notif"), t("conta.notif_sub"),
    el("label", { class: "conta-check" }, chkNavegador, el("span", { text: t("conta.notif_navegador") })),
    el("label", { class: "conta-check" }, chkPush, el("span", { text: t("conta.notif_push") })),
    dispositivo,
    el("h4", { class: "conta-sub", text: t("conta.notif_eventos") }),
    grupos,
    el("div", { class: "conta-acoes" }, btnSalvar, btnPadrao),
  );
}

// renderPush mostra o estado do push neste dispositivo e o botão de ativar ou
// desativar. `assinaturas` é quantos dispositivos do usuário estão assinados.
async function renderPush(cont, assinaturas) {
  limpar(cont);
  const st = await push.estado();
  if (!st.suportado) {
    cont.append(el("p", { class: "sub", text: t("conta.push_nao_suportado") }));
    return;
  }
  let texto;
  if (st.assinado) texto = t("conta.push_ativo");
  else if (st.permissao === "denied") texto = t("conta.push_negado");
  else texto = t("conta.push_inativo");
  const info = el("p", { class: "sub" }, el("b", { text: texto }), " ", el("span", { text: t("conta.push_dispositivos", { n: assinaturas }) }));
  const btn = el("button", { class: "btn ghost sm", text: st.assinado ? t("conta.push_desativar") : t("conta.push_ativar") });
  btn.disabled = !st.assinado && st.permissao === "denied";
  btn.onclick = async () => {
    btn.disabled = true;
    try {
      if (st.assinado) {
        await push.desativar();
        toast(t("conta.push_desativado"));
        await renderPush(cont, Math.max(0, assinaturas - 1));
      } else {
        await push.ativar();
        toast(t("conta.push_ativado"));
        await renderPush(cont, assinaturas + 1);
      }
    } catch (e) {
      toast(e && e.message === "permissao_negada" ? t("conta.push_negado") : (e && e.message) || t("auth.falha"), "err");
      btn.disabled = false;
    }
  };
  cont.append(info, btn);
}
