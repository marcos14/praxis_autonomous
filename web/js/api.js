// Cliente HTTP fino sobre a API REST /api/v1. Cada função devolve o JSON já
// decodificado ou lança um ErroAPI com o código/mensagem padronizados do
// backend (envelope {erro:{codigo,mensagem}}).

import { tokenAtual, logout } from "./auth.js";

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
// Anexa o JWT da sessão (Authorization: Bearer) quando há token; em 401 (sessão
// expirada/inválida) derruba a sessão para a shell exibir o login.
async function req(metodo, caminho, corpo) {
  const opts = { method: metodo, headers: {} };
  const tok = tokenAtual();
  if (tok) opts.headers["Authorization"] = "Bearer " + tok;
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
  if (resp.status === 401) {
    logout();
    throw new ErroAPI(401, "nao_autenticado", "sessão expirada: faça login novamente");
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

// reqUpload envia um arquivo via multipart/form-data (o req é só JSON). Mesmo
// tratamento de token, 401 e envelope de erro do req.
async function reqUpload(caminho, campo, arquivo) {
  const fd = new FormData();
  fd.append(campo, arquivo, arquivo.name);
  const opts = { method: "POST", headers: {}, body: fd };
  const tok = tokenAtual();
  if (tok) opts.headers["Authorization"] = "Bearer " + tok;
  let resp;
  try {
    resp = await fetch(caminho, opts);
  } catch (e) {
    throw new ErroAPI(0, "rede", "falha de rede: " + e.message);
  }
  if (resp.status === 401) {
    logout();
    throw new ErroAPI(401, "nao_autenticado", "sessão expirada: faça login novamente");
  }
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

// comToken anexa o JWT da sessão como query param a uma URL de stream (SSE). O
// EventSource não permite enviar o header Authorization, então as rotas de SSE
// recebem o token por `?token=` (o backend aceita essa forma só para autenticar;
// o token não é registrado nos logs de acesso, que gravam apenas o caminho).
function comToken(url) {
  const t = tokenAtual();
  if (!t) return url;
  return url + (url.includes("?") ? "&" : "?") + "token=" + encodeURIComponent(t);
}

export const api = {
  // projetos (Fase 1c)
  listarProjetos: () => req("GET", "/api/v1/projects"),
  obterProjeto: (id) => req("GET", `/api/v1/projects/${id}`),
  criarProjeto: (p) => req("POST", "/api/v1/projects", p),
  atualizarProjeto: (id, p) => req("PUT", `/api/v1/projects/${id}`, p),
  // overview do repositório (feature de consultas): edição manual e geração
  // pelo harness (202 — acompanhe pelo evento overview_gerado no SSE global).
  salvarOverview: (id, overviewMd) => req("PUT", `/api/v1/projects/${id}/overview`, { overview_md: overviewMd }),
  gerarOverview: (id) => req("POST", `/api/v1/projects/${id}/overview/gerar`),
  // ACL de visibilidade do projeto: quem (usuários/grupos de usuários) enxerga o
  // projeto. Listas vazias = aberto a todos. Exige projetos.gerir.
  obterAcessoProjeto: (id) => req("GET", `/api/v1/projects/${id}/access`),
  definirAcessoProjeto: (id, acesso) => req("PUT", `/api/v1/projects/${id}/access`, acesso),

  // grupos de repositórios (feature de consultas): N:N com projetos; o primeiro
  // project_id é o repositório principal.
  listarGrupos: () => req("GET", "/api/v1/groups"),
  obterGrupo: (id) => req("GET", `/api/v1/groups/${id}`),
  criarGrupo: (g) => req("POST", "/api/v1/groups", g),
  atualizarGrupo: (id, g) => req("PUT", `/api/v1/groups/${id}`, g),
  excluirGrupo: (id) => req("DELETE", `/api/v1/groups/${id}`),

  // consultas (chat de análise de código para produto/suporte).
  listarConsultas: (q = {}) => {
    const p = new URLSearchParams();
    if (q.project) p.set("project", q.project);
    if (q.group) p.set("group", q.group);
    const qs = p.toString();
    return req("GET", "/api/v1/consultas" + (qs ? "?" + qs : ""));
  },
  obterConsulta: (id) => req("GET", `/api/v1/consultas/${id}`),
  criarConsulta: (c) => req("POST", "/api/v1/consultas", c),
  excluirConsulta: (id) => req("DELETE", `/api/v1/consultas/${id}`),
  listarChatConsulta: (id) => req("GET", `/api/v1/consultas/${id}/chat`),
  enviarChatConsulta: (id, conteudo) => req("POST", `/api/v1/consultas/${id}/chat`, { conteudo }),
  // Progresso SANITIZADO do turno (SSE): só resumos ("lendo arquivo…"), nunca o
  // log cru — o log cru contém código-fonte, que esta feature não expõe.
  urlProgressoConsulta: (id) => comToken(`/api/v1/consultas/${id}/progresso`),

  // planejamentos (PRD/ADR iterativos com o estrategista).
  listarPlanejamentos: (q = {}) => {
    const p = new URLSearchParams();
    if (q.project) p.set("project", q.project);
    if (q.group) p.set("group", q.group);
    const qs = p.toString();
    return req("GET", "/api/v1/planejamentos" + (qs ? "?" + qs : ""));
  },
  obterPlanejamento: (id) => req("GET", `/api/v1/planejamentos/${id}`),
  criarPlanejamento: (p) => req("POST", "/api/v1/planejamentos", p),
  atualizarPlanejamento: (id, p) => req("PUT", `/api/v1/planejamentos/${id}`, p),
  excluirPlanejamento: (id) => req("DELETE", `/api/v1/planejamentos/${id}`),
  listarChatPlanejamento: (id) => req("GET", `/api/v1/planejamentos/${id}/chat`),
  enviarChatPlanejamento: (id, conteudo) => req("POST", `/api/v1/planejamentos/${id}/chat`, { conteudo }),
  // Turno sem fala nova: disparo adiado (criação com anexos) e tentar novamente.
  dispararTurnoPlanejamento: (id) => req("POST", `/api/v1/planejamentos/${id}/turno`, {}),
  urlProgressoPlanejamento: (id) => comToken(`/api/v1/planejamentos/${id}/progresso`),
  listarDocumentosPlanejamento: (id) => req("GET", `/api/v1/planejamentos/${id}/documentos`),
  obterDocumentoPlanejamento: (id, arquivo, revisao) =>
    req("GET", `/api/v1/planejamentos/${id}/documentos/${encodeURIComponent(arquivo)}` +
      (revisao > 0 ? `?revisao=${revisao}` : "")),
  listarArtefatosPlanejamento: (id) => req("GET", `/api/v1/planejamentos/${id}/artefatos`),
  // O artefato abre em nova aba: o token vai na URL (como no SSE) porque a
  // navegação do navegador não envia o header Authorization.
  urlArtefatoPlanejamento: (id, arquivo) =>
    comToken(`/api/v1/planejamentos/${id}/artefatos/${encodeURIComponent(arquivo)}`),
  urlDownloadArtefatoPlanejamento: (id, arquivo) =>
    comToken(`/api/v1/planejamentos/${id}/artefatos/${encodeURIComponent(arquivo)}?download=1`),
  listarReferenciasPlanejamento: (id) => req("GET", `/api/v1/planejamentos/${id}/referencias`),
  enviarReferenciaPlanejamento: (id, arquivo) =>
    reqUpload(`/api/v1/planejamentos/${id}/referencias`, "arquivo", arquivo),
  urlReferenciaPlanejamento: (id, arquivo) =>
    comToken(`/api/v1/planejamentos/${id}/referencias/${encodeURIComponent(arquivo)}`),
  excluirReferenciaPlanejamento: (id, arquivo) =>
    req("DELETE", `/api/v1/planejamentos/${id}/referencias/${encodeURIComponent(arquivo)}`),
  listarDemandasPlanejamento: (id) => req("GET", `/api/v1/planejamentos/${id}/demandas`),
  criarDemandaDePlanejamento: (id, corpo = {}) =>
    req("POST", `/api/v1/planejamentos/${id}/criar-demanda`, corpo),

  // motores e contas (Fase 1d)
  listarMotores: () => req("GET", "/api/v1/engines"),
  obterMotor: (id) => req("GET", `/api/v1/engines/${id}`),
  criarMotor: (m) => req("POST", "/api/v1/engines", m),
  atualizarMotor: (id, m) => req("PUT", `/api/v1/engines/${id}`, m),
  reordenarMotores: (ids) => req("PUT", "/api/v1/engines/ordem", { ids }),
  // detecção automática: sugere motores a partir do ambiente do servidor
  // (harness instalado + variáveis) e cadastra os detectados com um POST.
  detectarMotores: () => req("GET", "/api/v1/engines/deteccao"),
  autocadastrarMotores: () => req("POST", "/api/v1/engines/deteccao"),
  // uso: consumo do Praxis por motor/perfil + franquia do vendor (monitor).
  usoMotores: () => req("GET", "/api/v1/engines/uso"),
  criarConta: (engineID, c) => req("POST", `/api/v1/engines/${engineID}/accounts`, c),
  atualizarConta: (engineID, contaID, c) => req("PUT", `/api/v1/engines/${engineID}/accounts/${contaID}`, c),
  removerConta: (engineID, contaID) => req("DELETE", `/api/v1/engines/${engineID}/accounts/${contaID}`),
  estadoAuthMotor: (engineID, contaID) => req("GET", `/api/v1/engines/${engineID}/accounts/${contaID}/auth`),
  iniciarLoginMotor: (engineID, contaID) => req("POST", `/api/v1/engines/${engineID}/accounts/${contaID}/login`),
  obterLoginMotor: (sessionID) => req("GET", `/api/v1/engine-auth-sessions/${sessionID}`),
  cancelarLoginMotor: (sessionID) => req("DELETE", `/api/v1/engine-auth-sessions/${sessionID}`),
  enviarCodigoLoginMotor: (sessionID, codigo) => req("POST", `/api/v1/engine-auth-sessions/${sessionID}/code`, { codigo }),

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
  // concluir manualmente uma fase que exige intervenção humana (requer_humano):
  // marca a fase como feita e, se a demanda estava pausada aguardando humano, a
  // devolve à fila do scheduler para seguir com as próximas fases.
  concluirFaseHumana: (id, codigo) =>
    req("POST", `/api/v1/demands/${id}/phases/${encodeURIComponent(codigo)}/complete`),
  // reiniciar uma fase automática travada: interrompe o run em andamento,
  // descarta o trabalho não commitado do worktree e devolve a fase a pendente.
  reiniciarFase: (id, codigo) =>
    req("POST", `/api/v1/demands/${id}/phases/${encodeURIComponent(codigo)}/restart`),
  aprovarPlano: (id) => req("POST", `/api/v1/demands/${id}/approve-plan`, { aprovar: true }),
  rejeitarPlano: (id, comentario) => req("POST", `/api/v1/demands/${id}/approve-plan`, { aprovar: false, comentario }),
  // ações de controle de execução (Fase 2i/4c/4d): pausar | retomar | cancelar |
  // publicar_branch | integrar | atualizar_branch.
  acaoDemanda: (id, acao) => req("POST", `/api/v1/demands/${id}/actions`, { acao }),
  // preview de integração (Fases 4c/4d): commits, conflito com a main e link do MR.
  mergePreview: (id) => req("GET", `/api/v1/demands/${id}/merge-preview`),
  // diff da demanda: todas as alterações (base...branch) ou só de uma fase (?fase=<codigo>).
  diffDemanda: (id, fase) =>
    req("GET", `/api/v1/demands/${id}/diff` + (fase ? "?fase=" + encodeURIComponent(fase) : "")),
  // sobreposição entre demandas (Fase 5c): mapa global (badges) e detalhe por demanda.
  overlaps: () => req("GET", "/api/v1/overlaps"),
  overlapDemanda: (id) => req("GET", `/api/v1/demands/${id}/overlap`),
  // urlLogsDemanda devolve a URL do stream SSE (consumida por um EventSource),
  // com o token da sessão embutido (EventSource não envia headers).
  urlLogsDemanda: (id) => comToken(`/api/v1/demands/${id}/logs`),

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
  urlEventos: () => comToken("/api/v1/events"),

  // Home (Fase 4b): métricas agregadas, "Precisa de você" e atividade recente.
  metricas: (periodo) => req("GET", "/api/v1/metrics" + (periodo ? "?periodo=" + encodeURIComponent(periodo) : "")),
  pendencias: () => req("GET", "/api/v1/pendencias"),
  atividade: (limite) => req("GET", "/api/v1/activity" + (limite ? "?limite=" + limite : "")),

  // IDE web (edição manual do worktree): o POST emite o cookie de sessão do
  // /ide/* e garante o serve-web no ar — 202 {estado:"preparando"} enquanto
  // sobe (repita o POST), 200 {estado:"pronto", url} quando pronto.
  sessaoIDE: (demandId) => req("POST", "/api/v1/ide/sessao", { demand_id: demandId }),
  statusIDE: () => req("GET", "/api/v1/ide/status"),

  // manual embutido (Fase 5b).
  listarManual: () => req("GET", "/api/v1/manual"),
  secaoManual: (slug) => req("GET", `/api/v1/manual/${slug}`),

  // tokens de API (Fase 5a): papel ∈ {leitor, operador, admin}.
  listarTokens: () => req("GET", "/api/v1/tokens"),
  criarToken: (nome, papel) => req("POST", "/api/v1/tokens", { nome, papel }),
  revogarToken: (id) => req("DELETE", `/api/v1/tokens/${id}`),

  // config em camadas (Fase 1e)
  obterConfigGlobal: () => req("GET", "/api/v1/config"),
  definirConfigGlobal: (entradas) => req("PUT", "/api/v1/config", entradas),
  obterConfigProjeto: (id) => req("GET", `/api/v1/projects/${id}/config`),
  definirConfigProjeto: (id, entradas) => req("PUT", `/api/v1/projects/${id}/config`, entradas),
  configEfetiva: (id) => req("GET", `/api/v1/projects/${id}/config/efetiva`),

  // usuários, papéis e catálogo de permissões (RBAC). Exigem usuarios.gerir.
  listarPermissoes: () => req("GET", "/api/v1/permissions"),
  listarUsuarios: () => req("GET", "/api/v1/users"),
  criarUsuario: (u) => req("POST", "/api/v1/users", u),
  atualizarUsuario: (id, u) => req("PUT", `/api/v1/users/${id}`, u),
  resetarSenha: (id, nova) => req("PUT", `/api/v1/users/${id}/senha`, { nova }),
  excluirUsuario: (id) => req("DELETE", `/api/v1/users/${id}`),
  listarPapeis: () => req("GET", "/api/v1/roles"),
  criarPapel: (p) => req("POST", "/api/v1/roles", p),
  atualizarPapel: (id, p) => req("PUT", `/api/v1/roles/${id}`, p),
  excluirPapel: (id) => req("DELETE", `/api/v1/roles/${id}`),

  // grupos de usuários (consultas): definem o motor/modelo das consultas dos
  // membros. Exigem usuarios.gerir.
  listarGruposUsuarios: () => req("GET", "/api/v1/user-groups"),
  criarGrupoUsuarios: (g) => req("POST", "/api/v1/user-groups", g),
  atualizarGrupoUsuarios: (id, g) => req("PUT", `/api/v1/user-groups/${id}`, g),
  excluirGrupoUsuarios: (id) => req("DELETE", `/api/v1/user-groups/${id}`),
};
