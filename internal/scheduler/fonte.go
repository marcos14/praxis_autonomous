package scheduler

import (
	"context"
	"fmt"

	"github.com/marcos14/praxis-autonomous/internal/db"
)

// Item e uma entrada da fila do scheduler: uma demanda candidata a (continuar a)
// executar. Carrega so o minimo que o scheduler precisa para decidir concorrencia
// (id da demanda, projeto para o limite por projeto, prioridade para a ordem). O
// Executor busca o resto do estado no banco quando de fato roda a demanda.
type Item struct {
	DemandaID  int64
	ProjectID  int64
	Prioridade int
}

// Fonte e a fila no banco: devolve, em ordem de prioridade, as demandas prontas
// para (continuar a) executar. E um seam — a implementacao de producao consulta o
// banco (FonteBanco); os testes injetam uma fila em memoria.
type Fonte interface {
	// Prontas devolve as demandas agendaveis no momento, ja ordenadas por
	// prioridade (mais prioritaria primeiro). O scheduler filtra as que ja estao
	// rodando, concluidas ou reagendadas para o futuro.
	Prontas(ctx context.Context) ([]Item, error)
}

// StatusAgendaveis sao os status de demanda que a FonteBanco considera na fila:
//   - pronta: aprovada, ainda nao iniciada;
//   - executando: em andamento (inclui as retomadas de trabalho ja comecado; a
//     recuperacao explicita pos-restart e refinada na Fase 2i);
//   - aguardando_franquia: pausada por franquia, aguardando o horario de retomada
//     (o scheduler segura o reagendamento ate RetomarEm via naoAntesDe).
var StatusAgendaveis = []string{
	db.StatusDemandaPronta,
	db.StatusDemandaExecutando,
	db.StatusDemandaAguardandoFranquia,
}

// FonteBanco le a fila diretamente do banco (tabela demands), retornando as
// demandas nos status agendaveis. E a fonte usada em producao.
type FonteBanco struct {
	Store *db.DB
}

// NovaFonteBanco cria uma FonteBanco sobre o banco dado.
func NovaFonteBanco(store *db.DB) *FonteBanco {
	return &FonteBanco{Store: store}
}

// Prontas consulta o banco por demandas nos StatusAgendaveis. Deduplica por id
// (uma demanda so aparece uma vez, mesmo que a query por status se sobreponha) e
// preserva a ordem de prioridade do banco (ListarDemandas ja ordena por
// prioridade, id DESC).
func (f *FonteBanco) Prontas(ctx context.Context) ([]Item, error) {
	if f.Store == nil {
		return nil, fmt.Errorf("FonteBanco sem Store")
	}
	vistos := map[int64]bool{}
	itens := []Item{}
	for _, st := range StatusAgendaveis {
		dems, err := f.Store.ListarDemandas(ctx, db.FiltroDemandas{Status: st})
		if err != nil {
			return nil, fmt.Errorf("listar demandas (%s): %w", st, err)
		}
		for _, d := range dems {
			if vistos[d.ID] {
				continue
			}
			vistos[d.ID] = true
			itens = append(itens, Item{DemandaID: d.ID, ProjectID: d.ProjectID, Prioridade: d.Prioridade})
		}
	}
	return itens, nil
}
