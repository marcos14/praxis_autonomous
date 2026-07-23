package db

import (
	"context"
	"fmt"
	"time"
)

// UsoJanela agrega o consumo de execuções numa janela de tempo.
type UsoJanela struct {
	Execucoes int64   `json:"execucoes"`
	CustoUSD  float64 `json:"custo_usd"`
	TokensIn  int64   `json:"tokens_in"`
	TokensOut int64   `json:"tokens_out"`
}

// UsoPraxis é o consumo registrado pelo Praxis para um par (motor, perfil),
// somando runs de demandas e de consultas. Conta vazia agrupa as execuções
// feitas sem perfil cadastrado (ou anteriores à migração 11).
type UsoPraxis struct {
	Engine       string    `json:"engine"`
	Conta        string    `json:"conta"`
	Hoje         UsoJanela `json:"hoje"`
	Ultimos7Dias UsoJanela `json:"ultimos_7_dias"`
	Total        UsoJanela `json:"total"`
}

// UsoPraxisPorConta agrega runs + consulta_runs por (engine, conta) nas janelas
// hoje (UTC), últimos 7 dias e total. `agora` permite testes determinísticos;
// produção passa time.Now().UTC(). Slice ordenado por engine e conta.
func (d *DB) UsoPraxisPorConta(ctx context.Context, agora time.Time) ([]UsoPraxis, error) {
	agora = agora.UTC()
	inicioHoje := agora.Truncate(24 * time.Hour).Format("2006-01-02T15:04:05.000Z")
	inicio7d := agora.AddDate(0, 0, -7).Format("2006-01-02T15:04:05.000Z")

	// iniciado_em é ISO-8601 UTC — comparação lexicográfica equivale à temporal.
	rows, err := d.Leitor.QueryContext(ctx, `
		SELECT engine, conta,
			SUM(CASE WHEN iniciado_em >= ?1 THEN 1 ELSE 0 END),
			SUM(CASE WHEN iniciado_em >= ?1 THEN custo_usd ELSE 0 END),
			SUM(CASE WHEN iniciado_em >= ?1 THEN tokens_in ELSE 0 END),
			SUM(CASE WHEN iniciado_em >= ?1 THEN tokens_out ELSE 0 END),
			SUM(CASE WHEN iniciado_em >= ?2 THEN 1 ELSE 0 END),
			SUM(CASE WHEN iniciado_em >= ?2 THEN custo_usd ELSE 0 END),
			SUM(CASE WHEN iniciado_em >= ?2 THEN tokens_in ELSE 0 END),
			SUM(CASE WHEN iniciado_em >= ?2 THEN tokens_out ELSE 0 END),
			COUNT(*), SUM(custo_usd), SUM(tokens_in), SUM(tokens_out)
		FROM (
			SELECT engine, conta, iniciado_em, custo_usd, tokens_in, tokens_out FROM runs
			UNION ALL
			SELECT engine, conta, iniciado_em, custo_usd, tokens_in, tokens_out FROM consulta_runs
		)
		WHERE engine != ''
		GROUP BY engine, conta
		ORDER BY engine, conta`, inicioHoje, inicio7d)
	if err != nil {
		return nil, fmt.Errorf("agregar uso por conta: %w", err)
	}
	defer rows.Close()

	usos := []UsoPraxis{}
	for rows.Next() {
		var u UsoPraxis
		if err := rows.Scan(
			&u.Engine, &u.Conta,
			&u.Hoje.Execucoes, &u.Hoje.CustoUSD, &u.Hoje.TokensIn, &u.Hoje.TokensOut,
			&u.Ultimos7Dias.Execucoes, &u.Ultimos7Dias.CustoUSD, &u.Ultimos7Dias.TokensIn, &u.Ultimos7Dias.TokensOut,
			&u.Total.Execucoes, &u.Total.CustoUSD, &u.Total.TokensIn, &u.Total.TokensOut,
		); err != nil {
			return nil, fmt.Errorf("agregar uso por conta: %w", err)
		}
		usos = append(usos, u)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("agregar uso por conta: %w", err)
	}
	return usos, nil
}
