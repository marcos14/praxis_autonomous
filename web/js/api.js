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
  // ações de controle de execução (Fase 2i): pausar | retomar | cancelar.
  acaoDemanda: (id, acao) => req("POST", `/api/v1/demands/${id}/actions`, { acao }),
  // urlLogsDemanda devolve a URL do stream SSE (consumida por um EventSource).
  urlLogsDemanda: (id) => `/api/v1/demands/${id}/logs`,

  // config em camadas (Fase 1e)
  obterConfigGlobal: () => req("GET", "/api/v1/config"),
  definirConfigGlobal: (entradas) => req("PUT", "/api/v1/config", entradas),
  obterConfigProjeto: (id) => req("GET", `/api/v1/projects/${id}/config`),
  definirConfigProjeto: (id, entradas) => req("PUT", `/api/v1/projects/${id}/config`, entradas),
  configEfetiva: (id) => req("GET", `/api/v1/projects/${id}/config/efetiva`),
};
