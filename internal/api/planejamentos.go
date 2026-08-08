package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/marcos14/praxis-autonomous/internal/db"
	"github.com/marcos14/praxis-autonomous/internal/estrategista"
	"github.com/marcos14/praxis-autonomous/internal/gitops"
)

// reqNovoPlanejamento é o corpo de POST /planejamentos: o alvo (projeto OU
// grupo, exclusivo), o foco documental (prd|adr|ambos), o nível visual
// (documento|apresentacao|prototipo), um título opcional e a necessidade
// inicial do usuário.
type reqNovoPlanejamento struct {
	ProjectID   int64  `json:"project_id"`
	GroupID     int64  `json:"group_id"`
	Titulo      string `json:"titulo"`
	Foco        string `json:"foco"`
	NivelVisual string `json:"nivel_visual"`
	Mensagem    string `json:"mensagem"`
}

// reqEditarPlanejamento é o corpo de PUT /planejamentos/{id}: campos ajustáveis
// entre turnos (o próximo turno usa os valores novos).
type reqEditarPlanejamento struct {
	Titulo      string `json:"titulo"`
	Foco        string `json:"foco"`
	NivelVisual string `json:"nivel_visual"`
}

// reqChatPlanejamento é o corpo de POST /planejamentos/{id}/chat: uma fala do
// usuário. O papel é sempre user — falas do estrategista/sistema são geradas
// pelo backend.
type reqChatPlanejamento struct {
	Conteudo string `json:"conteudo"`
}

// reqDemandaDePlanejamento é o corpo de POST /planejamentos/{id}/criar-demanda.
// ProjectID só é exigido em planejamento de grupo (escolhe qual repositório
// recebe a demanda); Titulo e Branch são opcionais.
type reqDemandaDePlanejamento struct {
	ProjectID int64  `json:"project_id"`
	Titulo    string `json:"titulo"`
	Branch    string `json:"branch"`
}

// registrarRotasPlanejamentos registra as rotas da feature de planejamentos
// (sessões do estrategista: PRD/ADR iterativos com documentos e artefatos).
// Leituras exigem autenticação; mutações exigem planejamentos.usar (ver
// permissaoMutacao) — e criar-demanda exige também demandas.criar.
func (s *Servidor) registrarRotasPlanejamentos(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/planejamentos", s.handleCriarPlanejamento)
	mux.HandleFunc("GET /api/v1/planejamentos", s.handleListarPlanejamentos)
	mux.HandleFunc("GET /api/v1/planejamentos/{id}", s.handleObterPlanejamento)
	mux.HandleFunc("PUT /api/v1/planejamentos/{id}", s.handleEditarPlanejamento)
	mux.HandleFunc("DELETE /api/v1/planejamentos/{id}", s.handleExcluirPlanejamento)
	mux.HandleFunc("GET /api/v1/planejamentos/{id}/chat", s.handleListarChatPlanejamento)
	mux.HandleFunc("POST /api/v1/planejamentos/{id}/chat", s.handleChatPlanejamento)
	mux.HandleFunc("GET /api/v1/planejamentos/{id}/progresso", s.handleProgressoPlanejamento)
	mux.HandleFunc("GET /api/v1/planejamentos/{id}/documentos", s.handleListarDocumentosPlanejamento)
	mux.HandleFunc("GET /api/v1/planejamentos/{id}/documentos/{arquivo}", s.handleObterDocumentoPlanejamento)
	mux.HandleFunc("GET /api/v1/planejamentos/{id}/artefatos", s.handleListarArtefatosPlanejamento)
	mux.HandleFunc("GET /api/v1/planejamentos/{id}/artefatos/{arquivo}", s.handleServirArtefatoPlanejamento)
	mux.HandleFunc("POST /api/v1/planejamentos/{id}/criar-demanda", s.handleCriarDemandaDePlanejamento)
}

// handleCriarPlanejamento cria o planejamento com a primeira fala do usuário e
// dispara o primeiro turno do estrategista em background. Devolve 201.
func (s *Servidor) handleCriarPlanejamento(w http.ResponseWriter, r *http.Request) {
	var req reqNovoPlanejamento
	if !decodificarCorpo(w, r, &req) {
		return
	}
	if (req.ProjectID > 0) == (req.GroupID > 0) {
		responderErro(w, http.StatusBadRequest, "invalido",
			"informe project_id OU group_id (exatamente um)")
		return
	}
	mensagem := strings.TrimSpace(req.Mensagem)
	if mensagem == "" {
		responderErro(w, http.StatusBadRequest, "invalido", "mensagem é obrigatória")
		return
	}
	foco := strings.TrimSpace(req.Foco)
	if foco != "" && !db.FocoPlanejamentoValido(foco) {
		responderErro(w, http.StatusBadRequest, "invalido", "foco deve ser 'prd', 'adr' ou 'ambos'")
		return
	}
	nivel := strings.TrimSpace(req.NivelVisual)
	if nivel != "" && !db.NivelVisualValido(nivel) {
		responderErro(w, http.StatusBadRequest, "invalido",
			"nivel_visual deve ser 'documento', 'apresentacao' ou 'prototipo'")
		return
	}
	// ACL de projetos: um usuário restrito não abre planejamento sobre projeto
	// (ou grupo) que a ACL esconde dele. 404 — sem revelar existência.
	if uid := visibilidadeDaRequisicao(r); uid != nil {
		var (
			ve  bool
			err error
		)
		if req.ProjectID > 0 {
			ve, err = s.banco.UsuarioVeProjeto(r.Context(), *uid, req.ProjectID)
		} else {
			ve, err = s.banco.UsuarioVeGrupoProjetos(r.Context(), *uid, req.GroupID)
		}
		if err != nil {
			s.responderErroPlanejamento(w, err)
			return
		}
		if !ve {
			responderErro(w, http.StatusNotFound, "nao_encontrado", "projeto ou grupo não encontrado")
			return
		}
	}

	plan := db.Planejamento{
		Titulo: strings.TrimSpace(req.Titulo), Foco: foco, NivelVisual: nivel,
		Status: db.StatusPlanejamentoPensando,
	}
	if req.ProjectID > 0 {
		plan.ProjectID = &req.ProjectID
	} else {
		plan.GroupID = &req.GroupID
	}
	if plan.Titulo == "" {
		plan.Titulo = tituloDaMensagem(mensagem)
	}
	if pr := principalDaRequisicao(r); pr.userID > 0 {
		uid := pr.userID
		plan.CriadoPor = &uid
	}

	criado, _, err := s.banco.CriarPlanejamentoComChat(r.Context(), plan,
		db.MensagemPlanejamento{Papel: db.PapelPlanejamentoUser, Conteudo: mensagem})
	if err != nil {
		s.responderErroPlanejamento(w, err)
		return
	}
	if s.estrategista != nil {
		s.estrategista.DispararResposta(criado.ID)
	}
	responderJSON(w, http.StatusCreated, criado)
}

// handleListarPlanejamentos lista os planejamentos (filtros ?project= e
// ?group=). Para usuários restritos pela ACL, só os de projetos/grupos visíveis.
func (s *Servidor) handleListarPlanejamentos(w http.ResponseWriter, r *http.Request) {
	projectID, _ := strconv.ParseInt(r.URL.Query().Get("project"), 10, 64)
	groupID, _ := strconv.ParseInt(r.URL.Query().Get("group"), 10, 64)
	planejamentos, err := s.banco.ListarPlanejamentos(r.Context(), projectID, groupID)
	if err != nil {
		s.responderErroPlanejamento(w, err)
		return
	}
	if uid := visibilidadeDaRequisicao(r); uid != nil {
		if planejamentos, err = s.filtrarPlanejamentosVisiveis(r.Context(), *uid, planejamentos); err != nil {
			s.responderErroPlanejamento(w, err)
			return
		}
	}
	responderJSON(w, http.StatusOK, planejamentos)
}

// filtrarPlanejamentosVisiveis descarta os planejamentos de projetos/grupos que
// a ACL esconde do usuário, memoizando a decisão por alvo.
func (s *Servidor) filtrarPlanejamentosVisiveis(ctx context.Context, userID int64, planejamentos []db.Planejamento) ([]db.Planejamento, error) {
	memoProj := map[int64]bool{}
	memoGrupo := map[int64]bool{}
	visiveis := []db.Planejamento{}
	for _, p := range planejamentos {
		ve := true
		switch {
		case p.ProjectID != nil:
			v, ok := memoProj[*p.ProjectID]
			if !ok {
				var err error
				if v, err = s.banco.UsuarioVeProjeto(ctx, userID, *p.ProjectID); err != nil {
					return nil, err
				}
				memoProj[*p.ProjectID] = v
			}
			ve = v
		case p.GroupID != nil:
			v, ok := memoGrupo[*p.GroupID]
			if !ok {
				var err error
				if v, err = s.banco.UsuarioVeGrupoProjetos(ctx, userID, *p.GroupID); err != nil {
					return nil, err
				}
				memoGrupo[*p.GroupID] = v
			}
			ve = v
		}
		if ve {
			visiveis = append(visiveis, p)
		}
	}
	return visiveis, nil
}

func (s *Servidor) handleObterPlanejamento(w http.ResponseWriter, r *http.Request) {
	plan, ok := s.obterPlanejamentoOu404(w, r)
	if !ok {
		return
	}
	responderJSON(w, http.StatusOK, plan)
}

// handleEditarPlanejamento ajusta título, foco e/ou nível visual entre turnos.
// Com um turno em voo responde 409 (o turno usa os valores do disparo).
func (s *Servidor) handleEditarPlanejamento(w http.ResponseWriter, r *http.Request) {
	plan, ok := s.obterPlanejamentoOu404(w, r)
	if !ok {
		return
	}
	if plan.Status == db.StatusPlanejamentoPensando {
		responderErro(w, http.StatusConflict, "pensando",
			"o estrategista está trabalhando — ajuste o planejamento após a resposta")
		return
	}
	var req reqEditarPlanejamento
	if !decodificarCorpo(w, r, &req) {
		return
	}
	if t := strings.TrimSpace(req.Titulo); t != "" {
		plan.Titulo = t
	}
	if f := strings.TrimSpace(req.Foco); f != "" {
		if !db.FocoPlanejamentoValido(f) {
			responderErro(w, http.StatusBadRequest, "invalido", "foco deve ser 'prd', 'adr' ou 'ambos'")
			return
		}
		plan.Foco = f
	}
	if n := strings.TrimSpace(req.NivelVisual); n != "" {
		if !db.NivelVisualValido(n) {
			responderErro(w, http.StatusBadRequest, "invalido",
				"nivel_visual deve ser 'documento', 'apresentacao' ou 'prototipo'")
			return
		}
		plan.NivelVisual = n
	}
	atualizado, err := s.banco.AtualizarPlanejamento(r.Context(), plan)
	if err != nil {
		s.responderErroPlanejamento(w, err)
		return
	}
	responderJSON(w, http.StatusOK, atualizado)
}

// handleExcluirPlanejamento remove o planejamento (banco + pasta de trabalho).
// Só o criador (ou um admin/curinga) pode excluir.
func (s *Servidor) handleExcluirPlanejamento(w http.ResponseWriter, r *http.Request) {
	plan, ok := s.obterPlanejamentoOu404(w, r)
	if !ok {
		return
	}
	pr := principalDaRequisicao(r)
	dono := plan.CriadoPor != nil && pr.userID == *plan.CriadoPor
	if !dono && !pr.tem(db.PermCuringa) {
		responderErro(w, http.StatusForbidden, "sem_permissao",
			"só quem criou o planejamento (ou um administrador) pode excluí-lo")
		return
	}
	if err := s.banco.ExcluirPlanejamento(r.Context(), plan.ID); err != nil {
		s.responderErroPlanejamento(w, err)
		return
	}
	// A pasta de trabalho sai junto (best-effort — o banco é a fonte da verdade
	// dos documentos; artefatos órfãos não são mais servíveis sem a linha do índice).
	if s.estrategista != nil {
		if pasta := s.estrategista.Pasta(plan.ID); pasta != "" {
			if err := os.RemoveAll(pasta); err != nil {
				s.log.Warn("remover pasta do planejamento", "erro", err, "planejamento", plan.ID)
			}
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Servidor) handleListarChatPlanejamento(w http.ResponseWriter, r *http.Request) {
	plan, ok := s.obterPlanejamentoOu404(w, r)
	if !ok {
		return
	}
	msgs, err := s.banco.ListarMensagensPlanejamento(r.Context(), plan.ID)
	if err != nil {
		s.responderErroPlanejamento(w, err)
		return
	}
	responderJSON(w, http.StatusOK, msgs)
}

// handleChatPlanejamento acrescenta uma fala do usuário e dispara o próximo
// turno. Enquanto o estrategista está pensando, novas falas são recusadas com
// 409 (um turno por vez — o motor é stateless e o turno em voo não veria a fala).
func (s *Servidor) handleChatPlanejamento(w http.ResponseWriter, r *http.Request) {
	plan, ok := s.obterPlanejamentoOu404(w, r)
	if !ok {
		return
	}
	if plan.Status == db.StatusPlanejamentoPensando {
		responderErro(w, http.StatusConflict, "pensando",
			"o estrategista ainda está trabalhando na mensagem anterior — aguarde a resposta")
		return
	}
	var req reqChatPlanejamento
	if !decodificarCorpo(w, r, &req) {
		return
	}
	conteudo := strings.TrimSpace(req.Conteudo)
	if conteudo == "" {
		responderErro(w, http.StatusBadRequest, "invalido", "conteudo é obrigatório")
		return
	}

	msg, err := s.banco.CriarMensagemPlanejamento(r.Context(), db.MensagemPlanejamento{
		PlanejamentoID: plan.ID, Papel: db.PapelPlanejamentoUser, Conteudo: conteudo,
	})
	if err != nil {
		s.responderErroPlanejamento(w, err)
		return
	}
	plan.Status = db.StatusPlanejamentoPensando
	plan.Erro = ""
	if _, err := s.banco.AtualizarPlanejamento(r.Context(), plan); err != nil {
		s.log.Error("marcar planejamento pensando", "erro", err)
	}
	if s.estrategista != nil {
		s.estrategista.DispararResposta(plan.ID)
	}
	responderJSON(w, http.StatusAccepted, msg)
}

// handleProgressoPlanejamento transmite, via SSE, o progresso SANITIZADO do
// turno em andamento — mesmo funil allowlisted do progresso de consultas
// (resumirLinhaStream): nunca repassa a linha crua do .jsonl.
func (s *Servidor) handleProgressoPlanejamento(w http.ResponseWriter, r *http.Request) {
	plan, ok := s.obterPlanejamentoOu404(w, r)
	if !ok {
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		responderErro(w, http.StatusInternalServerError, "sem_streaming", "streaming não suportado por esta conexão")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, ": conectado\n\n")
	flusher.Flush()

	intervalo := s.intervaloPollLog
	if intervalo <= 0 {
		intervalo = intervaloPollLogPadrao
	}
	ticker := time.NewTicker(intervalo)
	defer ticker.Stop()

	var (
		runAtual int64
		caminho  string
		offset   int64
	)
	for {
		if run, temLog, err := s.banco.UltimaExecucaoPlanejamentoComLog(r.Context(), plan.ID); err == nil && temLog && run.ID != runAtual {
			runAtual = run.ID
			caminho = run.LogRef
			offset = 0
		}
		if caminho != "" {
			linhas, novo, err := lerNovasLinhas(caminho, offset)
			if err == nil {
				offset = novo
				emitiu := false
				for _, ln := range linhas {
					if resumo, ok := resumirLinhaStream(ln); ok {
						enviarDadosSSE(w, resumo)
						emitiu = true
					}
				}
				if emitiu {
					flusher.Flush()
				}
			}
		}

		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
		}
	}
}

// handleListarDocumentosPlanejamento devolve a revisão mais recente de cada
// documento canônico do planejamento (com conteúdo — a UI renderiza direto).
func (s *Servidor) handleListarDocumentosPlanejamento(w http.ResponseWriter, r *http.Request) {
	plan, ok := s.obterPlanejamentoOu404(w, r)
	if !ok {
		return
	}
	docs, err := s.banco.ListarDocumentosPlanejamento(r.Context(), plan.ID)
	if err != nil {
		s.responderErroPlanejamento(w, err)
		return
	}
	responderJSON(w, http.StatusOK, docs)
}

// handleObterDocumentoPlanejamento devolve uma revisão do documento
// (?revisao=N; sem o parâmetro, a mais recente).
func (s *Servidor) handleObterDocumentoPlanejamento(w http.ResponseWriter, r *http.Request) {
	plan, ok := s.obterPlanejamentoOu404(w, r)
	if !ok {
		return
	}
	arquivo := strings.TrimSpace(r.PathValue("arquivo"))
	revisao, _ := strconv.ParseInt(r.URL.Query().Get("revisao"), 10, 64)
	doc, err := s.banco.ObterDocumentoPlanejamento(r.Context(), plan.ID, arquivo, revisao)
	if err != nil {
		s.responderErroPlanejamento(w, err)
		return
	}
	responderJSON(w, http.StatusOK, doc)
}

func (s *Servidor) handleListarArtefatosPlanejamento(w http.ResponseWriter, r *http.Request) {
	plan, ok := s.obterPlanejamentoOu404(w, r)
	if !ok {
		return
	}
	artefatos, err := s.banco.ListarArtefatosPlanejamento(r.Context(), plan.ID)
	if err != nil {
		s.responderErroPlanejamento(w, err)
		return
	}
	responderJSON(w, http.StatusOK, artefatos)
}

// handleServirArtefatoPlanejamento serve um artefato .html da pasta do
// planejamento para abrir em nova aba. O HTML é gerado pelo harness, então NUNCA
// sai sem a jaula: `Content-Security-Policy: sandbox` sem allow-same-origin dá
// ao documento uma origem opaca — o JS roda (gráficos, navegação do protótipo),
// mas não enxerga cookies nem consegue chamar a API do Praxis com a sessão do
// usuário. Só arquivos indexados no banco (linha em planejamento_artefatos) e
// com nome validado são servidos — nada de path traversal nem arquivos avulsos.
func (s *Servidor) handleServirArtefatoPlanejamento(w http.ResponseWriter, r *http.Request) {
	plan, ok := s.obterPlanejamentoOu404(w, r)
	if !ok {
		return
	}
	if s.estrategista == nil {
		responderErro(w, http.StatusServiceUnavailable, "indisponivel",
			"o serviço de planejamentos não está ativo neste servidor")
		return
	}
	arquivo := strings.TrimSpace(r.PathValue("arquivo"))
	if !estrategista.NomeArtefatoValido(arquivo) {
		responderErro(w, http.StatusBadRequest, "invalido", "nome de artefato inválido")
		return
	}
	if _, err := s.banco.ObterArtefatoPlanejamento(r.Context(), plan.ID, arquivo); err != nil {
		s.responderErroPlanejamento(w, err)
		return
	}
	conteudo, err := os.ReadFile(filepath.Join(s.estrategista.Pasta(plan.ID), arquivo))
	if err != nil {
		if os.IsNotExist(err) {
			responderErro(w, http.StatusNotFound, "nao_encontrado", "o arquivo do artefato não está mais na pasta do planejamento")
			return
		}
		s.log.Error("ler artefato do planejamento", "erro", err, "planejamento", plan.ID, "arquivo", arquivo)
		responderErro(w, http.StatusInternalServerError, "erro_interno", "erro interno do servidor")
		return
	}

	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Content-Security-Policy", "sandbox allow-scripts allow-popups")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(conteudo)
}

// handleCriarDemandaDePlanejamento é o handoff: cria uma demanda (intake por
// chat) cujo PRD é o documento mais recente do planejamento, e vincula a
// demanda criada ao planejamento (demand_id). Exige também demandas.criar —
// planejar e abrir demanda são capacidades distintas.
func (s *Servidor) handleCriarDemandaDePlanejamento(w http.ResponseWriter, r *http.Request) {
	plan, ok := s.obterPlanejamentoOu404(w, r)
	if !ok {
		return
	}
	if !exigirPermissao(w, r, db.PermDemandasCriar) {
		return
	}
	if plan.Status == db.StatusPlanejamentoPensando {
		responderErro(w, http.StatusConflict, "pensando",
			"o estrategista está trabalhando — crie a demanda após a resposta (o documento pode mudar)")
		return
	}
	if plan.DemandID != nil {
		responderErro(w, http.StatusConflict, "ja_criada",
			fmt.Sprintf("este planejamento já gerou a demanda #%d", *plan.DemandID))
		return
	}
	var req reqDemandaDePlanejamento
	if !decodificarCorpo(w, r, &req) {
		return
	}

	projectID, msg := s.resolverProjetoDoHandoff(r, plan, req.ProjectID)
	if msg != "" {
		responderErro(w, http.StatusBadRequest, "invalido", msg)
		return
	}

	prd, err := s.montarPRDDoPlanejamento(r.Context(), plan)
	if err != nil {
		if errors.Is(err, db.ErrNaoEncontrado) {
			responderErro(w, http.StatusConflict, "sem_documento",
				"o planejamento ainda não tem documento gerado — converse com o estrategista primeiro")
			return
		}
		s.responderErroPlanejamento(w, err)
		return
	}

	titulo := strings.TrimSpace(req.Titulo)
	if titulo == "" {
		titulo = plan.Titulo
	}
	branch, err := gitops.NormalizarBranch(req.Branch)
	if err != nil {
		responderErro(w, http.StatusBadRequest, "invalido", msgBranchInvalida)
		return
	}

	dem := db.Demanda{
		ProjectID: projectID,
		Titulo:    titulo,
		Origem:    db.OrigemUI,
		OrigemRef: fmt.Sprintf("planejamento #%d", plan.ID),
		Status:    db.StatusDemandaRecebida,
		Branch:    branch,
		CriadoPor: usuarioDaRequisicao(r),
	}
	criada, _, err := s.banco.CriarDemandaComChat(r.Context(), dem, db.MensagemChat{
		Papel:    db.PapelUser,
		Conteudo: prd,
	})
	if err != nil {
		s.responderErroDemanda(w, err)
		return
	}

	did := criada.ID
	plan.DemandID = &did
	if _, err := s.banco.AtualizarPlanejamento(r.Context(), plan); err != nil {
		s.log.Warn("vincular demanda ao planejamento", "erro", err, "planejamento", plan.ID, "demanda", did)
	}

	pid := criada.ProjectID
	if _, err := s.banco.RegistrarEvento(r.Context(), db.Evento{
		ProjectID: &pid, DemandID: &did,
		Tipo:    "demanda_criada",
		Titulo:  "Praxis: demanda criada",
		Detalhe: fmt.Sprintf("Demanda criada a partir do planejamento #%d (%s).", plan.ID, plan.Titulo),
	}); err != nil {
		s.log.Warn("registrar evento de criação da demanda", "erro", err, "demanda", did)
	}

	if s.intake != nil {
		s.intake.Disparar(criada.ID)
	}
	responderJSON(w, http.StatusCreated, respDemanda{Demanda: criada, Fases: []db.Fase{}})
}

// resolverProjetoDoHandoff decide o projeto que recebe a demanda: planejamento
// de projeto usa o próprio; planejamento de grupo exige project_id no corpo e
// valida que ele é membro do grupo. Devolve mensagem de erro legível quando a
// escolha é inválida.
func (s *Servidor) resolverProjetoDoHandoff(r *http.Request, plan db.Planejamento, pedido int64) (int64, string) {
	if plan.ProjectID != nil {
		return *plan.ProjectID, ""
	}
	if pedido <= 0 {
		return 0, "planejamento de grupo: informe project_id (o repositório que recebe a demanda)"
	}
	grupo, err := s.banco.ObterGrupo(r.Context(), *plan.GroupID)
	if err != nil {
		return 0, "não consegui resolver o grupo do planejamento"
	}
	for _, m := range grupo.Membros {
		if m.ProjectID == pedido {
			return pedido, ""
		}
	}
	return 0, "project_id não é um repositório do grupo deste planejamento"
}

// montarPRDDoPlanejamento compõe o texto do PRD do handoff a partir das
// revisões mais recentes: prd.md e, quando o foco inclui ADRs, adrs.md anexado
// como seção de decisões arquiteturais. Sem documento nenhum → ErrNaoEncontrado.
func (s *Servidor) montarPRDDoPlanejamento(ctx context.Context, plan db.Planejamento) (string, error) {
	var partes []string
	if plan.Foco != db.FocoPlanejamentoADR {
		if doc, err := s.banco.ObterDocumentoPlanejamento(ctx, plan.ID, "prd.md", 0); err == nil {
			partes = append(partes, strings.TrimSpace(doc.Conteudo))
		} else if !errors.Is(err, db.ErrNaoEncontrado) {
			return "", err
		}
	}
	if plan.Foco != db.FocoPlanejamentoPRD {
		if doc, err := s.banco.ObterDocumentoPlanejamento(ctx, plan.ID, "adrs.md", 0); err == nil {
			adrs := strings.TrimSpace(doc.Conteudo)
			if len(partes) > 0 {
				adrs = "---\n\n# Decisões arquiteturais (ADRs)\n\n" + adrs
			}
			partes = append(partes, adrs)
		} else if !errors.Is(err, db.ErrNaoEncontrado) {
			return "", err
		}
	}
	if len(partes) == 0 {
		return "", db.ErrNaoEncontrado
	}
	return strings.Join(partes, "\n\n"), nil
}

// obterPlanejamentoOu404 resolve o path param {id} para o planejamento ou
// responde 400/404.
func (s *Servidor) obterPlanejamentoOu404(w http.ResponseWriter, r *http.Request) (db.Planejamento, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		responderErro(w, http.StatusBadRequest, "invalido", "id inválido")
		return db.Planejamento{}, false
	}
	plan, err := s.banco.ObterPlanejamento(r.Context(), id)
	if err != nil {
		s.responderErroPlanejamento(w, err)
		return db.Planejamento{}, false
	}
	return plan, true
}

// responderErroPlanejamento traduz os erros do store de planejamentos para
// respostas HTTP.
func (s *Servidor) responderErroPlanejamento(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, db.ErrNaoEncontrado):
		responderErro(w, http.StatusNotFound, "nao_encontrado", "planejamento, documento, projeto ou grupo não encontrado")
	case errors.Is(err, db.ErrPapelInvalido):
		responderErro(w, http.StatusBadRequest, "invalido", "papel de mensagem inválido")
	case errors.Is(err, db.ErrValorInvalido):
		responderErro(w, http.StatusBadRequest, "invalido", "valor inválido para foco ou nível visual")
	default:
		s.log.Error("erro no store de planejamentos", "erro", err)
		responderErro(w, http.StatusInternalServerError, "erro_interno", "erro interno do servidor")
	}
}
