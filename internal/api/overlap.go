package api

import (
	"context"
	"net/http"
	"sort"

	"github.com/marcos14/praxis-autonomous/internal/db"
	"github.com/marcos14/praxis-autonomous/internal/gitops"
)

// registrarRotasOverlap registra as rotas de sobreposição entre demandas (Fase
// 5c): o mapa global (para os badges do kanban) e o detalhe por demanda.
func (s *Servidor) registrarRotasOverlap(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/overlaps", s.handleOverlaps)
	mux.HandleFunc("GET /api/v1/demands/{id}/overlap", s.handleOverlapDemanda)
}

// Sobreposicao descreve a interseção de arquivos entre a demanda de referência e
// outra demanda.
type Sobreposicao struct {
	DemandID int64    `json:"demand_id"`
	Titulo   string   `json:"titulo"`
	Arquivos []string `json:"arquivos"`
}

// statusOverlap são os status considerados na detecção de sobreposição: demandas
// em andamento (não terminais) que efetivamente concorrem por arquivos.
var statusOverlap = []string{
	db.StatusDemandaAnalisando, db.StatusDemandaAguardandoRespostas,
	db.StatusDemandaPlanejando, db.StatusDemandaAguardandoAprovacao,
	db.StatusDemandaPronta, db.StatusDemandaExecutando,
	db.StatusDemandaAguardandoFranquia, db.StatusDemandaConflito,
	db.StatusDemandaConcluida, db.StatusDemandaPausada,
}

// conjuntoArquivosDemanda devolve o conjunto de arquivos "tocados" por uma
// demanda: a união de arquivos_provaveis (analista) com o diff da branch em
// relação à main (quando a demanda já tem branch e o projeto está acessível).
func (s *Servidor) conjuntoArquivosDemanda(ctx context.Context, dem db.Demanda, projPasta, base string) map[string]bool {
	set := map[string]bool{}
	if provaveis, err := s.banco.ArquivosProvaveisDemanda(ctx, dem.ID); err == nil {
		for _, a := range provaveis {
			set[a] = true
		}
	}
	if dem.Branch != "" && projPasta != "" {
		if nomes, err := gitops.DiffNames(projPasta, base, dem.Branch); err == nil {
			for _, n := range nomes {
				set[n] = true
			}
		}
	}
	return set
}

// mapaSobreposicoes calcula, para as demandas ativas, o conjunto de arquivos de
// cada uma e as interseções par a par. Devolve demandID → lista de sobreposições.
// A comparação é feita apenas ENTRE demandas do MESMO projeto (branches de
// projetos distintos não colidem).
func (s *Servidor) mapaSobreposicoes(ctx context.Context) (map[int64][]Sobreposicao, error) {
	demandas, err := s.banco.ListarDemandasPorStatus(ctx, statusOverlap)
	if err != nil {
		return nil, err
	}
	// cache de pasta/base por projeto (evita reconsultar o projeto por demanda).
	type projInfo struct{ pasta, base string }
	projs := map[int64]projInfo{}
	infoProjeto := func(pid int64) projInfo {
		if p, ok := projs[pid]; ok {
			return p
		}
		p := projInfo{}
		if proj, err := s.banco.ObterProjeto(ctx, pid); err == nil {
			p.pasta = proj.Pasta
			p.base = proj.BranchPrincipal
			if p.base == "" {
				p.base = "main"
			}
		}
		projs[pid] = p
		return p
	}

	sets := make(map[int64]map[string]bool, len(demandas))
	for _, dem := range demandas {
		pi := infoProjeto(dem.ProjectID)
		sets[dem.ID] = s.conjuntoArquivosDemanda(ctx, dem, pi.pasta, pi.base)
	}

	titulos := make(map[int64]string, len(demandas))
	porProjeto := map[int64][]db.Demanda{}
	for _, dem := range demandas {
		titulos[dem.ID] = dem.Titulo
		porProjeto[dem.ProjectID] = append(porProjeto[dem.ProjectID], dem)
	}

	resultado := map[int64][]Sobreposicao{}
	for _, grupo := range porProjeto {
		for i := 0; i < len(grupo); i++ {
			for j := i + 1; j < len(grupo); j++ {
				a, b := grupo[i], grupo[j]
				comuns := intersecao(sets[a.ID], sets[b.ID])
				if len(comuns) == 0 {
					continue
				}
				resultado[a.ID] = append(resultado[a.ID], Sobreposicao{DemandID: b.ID, Titulo: titulos[b.ID], Arquivos: comuns})
				resultado[b.ID] = append(resultado[b.ID], Sobreposicao{DemandID: a.ID, Titulo: titulos[a.ID], Arquivos: comuns})
			}
		}
	}
	return resultado, nil
}

// intersecao devolve as chaves presentes em ambos os conjuntos, ordenadas.
func intersecao(a, b map[string]bool) []string {
	comuns := []string{}
	for k := range a {
		if b[k] {
			comuns = append(comuns, k)
		}
	}
	sort.Strings(comuns)
	return comuns
}

// handleOverlaps devolve o mapa demandID → sobreposições (para os badges do
// kanban). Demandas sem sobreposição não aparecem no mapa.
func (s *Servidor) handleOverlaps(w http.ResponseWriter, r *http.Request) {
	mapa, err := s.mapaSobreposicoes(r.Context())
	if err != nil {
		s.responderErroDemanda(w, err)
		return
	}
	responderJSON(w, http.StatusOK, mapa)
}

// handleOverlapDemanda devolve as sobreposições de uma demanda específica (aba/
// badge do card). Slice vazio quando não há.
func (s *Servidor) handleOverlapDemanda(w http.ResponseWriter, r *http.Request) {
	dem, ok := s.obterDemandaOu404(w, r)
	if !ok {
		return
	}
	sobre, err := s.sobreposicoesDe(r.Context(), dem)
	if err != nil {
		s.responderErroDemanda(w, err)
		return
	}
	responderJSON(w, http.StatusOK, sobre)
}

// sobreposicoesDe calcula as sobreposições de uma única demanda contra as demais
// ativas do mesmo projeto.
func (s *Servidor) sobreposicoesDe(ctx context.Context, dem db.Demanda) ([]Sobreposicao, error) {
	proj, err := s.banco.ObterProjeto(ctx, dem.ProjectID)
	base := "main"
	pasta := ""
	if err == nil {
		pasta = proj.Pasta
		if proj.BranchPrincipal != "" {
			base = proj.BranchPrincipal
		}
	}
	meuSet := s.conjuntoArquivosDemanda(ctx, dem, pasta, base)
	if len(meuSet) == 0 {
		return []Sobreposicao{}, nil
	}

	outras, err := s.banco.ListarDemandasPorStatus(ctx, statusOverlap)
	if err != nil {
		return nil, err
	}
	sobre := []Sobreposicao{}
	for _, o := range outras {
		if o.ID == dem.ID || o.ProjectID != dem.ProjectID {
			continue
		}
		oSet := s.conjuntoArquivosDemanda(ctx, o, pasta, base)
		if comuns := intersecao(meuSet, oSet); len(comuns) > 0 {
			sobre = append(sobre, Sobreposicao{DemandID: o.ID, Titulo: o.Titulo, Arquivos: comuns})
		}
	}
	return sobre, nil
}
