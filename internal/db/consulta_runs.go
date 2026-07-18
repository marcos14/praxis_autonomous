package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Operações de execução do consultor (coluna consulta_runs.operacao). Separadas
// das operações de runs (que exige demand_id): um turno de consulta e a geração
// de overview de projeto não pertencem a nenhuma demanda.
const (
	OperacaoConsultor = "consultor"
	OperacaoOverview  = "overview"
)

// ExecucaoConsulta é uma linha de consulta_runs (uma invocação de motor a
// serviço da feature de consultas). ConsultaID é nulo na geração de overview
// (que pertence a um projeto, não a uma conversa); ProjectID é nulo nos turnos
// de consulta.
type ExecucaoConsulta struct {
	ID          int64   `json:"id"`
	ConsultaID  *int64  `json:"consulta_id"`
	ProjectID   *int64  `json:"project_id"`
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

// colunasExecucaoConsulta lista as colunas de consulta_runs na ordem esperada
// por scanExecucaoConsulta.
const colunasExecucaoConsulta = `id, consulta_id, project_id, operacao, engine, modelo,
	custo_usd, tokens_in, tokens_out, is_error, log_ref, iniciado_em, terminado_em`

// scanExecucaoConsulta lê uma linha de consulta_runs para ExecucaoConsulta.
func scanExecucaoConsulta(sc interface{ Scan(...any) error }) (ExecucaoConsulta, error) {
	var (
		e              ExecucaoConsulta
		consID, projID sql.NullInt64
		isError        int
	)
	if err := sc.Scan(&e.ID, &consID, &projID, &e.Operacao, &e.Engine, &e.Modelo,
		&e.CustoUSD, &e.TokensIn, &e.TokensOut, &isError, &e.LogRef,
		&e.IniciadoEm, &e.TerminadoEm); err != nil {
		return ExecucaoConsulta{}, err
	}
	e.ConsultaID = ptrDeNull(consID)
	e.ProjectID = ptrDeNull(projID)
	e.IsError = isError != 0
	return e, nil
}

// CriarExecucaoConsulta insere uma execução e devolve a linha persistida (com id
// e iniciado_em preenchidos pelo banco). Consulta/projeto inexistente vira
// ErrNaoEncontrado (violação de FK).
func (d *DB) CriarExecucaoConsulta(ctx context.Context, e ExecucaoConsulta) (ExecucaoConsulta, error) {
	row := d.Escritor.QueryRowContext(ctx, `
		INSERT INTO consulta_runs
			(consulta_id, project_id, operacao, engine, modelo, custo_usd,
			 tokens_in, tokens_out, is_error, log_ref, terminado_em)
		VALUES (?,?,?,?,?,?,?,?,?,?,?)
		RETURNING id, iniciado_em`,
		nullInt(e.ConsultaID), nullInt(e.ProjectID), e.Operacao, e.Engine, e.Modelo,
		e.CustoUSD, e.TokensIn, e.TokensOut, booleanParaInt(e.IsError), e.LogRef, e.TerminadoEm,
	)
	if err := row.Scan(&e.ID, &e.IniciadoEm); err != nil {
		return ExecucaoConsulta{}, traduzirErroFK(err)
	}
	return e, nil
}

// ObterExecucaoConsulta devolve a execução de id. Se não existir, devolve
// ErrNaoEncontrado.
func (d *DB) ObterExecucaoConsulta(ctx context.Context, id int64) (ExecucaoConsulta, error) {
	row := d.Leitor.QueryRowContext(ctx,
		`SELECT `+colunasExecucaoConsulta+` FROM consulta_runs WHERE id = ?`, id)
	e, err := scanExecucaoConsulta(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ExecucaoConsulta{}, ErrNaoEncontrado
	}
	if err != nil {
		return ExecucaoConsulta{}, fmt.Errorf("obter execução de consulta %d: %w", id, err)
	}
	return e, nil
}

// AtualizarExecucaoConsulta grava os campos mutáveis da execução e.ID (custo,
// tokens, erro, log_ref, terminado_em) — tipicamente ao encerrar o run. Execução
// inexistente vira ErrNaoEncontrado.
func (d *DB) AtualizarExecucaoConsulta(ctx context.Context, e ExecucaoConsulta) (ExecucaoConsulta, error) {
	res, err := d.Escritor.ExecContext(ctx, `
		UPDATE consulta_runs SET
			engine = ?, modelo = ?, custo_usd = ?, tokens_in = ?, tokens_out = ?,
			is_error = ?, log_ref = ?, terminado_em = ?
		WHERE id = ?`,
		e.Engine, e.Modelo, e.CustoUSD, e.TokensIn, e.TokensOut,
		booleanParaInt(e.IsError), e.LogRef, e.TerminadoEm, e.ID,
	)
	if err != nil {
		return ExecucaoConsulta{}, fmt.Errorf("atualizar execução de consulta %d: %w", e.ID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return ExecucaoConsulta{}, fmt.Errorf("atualizar execução de consulta %d: %w", e.ID, err)
	}
	if n == 0 {
		return ExecucaoConsulta{}, ErrNaoEncontrado
	}
	return d.ObterExecucaoConsulta(ctx, e.ID)
}

// UltimaExecucaoConsultaComLog devolve a execução mais recente (maior id) da
// consulta que já tem log_ref gravado — o alvo do SSE de progresso sanitizado.
// Devolve ok=false quando a consulta ainda não tem execução com log.
func (d *DB) UltimaExecucaoConsultaComLog(ctx context.Context, consultaID int64) (ExecucaoConsulta, bool, error) {
	row := d.Leitor.QueryRowContext(ctx,
		`SELECT `+colunasExecucaoConsulta+` FROM consulta_runs
		 WHERE consulta_id = ? AND log_ref != '' ORDER BY id DESC LIMIT 1`, consultaID)
	e, err := scanExecucaoConsulta(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ExecucaoConsulta{}, false, nil
	}
	if err != nil {
		return ExecucaoConsulta{}, false, fmt.Errorf("última execução com log da consulta %d: %w", consultaID, err)
	}
	return e, true, nil
}
