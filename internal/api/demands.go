package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/marcos14/praxis-autonomous/internal/db"
)

// reqFaseNova é uma fase informada na criação de uma demanda manual (sem intake).
type reqFaseNova struct {
	Codigo       string   `json:"codigo"`
	Titulo       string   `json:"titulo"`
	DependeDe    []string `json:"depende_de"`
	RequerHumano bool     `json:"requer_humano"`
	GateExtra    string   `json:"gate_extra"`
	Modelo       string   `json:"modelo"`
	Ordem        int      `json:"ordem"`
}

// reqDemanda é o corpo aceito em POST /projects/{id}/demands: cria uma demanda já
// com as fases informadas. É o intake "manual" da Fase 2g (sem PRD/analista): a
// demanda nasce pronta e o scheduler a executa em background.
type reqDemanda struct {
	Titulo     string        `json:"titulo"`
	Origem     string        `json:"origem"`
	OrigemRef  string        `json:"origem_ref"`
	Prioridade int           `json:"prioridade"`
	BudgetUSD  float64       `json:"budget_usd"`
	PlanoMD    string        `json:"plano_md"`
	Fases      []reqFaseNova `json:"fases"`
}

// respDemanda serializa a demanda criada junto de suas fases.
type respDemanda struct {
	db.Demanda
	Fases []db.Fase `json:"fases"`
}

// registrarRotasDemandas registra as rotas de demandas no mux.
func (s *Servidor) registrarRotasDemandas(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/projects/{id}/demands", s.handleCriarDemanda)
}

// handleCriarDemanda cria uma demanda com fases manuais sob um projeto. A demanda
// nasce no status "pronta" — o scheduler a puxa e conduz todas as fases
// automaticamente (Fase 2g). Devolve 201 com a demanda e as fases persistidas.
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

	dem, fases, msg := montarDemanda(id, req)
	if msg != "" {
		responderErro(w, http.StatusBadRequest, "invalido", msg)
		return
	}

	criada, criadas, err := s.banco.CriarDemandaComFases(r.Context(), dem, fases)
	if err != nil {
		s.responderErroDemanda(w, err)
		return
	}
	responderJSON(w, http.StatusCreated, respDemanda{Demanda: criada, Fases: criadas})
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

	// valida as fases: codigo/titulo obrigatórios e codigos únicos na demanda.
	codigos := map[string]bool{}
	fases := make([]db.Fase, 0, len(req.Fases))
	for i, rf := range req.Fases {
		codigo := strings.TrimSpace(rf.Codigo)
		if codigo == "" {
			return db.Demanda{}, nil, "toda fase precisa de um codigo"
		}
		if strings.TrimSpace(rf.Titulo) == "" {
			return db.Demanda{}, nil, "a fase " + codigo + " precisa de um titulo"
		}
		if codigos[codigo] {
			return db.Demanda{}, nil, "codigo de fase repetido: " + codigo
		}
		codigos[codigo] = true

		ordem := rf.Ordem
		if ordem == 0 {
			ordem = i + 1
		}
		fases = append(fases, db.Fase{
			Codigo:       codigo,
			Titulo:       strings.TrimSpace(rf.Titulo),
			Status:       db.StatusFasePendente,
			DependeDe:    rf.DependeDe,
			RequerHumano: rf.RequerHumano,
			GateExtra:    strings.TrimSpace(rf.GateExtra),
			Modelo:       strings.TrimSpace(rf.Modelo),
			Ordem:        ordem,
		})
	}
	// depende_de não pode apontar para uma fase inexistente na demanda (dangling):
	// isso travaria a fila (a dependência nunca conclui).
	for _, f := range fases {
		for _, dep := range f.DependeDe {
			dep = strings.TrimSpace(dep)
			if dep != "" && !codigos[dep] {
				return db.Demanda{}, nil, "a fase " + f.Codigo + " depende de um codigo inexistente: " + dep
			}
		}
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
	}
	return dem, fases, ""
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
