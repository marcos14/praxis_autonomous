package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/marcos14/praxis-autonomous/internal/db"
	"github.com/marcos14/praxis-autonomous/internal/motor"
)

// reqMotor é o corpo aceito em POST/PUT de motores. Os campos opcionais são
// ponteiros para distinguir "não informado" (assume default na criação / preserva
// no update) de um valor explícito (inclusive zero/false). A prioridade só é
// aplicada na criação; no update a ordem de fallback é gerenciada por /ordem.
type reqMotor struct {
	Nome           string          `json:"nome"`
	Prioridade     *int            `json:"prioridade"`
	Ativo          *bool           `json:"ativo"`
	ModeloExec     string          `json:"modelo_exec"`
	ModeloAnalise  string          `json:"modelo_analise"`
	ModeloConsulta string          `json:"modelo_consulta"`
	BudgetFaseUSD  *float64        `json:"budget_fase_usd"`
	TimeoutMin     *int            `json:"timeout_min"`
	Params         json.RawMessage `json:"params"`
}

// reqOrdem é o corpo de PUT /engines/ordem: a nova ordem de fallback (posição 0 =
// primeiro).
type reqOrdem struct {
	IDs []int64 `json:"ids"`
}

// reqConta é o corpo aceito em POST/PUT de contas de um motor.
type reqConta struct {
	Alias     string `json:"alias"`
	ConfigDir string `json:"config_dir"`
	Ativo     *bool  `json:"ativo"`
}

// registrarRotasMotores registra as rotas de CRUD de motores e contas no mux.
// A rota literal /engines/ordem é mais específica que /engines/{id} e tem
// precedência no ServeMux, então não há conflito.
func (s *Servidor) registrarRotasMotores(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/engines", s.handleCriarMotor)
	mux.HandleFunc("GET /api/v1/engines", s.handleListarMotores)
	mux.HandleFunc("PUT /api/v1/engines/ordem", s.handleReordenarMotores)
	mux.HandleFunc("GET /api/v1/engines/deteccao", s.handleDetectarMotores)
	mux.HandleFunc("POST /api/v1/engines/deteccao", s.handleAutocadastrarMotores)
	mux.HandleFunc("GET /api/v1/engines/{id}", s.handleObterMotor)
	mux.HandleFunc("PUT /api/v1/engines/{id}", s.handleAtualizarMotor)
	mux.HandleFunc("POST /api/v1/engines/{id}/accounts", s.handleCriarConta)
	mux.HandleFunc("PUT /api/v1/engines/{id}/accounts/{contaId}", s.handleAtualizarConta)
	mux.HandleFunc("DELETE /api/v1/engines/{id}/accounts/{contaId}", s.handleRemoverConta)
}

// handleCriarMotor cria um motor a partir do corpo, aplicando defaults e
// validações, e devolve 201 com o motor persistido.
func (s *Servidor) handleCriarMotor(w http.ResponseWriter, r *http.Request) {
	var req reqMotor
	if !decodificarCorpo(w, r, &req) {
		return
	}
	m, msg := montarMotor(req, db.Motor{}, true)
	if msg != "" {
		responderErro(w, http.StatusBadRequest, "invalido", msg)
		return
	}
	if req.Prioridade == nil {
		prox, err := s.banco.ProximaPrioridadeMotor(r.Context())
		if err != nil {
			s.responderErroMotor(w, err)
			return
		}
		m.Prioridade = prox
	}
	criado, err := s.banco.CriarMotor(r.Context(), m)
	if err != nil {
		s.responderErroMotor(w, err)
		return
	}
	responderJSON(w, http.StatusCreated, criado)
}

// handleListarMotores devolve os motores ordenados por prioridade (fallback).
func (s *Servidor) handleListarMotores(w http.ResponseWriter, r *http.Request) {
	motores, err := s.banco.ListarMotores(r.Context())
	if err != nil {
		s.responderErroMotor(w, err)
		return
	}
	responderJSON(w, http.StatusOK, motores)
}

// handleDetectarMotores inspeciona o ambiente (PATH + variaveis) e devolve, para
// cada harness conhecido, uma sugestao de cadastro (modelos, budget, timeout e
// contas derivadas do ambiente), marcando quais já estão cadastrados. Ajuda a
// cadastrar motores num servidor remoto sem saber, de antemão, o que está
// instalado. Não altera nada.
func (s *Servidor) handleDetectarMotores(w http.ResponseWriter, r *http.Request) {
	sugestoes, err := s.detectarMotores(r)
	if err != nil {
		s.responderErroMotor(w, err)
		return
	}
	responderJSON(w, http.StatusOK, sugestoes)
}

// handleAutocadastrarMotores cadastra automaticamente todos os harnesses
// detectados que estão instalados e ainda não constam no banco, criando também
// as contas derivadas do ambiente. Devolve os motores criados (vazio quando não
// havia nada novo a cadastrar). Idempotente: rodar de novo não duplica.
func (s *Servidor) handleAutocadastrarMotores(w http.ResponseWriter, r *http.Request) {
	sugestoes, err := s.detectarMotores(r)
	if err != nil {
		s.responderErroMotor(w, err)
		return
	}
	criados := []db.Motor{}
	for _, sug := range sugestoes {
		if !sug.Instalado || sug.JaCadastrado {
			continue
		}
		prox, err := s.banco.ProximaPrioridadeMotor(r.Context())
		if err != nil {
			s.responderErroMotor(w, err)
			return
		}
		m, err := s.banco.CriarMotor(r.Context(), db.Motor{
			Nome:           sug.Nome,
			Prioridade:     prox,
			Ativo:          true,
			ModeloExec:     sug.ModeloExec,
			ModeloAnalise:  sug.ModeloAnalise,
			ModeloConsulta: sug.ModeloConsulta,
			BudgetFaseUSD:  sug.BudgetFaseUSD,
			TimeoutMin:     sug.TimeoutMin,
			Params:         json.RawMessage("{}"),
		})
		if err != nil {
			s.responderErroMotor(w, err)
			return
		}
		for _, c := range sug.Contas {
			conta, msg := montarConta(reqConta{Alias: c.Alias, ConfigDir: c.ConfigDir}, db.Conta{EngineID: m.ID}, true)
			if msg != "" {
				continue
			}
			if criada, err := s.banco.CriarConta(r.Context(), conta); err == nil {
				m.Contas = append(m.Contas, criada)
			}
		}
		criados = append(criados, m)
	}
	responderJSON(w, http.StatusOK, criados)
}

// detectarMotores roda a detecção de ambiente e marca cada sugestão com
// JaCadastrado comparando (case-insensitive) com os motores já persistidos.
func (s *Servidor) detectarMotores(r *http.Request) ([]motor.SugestaoMotor, error) {
	sugestoes := motor.DetectarMotores()
	motores, err := s.banco.ListarMotores(r.Context())
	if err != nil {
		return nil, err
	}
	existentes := map[string]bool{}
	for _, m := range motores {
		existentes[strings.ToLower(strings.TrimSpace(m.Nome))] = true
	}
	for i := range sugestoes {
		sugestoes[i].JaCadastrado = existentes[strings.ToLower(strings.TrimSpace(sugestoes[i].Nome))]
	}
	return sugestoes, nil
}

// handleObterMotor devolve um motor por id (com contas).
func (s *Servidor) handleObterMotor(w http.ResponseWriter, r *http.Request) {
	id, ok := lerID(w, r, "id")
	if !ok {
		return
	}
	m, err := s.banco.ObterMotor(r.Context(), id)
	if err != nil {
		s.responderErroMotor(w, err)
		return
	}
	responderJSON(w, http.StatusOK, m)
}

// handleAtualizarMotor atualiza os campos de um motor existente, preservando os
// não fornecidos. A prioridade não é alterada aqui (use /engines/ordem).
func (s *Servidor) handleAtualizarMotor(w http.ResponseWriter, r *http.Request) {
	id, ok := lerID(w, r, "id")
	if !ok {
		return
	}
	atual, err := s.banco.ObterMotor(r.Context(), id)
	if err != nil {
		s.responderErroMotor(w, err)
		return
	}
	var req reqMotor
	if !decodificarCorpo(w, r, &req) {
		return
	}
	m, msg := montarMotor(req, atual, false)
	if msg != "" {
		responderErro(w, http.StatusBadRequest, "invalido", msg)
		return
	}
	m.ID = id
	atualizado, err := s.banco.AtualizarMotor(r.Context(), m)
	if err != nil {
		s.responderErroMotor(w, err)
		return
	}
	responderJSON(w, http.StatusOK, atualizado)
}

// handleReordenarMotores redefine a ordem de fallback dos motores.
func (s *Servidor) handleReordenarMotores(w http.ResponseWriter, r *http.Request) {
	var req reqOrdem
	if !decodificarCorpo(w, r, &req) {
		return
	}
	if len(req.IDs) == 0 {
		responderErro(w, http.StatusBadRequest, "invalido", "ids é obrigatório")
		return
	}
	if err := s.banco.ReordenarMotores(r.Context(), req.IDs); err != nil {
		s.responderErroMotor(w, err)
		return
	}
	motores, err := s.banco.ListarMotores(r.Context())
	if err != nil {
		s.responderErroMotor(w, err)
		return
	}
	responderJSON(w, http.StatusOK, motores)
}

// handleCriarConta cria uma conta no motor {id} e devolve 201 com a conta.
func (s *Servidor) handleCriarConta(w http.ResponseWriter, r *http.Request) {
	engineID, ok := lerID(w, r, "id")
	if !ok {
		return
	}
	// Confirma que o motor existe para devolver 404 (em vez de erro de FK).
	if _, err := s.banco.ObterMotor(r.Context(), engineID); err != nil {
		s.responderErroMotor(w, err)
		return
	}
	var req reqConta
	if !decodificarCorpo(w, r, &req) {
		return
	}
	c, msg := montarConta(req, db.Conta{EngineID: engineID}, true)
	if msg != "" {
		responderErro(w, http.StatusBadRequest, "invalido", msg)
		return
	}
	criada, err := s.banco.CriarConta(r.Context(), c)
	if err != nil {
		s.responderErroMotor(w, err)
		return
	}
	responderJSON(w, http.StatusCreated, criada)
}

// handleAtualizarConta atualiza uma conta {contaId} do motor {id}.
func (s *Servidor) handleAtualizarConta(w http.ResponseWriter, r *http.Request) {
	engineID, ok := lerID(w, r, "id")
	if !ok {
		return
	}
	contaID, ok := lerID(w, r, "contaId")
	if !ok {
		return
	}
	atual, err := s.banco.ObterMotor(r.Context(), engineID)
	if err != nil {
		s.responderErroMotor(w, err)
		return
	}
	base, achou := db.Conta{}, false
	for _, c := range atual.Contas {
		if c.ID == contaID {
			base, achou = c, true
			break
		}
	}
	if !achou {
		responderErro(w, http.StatusNotFound, "nao_encontrado", "conta não encontrada")
		return
	}
	var req reqConta
	if !decodificarCorpo(w, r, &req) {
		return
	}
	c, msg := montarConta(req, base, false)
	if msg != "" {
		responderErro(w, http.StatusBadRequest, "invalido", msg)
		return
	}
	c.ID = contaID
	c.EngineID = engineID
	atualizada, err := s.banco.AtualizarConta(r.Context(), c)
	if err != nil {
		s.responderErroMotor(w, err)
		return
	}
	responderJSON(w, http.StatusOK, atualizada)
}

// handleRemoverConta apaga a conta {contaId} do motor {id}.
func (s *Servidor) handleRemoverConta(w http.ResponseWriter, r *http.Request) {
	engineID, ok := lerID(w, r, "id")
	if !ok {
		return
	}
	contaID, ok := lerID(w, r, "contaId")
	if !ok {
		return
	}
	if err := s.banco.RemoverConta(r.Context(), engineID, contaID); err != nil {
		if errors.Is(err, db.ErrNaoEncontrado) {
			responderErro(w, http.StatusNotFound, "nao_encontrado", "conta não encontrada")
			return
		}
		s.responderErroMotor(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// montarMotor aplica defaults e validações sobre req, usando base como valores
// atuais (no update, campos ausentes herdam de base). A prioridade só é aplicada
// na criação. Devolve o Motor pronto para persistir e, em falha, uma mensagem.
func montarMotor(req reqMotor, base db.Motor, criando bool) (db.Motor, string) {
	m := base

	nome := strings.TrimSpace(req.Nome)
	if nome != "" {
		m.Nome = nome
	} else if criando {
		return db.Motor{}, "nome é obrigatório"
	}
	if strings.TrimSpace(m.Nome) == "" {
		return db.Motor{}, "nome é obrigatório"
	}

	if criando && req.Prioridade != nil {
		m.Prioridade = *req.Prioridade
	}

	// Campos de texto: quando vêm em branco preservam o valor atual (no update).
	if s := strings.TrimSpace(req.ModeloExec); s != "" {
		m.ModeloExec = s
	}
	if s := strings.TrimSpace(req.ModeloAnalise); s != "" {
		m.ModeloAnalise = s
	}
	if s := strings.TrimSpace(req.ModeloConsulta); s != "" {
		m.ModeloConsulta = s
	}

	if req.BudgetFaseUSD != nil {
		if *req.BudgetFaseUSD < 0 {
			return db.Motor{}, "budget_fase_usd não pode ser negativo"
		}
		m.BudgetFaseUSD = *req.BudgetFaseUSD
	}
	if req.TimeoutMin != nil {
		if *req.TimeoutMin < 0 {
			return db.Motor{}, "timeout_min não pode ser negativo"
		}
		m.TimeoutMin = *req.TimeoutMin
	}

	if req.Ativo != nil {
		m.Ativo = *req.Ativo
	} else if criando {
		m.Ativo = true
	}

	if req.Params != nil {
		if msg := validarParams(req.Params); msg != "" {
			return db.Motor{}, msg
		}
		m.Params = req.Params
	} else if criando {
		m.Params = json.RawMessage("{}")
	}

	return m, ""
}

// montarConta aplica defaults e validações sobre req, usando base como valores
// atuais. Devolve a Conta pronta para persistir e, em falha, uma mensagem.
func montarConta(req reqConta, base db.Conta, criando bool) (db.Conta, string) {
	c := base

	alias := strings.TrimSpace(req.Alias)
	if alias != "" {
		c.Alias = alias
	} else if criando {
		return db.Conta{}, "alias é obrigatório"
	}
	if strings.TrimSpace(c.Alias) == "" {
		return db.Conta{}, "alias é obrigatório"
	}

	c.ConfigDir = strings.TrimSpace(req.ConfigDir)

	if req.Ativo != nil {
		c.Ativo = *req.Ativo
	} else if criando {
		c.Ativo = true
	}

	return c, ""
}

// validarParams confirma que params é um objeto JSON. Devolve "" se válido.
func validarParams(bruto json.RawMessage) string {
	var obj map[string]any
	if err := json.Unmarshal(bruto, &obj); err != nil {
		return "params deve ser um objeto JSON válido"
	}
	return ""
}

// lerID extrai e valida um path param inteiro positivo. Em erro, escreve 400 e
// devolve false.
func lerID(w http.ResponseWriter, r *http.Request, nome string) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue(nome), 10, 64)
	if err != nil || id <= 0 {
		responderErro(w, http.StatusBadRequest, "invalido", nome+" inválido")
		return 0, false
	}
	return id, true
}

// responderErroMotor traduz os erros do store de motores/contas para respostas HTTP.
func (s *Servidor) responderErroMotor(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, db.ErrNaoEncontrado):
		responderErro(w, http.StatusNotFound, "nao_encontrado", "motor não encontrado")
	case errors.Is(err, db.ErrNomeDuplicado):
		responderErro(w, http.StatusConflict, "nome_duplicado", "já existe um motor com esse nome")
	case errors.Is(err, db.ErrAliasDuplicado):
		responderErro(w, http.StatusConflict, "alias_duplicado", "já existe uma conta com esse alias neste motor")
	case errors.Is(err, db.ErrOrdemInvalida):
		responderErro(w, http.StatusBadRequest, "invalido", err.Error())
	default:
		s.log.Error("erro no store de motores", "erro", err)
		responderErro(w, http.StatusInternalServerError, "erro_interno", "erro interno do servidor")
	}
}
