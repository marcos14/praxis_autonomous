package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/marcos14/praxis-autonomous/internal/db"
	"github.com/marcos14/praxis-autonomous/internal/referencias"
)

// reqNovaConsulta é o corpo de POST /consultas: o alvo (projeto OU grupo,
// exclusivo), um título opcional e a primeira pergunta do usuário.
// AnexosPendentes=true segura o primeiro turno: a consulta nasce ociosa para o
// usuário subir os arquivos e então disparar via POST /consultas/{id}/turno —
// sem isso o consultor rodaria antes de os anexos chegarem.
type reqNovaConsulta struct {
	ProjectID       int64  `json:"project_id"`
	GroupID         int64  `json:"group_id"`
	Titulo          string `json:"titulo"`
	Mensagem        string `json:"mensagem"`
	AnexosPendentes bool   `json:"anexos_pendentes"`
}

// reqChatConsulta é o corpo de POST /consultas/{id}/chat: uma fala do usuário.
// O papel é sempre user — falas do consultor/sistema são geradas pelo backend.
type reqChatConsulta struct {
	Conteudo string `json:"conteudo"`
}

// registrarRotasConsultas registra as rotas da feature de consultas (chat de
// análise de código para produto/suporte). Leituras exigem autenticação;
// mutações exigem consultas.usar (ver permissaoMutacao).
func (s *Servidor) registrarRotasConsultas(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/consultas", s.handleCriarConsulta)
	mux.HandleFunc("GET /api/v1/consultas", s.handleListarConsultas)
	mux.HandleFunc("GET /api/v1/consultas/{id}", s.handleObterConsulta)
	mux.HandleFunc("DELETE /api/v1/consultas/{id}", s.handleExcluirConsulta)
	mux.HandleFunc("GET /api/v1/consultas/{id}/chat", s.handleListarChatConsulta)
	mux.HandleFunc("POST /api/v1/consultas/{id}/chat", s.handleChatConsulta)
	mux.HandleFunc("POST /api/v1/consultas/{id}/turno", s.handleDispararTurnoConsulta)
	mux.HandleFunc("GET /api/v1/consultas/{id}/progresso", s.handleProgressoConsulta)
	mux.HandleFunc("GET /api/v1/consultas/{id}/referencias", s.handleListarReferenciasConsulta)
	mux.HandleFunc("POST /api/v1/consultas/{id}/referencias", s.handleEnviarReferenciaConsulta)
	mux.HandleFunc("GET /api/v1/consultas/{id}/referencias/{arquivo}", s.handleBaixarReferenciaConsulta)
	mux.HandleFunc("DELETE /api/v1/consultas/{id}/referencias/{arquivo}", s.handleExcluirReferenciaConsulta)
}

// handleCriarConsulta cria a consulta com a primeira fala do usuário e dispara
// o primeiro turno do consultor em background. Devolve 201 com a consulta.
func (s *Servidor) handleCriarConsulta(w http.ResponseWriter, r *http.Request) {
	var req reqNovaConsulta
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
	// ACL de projetos: um usuário restrito não abre consulta sobre projeto (ou
	// grupo de repositórios) que a ACL esconde dele. 404 — sem revelar existência.
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
			s.responderErroConsulta(w, r, err)
			return
		}
		if !ve {
			erroT(w, r, http.StatusNotFound, "nao_encontrado", "erro.consulta_projeto_ou_grupo_nao_encontrado")
			return
		}
	}

	status := db.StatusConsultaPensando
	if req.AnexosPendentes {
		// Nasce ociosa: o usuário ainda vai subir os arquivos e disparar o
		// primeiro turno explicitamente (POST /turno).
		status = db.StatusConsultaOciosa
	}
	cons := db.Consulta{Titulo: strings.TrimSpace(req.Titulo), Status: status}
	if req.ProjectID > 0 {
		cons.ProjectID = &req.ProjectID
	} else {
		cons.GroupID = &req.GroupID
	}
	if cons.Titulo == "" {
		cons.Titulo = tituloDaMensagem(mensagem)
	}
	if pr := principalDaRequisicao(r); pr.userID > 0 {
		uid := pr.userID
		cons.CriadoPor = &uid
	}

	criada, _, err := s.banco.CriarConsultaComChat(r.Context(), cons,
		db.MensagemConsulta{Papel: db.PapelConsultaUser, Conteudo: mensagem})
	if err != nil {
		s.responderErroConsulta(w, r, err)
		return
	}
	if !req.AnexosPendentes && s.consultor != nil {
		s.consultor.DispararResposta(criada.ID)
	}
	responderJSON(w, http.StatusCreated, criada)
}

// handleDispararTurnoConsulta roda um turno do consultor SEM fala nova: é o
// disparo adiado da criação com anexos pendentes e o "tentar novamente" após um
// turno falhado (o motor é stateless — o turno reprocessa a conversa e as
// referências atuais). Com turno em voo responde 409.
func (s *Servidor) handleDispararTurnoConsulta(w http.ResponseWriter, r *http.Request) {
	cons, ok := s.obterConsultaOu404(w, r)
	if !ok {
		return
	}
	if cons.Status == db.StatusConsultaPensando {
		erroT(w, r, http.StatusConflict, "pensando", "erro.consulta_pensando")
		return
	}
	cons.Status = db.StatusConsultaPensando
	cons.Erro = ""
	atualizada, err := s.banco.AtualizarConsulta(r.Context(), cons)
	if err != nil {
		s.responderErroConsulta(w, r, err)
		return
	}
	if s.consultor != nil {
		s.consultor.DispararResposta(cons.ID)
	}
	responderJSON(w, http.StatusAccepted, atualizada)
}

// handleListarConsultas lista as consultas (filtros ?project= e ?group=). Para
// usuários restritos pela ACL, só as consultas de projetos/grupos visíveis.
func (s *Servidor) handleListarConsultas(w http.ResponseWriter, r *http.Request) {
	projectID, _ := strconv.ParseInt(r.URL.Query().Get("project"), 10, 64)
	groupID, _ := strconv.ParseInt(r.URL.Query().Get("group"), 10, 64)
	consultas, err := s.banco.ListarConsultas(r.Context(), projectID, groupID)
	if err != nil {
		s.responderErroConsulta(w, r, err)
		return
	}
	if uid := visibilidadeDaRequisicao(r); uid != nil {
		if consultas, err = s.filtrarConsultasVisiveis(r.Context(), *uid, consultas); err != nil {
			s.responderErroConsulta(w, r, err)
			return
		}
	}
	responderJSON(w, http.StatusOK, consultas)
}

// filtrarConsultasVisiveis descarta as consultas de projetos/grupos que a ACL
// esconde do usuário, memoizando a decisão por alvo (as consultas se repetem em
// poucos projetos/grupos).
func (s *Servidor) filtrarConsultasVisiveis(ctx context.Context, userID int64, consultas []db.Consulta) ([]db.Consulta, error) {
	memoProj := map[int64]bool{}
	memoGrupo := map[int64]bool{}
	visiveis := []db.Consulta{}
	for _, c := range consultas {
		ve := true
		switch {
		case c.ProjectID != nil:
			v, ok := memoProj[*c.ProjectID]
			if !ok {
				var err error
				if v, err = s.banco.UsuarioVeProjeto(ctx, userID, *c.ProjectID); err != nil {
					return nil, err
				}
				memoProj[*c.ProjectID] = v
			}
			ve = v
		case c.GroupID != nil:
			v, ok := memoGrupo[*c.GroupID]
			if !ok {
				var err error
				if v, err = s.banco.UsuarioVeGrupoProjetos(ctx, userID, *c.GroupID); err != nil {
					return nil, err
				}
				memoGrupo[*c.GroupID] = v
			}
			ve = v
		}
		if ve {
			visiveis = append(visiveis, c)
		}
	}
	return visiveis, nil
}

func (s *Servidor) handleObterConsulta(w http.ResponseWriter, r *http.Request) {
	cons, ok := s.obterConsultaOu404(w, r)
	if !ok {
		return
	}
	responderJSON(w, http.StatusOK, cons)
}

// handleExcluirConsulta remove a consulta. Só o criador (ou um admin/curinga)
// pode excluir; consultas sem criador registrado seguem a regra do admin.
func (s *Servidor) handleExcluirConsulta(w http.ResponseWriter, r *http.Request) {
	cons, ok := s.obterConsultaOu404(w, r)
	if !ok {
		return
	}
	pr := principalDaRequisicao(r)
	dono := cons.CriadoPor != nil && pr.userID == *cons.CriadoPor
	if !dono && !pr.tem(db.PermCuringa) {
		erroT(w, r, http.StatusForbidden, "sem_permissao", "erro.consulta_excluir_sem_permissao")
		return
	}
	if err := s.banco.ExcluirConsulta(r.Context(), cons.ID); err != nil {
		s.responderErroConsulta(w, r, err)
		return
	}
	// A pasta de trabalho (arquivos anexados) sai junto — best-effort: o banco é
	// a fonte da verdade da consulta.
	if s.consultor != nil {
		if pasta := s.consultor.Pasta(cons.ID); pasta != "" {
			if err := os.RemoveAll(pasta); err != nil {
				s.log.Warn("remover pasta da consulta", "erro", err, "consulta", cons.ID)
			}
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Servidor) handleListarChatConsulta(w http.ResponseWriter, r *http.Request) {
	cons, ok := s.obterConsultaOu404(w, r)
	if !ok {
		return
	}
	msgs, err := s.banco.ListarMensagensConsulta(r.Context(), cons.ID)
	if err != nil {
		s.responderErroConsulta(w, r, err)
		return
	}
	responderJSON(w, http.StatusOK, msgs)
}

// handleChatConsulta acrescenta uma fala do usuário e dispara o próximo turno.
// Enquanto o consultor está pensando, novas falas são recusadas com 409 (um
// turno por vez — o motor é stateless e o turno em voo não veria a fala nova).
func (s *Servidor) handleChatConsulta(w http.ResponseWriter, r *http.Request) {
	cons, ok := s.obterConsultaOu404(w, r)
	if !ok {
		return
	}
	if cons.Status == db.StatusConsultaPensando {
		erroT(w, r, http.StatusConflict, "pensando", "erro.consulta_pensando")
		return
	}
	var req reqChatConsulta
	if !decodificarCorpo(w, r, &req) {
		return
	}
	conteudo := strings.TrimSpace(req.Conteudo)
	if conteudo == "" {
		erroT(w, r, http.StatusBadRequest, "invalido", "erro.chat_conteudo_obrigatorio")
		return
	}

	msg, err := s.banco.CriarMensagemConsulta(r.Context(), db.MensagemConsulta{
		ConsultaID: cons.ID, Papel: db.PapelConsultaUser, Conteudo: conteudo,
	})
	if err != nil {
		s.responderErroConsulta(w, r, err)
		return
	}
	cons.Status = db.StatusConsultaPensando
	cons.Erro = ""
	if _, err := s.banco.AtualizarConsulta(r.Context(), cons); err != nil {
		s.log.Error("marcar consulta pensando", "erro", err)
	}
	if s.consultor != nil {
		s.consultor.DispararResposta(cons.ID)
	}
	responderJSON(w, http.StatusAccepted, msg)
}

// handleProgressoConsulta transmite, via SSE, o PROGRESSO SANITIZADO do turno em
// andamento. Diferente do log ao vivo das demandas, este stream NUNCA repassa a
// linha crua do .jsonl — as linhas contêm o conteúdo dos arquivos que o harness
// leu (código-fonte), que esta feature não pode expor. Cada linha é parseada no
// servidor e reduzida a um evento allowlisted:
//
//	data: {"acao":"investigando","detalhe":"lendo <nome do arquivo>"}
//	data: {"acao":"concluindo"}
//
// Linhas não reconhecidas são DESCARTADAS (fail-closed).
func (s *Servidor) handleProgressoConsulta(w http.ResponseWriter, r *http.Request) {
	cons, ok := s.obterConsultaOu404(w, r)
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

	ctx, cancelar := contextoDoStream(r)
	defer cancelar()
	s.transmitirProgressoConsulta(ctx, w, flusher, cons.ID)
	avisarTokenExpirado(ctx, r, w, flusher)
}

// transmitirProgressoConsulta é o laço do SSE sanitizado: mesmo tailing de
// .jsonl do log de demandas (troca de alvo quando surge execução mais nova),
// mas cada linha passa por resumirLinhaStream antes de sair — e só sai se
// reconhecida.
func (s *Servidor) transmitirProgressoConsulta(ctx context.Context, w io.Writer, flusher http.Flusher, consultaID int64) {
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
		if run, temLog, err := s.banco.UltimaExecucaoConsultaComLog(ctx, consultaID); err == nil && temLog && run.ID != runAtual {
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
			return
		case <-ticker.C:
		}
	}
}

// linhaStreamConsulta é o subconjunto mínimo de uma linha stream-json que o
// resumo sanitizado precisa. Input fica como JSON cru e só campos allowlisted
// são extraídos dele.
type linhaStreamConsulta struct {
	Type    string `json:"type"`
	Message struct {
		Content []struct {
			Type  string          `json:"type"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		} `json:"content"`
	} `json:"message"`
}

// ferramentasDeLeitura são as tools cujo alvo (nome de arquivo/pasta, SEM
// conteúdo) pode aparecer no progresso.
var ferramentasDeLeitura = map[string]bool{"Read": true, "Grep": true, "Glob": true, "LS": true}

// resumirLinhaStream reduz uma linha crua do .jsonl a um evento seguro de
// progresso. Devolve ok=false para toda linha que não case com a allowlist —
// nada além de "lendo <basename>" / "executando verificação" / "concluindo"
// jamais sai deste funil.
func resumirLinhaStream(linha string) (string, bool) {
	var ev linhaStreamConsulta
	if err := json.Unmarshal([]byte(linha), &ev); err != nil {
		return "", false
	}
	switch ev.Type {
	case "result":
		return `{"acao":"concluindo"}`, true
	case "assistant":
		for _, c := range ev.Message.Content {
			if c.Type != "tool_use" {
				continue
			}
			if ferramentasDeLeitura[c.Name] {
				if alvo := alvoDaTool(c.Input); alvo != "" {
					b, err := json.Marshal(map[string]string{
						"acao": "investigando", "detalhe": "lendo " + alvo,
					})
					if err == nil {
						return string(b), true
					}
				}
				return `{"acao":"investigando","detalhe":"lendo arquivos do projeto"}`, true
			}
			if c.Name == "Bash" {
				return `{"acao":"investigando","detalhe":"executando verificação"}`, true
			}
			return `{"acao":"investigando","detalhe":"analisando"}`, true
		}
	}
	return "", false
}

// alvoDaTool extrai APENAS o nome-base do caminho de uma tool de leitura (nunca
// conteúdo, nunca o caminho completo — que pode expor estrutura interna).
func alvoDaTool(input json.RawMessage) string {
	if len(input) == 0 {
		return ""
	}
	var campos struct {
		FilePath string `json:"file_path"`
		Path     string `json:"path"`
	}
	if err := json.Unmarshal(input, &campos); err != nil {
		return ""
	}
	caminho := campos.FilePath
	if caminho == "" {
		caminho = campos.Path
	}
	if caminho == "" {
		return ""
	}
	return filepath.Base(strings.TrimSpace(caminho))
}

// dirReferenciasConsulta resolve a subpasta de referências da consulta (os
// arquivos que o usuário anexa como insumo da análise). Devolve "" quando o
// serviço de consultas não está ativo ou subiu sem pasta de trabalho.
func (s *Servidor) dirReferenciasConsulta(consultaID int64) string {
	if s.consultor == nil {
		return ""
	}
	pasta := s.consultor.Pasta(consultaID)
	if pasta == "" {
		return ""
	}
	return filepath.Join(pasta, referencias.Dir)
}

// handleListarReferenciasConsulta lista os arquivos anexados à consulta.
func (s *Servidor) handleListarReferenciasConsulta(w http.ResponseWriter, r *http.Request) {
	cons, ok := s.obterConsultaOu404(w, r)
	if !ok {
		return
	}
	dir := s.dirReferenciasConsulta(cons.ID)
	if dir == "" {
		erroT(w, r, http.StatusServiceUnavailable, "indisponivel", "erro.consulta_servico_inativo")
		return
	}
	s.listarReferenciasDir(w, dir)
}

// handleEnviarReferenciaConsulta recebe um arquivo via multipart/form-data
// (campo "arquivo") e o grava em referencias/. O anexo vira fala de sistema no
// chat — o próximo turno do consultor o vê listado e pode lê-lo (somente
// leitura, como o código dos repositórios).
func (s *Servidor) handleEnviarReferenciaConsulta(w http.ResponseWriter, r *http.Request) {
	cons, ok := s.obterConsultaOu404(w, r)
	if !ok {
		return
	}
	dir := s.dirReferenciasConsulta(cons.ID)
	if dir == "" {
		erroT(w, r, http.StatusServiceUnavailable, "indisponivel", "erro.consulta_servico_inativo")
		return
	}
	ref, ok := s.receberReferencia(w, r, dir)
	if !ok {
		return
	}
	s.registrarFalaReferenciaConsulta(r, cons.ID,
		fmt.Sprintf("Arquivo anexado: %s", ref.Arquivo))
	responderJSON(w, http.StatusCreated, ref)
}

// handleBaixarReferenciaConsulta devolve o arquivo anexado sempre como download.
func (s *Servidor) handleBaixarReferenciaConsulta(w http.ResponseWriter, r *http.Request) {
	cons, ok := s.obterConsultaOu404(w, r)
	if !ok {
		return
	}
	s.baixarReferencia(w, r, s.dirReferenciasConsulta(cons.ID), strings.TrimSpace(r.PathValue("arquivo")))
}

// handleExcluirReferenciaConsulta remove um arquivo anexado.
func (s *Servidor) handleExcluirReferenciaConsulta(w http.ResponseWriter, r *http.Request) {
	cons, ok := s.obterConsultaOu404(w, r)
	if !ok {
		return
	}
	arquivo := strings.TrimSpace(r.PathValue("arquivo"))
	if !s.excluirReferencia(w, r, s.dirReferenciasConsulta(cons.ID), arquivo) {
		return
	}
	s.registrarFalaReferenciaConsulta(r, cons.ID, fmt.Sprintf("Arquivo removido: %s", arquivo))
	w.WriteHeader(http.StatusNoContent)
}

// registrarFalaReferenciaConsulta grava a fala de sistema de anexo/remoção
// (best-effort), com o autor quando a sessão o identifica — é assim que o
// consultor fica sabendo da mudança nos arquivos da consulta.
func (s *Servidor) registrarFalaReferenciaConsulta(r *http.Request, consultaID int64, texto string) {
	if pr := principalDaRequisicao(r); strings.TrimSpace(pr.nome) != "" {
		texto += " (por " + strings.TrimSpace(pr.nome) + ")"
	}
	if _, err := s.banco.CriarMensagemConsulta(r.Context(), db.MensagemConsulta{
		ConsultaID: consultaID, Papel: db.PapelConsultaSistema, Conteudo: texto,
	}); err != nil {
		s.log.Warn("registrar fala de referência", "erro", err, "consulta", consultaID)
	}
}

// tituloDaMensagem deriva um título curto da primeira pergunta (a UI lista as
// consultas por título).
func tituloDaMensagem(msg string) string {
	linha := msg
	if i := strings.IndexAny(msg, "\r\n"); i >= 0 {
		linha = msg[:i]
	}
	linha = strings.TrimSpace(linha)
	r := []rune(linha)
	if len(r) > 80 {
		return string(r[:77]) + "…"
	}
	return linha
}

// obterConsultaOu404 resolve o path param {id} para a consulta ou responde
// 400/404.
func (s *Servidor) obterConsultaOu404(w http.ResponseWriter, r *http.Request) (db.Consulta, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		erroT(w, r, http.StatusBadRequest, "invalido", "erro.id_invalido")
		return db.Consulta{}, false
	}
	cons, err := s.banco.ObterConsulta(r.Context(), id)
	if err != nil {
		s.responderErroConsulta(w, r, err)
		return db.Consulta{}, false
	}
	return cons, true
}

// responderErroConsulta traduz os erros do store de consultas para respostas HTTP.
func (s *Servidor) responderErroConsulta(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, db.ErrNaoEncontrado):
		erroT(w, r, http.StatusNotFound, "nao_encontrado", "erro.consulta_nao_encontrada")
	case errors.Is(err, db.ErrPapelInvalido):
		erroT(w, r, http.StatusBadRequest, "invalido", "erro.chat_papel_invalido")
	default:
		s.log.Error("erro no store de consultas", "erro", err)
		erroT(w, r, http.StatusInternalServerError, "erro_interno", "erro.interno")
	}
}
