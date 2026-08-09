package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/marcos14/praxis-autonomous/internal/db"
)

// reqNovaConsulta é o corpo de POST /consultas: o alvo (projeto OU grupo,
// exclusivo), um título opcional e a primeira pergunta do usuário.
type reqNovaConsulta struct {
	ProjectID int64  `json:"project_id"`
	GroupID   int64  `json:"group_id"`
	Titulo    string `json:"titulo"`
	Mensagem  string `json:"mensagem"`
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
	mux.HandleFunc("GET /api/v1/consultas/{id}/progresso", s.handleProgressoConsulta)
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

	cons := db.Consulta{Titulo: strings.TrimSpace(req.Titulo), Status: db.StatusConsultaPensando}
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
	if s.consultor != nil {
		s.consultor.DispararResposta(criada.ID)
	}
	responderJSON(w, http.StatusCreated, criada)
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

	s.transmitirProgressoConsulta(r.Context(), w, flusher, cons.ID)
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
