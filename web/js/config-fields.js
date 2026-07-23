// Metadados das chaves de config conhecidas e conversões valor↔JSON.
//
// O store de config (Fase 1e) é livre: chave→valor JSON, sem whitelist. Aqui
// declaramos as chaves que a UI apresenta como formulário estruturado; chaves
// desconhecidas presentes no banco são preservadas (ver preservarDesconhecidas).
//
// Tipos:
//   "number" → JSON number
//   "text"   → JSON string
//   "lines"  → JSON array de strings (uma por linha no textarea; vazias ignoradas)

// CAMPOS é a lista canônica de chaves conhecidas, na ordem de exibição.
// `escopo` indica onde o campo aparece: "ambos" (global + override) ou "global".
export const CAMPOS = [
  { chave: "motor_preferido", rotulo: "Motor preferido", tipo: "text", escopo: "ambos",
    hint: "Nome do motor a tentar primeiro. Em branco, usa a ordem de prioridade dos motores." },
  { chave: "execucoes_simultaneas", rotulo: "Execuções simultâneas", tipo: "number", escopo: "ambos",
    hint: "Demandas executando ao mesmo tempo (global: máquina; projeto: neste projeto)." },
  { chave: "gates_simultaneos", rotulo: "Gates simultâneos", tipo: "number", escopo: "global",
    hint: "Builds/testes pesados rodando em paralelo — 1 evita saturar a máquina." },
  { chave: "max_correcoes", rotulo: "Máx. correções por fase", tipo: "number", escopo: "ambos" },
  { chave: "max_ciclos_revisao", rotulo: "Máx. ciclos de revisão", tipo: "number", escopo: "ambos" },
  { chave: "max_fases_novas", rotulo: "Máx. fases novas por demanda", tipo: "number", escopo: "ambos",
    hint: "Teto de fases que o agente pode criar ao descobrir trabalho fora do escopo." },
  { chave: "budget_demanda_usd", rotulo: "Budget por demanda (US$)", tipo: "number", escopo: "ambos" },
  { chave: "gates", rotulo: "Gates (validação determinística)", tipo: "lines", escopo: "ambos",
    hint: "Um comando por linha. Executados após cada fase; a fase só conclui com todos verdes." },
  { chave: "uso_intervalo_min", rotulo: "Verificação de franquia (min)", tipo: "number", escopo: "global",
    hint: "De quanto em quanto tempo o Praxis consulta a franquia de cada perfil dos motores (padrão 5 minutos; mínimo 1). Vale sem reiniciar o serviço." },
];

// camposDoEscopo devolve os campos visíveis num escopo ("global" ou "project").
export function camposDoEscopo(escopo) {
  return CAMPOS.filter((c) => c.escopo === "ambos" || c.escopo === escopo);
}

// chavesConhecidas é o conjunto de chaves que a UI edita de forma estruturada.
export const chavesConhecidas = new Set(CAMPOS.map((c) => c.chave));

// jsonParaTexto converte um valor JSON (já decodificado) para o texto exibido no
// campo, conforme o tipo. Valores ausentes/incompatíveis viram "".
export function jsonParaTexto(valor, tipo) {
  if (valor == null) return "";
  if (tipo === "lines") return Array.isArray(valor) ? valor.join("\n") : "";
  if (tipo === "number") return typeof valor === "number" ? String(valor) : "";
  return typeof valor === "string" ? valor : String(valor);
}

// textoParaJSON converte o texto do campo para o valor JSON a persistir. Devolve
// {ok, valor} — ok=false quando a entrada é inválida (ex.: número não numérico).
// Texto vazio para "text"/"lines" ainda é considerado válido (string vazia /
// lista vazia); quem decide se persiste é a tela (herança).
export function textoParaJSON(texto, tipo) {
  if (tipo === "number") {
    const t = texto.trim();
    if (t === "") return { ok: false, valor: null };
    const n = Number(t);
    if (!Number.isFinite(n)) return { ok: false, valor: null };
    return { ok: true, valor: n };
  }
  if (tipo === "lines") {
    const linhas = texto.split("\n").map((l) => l.trim()).filter((l) => l !== "");
    return { ok: true, valor: linhas };
  }
  return { ok: true, valor: texto.trim() };
}

// preservarDesconhecidas devolve as entradas do escopo cujas chaves NÃO são
// conhecidas pela UI, para reenviá-las intactas no PUT (que é full replace).
// `entradas` é o mapa chave→valorJSON já decodificado vindo do backend.
export function preservarDesconhecidas(entradas) {
  const fora = {};
  for (const [k, v] of Object.entries(entradas || {})) {
    if (!chavesConhecidas.has(k)) fora[k] = v;
  }
  return fora;
}
