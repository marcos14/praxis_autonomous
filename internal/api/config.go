package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/marcos14/praxis-autonomous/internal/i18n"
)

// registrarRotasConfig registra as rotas de config em camadas: global,
// override por projeto e a config efetiva resolvida.
func (s *Servidor) registrarRotasConfig(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/config", s.handleObterConfigGlobal)
	mux.HandleFunc("PUT /api/v1/config", s.handleDefinirConfigGlobal)
	// A rota literal /config/efetiva é mais específica que /config e tem
	// precedência no ServeMux, então convive sem conflito.
	mux.HandleFunc("GET /api/v1/projects/{id}/config/efetiva", s.handleConfigEfetiva)
	mux.HandleFunc("GET /api/v1/projects/{id}/config", s.handleObterConfigProjeto)
	mux.HandleFunc("PUT /api/v1/projects/{id}/config", s.handleDefinirConfigProjeto)
}

// handleObterConfigGlobal devolve as entradas de config do escopo global como um
// mapa chave→valor (JSON).
func (s *Servidor) handleObterConfigGlobal(w http.ResponseWriter, r *http.Request) {
	cfg, err := s.banco.ObterConfigGlobal(r.Context())
	if err != nil {
		s.responderErroConfig(w, r, err)
		return
	}
	responderJSON(w, http.StatusOK, cfg)
}

// handleDefinirConfigGlobal substitui as entradas do escopo global pelo corpo
// (full replace) e devolve o resultado persistido.
func (s *Servidor) handleDefinirConfigGlobal(w http.ResponseWriter, r *http.Request) {
	entradas, ok := lerConfigBody(w, r)
	if !ok {
		return
	}
	if err := s.banco.DefinirConfigGlobal(r.Context(), entradas); err != nil {
		s.responderErroConfig(w, r, err)
		return
	}
	// i18n: a chave global `idioma` alimenta o idioma da instância (eventos e
	// notificações) — aplica sem reiniciar; ausente/inválida volta ao padrão.
	var idioma string
	if raw, ok := entradas["idioma"]; ok {
		_ = json.Unmarshal(raw, &idioma)
	}
	i18n.DefinirIdiomaInstancia(idioma)
	cfg, err := s.banco.ObterConfigGlobal(r.Context())
	if err != nil {
		s.responderErroConfig(w, r, err)
		return
	}
	responderJSON(w, http.StatusOK, cfg)
}

// handleObterConfigProjeto devolve só as entradas de override do projeto (não
// inclui as herdadas do global; para isso, use a config efetiva).
func (s *Servidor) handleObterConfigProjeto(w http.ResponseWriter, r *http.Request) {
	id, ok := lerID(w, r, "id")
	if !ok {
		return
	}
	if _, err := s.banco.ObterProjeto(r.Context(), id); err != nil {
		s.responderErroConfig(w, r, err)
		return
	}
	cfg, err := s.banco.ObterConfigProjeto(r.Context(), id)
	if err != nil {
		s.responderErroConfig(w, r, err)
		return
	}
	responderJSON(w, http.StatusOK, cfg)
}

// handleDefinirConfigProjeto substitui os overrides do projeto pelo corpo (full
// replace). Remover uma chave faz o projeto voltar a herdar o valor global.
// Quem altera: projetos.gerir ou o DONO do projeto.
func (s *Servidor) handleDefinirConfigProjeto(w http.ResponseWriter, r *http.Request) {
	id, ok := lerID(w, r, "id")
	if !ok {
		return
	}
	atual, err := s.banco.ObterProjeto(r.Context(), id)
	if err != nil {
		s.responderErroConfig(w, r, err)
		return
	}
	if !exigirGestaoProjeto(w, r, atual) {
		return
	}
	entradas, ok := lerConfigBody(w, r)
	if !ok {
		return
	}
	if err := s.banco.DefinirConfigProjeto(r.Context(), id, entradas); err != nil {
		s.responderErroConfig(w, r, err)
		return
	}
	cfg, err := s.banco.ObterConfigProjeto(r.Context(), id)
	if err != nil {
		s.responderErroConfig(w, r, err)
		return
	}
	responderJSON(w, http.StatusOK, cfg)
}

// handleConfigEfetiva devolve a config efetiva do projeto (global × override),
// com a origem de cada chave.
func (s *Servidor) handleConfigEfetiva(w http.ResponseWriter, r *http.Request) {
	id, ok := lerID(w, r, "id")
	if !ok {
		return
	}
	if _, err := s.banco.ObterProjeto(r.Context(), id); err != nil {
		s.responderErroConfig(w, r, err)
		return
	}
	efetiva, err := s.banco.ConfigEfetiva(r.Context(), id)
	if err != nil {
		s.responderErroConfig(w, r, err)
		return
	}
	responderJSON(w, http.StatusOK, efetiva)
}

// lerConfigBody decodifica o corpo como um mapa chave→valor (JSON) e valida as
// chaves (não-vazias). Em erro, escreve a resposta 400 e devolve false. Corpo
// vazio (sem chaves) é aceito e limpa o escopo.
func lerConfigBody(w http.ResponseWriter, r *http.Request) (map[string]json.RawMessage, bool) {
	var entradas map[string]json.RawMessage
	if !decodificarCorpo(w, r, &entradas) {
		return nil, false
	}
	if entradas == nil {
		entradas = map[string]json.RawMessage{}
	}
	limpo := make(map[string]json.RawMessage, len(entradas))
	for chave, valor := range entradas {
		k := strings.TrimSpace(chave)
		if k == "" {
			responderErro(w, http.StatusBadRequest, "invalido", "chave de config não pode ser vazia")
			return nil, false
		}
		if len(valor) == 0 {
			valor = json.RawMessage("null")
		}
		limpo[k] = valor
	}
	return limpo, true
}

// responderErroConfig traduz os erros do store de config para respostas HTTP.
func (s *Servidor) responderErroConfig(w http.ResponseWriter, r *http.Request, err error) {
	s.responderErroProjeto(w, r, err)
}
