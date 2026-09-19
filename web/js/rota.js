// Rotas com id (M4 do PLANO_INTERNET): "#consultas/7", "#planejamentos/3",
// "#demandas/12". O app.js separa a view do id e entrega o id ao montar da
// view; a view, ao abrir um item, fixa o hash com pushState — que NÃO dispara
// hashchange (senão a view seria remontada a cada clique). Voltar/avançar do
// navegador dispara hashchange normalmente, e aí a view remonta com o id certo.

// separarRota devolve { view, id } do hash ("#consultas/7" → consultas, "7";
// "#home" → home, ""). Id só numérico; qualquer outra coisa é ignorada.
export function separarRota(hash) {
  const s = (hash || "").replace(/^#/, "");
  const barra = s.indexOf("/");
  if (barra < 0) return { view: s, id: "" };
  const id = s.slice(barra + 1);
  return { view: s.slice(0, barra), id: /^\d+$/.test(id) ? id : "" };
}

// fixarRota registra "#view/id" no histórico sem remontar a view. Se o hash
// já é esse (chegamos aqui por voltar/avançar ou por uma notificação), não
// empilha entrada repetida.
export function fixarRota(view, id) {
  const alvo = `#${view}/${id}`;
  if (location.hash === alvo) return;
  history.pushState(null, "", alvo);
}

// limparRotaID volta o hash para "#view" (sem id) ao fechar um item aberto
// por cima da lista (o card da demanda), substituindo a entrada atual — assim
// recarregar a página não reabre o item.
export function limparRotaID(view) {
  if (location.hash.startsWith(`#${view}/`)) history.replaceState(null, "", `#${view}`);
}
