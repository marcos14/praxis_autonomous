package db

import (
	"context"
	"database/sql"
	"fmt"
)

// Tipos de vínculo planejamento→demanda (coluna planejamento_demandas.tipo —
// CHECK no banco). Completa = a demanda levou o documento inteiro; complementar
// = levou só o delta desde uma demanda base (fase 2 do fluxo de handoffs).
const (
	TipoDemandaPlanejamentoCompleta     = "completa"
	TipoDemandaPlanejamentoComplementar = "complementar"
)

// VinculoPlanejamentoDemanda é uma linha de planejamento_demandas: uma demanda
// gerada a partir de um planejamento, com a revisão dos documentos entregue no
// handoff (0 = desconhecida, caso dos vínculos migrados da era demand_id).
// DemandaTitulo/DemandaStatus são resolvidos por join nas listagens.
type VinculoPlanejamentoDemanda struct {
	ID             int64  `json:"id"`
	PlanejamentoID int64  `json:"planejamento_id"`
	DemandID       int64  `json:"demand_id"`
	Tipo           string `json:"tipo"`
	PRDRev         int64  `json:"prd_rev"`
	ADRsRev        int64  `json:"adrs_rev"`
	BaseDemandID   *int64 `json:"base_demand_id"`
	CriadoEm       string `json:"criado_em"`
	DemandaTitulo  string `json:"demanda_titulo,omitempty"`
	DemandaStatus  string `json:"demanda_status,omitempty"`
}

// CriarVinculoPlanejamentoDemanda registra a demanda gerada pelo handoff e
// devolve a linha persistida. Tipo em branco cai em completa; planejamento ou
// demanda inexistente vira ErrNaoEncontrado (violação de FK).
func (d *DB) CriarVinculoPlanejamentoDemanda(ctx context.Context, v VinculoPlanejamentoDemanda) (VinculoPlanejamentoDemanda, error) {
	if v.Tipo == "" {
		v.Tipo = TipoDemandaPlanejamentoCompleta
	}
	row := d.Escritor.QueryRowContext(ctx, `
		INSERT INTO planejamento_demandas
			(planejamento_id, demand_id, tipo, prd_rev, adrs_rev, base_demand_id)
		VALUES (?,?,?,?,?,?)
		RETURNING id, criado_em`,
		v.PlanejamentoID, v.DemandID, v.Tipo, v.PRDRev, v.ADRsRev, nullInt(v.BaseDemandID),
	)
	if err := row.Scan(&v.ID, &v.CriadoEm); err != nil {
		return VinculoPlanejamentoDemanda{}, traduzirErroFK(err)
	}
	return v, nil
}

// ListarDemandasDoPlanejamento devolve os vínculos do planejamento em ordem de
// criação, com título e status da demanda resolvidos por join (a UI mostra o
// estado de cada demanda gerada). Slice não-nil.
func (d *DB) ListarDemandasDoPlanejamento(ctx context.Context, planejamentoID int64) ([]VinculoPlanejamentoDemanda, error) {
	rows, err := d.Leitor.QueryContext(ctx, `
		SELECT pd.id, pd.planejamento_id, pd.demand_id, pd.tipo, pd.prd_rev,
		       pd.adrs_rev, pd.base_demand_id, pd.criado_em,
		       COALESCE(dm.titulo, ''), COALESCE(dm.status, '')
		FROM planejamento_demandas pd
		LEFT JOIN demands dm ON dm.id = pd.demand_id
		WHERE pd.planejamento_id = ?
		ORDER BY pd.id`, planejamentoID)
	if err != nil {
		return nil, fmt.Errorf("listar demandas do planejamento %d: %w", planejamentoID, err)
	}
	defer rows.Close()

	vinculos := []VinculoPlanejamentoDemanda{}
	for rows.Next() {
		var (
			v    VinculoPlanejamentoDemanda
			base sql.NullInt64
		)
		if err := rows.Scan(&v.ID, &v.PlanejamentoID, &v.DemandID, &v.Tipo, &v.PRDRev,
			&v.ADRsRev, &base, &v.CriadoEm, &v.DemandaTitulo, &v.DemandaStatus); err != nil {
			return nil, err
		}
		v.BaseDemandID = ptrDeNull(base)
		vinculos = append(vinculos, v)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listar demandas do planejamento %d: %w", planejamentoID, err)
	}
	return vinculos, nil
}
