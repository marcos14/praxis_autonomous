package db

import (
	"context"
	"fmt"
	"strings"
)

// MetricaDia é uma linha do agregado diário (dia × projeto × motor) que alimenta
// os tiles e o gráfico da Home.
type MetricaDia struct {
	Dia             string  `json:"dia"`
	ProjectID       int64   `json:"project_id"`
	Engine          string  `json:"engine"`
	CustoUSD        float64 `json:"custo_usd"`
	FasesConcluidas int     `json:"fases_concluidas"`
}

// FiltroMetricas restringe ListarMetricasDia. Campos vazios/nil não filtram.
type FiltroMetricas struct {
	ProjectID *int64 // filtra por projeto quando não-nil
	DiaDe     string // 'YYYY-MM-DD' inclusivo quando não-vazio
	DiaAte    string // 'YYYY-MM-DD' inclusivo quando não-vazio
}

// AcumularMetricaDia soma custo e fases concluídas ao agregado da chave
// (dia, project_id, engine), criando a linha se ainda não existir. É idempotente
// por incremento: cada chamada adiciona os deltas informados. A transação é
// curta (um único UPSERT).
func (d *DB) AcumularMetricaDia(ctx context.Context, m MetricaDia) error {
	_, err := d.Escritor.ExecContext(ctx, `
		INSERT INTO metrics_dia (dia, project_id, engine, custo_usd, fases_concluidas)
		VALUES (?,?,?,?,?)
		ON CONFLICT (dia, project_id, engine) DO UPDATE SET
			custo_usd        = custo_usd + excluded.custo_usd,
			fases_concluidas = fases_concluidas + excluded.fases_concluidas`,
		m.Dia, m.ProjectID, m.Engine, m.CustoUSD, m.FasesConcluidas,
	)
	if err != nil {
		return traduzirErroFK(err)
	}
	return nil
}

// ListarMetricasDia devolve as linhas do agregado que casam com o filtro,
// ordenadas por dia, projeto e motor. Slice não-nil.
func (d *DB) ListarMetricasDia(ctx context.Context, f FiltroMetricas) ([]MetricaDia, error) {
	sqlStr := `SELECT dia, project_id, engine, custo_usd, fases_concluidas FROM metrics_dia`
	cond := []string{}
	args := []any{}
	if f.ProjectID != nil {
		cond = append(cond, "project_id = ?")
		args = append(args, *f.ProjectID)
	}
	if strings.TrimSpace(f.DiaDe) != "" {
		cond = append(cond, "dia >= ?")
		args = append(args, f.DiaDe)
	}
	if strings.TrimSpace(f.DiaAte) != "" {
		cond = append(cond, "dia <= ?")
		args = append(args, f.DiaAte)
	}
	if len(cond) > 0 {
		sqlStr += " WHERE " + strings.Join(cond, " AND ")
	}
	sqlStr += " ORDER BY dia, project_id, engine"

	rows, err := d.Leitor.QueryContext(ctx, sqlStr, args...)
	if err != nil {
		return nil, fmt.Errorf("listar métricas: %w", err)
	}
	defer rows.Close()
	metricas := []MetricaDia{}
	for rows.Next() {
		var m MetricaDia
		if err := rows.Scan(&m.Dia, &m.ProjectID, &m.Engine, &m.CustoUSD, &m.FasesConcluidas); err != nil {
			return nil, err
		}
		metricas = append(metricas, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listar métricas: %w", err)
	}
	return metricas, nil
}
