package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/marcos14/praxis-autonomous/internal/db"
	"github.com/marcos14/praxis-autonomous/internal/gitops"
)

// msgBranchInvalida é a mensagem de erro (400) quando o nome de branch informado
// na criação da demanda não é uma ref git válida.
const msgBranchInvalida = "nome de branch inválido: use apenas letras, números e - _ . / (o prefixo praxis/ é adicionado automaticamente)"

// reqFaseNova é uma fase informada na criação de uma demanda manual (sem intake)
// ou na edição do plano na aba Plano & Fases (Fase 3c).
type reqFaseNova struct {
	Codigo       string   `json:"codigo"`
	Titulo       string   `json:"titulo"`
	DependeDe    []string `json:"depende_de"`
	RequerHumano bool     `json:"requer_humano"`
	GateExtra    string   `json:"gate_extra"`
	Modelo       string   `json:"modelo"`
	Observacao   string   `json:"observacao"`
	Ordem        int      `json:"ordem"`
}

// reqDemanda é o corpo aceito em POST /projects/{id}/demands. Tem dois modos:
//   - com fases: intake "manual" da Fase 2g (sem PRD/analista) — a demanda nasce
//     pronta e o scheduler a executa em background;
//   - com PRD e sem fases: intake por chat da Fase 3a — a demanda nasce como
//     conversa (status recebida), com o PRD colado como a primeira mensagem.
type reqDemanda struct {
	Titulo     string  `json:"titulo"`
	Origem     string  `json:"origem"`
	OrigemRef  string  `json:"origem_ref"`
	Prioridade int     `json:"prioridade"`
	BudgetUSD  float64 `json:"budget_usd"`
	PlanoMD    string  `json:"plano_md"`
	PRD        string  `json:"prd"`
	// Branch e o nome de branch escolhido pelo dev (opcional). Vazio → o pipeline
	// gera praxis/d<id>-<slug> na preparacao. O prefixo praxis/ e sempre garantido
	// (gitops.NormalizarBranch), pois push/worktree so operam nesse prefixo.
	Branch string        `json:"branch"`
	Fases  []reqFaseNova `json:"fases"`
}

// respDemanda serializa a demanda criada junto de suas fases.
type respDemanda struct {
	db.Demanda
	Fases []db.Fase `json:"fases"`
}

// registrarRotasDemandas registra as rotas de demandas no mux.
func (s *Servidor) registrarRotasDemandas(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/projects/{id}/demands", s.handleCriarDemanda)
	mux.HandleFunc("GET /api/v1/demands", s.handleListarDemandas)
	mux.HandleFunc("GET /api/v1/demands/{id}", s.handleObterDemanda)
	mux.HandleFunc("GET /api/v1/demands/{id}/events", s.handleEventosDemanda)
	mux.HandleFunc("GET /api/v1/demands/{id}/logs", s.handleLogsDemanda)
	mux.HandleFunc("POST /api/v1/demands/{id}/actions", s.handleAcaoDemanda)
	mux.HandleFunc("POST /api/v1/demands/{id}/chat", s.handleChatDemanda)
	mux.HandleFunc("GET /api/v1/demands/{id}/chat", s.handleListarChat)
	mux.HandleFunc("GET /api/v1/demands/{id}/questions", s.handleListarPerguntas)
	mux.HandleFunc("POST /api/v1/demands/{id}/answers", s.handleResponderPerguntas)
	mux.HandleFunc("PUT /api/v1/demands/{id}/phases", s.handleEditarFases)
	mux.HandleFunc("POST /api/v1/demands/{id}/approve-plan", s.handleAprovarPlano)
}

// handleCriarDemanda cria uma demanda sob um projeto, em um de dois modos:
//   - com fases: a demanda nasce "pronta" e o scheduler conduz as fases
//     automaticamente (Fase 2g);
//   - sem fases e com PRD: a demanda nasce como conversa (status "recebida"), com
//     o PRD colado como a primeira mensagem do chat (Fase 3a).
//
// Devolve 201 com a demanda e as fases persistidas (fases vazias no modo chat).
func (s *Servidor) handleCriarDemanda(w http.ResponseWriter, r *http.Request) {
	id, ok := lerIDProjeto(w, r)
	if !ok {
		return
	}
	// projeto precisa existir (404 claro em vez do FK genérico do store).
	if _, err := s.banco.ObterProjeto(r.Context(), id); err != nil {
		s.responderErroDemanda(w, err)
		return
	}

	var req reqDemanda
	if !decodificarCorpo(w, r, &req) {
		return
	}

	// Sem fases → intake por chat (Fase 3a): a demanda nasce da conversa.
	if len(req.Fases) == 0 {
		s.criarDemandaChat(w, r, id, req)
		return
	}

	dem, fases, msg := montarDemanda(id, req)
	if msg != "" {
		responderErro(w, http.StatusBadRequest, "invalido", msg)
		return
	}
	dem.CriadoPor = usuarioDaRequisicao(r)

	criada, criadas, err := s.banco.CriarDemandaComFases(r.Context(), dem, fases)
	if err != nil {
		s.responderErroDemanda(w, err)
		return
	}
	responderJSON(w, http.StatusCreated, respDemanda{Demanda: criada, Fases: criadas})
}

// criarDemandaChat cria uma demanda que nasce como conversa (Fase 3a): valida o
// PRD, monta a demanda no status "recebida" e persiste o PRD como a primeira
// mensagem do chat (papel user), tudo numa transação. Registra um evento de
// criação (best-effort) e devolve 201 com a demanda (sem fases ainda).
func (s *Servidor) criarDemandaChat(w http.ResponseWriter, r *http.Request, projectID int64, req reqDemanda) {
	prd := strings.TrimSpace(req.PRD)
	if prd == "" {
		responderErro(w, http.StatusBadRequest, "invalido", "informe o PRD (ou ao menos uma fase)")
		return
	}

	origem := strings.TrimSpace(req.Origem)
	if origem == "" {
		origem = db.OrigemUI
	}
	if origem != db.OrigemUI && origem != db.OrigemAPI {
		responderErro(w, http.StatusBadRequest, "invalido", "origem deve ser 'ui' ou 'api'")
		return
	}

	titulo := strings.TrimSpace(req.Titulo)
	if titulo == "" {
		titulo = tituloDePRD(prd)
	}

	branch, err := gitops.NormalizarBranch(req.Branch)
	if err != nil {
		responderErro(w, http.StatusBadRequest, "invalido", msgBranchInvalida)
		return
	}

	dem := db.Demanda{
		ProjectID:  projectID,
		Titulo:     titulo,
		Origem:     origem,
		OrigemRef:  strings.TrimSpace(req.OrigemRef),
		Status:     db.StatusDemandaRecebida,
		Prioridade: req.Prioridade,
		PlanoMD:    req.PlanoMD,
		BudgetUSD:  req.BudgetUSD,
		Branch:     branch,
		CriadoPor:  usuarioDaRequisicao(r),
	}
	criada, _, err := s.banco.CriarDemandaComChat(r.Context(), dem, db.MensagemChat{
		Papel:    db.PapelUser,
		Conteudo: prd,
	})
	if err != nil {
		s.responderErroDemanda(w, err)
		return
	}

	// evento de criação (best-effort) para alimentar a aba Eventos / SSE.
	pid, did := criada.ProjectID, criada.ID
	if _, err := s.banco.RegistrarEvento(r.Context(), db.Evento{
		ProjectID: &pid, DemandID: &did,
		Tipo:    "demanda_criada",
		Titulo:  "Praxis: demanda criada",
		Detalhe: "Demanda criada a partir do PRD colado no chat.",
	}); err != nil {
		s.log.Warn("registrar evento de criação da demanda", "erro", err, "demanda", did)
	}

	// Dispara o analista (readonly) em background — a demanda "anda sozinha" da
	// criação até `aguardando_respostas` (Fase 3b). Sem intake acoplado, fica em
	// `recebida` (mecanismo antes do wiring).
	if s.intake != nil {
		s.intake.Disparar(criada.ID)
	}

	responderJSON(w, http.StatusCreated, respDemanda{Demanda: criada, Fases: []db.Fase{}})
}

// tituloDePRD deriva um título curto da primeira linha não-vazia do PRD, para o
// caso de a tela "Nova demanda" não informar um título explícito.
func tituloDePRD(prd string) string {
	titulo := ""
	for _, linha := range strings.Split(prd, "\n") {
		if l := strings.TrimSpace(linha); l != "" {
			titulo = l
			break
		}
	}
	// remove marcação de título markdown ("# ", "## "…) só para o rótulo.
	titulo = strings.TrimLeft(titulo, "#")
	titulo = strings.TrimSpace(titulo)
	const max = 80
	if len([]rune(titulo)) > max {
		titulo = string([]rune(titulo)[:max]) + "…"
	}
	if titulo == "" {
		titulo = "Nova demanda"
	}
	return titulo
}

// handleListarDemandas devolve as demandas, opcionalmente filtradas por
// ?project=<id> e ?status=<status>. Alimenta a lista que abre os cards.
func (s *Servidor) handleListarDemandas(w http.ResponseWriter, r *http.Request) {
	var filtro db.FiltroDemandas
	if v := strings.TrimSpace(r.URL.Query().Get("project")); v != "" {
		pid, err := strconv.ParseInt(v, 10, 64)
		if err != nil || pid <= 0 {
			responderErro(w, http.StatusBadRequest, "invalido", "project inválido")
			return
		}
		filtro.ProjectID = &pid
	}
	filtro.Status = strings.TrimSpace(r.URL.Query().Get("status"))
	filtro.VisiveisPara = visibilidadeDaRequisicao(r)

	demandas, err := s.banco.ListarDemandas(r.Context(), filtro)
	if err != nil {
		s.responderErroDemanda(w, err)
		return
	}
	responderJSON(w, http.StatusOK, demandas)
}

// handleObterDemanda devolve a demanda com suas fases (aba Plano & Fases do card).
func (s *Servidor) handleObterDemanda(w http.ResponseWriter, r *http.Request) {
	dem, ok := s.obterDemandaOu404(w, r)
	if !ok {
		return
	}
	fases, err := s.banco.ListarFases(r.Context(), dem.ID)
	if err != nil {
		s.responderErroDemanda(w, err)
		return
	}
	responderJSON(w, http.StatusOK, respDemanda{Demanda: dem, Fases: fases})
}

// handleEventosDemanda devolve os eventos da demanda (aba Eventos do card), dos
// mais recentes para os mais antigos.
func (s *Servidor) handleEventosDemanda(w http.ResponseWriter, r *http.Request) {
	dem, ok := s.obterDemandaOu404(w, r)
	if !ok {
		return
	}
	eventos, err := s.banco.ListarEventos(r.Context(), db.FiltroEventos{DemandID: &dem.ID})
	if err != nil {
		s.responderErroDemanda(w, err)
		return
	}
	responderJSON(w, http.StatusOK, eventos)
}

// obterDemandaOu404 lê o {id} da rota e busca a demanda. Em id inválido escreve
// 400; em demanda inexistente escreve 404 ("demanda não encontrada"); em erro de
// infra, 500. Devolve ok=false quando já respondeu.
func (s *Servidor) obterDemandaOu404(w http.ResponseWriter, r *http.Request) (db.Demanda, bool) {
	id, ok := lerIDProjeto(w, r)
	if !ok {
		return db.Demanda{}, false
	}
	dem, err := s.banco.ObterDemanda(r.Context(), id)
	if err != nil {
		if errors.Is(err, db.ErrNaoEncontrado) {
			responderErro(w, http.StatusNotFound, "nao_encontrado", "demanda não encontrada")
		} else {
			s.log.Error("erro no store de demandas", "erro", err)
			responderErro(w, http.StatusInternalServerError, "erro_interno", "erro interno do servidor")
		}
		return db.Demanda{}, false
	}
	return dem, true
}

// montarDemanda valida o corpo e monta a demanda e as fases prontas para
// persistir. Devolve uma mensagem não-vazia em falha de validação.
func montarDemanda(projectID int64, req reqDemanda) (db.Demanda, []db.Fase, string) {
	titulo := strings.TrimSpace(req.Titulo)
	if titulo == "" {
		return db.Demanda{}, nil, "titulo é obrigatório"
	}
	if len(req.Fases) == 0 {
		return db.Demanda{}, nil, "informe ao menos uma fase"
	}

	origem := strings.TrimSpace(req.Origem)
	if origem == "" {
		origem = db.OrigemAPI
	}
	if origem != db.OrigemUI && origem != db.OrigemAPI {
		return db.Demanda{}, nil, "origem deve ser 'ui' ou 'api'"
	}

	fases, msg := validarFasesReq(req.Fases)
	if msg != "" {
		return db.Demanda{}, nil, msg
	}

	branch, err := gitops.NormalizarBranch(req.Branch)
	if err != nil {
		return db.Demanda{}, nil, msgBranchInvalida
	}

	dem := db.Demanda{
		ProjectID:  projectID,
		Titulo:     titulo,
		Origem:     origem,
		OrigemRef:  strings.TrimSpace(req.OrigemRef),
		Status:     db.StatusDemandaPronta,
		Prioridade: req.Prioridade,
		PlanoMD:    req.PlanoMD,
		BudgetUSD:  req.BudgetUSD,
		Branch:     branch,
	}
	return dem, fases, ""
}

// validarFasesReq valida uma lista de fases vinda da API (criação manual ou
// edição do plano) e a converte em []db.Fase. Regras: código e título
// obrigatórios, códigos únicos na demanda, e depende_de sem apontar para um
// código inexistente (dangling travaria a fila). A ordem é derivada da posição
// (1..N); o campo Ordem de entrada é ignorado (o store reatribui na persistência).
// Devolve uma mensagem não-vazia em falha de validação.
func validarFasesReq(reqFases []reqFaseNova) ([]db.Fase, string) {
	codigos := map[string]bool{}
	fases := make([]db.Fase, 0, len(reqFases))
	for i, rf := range reqFases {
		codigo := strings.TrimSpace(rf.Codigo)
		if codigo == "" {
			return nil, "toda fase precisa de um codigo"
		}
		if strings.TrimSpace(rf.Titulo) == "" {
			return nil, "a fase " + codigo + " precisa de um titulo"
		}
		if codigos[codigo] {
			return nil, "codigo de fase repetido: " + codigo
		}
		codigos[codigo] = true
		fases = append(fases, db.Fase{
			Codigo:       codigo,
			Titulo:       strings.TrimSpace(rf.Titulo),
			Status:       db.StatusFasePendente,
			DependeDe:    rf.DependeDe,
			RequerHumano: rf.RequerHumano,
			GateExtra:    strings.TrimSpace(rf.GateExtra),
			Modelo:       strings.TrimSpace(rf.Modelo),
			Observacao:   strings.TrimSpace(rf.Observacao),
			Ordem:        i + 1,
		})
	}
	for _, f := range fases {
		for _, dep := range f.DependeDe {
			dep = strings.TrimSpace(dep)
			if dep != "" && !codigos[dep] {
				return nil, "a fase " + f.Codigo + " depende de um codigo inexistente: " + dep
			}
		}
	}
	return fases, ""
}

// responderErroDemanda traduz os erros do store para respostas HTTP.
func (s *Servidor) responderErroDemanda(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, db.ErrNaoEncontrado):
		responderErro(w, http.StatusNotFound, "nao_encontrado", "projeto não encontrado")
	case errors.Is(err, db.ErrCodigoFaseDuplicado):
		responderErro(w, http.StatusConflict, "codigo_fase_duplicado", "há fases com o mesmo codigo na demanda")
	default:
		s.log.Error("erro no store de demandas", "erro", err)
		responderErro(w, http.StatusInternalServerError, "erro_interno", "erro interno do servidor")
	}
}
