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

// GastoDia é o custo total de um dia (agregado de runs), para o gráfico da Home.
type GastoDia struct {
	Dia      string  `json:"dia"`       // 'YYYY-MM-DD'
	CustoUSD float64 `json:"custo_usd"` // soma dos custos das execuções do dia
}

// ResumoProjeto agrega demandas ativas e custo por projeto (tabela da Home).
type ResumoProjeto struct {
	ProjectID      int64   `json:"project_id"`
	DemandasAtivas int     `json:"demandas_ativas"`
	CustoUSD       float64 `json:"custo_usd"`
}

// ResumoHome reúne os números da Home (Fase 4b). É calculado direto das tabelas
// de origem (runs/demands/phases) — metrics_dia é um agregado opcional que não é
// pré-requisito para a Home funcionar.
type ResumoHome struct {
	GastoMes           float64         `json:"gasto_mes"`
	DemandasAtivas     int             `json:"demandas_ativas"`
	FasesConcluidas7d  int             `json:"fases_concluidas_7d"`
	IntegradasMes      int             `json:"integradas_mes"`
	AguardandoFranquia int             `json:"aguardando_franquia"`
	GastosPorDia       []GastoDia      `json:"gastos_por_dia"`
	PorProjeto         []ResumoProjeto `json:"por_projeto"`
}

// statusAtivos são os estados que contam como "demanda ativa" (em andamento):
// tudo que não é terminal (integrada/cancelada/falhou). concluida conta como
// ativa (aguardando MR/integração).
var statusAtivos = []string{
	StatusDemandaRecebida, StatusDemandaAnalisando, StatusDemandaAguardandoRespostas,
	StatusDemandaPlanejando, StatusDemandaAguardandoAprovacao, StatusDemandaPronta,
	StatusDemandaExecutando, StatusDemandaConcluida, StatusDemandaPausada,
	StatusDemandaAguardandoFranquia, StatusDemandaConflito,
}

// ResumoHome calcula os agregados da Home. As três datas de corte são 'YYYY-MM-DD'
// e vêm do handler (a partir de time.Now), para o teste poder fixá-las:
//   - inicioMes: primeiro dia do mês corrente (gasto do mês, integradas no mês);
//   - corte7d: hoje menos 7 dias (fases concluídas nos últimos 7 dias);
//   - corteGrafico: primeiro dia da janela do gráfico de gastos por dia.
func (d *DB) ResumoHome(ctx context.Context, inicioMes, corte7d, corteGrafico string) (ResumoHome, error) {
	var r ResumoHome

	// Gasto do mês: soma dos custos das execuções iniciadas no mês.
	if err := d.Leitor.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(custo_usd),0) FROM runs WHERE substr(iniciado_em,1,10) >= ?`,
		inicioMes).Scan(&r.GastoMes); err != nil {
		return r, fmt.Errorf("resumo home (gasto mês): %w", err)
	}

	// Demandas ativas (status não-terminal).
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(statusAtivos)), ",")
	argsAtivos := make([]any, len(statusAtivos))
	for i, s := range statusAtivos {
		argsAtivos[i] = s
	}
	if err := d.Leitor.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM demands WHERE status IN (`+placeholders+`)`, argsAtivos...).
		Scan(&r.DemandasAtivas); err != nil {
		return r, fmt.Errorf("resumo home (ativas): %w", err)
	}

	// Fases concluídas nos últimos 7 dias.
	if err := d.Leitor.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM phases WHERE status = ? AND substr(concluido_em,1,10) >= ?`,
		StatusFaseConcluida, corte7d).Scan(&r.FasesConcluidas7d); err != nil {
		return r, fmt.Errorf("resumo home (fases 7d): %w", err)
	}

	// Integradas no mês.
	if err := d.Leitor.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM demands WHERE status = ? AND substr(atualizado_em,1,10) >= ?`,
		StatusDemandaIntegrada, inicioMes).Scan(&r.IntegradasMes); err != nil {
		return r, fmt.Errorf("resumo home (integradas): %w", err)
	}

	// Aguardando franquia (proxy do tile de franquia enquanto o espelho de
	// esgotamento por conta não é persistido — ver pendência 1a.n1).
	if err := d.Leitor.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM demands WHERE status = ?`, StatusDemandaAguardandoFranquia).
		Scan(&r.AguardandoFranquia); err != nil {
		return r, fmt.Errorf("resumo home (franquia): %w", err)
	}

	// Gráfico: gastos por dia na janela.
	rows, err := d.Leitor.QueryContext(ctx,
		`SELECT substr(iniciado_em,1,10) AS dia, SUM(custo_usd)
		 FROM runs WHERE substr(iniciado_em,1,10) >= ?
		 GROUP BY dia ORDER BY dia`, corteGrafico)
	if err != nil {
		return r, fmt.Errorf("resumo home (gráfico): %w", err)
	}
	r.GastosPorDia = []GastoDia{}
	for rows.Next() {
		var g GastoDia
		if err := rows.Scan(&g.Dia, &g.CustoUSD); err != nil {
			rows.Close()
			return r, err
		}
		r.GastosPorDia = append(r.GastosPorDia, g)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return r, fmt.Errorf("resumo home (gráfico): %w", err)
	}

	// Tabela por projeto: demandas ativas e custo acumulado.
	prows, err := d.Leitor.QueryContext(ctx,
		`SELECT project_id,
		        SUM(CASE WHEN status IN (`+placeholders+`) THEN 1 ELSE 0 END) AS ativas,
		        SUM(custo_usd) AS custo
		 FROM demands GROUP BY project_id ORDER BY custo DESC`, argsAtivos...)
	if err != nil {
		return r, fmt.Errorf("resumo home (por projeto): %w", err)
	}
	defer prows.Close()
	r.PorProjeto = []ResumoProjeto{}
	for prows.Next() {
		var rp ResumoProjeto
		if err := prows.Scan(&rp.ProjectID, &rp.DemandasAtivas, &rp.CustoUSD); err != nil {
			return r, err
		}
		r.PorProjeto = append(r.PorProjeto, rp)
	}
	if err := prows.Err(); err != nil {
		return r, fmt.Errorf("resumo home (por projeto): %w", err)
	}
	return r, nil
}

// ListarDemandasPorStatus devolve as demandas cujo status está na lista, na
// ordem de ListarDemandas (prioridade, id decrescente). Alimenta a lista
// "Precisa de você" da Home (aguardando_respostas/aguardando_aprovacao/conflito).
// Slice não-nil.
func (d *DB) ListarDemandasPorStatus(ctx context.Context, statuses []string) ([]Demanda, error) {
	if len(statuses) == 0 {
		return []Demanda{}, nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(statuses)), ",")
	args := make([]any, len(statuses))
	for i, s := range statuses {
		args[i] = s
	}
	rows, err := d.Leitor.QueryContext(ctx,
		`SELECT `+colunasDemanda+` FROM demands WHERE status IN (`+placeholders+`)
		 ORDER BY prioridade, id DESC`, args...)
	if err != nil {
		return nil, fmt.Errorf("listar demandas por status: %w", err)
	}
	defer rows.Close()
	demandas := []Demanda{}
	for rows.Next() {
		dem, err := scanDemanda(rows)
		if err != nil {
			return nil, err
		}
		demandas = append(demandas, dem)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listar demandas por status: %w", err)
	}
	return demandas, nil
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
