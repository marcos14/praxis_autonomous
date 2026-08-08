package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// ExecucaoPlanejamento é uma linha de planejamento_runs (uma invocação de motor
// a serviço de um turno do estrategista). Espelha ExecucaoConsulta sem o campo
// operacao — todo run desta tabela é um turno de planejamento.
type ExecucaoPlanejamento struct {
	ID             int64  `json:"id"`
	PlanejamentoID int64  `json:"planejamento_id"`
	Engine         string `json:"engine"`
	// Conta é o alias do perfil (engine_accounts.alias) usado na execução,
	// vigente no momento do run ("" quando o motor rodou sem perfil cadastrado).
	Conta       string  `json:"conta"`
	Modelo      string  `json:"modelo"`
	CustoUSD    float64 `json:"custo_usd"`
	TokensIn    int64   `json:"tokens_in"`
	TokensOut   int64   `json:"tokens_out"`
	IsError     bool    `json:"is_error"`
	LogRef      string  `json:"log_ref"`
	IniciadoEm  string  `json:"iniciado_em"`
	TerminadoEm string  `json:"terminado_em"`
}

// colunasExecucaoPlanejamento lista as colunas de planejamento_runs na ordem
// esperada por scanExecucaoPlanejamento.
const colunasExecucaoPlanejamento = `id, planejamento_id, engine, conta, modelo,
	custo_usd, tokens_in, tokens_out, is_error, log_ref, iniciado_em, terminado_em`

// scanExecucaoPlanejamento lê uma linha de planejamento_runs para ExecucaoPlanejamento.
func scanExecucaoPlanejamento(sc interface{ Scan(...any) error }) (ExecucaoPlanejamento, error) {
	var (
		e       ExecucaoPlanejamento
		isError int
	)
	if err := sc.Scan(&e.ID, &e.PlanejamentoID, &e.Engine, &e.Conta, &e.Modelo,
		&e.CustoUSD, &e.TokensIn, &e.TokensOut, &isError, &e.LogRef,
		&e.IniciadoEm, &e.TerminadoEm); err != nil {
		return ExecucaoPlanejamento{}, err
	}
	e.IsError = isError != 0
	return e, nil
}

// CriarExecucaoPlanejamento insere uma execução e devolve a linha persistida
// (com id e iniciado_em preenchidos pelo banco). Planejamento inexistente vira
// ErrNaoEncontrado (violação de FK).
func (d *DB) CriarExecucaoPlanejamento(ctx context.Context, e ExecucaoPlanejamento) (ExecucaoPlanejamento, error) {
	row := d.Escritor.QueryRowContext(ctx, `
		INSERT INTO planejamento_runs
			(planejamento_id, engine, conta, modelo, custo_usd,
			 tokens_in, tokens_out, is_error, log_ref, terminado_em)
		VALUES (?,?,?,?,?,?,?,?,?,?)
		RETURNING id, iniciado_em`,
		e.PlanejamentoID, e.Engine, e.Conta, e.Modelo, e.CustoUSD,
		e.TokensIn, e.TokensOut, booleanParaInt(e.IsError), e.LogRef, e.TerminadoEm,
	)
	if err := row.Scan(&e.ID, &e.IniciadoEm); err != nil {
		return ExecucaoPlanejamento{}, traduzirErroFK(err)
	}
	return e, nil
}

// ObterExecucaoPlanejamento devolve a execução de id. Se não existir, devolve
// ErrNaoEncontrado.
func (d *DB) ObterExecucaoPlanejamento(ctx context.Context, id int64) (ExecucaoPlanejamento, error) {
	row := d.Leitor.QueryRowContext(ctx,
		`SELECT `+colunasExecucaoPlanejamento+` FROM planejamento_runs WHERE id = ?`, id)
	e, err := scanExecucaoPlanejamento(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ExecucaoPlanejamento{}, ErrNaoEncontrado
	}
	if err != nil {
		return ExecucaoPlanejamento{}, fmt.Errorf("obter execução de planejamento %d: %w", id, err)
	}
	return e, nil
}

// AtualizarExecucaoPlanejamento grava os campos mutáveis da execução e.ID
// (custo, tokens, erro, log_ref, terminado_em) — tipicamente ao encerrar o run.
// Execução inexistente vira ErrNaoEncontrado.
func (d *DB) AtualizarExecucaoPlanejamento(ctx context.Context, e ExecucaoPlanejamento) (ExecucaoPlanejamento, error) {
	res, err := d.Escritor.ExecContext(ctx, `
		UPDATE planejamento_runs SET
			engine = ?, conta = ?, modelo = ?, custo_usd = ?, tokens_in = ?, tokens_out = ?,
			is_error = ?, log_ref = ?, terminado_em = ?
		WHERE id = ?`,
		e.Engine, e.Conta, e.Modelo, e.CustoUSD, e.TokensIn, e.TokensOut,
		booleanParaInt(e.IsError), e.LogRef, e.TerminadoEm, e.ID,
	)
	if err != nil {
		return ExecucaoPlanejamento{}, fmt.Errorf("atualizar execução de planejamento %d: %w", e.ID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return ExecucaoPlanejamento{}, fmt.Errorf("atualizar execução de planejamento %d: %w", e.ID, err)
	}
	if n == 0 {
		return ExecucaoPlanejamento{}, ErrNaoEncontrado
	}
	return d.ObterExecucaoPlanejamento(ctx, e.ID)
}

// UltimaExecucaoPlanejamentoComLog devolve a execução mais recente (maior id)
// do planejamento que já tem log_ref gravado — o alvo do SSE de progresso
// sanitizado. Devolve ok=false quando ainda não há execução com log.
func (d *DB) UltimaExecucaoPlanejamentoComLog(ctx context.Context, planejamentoID int64) (ExecucaoPlanejamento, bool, error) {
	row := d.Leitor.QueryRowContext(ctx,
		`SELECT `+colunasExecucaoPlanejamento+` FROM planejamento_runs
		 WHERE planejamento_id = ? AND log_ref != '' ORDER BY id DESC LIMIT 1`, planejamentoID)
	e, err := scanExecucaoPlanejamento(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ExecucaoPlanejamento{}, false, nil
	}
	if err != nil {
		return ExecucaoPlanejamento{}, false, fmt.Errorf("última execução com log do planejamento %d: %w", planejamentoID, err)
	}
	return e, true, nil
}
