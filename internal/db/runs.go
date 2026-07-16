package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Operações de execução (coluna runs.operacao) — cada etapa do ciclo de fase,
// mais as etapas de intake (analista/planejador, sem fase associada).
const (
	OperacaoExecutor   = "executor"
	OperacaoCorretor   = "corretor"
	OperacaoRevisor    = "revisor"
	OperacaoGates      = "gates"
	OperacaoAnalista   = "analista"
	OperacaoPlanejador = "planejador"
)

// Execucao é uma linha da tabela runs (uma invocação de motor/gate). PhaseID é
// opcional: execuções de intake (analista/planejador) não têm fase.
type Execucao struct {
	ID          int64   `json:"id"`
	DemandID    int64   `json:"demand_id"`
	PhaseID     *int64  `json:"phase_id"`
	Operacao    string  `json:"operacao"`
	Engine      string  `json:"engine"`
	Modelo      string  `json:"modelo"`
	CustoUSD    float64 `json:"custo_usd"`
	TokensIn    int64   `json:"tokens_in"`
	TokensOut   int64   `json:"tokens_out"`
	IsError     bool    `json:"is_error"`
	LogRef      string  `json:"log_ref"`
	IniciadoEm  string  `json:"iniciado_em"`
	TerminadoEm string  `json:"terminado_em"`
}

// colunasExecucao lista as colunas de runs na ordem esperada por scanExecucao.
const colunasExecucao = `id, demand_id, phase_id, operacao, engine, modelo, custo_usd,
	tokens_in, tokens_out, is_error, log_ref, iniciado_em, terminado_em`

// scanExecucao lê uma linha de runs (na ordem de colunasExecucao) para Execucao,
// tratando phase_id (nullable) e is_error (0/1).
func scanExecucao(sc interface{ Scan(...any) error }) (Execucao, error) {
	var (
		e       Execucao
		phaseID sql.NullInt64
		isError int
	)
	if err := sc.Scan(&e.ID, &e.DemandID, &phaseID, &e.Operacao, &e.Engine, &e.Modelo,
		&e.CustoUSD, &e.TokensIn, &e.TokensOut, &isError, &e.LogRef,
		&e.IniciadoEm, &e.TerminadoEm); err != nil {
		return Execucao{}, err
	}
	e.PhaseID = ptrDeNull(phaseID)
	e.IsError = isError != 0
	return e, nil
}

// CriarExecucao insere uma execução e devolve a linha persistida (com id e
// iniciado_em preenchidos pelo banco). Demanda/fase inexistente vira
// ErrNaoEncontrado (violação de FK).
func (d *DB) CriarExecucao(ctx context.Context, e Execucao) (Execucao, error) {
	row := d.Escritor.QueryRowContext(ctx, `
		INSERT INTO runs
			(demand_id, phase_id, operacao, engine, modelo, custo_usd,
			 tokens_in, tokens_out, is_error, log_ref, terminado_em)
		VALUES (?,?,?,?,?,?,?,?,?,?,?)
		RETURNING id, iniciado_em`,
		e.DemandID, nullInt(e.PhaseID), e.Operacao, e.Engine, e.Modelo, e.CustoUSD,
		e.TokensIn, e.TokensOut, booleanParaInt(e.IsError), e.LogRef, e.TerminadoEm,
	)
	if err := row.Scan(&e.ID, &e.IniciadoEm); err != nil {
		return Execucao{}, traduzirErroFK(err)
	}
	return e, nil
}

// ListarExecucoes devolve as execuções da demanda demandID ordenadas por id
// (ordem cronológica de criação). Slice não-nil.
func (d *DB) ListarExecucoes(ctx context.Context, demandID int64) ([]Execucao, error) {
	rows, err := d.Leitor.QueryContext(ctx,
		`SELECT `+colunasExecucao+` FROM runs WHERE demand_id = ? ORDER BY id`, demandID)
	if err != nil {
		return nil, fmt.Errorf("listar execuções da demanda %d: %w", demandID, err)
	}
	defer rows.Close()
	execs := []Execucao{}
	for rows.Next() {
		e, err := scanExecucao(rows)
		if err != nil {
			return nil, err
		}
		execs = append(execs, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listar execuções da demanda %d: %w", demandID, err)
	}
	return execs, nil
}

// UltimaExecucaoComLog devolve a execução mais recente (maior id) da demanda que
// já tem um log_ref gravado — o alvo do "log ao vivo" (SSE) do card. Enquanto uma
// fase roda, o log_ref só é preenchido ao FECHAR a execução (AtualizarExecucao);
// por isso o alvo é sempre a última execução JÁ com log. Devolve ok=false quando
// a demanda ainda não tem nenhuma execução com log.
func (d *DB) UltimaExecucaoComLog(ctx context.Context, demandID int64) (Execucao, bool, error) {
	row := d.Leitor.QueryRowContext(ctx,
		`SELECT `+colunasExecucao+` FROM runs
		 WHERE demand_id = ? AND log_ref != '' ORDER BY id DESC LIMIT 1`, demandID)
	e, err := scanExecucao(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Execucao{}, false, nil
	}
	if err != nil {
		return Execucao{}, false, fmt.Errorf("última execução com log da demanda %d: %w", demandID, err)
	}
	return e, true, nil
}

// ObterExecucao devolve a execução de id. Se não existir, devolve ErrNaoEncontrado.
func (d *DB) ObterExecucao(ctx context.Context, id int64) (Execucao, error) {
	row := d.Leitor.QueryRowContext(ctx,
		`SELECT `+colunasExecucao+` FROM runs WHERE id = ?`, id)
	e, err := scanExecucao(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Execucao{}, ErrNaoEncontrado
	}
	if err != nil {
		return Execucao{}, fmt.Errorf("obter execução %d: %w", id, err)
	}
	return e, nil
}

// AtualizarExecucao grava os campos mutáveis da execução e.ID (custo, tokens,
// erro, log_ref, terminado_em) — tipicamente ao encerrar o run. Devolve a linha
// resultante. Execução inexistente vira ErrNaoEncontrado. Não altera demand_id/
// phase_id/operacao/iniciado_em (imutáveis após a criação).
func (d *DB) AtualizarExecucao(ctx context.Context, e Execucao) (Execucao, error) {
	res, err := d.Escritor.ExecContext(ctx, `
		UPDATE runs SET
			engine = ?, modelo = ?, custo_usd = ?, tokens_in = ?, tokens_out = ?,
			is_error = ?, log_ref = ?, terminado_em = ?
		WHERE id = ?`,
		e.Engine, e.Modelo, e.CustoUSD, e.TokensIn, e.TokensOut,
		booleanParaInt(e.IsError), e.LogRef, e.TerminadoEm, e.ID,
	)
	if err != nil {
		return Execucao{}, fmt.Errorf("atualizar execução %d: %w", e.ID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return Execucao{}, fmt.Errorf("atualizar execução %d: %w", e.ID, err)
	}
	if n == 0 {
		return Execucao{}, ErrNaoEncontrado
	}
	return d.ObterExecucao(ctx, e.ID)
}
