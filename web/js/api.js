// Cliente HTTP fino sobre a API REST /api/v1. Cada função devolve o JSON já
// decodificado ou lança um ErroAPI com o código/mensagem padronizados do
// backend (envelope {erro:{codigo,mensagem}}).

// ErroAPI carrega o status HTTP e o código estável do backend, além da
// mensagem legível. As telas usam .mensagem para exibir e .codigo/.status para
// decidir tratamento (ex.: 409 slug_duplicado).
export class ErroAPI extends Error {
  constructor(status, codigo, mensagem) {
    super(mensagem || `erro ${status}`);
    this.name = "ErroAPI";
    this.status = status;
    this.codigo = codigo || "";
  }
}

// req executa uma requisição JSON e trata o envelope de erro padronizado.
// Devolve o corpo decodificado (ou null em 204). Lança ErroAPI em status >= 400.
async function req(metodo, caminho, corpo) {
  const opts = { method: metodo, headers: {} };
  if (corpo !== undefined) {
    opts.headers["Content-Type"] = "application/json";
    opts.body = JSON.stringify(corpo);
  }
  let resp;
  try {
    resp = await fetch(caminho, opts);
  } catch (e) {
    throw new ErroAPI(0, "rede", "falha de rede: " + e.message);
  }
  if (resp.status === 204) return null;
  const texto = await resp.text();
  let dados = null;
  if (texto) {
    try {
      dados = JSON.parse(texto);
    } catch {
      if (!resp.ok) throw new ErroAPI(resp.status, "invalido", texto.slice(0, 200));
    }
  }
  if (!resp.ok) {
    const e = dados && dados.erro;
    throw new ErroAPI(resp.status, e && e.codigo, (e && e.mensagem) || `erro ${resp.status}`);
  }
  return dados;
}

export const api = {
  // projetos (Fase 1c)
  listarProjetos: () => req("GET", "/api/v1/projects"),
  obterProjeto: (id) => req("GET", `/api/v1/projects/${id}`),
  criarProjeto: (p) => req("POST", "/api/v1/projects", p),
  atualizarProjeto: (id, p) => req("PUT", `/api/v1/projects/${id}`, p),

  // motores e contas (Fase 1d)
  listarMotores: () => req("GET", "/api/v1/engines"),
  obterMotor: (id) => req("GET", `/api/v1/engines/${id}`),
  criarMotor: (m) => req("POST", "/api/v1/engines", m),
  atualizarMotor: (id, m) => req("PUT", `/api/v1/engines/${id}`, m),
  reordenarMotores: (ids) => req("PUT", "/api/v1/engines/ordem", { ids }),
  criarConta: (engineID, c) => req("POST", `/api/v1/engines/${engineID}/accounts`, c),
  atualizarConta: (engineID, contaID, c) => req("PUT", `/api/v1/engines/${engineID}/accounts/${contaID}`, c),
  removerConta: (engineID, contaID) => req("DELETE", `/api/v1/engines/${engineID}/accounts/${contaID}`),

  // demandas (Fases 2g/2h)
  listarDemandas: (q = {}) => {
    const p = new URLSearchParams();
    if (q.project) p.set("project", q.project);
    if (q.status) p.set("status", q.status);
    const qs = p.toString();
    return req("GET", "/api/v1/demands" + (qs ? "?" + qs : ""));
  },
  obterDemanda: (id) => req("GET", `/api/v1/demands/${id}`),
  eventosDemanda: (id) => req("GET", `/api/v1/demands/${id}/events`),
  // intake por chat (Fase 3a): cria a demanda a partir do PRD (sem fases).
  criarDemandaChat: (projectId, d) => req("POST", `/api/v1/projects/${projectId}/demands`, d),
  // chat da demanda (Fase 3a): listar as falas e acrescentar uma do usuário.
  listarChat: (id) => req("GET", `/api/v1/demands/${id}/chat`),
  enviarChat: (id, conteudo) => req("POST", `/api/v1/demands/${id}/chat`, { conteudo }),
  // perguntas do analista (Fase 3b): listar e responder ("responder tudo e gerar plano").
  listarPerguntas: (id) => req("GET", `/api/v1/demands/${id}/questions`),
  responderPerguntas: (id, respostas) => req("POST", `/api/v1/demands/${id}/answers`, { respostas }),
  // plano & fases (Fase 3c): editar o conjunto de fases, aprovar ou rejeitar o plano.
  editarFases: (id, fases) => req("PUT", `/api/v1/demands/${id}/phases`, { fases }),
  aprovarPlano: (id) => req("POST", `/api/v1/demands/${id}/approve-plan`, { aprovar: true }),
  rejeitarPlano: (id, comentario) => req("POST", `/api/v1/demands/${id}/approve-plan`, { aprovar: false, comentario }),
  // ações de controle de execução (Fase 2i/4c/4d): pausar | retomar | cancelar |
  // publicar_branch | integrar | atualizar_branch.
  acaoDemanda: (id, acao) => req("POST", `/api/v1/demands/${id}/actions`, { acao }),
  // preview de integração (Fases 4c/4d): commits, conflito com a main e link do MR.
  mergePreview: (id) => req("GET", `/api/v1/demands/${id}/merge-preview`),
  // urlLogsDemanda devolve a URL do stream SSE (consumida por um EventSource).
  urlLogsDemanda: (id) => `/api/v1/demands/${id}/logs`,

  // kanban (Fase 4a): board = demandas enriquecidas (progresso + motor); ordem =
  // reordenar prioridade (arraste); urlEventos = SSE global de eventos.
  board: (q = {}) => {
    const p = new URLSearchParams();
    if (q.project) p.set("project", q.project);
    if (q.status) p.set("status", q.status);
    const qs = p.toString();
    return req("GET", "/api/v1/board" + (qs ? "?" + qs : ""));
  },
  reordenarDemandas: (ids) => req("PUT", "/api/v1/demands/ordem", { ids }),
  urlEventos: () => "/api/v1/events",

  // Home (Fase 4b): métricas agregadas, "Precisa de você" e atividade recente.
  metricas: (periodo) => req("GET", "/api/v1/metrics" + (periodo ? "?periodo=" + encodeURIComponent(periodo) : "")),
  pendencias: () => req("GET", "/api/v1/pendencias"),
  atividade: (limite) => req("GET", "/api/v1/activity" + (limite ? "?limite=" + limite : "")),

  // config em camadas (Fase 1e)
  obterConfigGlobal: () => req("GET", "/api/v1/config"),
  definirConfigGlobal: (entradas) => req("PUT", "/api/v1/config", entradas),
  obterConfigProjeto: (id) => req("GET", `/api/v1/projects/${id}/config`),
  definirConfigProjeto: (id, entradas) => req("PUT", `/api/v1/projects/${id}/config`, entradas),
  configEfetiva: (id) => req("GET", `/api/v1/projects/${id}/config/efetiva`),
};
