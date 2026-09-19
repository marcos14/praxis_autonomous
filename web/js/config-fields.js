// Metadados das chaves de config conhecidas, conversões valor↔JSON e o
// controle de edição compartilhado pelas telas de config (global e override
// por projeto).
//
// O store de config (Fase 1e) é livre: chave→valor JSON, sem whitelist. Aqui
// declaramos as chaves que a UI apresenta como formulário estruturado; chaves
// desconhecidas presentes no banco são preservadas (ver preservarDesconhecidas).
//
// Tipos (o tipo é o do JSON persistido, não o do widget):
//   "number" → JSON number
//   "text"   → JSON string
//   "lines"  → JSON array de strings (uma por linha no textarea; vazias ignoradas)
//
// Widget: um campo com `opcoes` (lista fixa) ou `opcoesDe` (lista carregada em
// runtime, ex.: os motores cadastrados) vira <select>; "lines" vira <textarea>;
// o resto, <input>. Toda chave de config é opcional — a opção de valor vazio
// mostra o que vale na ausência (`padrao`), para que um formulário em branco
// deixe de ser um formulário sem informação.

import { t, IDIOMAS } from "./i18n.js";
import { el } from "./ui.js";
import { api } from "./api.js";

// faixa devolve opções numéricas de min..max (o <select> só lida com texto).
function faixa(min, max) {
  const opcoes = [];
  for (let n = min; n <= max; n++) opcoes.push({ valor: String(n), rotulo: String(n) });
  return opcoes;
}

// minutos devolve opções de intervalo em minutos, rotuladas ("5 min").
function minutos(valores) {
  return valores.map((n) => ({ valor: String(n), rotulo: t("config.minutos", { n }) }));
}

// dias devolve opções de prazo em dias, rotuladas ("30 dias").
function dias(valores) {
  return valores.map((n) => ({ valor: String(n), rotulo: t("config.dias", { n }) }));
}

// SIM_NAO são as opções dos campos "bool" (o <select> guarda o texto; a
// conversão para o JSON true/false é de textoParaJSON).
const SIM_NAO = [{ valor: "true", rotulo: t("config.sim") }, { valor: "false", rotulo: t("config.nao") }];

// CAMPOS é a lista canônica de chaves conhecidas, na ordem de exibição —
// agrupada por assunto (`grupo`), para a tela não ser uma lista corrida de
// chaves soltas. `escopo` indica onde o campo aparece: "ambos" (global +
// override) ou "global". `padrao` é o que vale quando a chave está ausente —
// texto já traduzido, só para exibição. Rótulos e hints vêm do catálogo i18n
// (chaves config.<chave> / .hint); as chaves de config em si são contrato e
// não mudam com o idioma.
export const CAMPOS = [
  // --- Execução ---
  { chave: "motor_preferido", rotulo: t("config.motor_preferido"), tipo: "text", escopo: "ambos",
    grupo: t("config.grupo_execucao"), opcoesDe: "motores", padrao: t("config.padrao_automatico"),
    hint: t("config.motor_preferido.hint") },
  // Os dois limites de concorrência são lidos só do escopo global (ver
  // limitesGlobais em cmd/praxis/scheduler.go): override por projeto não teria
  // efeito, então não aparecem na aba do projeto.
  { chave: "execucoes_simultaneas", rotulo: t("config.execucoes_simultaneas"), tipo: "number", escopo: "global",
    grupo: t("config.grupo_execucao"), opcoes: faixa(1, 8), padrao: "2",
    hint: t("config.execucoes_simultaneas.hint") },
  { chave: "execucoes_por_projeto", rotulo: t("config.execucoes_por_projeto"), tipo: "number", escopo: "global",
    grupo: t("config.grupo_execucao"), opcoes: faixa(1, 4), padrao: "1",
    hint: t("config.execucoes_por_projeto.hint") },
  { chave: "git_sufixo_praxis", rotulo: t("config.git_sufixo_praxis"), tipo: "bool", escopo: "ambos",
    grupo: t("config.grupo_execucao"), opcoes: SIM_NAO, padrao: t("config.sim"),
    hint: t("config.git_sufixo_praxis.hint") },

  // --- Validação e revisão ---
  { chave: "gates", rotulo: t("config.gates"), tipo: "lines", escopo: "ambos",
    grupo: t("config.grupo_validacao"), placeholder: t("config.gates.placeholder"),
    hint: t("config.gates.hint") },
  { chave: "gates_simultaneos", rotulo: t("config.gates_simultaneos"), tipo: "number", escopo: "global",
    grupo: t("config.grupo_validacao"), opcoes: faixa(1, 4), padrao: "1",
    hint: t("config.gates_simultaneos.hint") },
  { chave: "max_correcoes", rotulo: t("config.max_correcoes"), tipo: "number", escopo: "ambos",
    grupo: t("config.grupo_validacao"), opcoes: faixa(0, 5), padrao: "2",
    hint: t("config.max_correcoes.hint") },
  { chave: "max_ciclos_revisao", rotulo: t("config.max_ciclos_revisao"), tipo: "number", escopo: "ambos",
    grupo: t("config.grupo_validacao"), opcoes: faixa(0, 5), padrao: "2",
    hint: t("config.max_ciclos_revisao.hint") },

  // --- Instância ---
  { chave: "uso_intervalo_min", rotulo: t("config.uso_intervalo_min"), tipo: "number", escopo: "global",
    grupo: t("config.grupo_instancia"), opcoes: minutos([1, 2, 5, 10, 15, 30, 60]),
    padrao: t("config.minutos", { n: 5 }), hint: t("config.uso_intervalo_min.hint") },
  { chave: "idioma", rotulo: t("config.idioma"), tipo: "text", escopo: "global",
    grupo: t("config.grupo_instancia"), padrao: "pt-BR",
    opcoes: IDIOMAS.map(([tag, nome]) => ({ valor: tag, rotulo: nome + " (" + tag + ")" })),
    hint: t("config.idioma.hint") },

  // --- Sessões e login (M1 do PLANO_INTERNET) --- lidas a cada emissão/renovação
  // de token (internal/api/auth_prazos.go): valem sem reiniciar.
  { chave: "sessao_jwt_min", rotulo: t("config.sessao_jwt_min"), tipo: "number", escopo: "global",
    grupo: t("config.grupo_sessao"), opcoes: minutos([15, 30, 60, 120, 240, 480]),
    padrao: t("config.minutos", { n: 60 }), hint: t("config.sessao_jwt_min.hint") },
  { chave: "sessao_inatividade_dias", rotulo: t("config.sessao_inatividade_dias"), tipo: "number", escopo: "global",
    grupo: t("config.grupo_sessao"), opcoes: dias([1, 3, 7, 14, 30, 60, 90]),
    padrao: t("config.dias", { n: 30 }), hint: t("config.sessao_inatividade_dias.hint") },
  { chave: "sessao_maxima_dias", rotulo: t("config.sessao_maxima_dias"), tipo: "number", escopo: "global",
    grupo: t("config.grupo_sessao"), opcoes: dias([7, 14, 30, 60, 90, 180, 365]),
    padrao: t("config.dias", { n: 90 }), hint: t("config.sessao_maxima_dias.hint") },

  // --- Visibilidade (M2 do PLANO_INTERNET): itens sem dono (token de API/bootstrap).
  { chave: "sem_dono_visibilidade", rotulo: t("config.sem_dono_visibilidade"), tipo: "text", escopo: "global",
    grupo: t("config.grupo_visibilidade"), padrao: t("config.sem_dono_admins"),
    opcoes: [
      { valor: "admins", rotulo: t("config.sem_dono_admins") },
      { valor: "grupo", rotulo: t("config.sem_dono_grupo") },
      { valor: "publica", rotulo: t("config.sem_dono_publica") },
    ],
    hint: t("config.sem_dono_visibilidade.hint") },
  { chave: "sem_dono_grupo_id", rotulo: t("config.sem_dono_grupo_id"), tipo: "number", escopo: "global",
    grupo: t("config.grupo_visibilidade"), opcoesDe: "gruposUsuarios", padrao: "—",
    hint: t("config.sem_dono_grupo_id.hint") },
];

// camposDoEscopo devolve os campos visíveis num escopo ("global" ou "project").
export function camposDoEscopo(escopo) {
  return CAMPOS.filter((c) => c.escopo === "ambos" || c.escopo === escopo);
}

// chavesConhecidas é o conjunto de chaves que a UI edita de forma estruturada.
export const chavesConhecidas = new Set(CAMPOS.map((c) => c.chave));

// OBSOLETAS são chaves que já foram gravadas por versões anteriores (e pelo
// importador do Praxis clássico) mas que nenhum código lê: ficavam na tela
// prometendo um limite que ninguém aplicava. Não são preservadas no PUT — o
// primeiro salvar limpa a sobra do banco.
const OBSOLETAS = new Set(["budget_demanda_usd", "max_fases_novas"]);

// listasDinamicas carrega as opções que dependem do cadastro — hoje só os
// motores, para o `motor_preferido`. Só motores ATIVOS entram: o executor
// ignora os inativos, então oferecê-los seria oferecer uma escolha sem efeito.
// Falha ao listar não derruba o formulário: o campo cai numa lista vazia (o
// valor já gravado continua selecionável, ver campoEntrada).
export async function listasDinamicas() {
  const listas = { motores: [], gruposUsuarios: [] };
  try {
    const motores = (await api.listarMotores()) || [];
    listas.motores = motores.filter((m) => m.ativo).map((m) => ({ valor: m.nome, rotulo: m.nome }));
  } catch { /* lista vazia */ }
  // Grupos de usuários (sem_dono_grupo_id) exigem usuarios.gerir: sem a
  // permissão, o campo fica só com o valor já gravado.
  try {
    const grupos = (await api.listarGruposUsuarios()) || [];
    listas.gruposUsuarios = grupos.map((g) => ({ valor: String(g.id), rotulo: g.nome }));
  } catch { /* lista vazia */ }
  return listas;
}

// opcoesDoCampo resolve a lista de opções do campo: as fixas (`opcoes`) ou as
// dinâmicas de `dinamicas[c.opcoesDe]`. null = campo livre (input/textarea).
function opcoesDoCampo(c, dinamicas) {
  if (c.opcoesDe) return (dinamicas || {})[c.opcoesDe] || [];
  return c.opcoes || null;
}

// campoEntrada cria o controle de edição de um campo já preenchido com
// `valorTexto` (saída de jsonParaTexto). Opções:
//   vazio     — rótulo da opção que representa "sem valor próprio" (cai no
//               default do sistema ou herda do global); também é o placeholder
//               dos campos livres.
//   dinamicas — listas de opções carregadas em runtime, por nome de `opcoesDe`
//               (ex.: { motores: [{valor, rotulo}, …] }).
// Um valor persistido fora da lista (vindo do importador ou da API) entra como
// opção extra, para que editar outra chave não o apague.
export function campoEntrada(c, valorTexto, { vazio = "", dinamicas = {} } = {}) {
  if (c.tipo === "lines") {
    return el("textarea", { placeholder: c.placeholder || null }, valorTexto);
  }
  const opcoes = opcoesDoCampo(c, dinamicas);
  if (opcoes) {
    const sel = el("select", {}, el("option", { value: "" }, vazio));
    for (const o of opcoes) sel.append(el("option", { value: o.valor }, o.rotulo));
    if (valorTexto !== "" && !opcoes.some((o) => o.valor === valorTexto)) {
      sel.append(el("option", { value: valorTexto }, valorTexto));
    }
    sel.value = valorTexto;
    return sel;
  }
  return el("input", {
    type: c.tipo === "number" ? "number" : "text",
    step: c.tipo === "number" ? "any" : null,
    min: c.tipo === "number" ? "0" : null,
    placeholder: c.placeholder || vazio || null,
    value: valorTexto,
  });
}

// jsonParaTexto converte um valor JSON (já decodificado) para o texto exibido no
// campo, conforme o tipo. Valores ausentes/incompatíveis viram "".
export function jsonParaTexto(valor, tipo) {
  if (valor == null) return "";
  if (tipo === "lines") return Array.isArray(valor) ? valor.join("\n") : "";
  if (tipo === "number") return typeof valor === "number" ? String(valor) : "";
  if (tipo === "bool") return typeof valor === "boolean" ? String(valor) : "";
  return typeof valor === "string" ? valor : String(valor);
}

// textoParaJSON converte o texto do campo para o valor JSON a persistir. Devolve
// {ok, valor} — ok=false quando a entrada é inválida (ex.: número não numérico).
// Texto vazio para "text"/"lines" ainda é considerado válido (string vazia /
// lista vazia); quem decide se persiste é a tela (herança).
export function textoParaJSON(texto, tipo) {
  if (tipo === "bool") {
    const v = texto.trim();
    if (v !== "true" && v !== "false") return { ok: false, valor: null };
    return { ok: true, valor: v === "true" };
  }
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
// `entradas` é o mapa chave→valorJSON já decodificado vindo do backend. As
// chaves OBSOLETAS ficam de fora de propósito: é assim que somem do banco.
export function preservarDesconhecidas(entradas) {
  const fora = {};
  for (const [k, v] of Object.entries(entradas || {})) {
    if (!chavesConhecidas.has(k) && !OBSOLETAS.has(k)) fora[k] = v;
  }
  return fora;
}
