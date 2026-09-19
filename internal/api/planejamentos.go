package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/marcos14/praxis-autonomous/internal/i18n"
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
// inicial do usuário. AnexosPendentes=true segura o primeiro turno: o
// planejamento nasce ocioso para o cliente subir as referências e então
// disparar via POST /planejamentos/{id}/turno — sem isso o estrategista rodaria
// antes de os anexos chegarem.
type reqNovoPlanejamento struct {
	ProjectID       int64  `json:"project_id"`
	GroupID         int64  `json:"group_id"`
	Titulo          string `json:"titulo"`
	Foco            string `json:"foco"`
	NivelVisual     string `json:"nivel_visual"`
	Mensagem        string `json:"mensagem"`
	AnexosPendentes bool   `json:"anexos_pendentes"`
	// Visibilidade: privada (default) | grupo | publica — quem enxerga o planejamento.
	Visibilidade string `json:"visibilidade"`
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
	mux.HandleFunc("PUT /api/v1/planejamentos/{id}/visibilidade", s.handleDefinirVisibilidadePlanejamento)
	mux.HandleFunc("GET /api/v1/planejamentos/{id}/chat", s.handleListarChatPlanejamento)
	mux.HandleFunc("POST /api/v1/planejamentos/{id}/chat", s.handleChatPlanejamento)
	mux.HandleFunc("POST /api/v1/planejamentos/{id}/turno", s.handleDispararTurnoPlanejamento)
	mux.HandleFunc("GET /api/v1/planejamentos/{id}/progresso", s.handleProgressoPlanejamento)
	mux.HandleFunc("GET /api/v1/planejamentos/{id}/documentos", s.handleListarDocumentosPlanejamento)
	mux.HandleFunc("GET /api/v1/planejamentos/{id}/documentos/{arquivo}", s.handleObterDocumentoPlanejamento)
	mux.HandleFunc("GET /api/v1/planejamentos/{id}/artefatos", s.handleListarArtefatosPlanejamento)
	mux.HandleFunc("GET /api/v1/planejamentos/{id}/artefatos/{arquivo}", s.handleServirArtefatoPlanejamento)
	mux.HandleFunc("GET /api/v1/planejamentos/{id}/referencias", s.handleListarReferenciasPlanejamento)
	mux.HandleFunc("POST /api/v1/planejamentos/{id}/referencias", s.handleEnviarReferenciaPlanejamento)
	mux.HandleFunc("GET /api/v1/planejamentos/{id}/referencias/{arquivo}", s.handleBaixarReferenciaPlanejamento)
	mux.HandleFunc("DELETE /api/v1/planejamentos/{id}/referencias/{arquivo}", s.handleExcluirReferenciaPlanejamento)
	mux.HandleFunc("GET /api/v1/planejamentos/{id}/demandas", s.handleListarDemandasDoPlanejamento)
	mux.HandleFunc("POST /api/v1/planejamentos/{id}/criar-demanda", s.handleCriarDemandaDePlanejamento)
}

// respDemandasDoPlanejamento é a resposta de GET /planejamentos/{id}/demandas:
// os vínculos (cada demanda gerada, com a revisão entregue) e a revisão ATUAL de
// cada documento — a UI compara os dois para mostrar o drift ("há mudanças não
// entregues desde a demanda #N").
type respDemandasDoPlanejamento struct {
	Demandas     []db.VinculoPlanejamentoDemanda `json:"demandas"`
	PRDRevAtual  int64                           `json:"prd_rev_atual"`
	ADRsRevAtual int64                           `json:"adrs_rev_atual"`
}

// handleListarDemandasDoPlanejamento lista as demandas geradas pelo planejamento.
func (s *Servidor) handleListarDemandasDoPlanejamento(w http.ResponseWriter, r *http.Request) {
	plan, ok := s.obterPlanejamentoOu404(w, r)
	if !ok {
		return
	}
	vinculos, err := s.banco.ListarDemandasDoPlanejamento(r.Context(), plan.ID)
	if err != nil {
		s.responderErroPlanejamento(w, r, err)
		return
	}
	// Uma demanda gerada pode ter visibilidade mais restrita que o planejamento
	// (o dono pode ter mudado): só lista as que este usuário enxerga.
	if v := s.visaoDaRequisicao(r); v.ACL != nil || v.Dono != nil {
		visiveis := vinculos[:0]
		for _, vinc := range vinculos {
			ve, err := s.banco.DemandaVisivel(r.Context(), vinc.DemandID, v)
			if err != nil {
				s.responderErroPlanejamento(w, r, err)
				return
			}
			if ve {
				visiveis = append(visiveis, vinc)
			}
		}
		vinculos = visiveis
	}
	resp := respDemandasDoPlanejamento{Demandas: vinculos}
	if doc, err := s.banco.ObterDocumentoPlanejamento(r.Context(), plan.ID, "prd.md", 0); err == nil {
		resp.PRDRevAtual = doc.Revisao
	}
	if doc, err := s.banco.ObterDocumentoPlanejamento(r.Context(), plan.ID, "adrs.md", 0); err == nil {
		resp.ADRsRevAtual = doc.Revisao
	}
	responderJSON(w, http.StatusOK, resp)
}

// handleCriarPlanejamento cria o planejamento com a primeira fala do usuário e
// dispara o primeiro turno do estrategista em background. Devolve 201.
func (s *Servidor) handleCriarPlanejamento(w http.ResponseWriter, r *http.Request) {
	var req reqNovoPlanejamento
	if !decodificarCorpo(w, r, &req) {
		return
	}
	if (req.ProjectID > 0) == (req.GroupID > 0) {
		erroT(w, r, http.StatusBadRequest, "invalido", "erro.consulta_alvo_exclusivo")
		return
	}
	mensagem := strings.TrimSpace(req.Mensagem)
	if mensagem == "" {
		erroT(w, r, http.StatusBadRequest, "invalido", "erro.consulta_mensagem_obrigatoria")
		return
	}
	foco := strings.TrimSpace(req.Foco)
	if foco != "" && !db.FocoPlanejamentoValido(foco) {
		erroT(w, r, http.StatusBadRequest, "invalido", "erro.planejamento_foco_invalido")
		return
	}
	nivel := strings.TrimSpace(req.NivelVisual)
	if nivel != "" && !db.NivelVisualValido(nivel) {
		erroT(w, r, http.StatusBadRequest, "invalido", "erro.planejamento_nivel_visual_invalido")
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
			s.responderErroPlanejamento(w, r, err)
			return
		}
		if !ve {
			erroT(w, r, http.StatusNotFound, "nao_encontrado", "erro.consulta_projeto_ou_grupo_nao_encontrado")
			return
		}
	}

	status := db.StatusPlanejamentoPensando
	if req.AnexosPendentes {
		// Nasce ocioso: o cliente ainda vai subir as referências e disparar o
		// primeiro turno explicitamente (POST /turno).
		status = db.StatusPlanejamentoOcioso
	}
	visibilidade, okVis := visibilidadeDoCorpo(w, r, req.Visibilidade)
	if !okVis {
		return
	}
	plan := db.Planejamento{
		Titulo: strings.TrimSpace(req.Titulo), Foco: foco, NivelVisual: nivel,
		Status: status, Visibilidade: visibilidade,
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
		db.MensagemPlanejamento{Papel: db.PapelPlanejamentoUser, Conteudo: mensagem,
			Meta: metaAutorDaRequisicao(r)})
	if err != nil {
		s.responderErroPlanejamento(w, r, err)
		return
	}
	if !req.AnexosPendentes && s.estrategista != nil {
		s.estrategista.DispararResposta(criado.ID)
	}
	responderJSON(w, http.StatusCreated, criado)
}

// handleDispararTurnoPlanejamento roda um turno do estrategista SEM fala nova:
// é o disparo adiado da criação com anexos pendentes e o "tentar novamente"
// após um turno falhado (o motor é stateless — o turno reprocessa a conversa e
// as referências atuais). Com turno em voo responde 409.
func (s *Servidor) handleDispararTurnoPlanejamento(w http.ResponseWriter, r *http.Request) {
	plan, ok := s.obterPlanejamentoOu404(w, r)
	if !ok {
		return
	}
	if plan.Status == db.StatusPlanejamentoPensando {
		erroT(w, r, http.StatusConflict, "pensando", "erro.planejamento_pensando")
		return
	}
	plan.Status = db.StatusPlanejamentoPensando
	plan.Erro = ""
	atualizado, err := s.banco.AtualizarPlanejamento(r.Context(), plan)
	if err != nil {
		s.responderErroPlanejamento(w, r, err)
		return
	}
	if s.estrategista != nil {
		s.estrategista.DispararResposta(plan.ID)
	}
	responderJSON(w, http.StatusAccepted, atualizado)
}

// handleListarPlanejamentos lista os planejamentos (filtros ?project= e
// ?group=). Para usuários restritos pela ACL, só os de projetos/grupos visíveis.
func (s *Servidor) handleListarPlanejamentos(w http.ResponseWriter, r *http.Request) {
	projectID, _ := strconv.ParseInt(r.URL.Query().Get("project"), 10, 64)
	groupID, _ := strconv.ParseInt(r.URL.Query().Get("group"), 10, 64)
	// A ACL de projeto/grupo, a regra de dono e o escopo (?escopo=) são
	// aplicados no SQL pela Visao (M2).
	visao, ok := s.visaoComEscopo(w, r)
	if !ok {
		return
	}
	planejamentos, err := s.banco.ListarPlanejamentos(r.Context(),
		db.FiltroPlanejamentos{ProjectID: projectID, GroupID: groupID, Visao: visao})
	if err != nil {
		s.responderErroPlanejamento(w, r, err)
		return
	}
	responderJSON(w, http.StatusOK, planejamentos)
}

// handleDefinirVisibilidadePlanejamento muda quem enxerga o planejamento
// (privada | grupo | publica). Só o criador ou um admin; sem criador, só o admin.
func (s *Servidor) handleDefinirVisibilidadePlanejamento(w http.ResponseWriter, r *http.Request) {
	plan, ok := s.obterPlanejamentoOu404(w, r)
	if !ok {
		return
	}
	if !podeAlterarVisibilidade(w, r, plan.CriadoPor) {
		return
	}
	vis, ok := lerVisibilidadeDoPut(w, r)
	if !ok {
		return
	}
	if err := s.banco.DefinirVisibilidadePlanejamento(r.Context(), plan.ID, vis); err != nil {
		s.responderErroPlanejamento(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
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
		erroT(w, r, http.StatusConflict, "pensando", "erro.planejamento_pensando_editar")
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
			erroT(w, r, http.StatusBadRequest, "invalido", "erro.planejamento_foco_invalido")
			return
		}
		plan.Foco = f
	}
	if n := strings.TrimSpace(req.NivelVisual); n != "" {
		if !db.NivelVisualValido(n) {
			erroT(w, r, http.StatusBadRequest, "invalido", "erro.planejamento_nivel_visual_invalido")
			return
		}
		plan.NivelVisual = n
	}
	atualizado, err := s.banco.AtualizarPlanejamento(r.Context(), plan)
	if err != nil {
		s.responderErroPlanejamento(w, r, err)
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
		erroT(w, r, http.StatusForbidden, "sem_permissao", "erro.planejamento_excluir_somente_criador")
		return
	}
	if err := s.banco.ExcluirPlanejamento(r.Context(), plan.ID); err != nil {
		s.responderErroPlanejamento(w, r, err)
		return
	}
	// A pasta de trabalho sai junto (best-effort — o banco é a fonte da verdade
	// dos documentos; artefatos órfãos não são mais servíveis sem a linha do índice).
	if s.estrategista != nil {
		if pasta := s.estrategista.Pasta(plan.ID); pasta != "" {
			if err := os.RemoveAll(pasta); err != nil {
				s.log.Warn("remover pasta do planejamento", "erro", err, i18n.TI("evento.etapa_planejamento"), plan.ID)
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
		s.responderErroPlanejamento(w, r, err)
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
		erroT(w, r, http.StatusConflict, "pensando", "erro.planejamento_pensando_chat")
		return
	}
	var req reqChatPlanejamento
	if !decodificarCorpo(w, r, &req) {
		return
	}
	conteudo := strings.TrimSpace(req.Conteudo)
	if conteudo == "" {
		erroT(w, r, http.StatusBadRequest, "invalido", "erro.chat_conteudo_obrigatorio")
		return
	}

	msg, err := s.banco.CriarMensagemPlanejamento(r.Context(), db.MensagemPlanejamento{
		PlanejamentoID: plan.ID, Papel: db.PapelPlanejamentoUser, Conteudo: conteudo,
		Meta: metaAutorDaRequisicao(r),
	})
	if err != nil {
		s.responderErroPlanejamento(w, r, err)
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
		erroT(w, r, http.StatusInternalServerError, "sem_streaming", "erro.sem_streaming")
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

	// O stream vive no máximo até o JWT vencer (contextoDoStream).
	ctx, cancelar := contextoDoStream(r)
	defer cancelar()

	var (
		runAtual int64
		caminho  string
		offset   int64
	)
	for {
		if run, temLog, err := s.banco.UltimaExecucaoPlanejamentoComLog(ctx, plan.ID); err == nil && temLog && run.ID != runAtual {
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
		case <-ctx.Done():
			avisarTokenExpirado(ctx, r, w, flusher)
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
		s.responderErroPlanejamento(w, r, err)
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
		s.responderErroPlanejamento(w, r, err)
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
		s.responderErroPlanejamento(w, r, err)
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
		erroT(w, r, http.StatusServiceUnavailable, "indisponivel", "erro.planejamento_indisponivel")
		return
	}
	arquivo := strings.TrimSpace(r.PathValue("arquivo"))
	if !estrategista.NomeArtefatoValido(arquivo) {
		erroT(w, r, http.StatusBadRequest, "invalido", "erro.planejamento_artefato_invalido")
		return
	}
	if _, err := s.banco.ObterArtefatoPlanejamento(r.Context(), plan.ID, arquivo); err != nil {
		s.responderErroPlanejamento(w, r, err)
		return
	}
	conteudo, err := os.ReadFile(filepath.Join(s.estrategista.Pasta(plan.ID), arquivo))
	if err != nil {
		if os.IsNotExist(err) {
			erroT(w, r, http.StatusNotFound, "nao_encontrado", "erro.planejamento_artefato_sumiu")
			return
		}
		s.log.Error("ler artefato do planejamento", "erro", err, i18n.TI("evento.etapa_planejamento"), plan.ID, "arquivo", arquivo)
		erroT(w, r, http.StatusInternalServerError, "erro_interno", "erro.interno")
		return
	}

	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Content-Security-Policy", "sandbox allow-scripts allow-popups")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Cache-Control", "no-cache")
	// ?download=1: baixar em vez de exibir (o arquivo é autocontido — funciona
	// aberto do disco do usuário, para anexar/compartilhar).
	if r.URL.Query().Get("download") == "1" {
		h.Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", arquivo))
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(conteudo)
}

// dirReferencias resolve a subpasta de referências do planejamento. Devolve ""
// quando o serviço de planejamentos não está ativo.
func (s *Servidor) dirReferencias(planejamentoID int64) string {
	if s.estrategista == nil {
		return ""
	}
	return filepath.Join(s.estrategista.Pasta(planejamentoID), estrategista.DirReferencias)
}

// handleListarReferenciasPlanejamento lista os arquivos de referência anexados.
func (s *Servidor) handleListarReferenciasPlanejamento(w http.ResponseWriter, r *http.Request) {
	plan, ok := s.obterPlanejamentoOu404(w, r)
	if !ok {
		return
	}
	dir := s.dirReferencias(plan.ID)
	if dir == "" {
		erroT(w, r, http.StatusServiceUnavailable, "indisponivel", "erro.planejamento_indisponivel")
		return
	}
	s.listarReferenciasDir(w, dir)
}

// handleEnviarReferenciaPlanejamento recebe um arquivo de referência via
// multipart/form-data (campo "arquivo") e o grava em referencias/. O anexo vira
// fala de sistema no chat — o próximo turno do estrategista o vê listado.
func (s *Servidor) handleEnviarReferenciaPlanejamento(w http.ResponseWriter, r *http.Request) {
	plan, ok := s.obterPlanejamentoOu404(w, r)
	if !ok {
		return
	}
	dir := s.dirReferencias(plan.ID)
	if dir == "" {
		erroT(w, r, http.StatusServiceUnavailable, "indisponivel", "erro.planejamento_indisponivel")
		return
	}
	ref, ok := s.receberReferencia(w, r, dir)
	if !ok {
		return
	}
	s.registrarFalaReferencia(r, plan.ID,
		fmt.Sprintf("Referência anexada: %s/%s", estrategista.DirReferencias, ref.Arquivo))
	responderJSON(w, http.StatusCreated, ref)
}

// handleBaixarReferenciaPlanejamento devolve o arquivo de referência SEMPRE
// como download (attachment): referência é insumo do usuário, nunca página a
// exibir no domínio do Praxis.
func (s *Servidor) handleBaixarReferenciaPlanejamento(w http.ResponseWriter, r *http.Request) {
	plan, ok := s.obterPlanejamentoOu404(w, r)
	if !ok {
		return
	}
	s.baixarReferencia(w, r, s.dirReferencias(plan.ID), strings.TrimSpace(r.PathValue("arquivo")))
}

// handleExcluirReferenciaPlanejamento remove um arquivo de referência.
func (s *Servidor) handleExcluirReferenciaPlanejamento(w http.ResponseWriter, r *http.Request) {
	plan, ok := s.obterPlanejamentoOu404(w, r)
	if !ok {
		return
	}
	arquivo := strings.TrimSpace(r.PathValue("arquivo"))
	if !s.excluirReferencia(w, r, s.dirReferencias(plan.ID), arquivo) {
		return
	}
	s.registrarFalaReferencia(r, plan.ID,
		fmt.Sprintf("Referência removida: %s/%s", estrategista.DirReferencias, arquivo))
	w.WriteHeader(http.StatusNoContent)
}

// registrarFalaReferencia grava a fala de sistema de anexo/remoção (best-effort),
// com o autor quando a sessão o identifica — é assim que o estrategista e os
// demais participantes ficam sabendo da mudança nas referências.
func (s *Servidor) registrarFalaReferencia(r *http.Request, planejamentoID int64, texto string) {
	if pr := principalDaRequisicao(r); strings.TrimSpace(pr.nome) != "" {
		texto += " (por " + strings.TrimSpace(pr.nome) + ")"
	}
	if _, err := s.banco.CriarMensagemPlanejamento(r.Context(), db.MensagemPlanejamento{
		PlanejamentoID: planejamentoID, Papel: db.PapelPlanejamentoSistema, Conteudo: texto,
	}); err != nil {
		s.log.Warn("registrar fala de referência", "erro", err, i18n.TI("evento.etapa_planejamento"), planejamentoID)
	}
}

// metaAutorDaRequisicao devolve o meta {"autor":"<nome>"} para as falas do
// usuário — o chat de um planejamento é colaborativo (PO e arquiteto na mesma
// conversa), então cada fala carrega quem falou. Nil no modo bootstrap/token
// (sem usuário identificado).
func metaAutorDaRequisicao(r *http.Request) json.RawMessage {
	nome := strings.TrimSpace(principalDaRequisicao(r).nome)
	if nome == "" {
		return nil
	}
	b, err := json.Marshal(map[string]string{"autor": nome})
	if err != nil {
		return nil
	}
	return b
}

// handleCriarDemandaDePlanejamento é o handoff: cria uma demanda (intake por
// chat) cujo PRD é o documento mais recente do planejamento, registrando em
// planejamento_demandas QUAL revisão foi entregue — um planejamento pode gerar
// várias demandas (refazer, variante A/B; o aviso de demanda anterior ativa é
// da UI, e a colisão de arquivos é coberta pela detecção de sobreposição).
// Exige também demandas.criar — planejar e abrir demanda são capacidades
// distintas.
func (s *Servidor) handleCriarDemandaDePlanejamento(w http.ResponseWriter, r *http.Request) {
	plan, ok := s.obterPlanejamentoOu404(w, r)
	if !ok {
		return
	}
	if !exigirPermissao(w, r, db.PermDemandasCriar) {
		return
	}
	if plan.Status == db.StatusPlanejamentoPensando {
		erroT(w, r, http.StatusConflict, "pensando", "erro.planejamento_pensando_demanda")
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

	prd, prdRev, adrsRev, err := s.montarPRDDoPlanejamento(r.Context(), plan)
	if err != nil {
		if errors.Is(err, db.ErrNaoEncontrado) {
			erroT(w, r, http.StatusConflict, "sem_documento", "erro.planejamento_sem_documento")
			return
		}
		s.responderErroPlanejamento(w, r, err)
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
		// A demanda herda quem enxerga o planejamento que a gerou.
		Visibilidade: plan.Visibilidade,
		ProjectID:    projectID,
		Titulo:       titulo,
		Origem:       db.OrigemUI,
		OrigemRef:    fmt.Sprintf("planejamento #%d", plan.ID),
		Status:       db.StatusDemandaRecebida,
		Branch:       branch,
		CriadoPor:    usuarioDaRequisicao(r),
	}
	criada, _, err := s.banco.CriarDemandaComChat(r.Context(), dem, db.MensagemChat{
		Papel:    db.PapelUser,
		Conteudo: prd,
	})
	if err != nil {
		s.responderErroDemanda(w, r, err)
		return
	}

	did := criada.ID
	if _, err := s.banco.CriarVinculoPlanejamentoDemanda(r.Context(), db.VinculoPlanejamentoDemanda{
		PlanejamentoID: plan.ID, DemandID: did,
		Tipo: db.TipoDemandaPlanejamentoCompleta, PRDRev: prdRev, ADRsRev: adrsRev,
	}); err != nil {
		s.log.Warn("vincular demanda ao planejamento", "erro", err, i18n.TI("evento.etapa_planejamento"), plan.ID, "demanda", did)
	}

	pid := criada.ProjectID
	if _, err := s.banco.RegistrarEvento(r.Context(), db.Evento{
		ProjectID: &pid, DemandID: &did,
		Tipo:    "demanda_criada",
		Titulo:  i18n.TI("evento.demanda_criada"),
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
// como seção de decisões arquiteturais. Devolve também a revisão de cada
// documento usada (0 = documento não entrou) — é o que o vínculo registra para
// a detecção de drift. Sem documento nenhum → ErrNaoEncontrado.
func (s *Servidor) montarPRDDoPlanejamento(ctx context.Context, plan db.Planejamento) (string, int64, int64, error) {
	var (
		partes          []string
		prdRev, adrsRev int64
	)
	if plan.Foco != db.FocoPlanejamentoADR {
		if doc, err := s.banco.ObterDocumentoPlanejamento(ctx, plan.ID, "prd.md", 0); err == nil {
			partes = append(partes, strings.TrimSpace(doc.Conteudo))
			prdRev = doc.Revisao
		} else if !errors.Is(err, db.ErrNaoEncontrado) {
			return "", 0, 0, err
		}
	}
	if plan.Foco != db.FocoPlanejamentoPRD {
		if doc, err := s.banco.ObterDocumentoPlanejamento(ctx, plan.ID, "adrs.md", 0); err == nil {
			adrs := strings.TrimSpace(doc.Conteudo)
			if len(partes) > 0 {
				adrs = "---\n\n# Decisões arquiteturais (ADRs)\n\n" + adrs
			}
			partes = append(partes, adrs)
			adrsRev = doc.Revisao
		} else if !errors.Is(err, db.ErrNaoEncontrado) {
			return "", 0, 0, err
		}
	}
	if len(partes) == 0 {
		return "", 0, 0, db.ErrNaoEncontrado
	}
	return strings.Join(partes, "\n\n"), prdRev, adrsRev, nil
}

// obterPlanejamentoOu404 resolve o path param {id} para o planejamento ou
// responde 400/404.
func (s *Servidor) obterPlanejamentoOu404(w http.ResponseWriter, r *http.Request) (db.Planejamento, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		erroT(w, r, http.StatusBadRequest, "invalido", "erro.id_invalido")
		return db.Planejamento{}, false
	}
	plan, err := s.banco.ObterPlanejamento(r.Context(), id)
	if err != nil {
		s.responderErroPlanejamento(w, r, err)
		return db.Planejamento{}, false
	}
	return plan, true
}

// responderErroPlanejamento traduz os erros do store de planejamentos para
// respostas HTTP.
func (s *Servidor) responderErroPlanejamento(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, db.ErrNaoEncontrado):
		erroT(w, r, http.StatusNotFound, "nao_encontrado", "erro.planejamento_nao_encontrado")
	case errors.Is(err, db.ErrPapelInvalido):
		erroT(w, r, http.StatusBadRequest, "invalido", "erro.chat_papel_invalido")
	case errors.Is(err, db.ErrValorInvalido):
		erroT(w, r, http.StatusBadRequest, "invalido", "erro.planejamento_valor_invalido")
	default:
		s.log.Error("erro no store de planejamentos", "erro", err)
		erroT(w, r, http.StatusInternalServerError, "erro_interno", "erro.interno")
	}
}
