package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// Origens possíveis de uma demanda (coluna demands.origem — CHECK no banco).
const (
	OrigemUI  = "ui"
	OrigemAPI = "api"
)

// Estados da máquina de demandas (coluna demands.status). São validados na
// aplicação, não por CHECK no banco, para não exigir migração a cada estado
// novo. Refletem o fluxo descrito no plano.
const (
	StatusDemandaRecebida            = "recebida"
	StatusDemandaAnalisando          = "analisando"
	StatusDemandaAguardandoRespostas = "aguardando_respostas"
	StatusDemandaPlanejando          = "planejando"
	StatusDemandaAguardandoAprovacao = "aguardando_aprovacao"
	StatusDemandaPronta              = "pronta"
	StatusDemandaExecutando          = "executando"
	StatusDemandaConcluida           = "concluida"
	StatusDemandaIntegrada           = "integrada"
	StatusDemandaPausada             = "pausada"
	StatusDemandaAguardandoFranquia  = "aguardando_franquia"
	StatusDemandaFalhou              = "falhou"
	StatusDemandaConflito            = "conflito"
	StatusDemandaCancelada           = "cancelada"
)

// Demanda é uma linha da tabela demands. As tags JSON refletem o modelo de
// dados do plano (snake_case) e são a forma serializada pela API.
type Demanda struct {
	ID           int64   `json:"id"`
	ProjectID    int64   `json:"project_id"`
	Titulo       string  `json:"titulo"`
	Origem       string  `json:"origem"`
	OrigemRef    string  `json:"origem_ref"`
	Status       string  `json:"status"`
	Prioridade   int     `json:"prioridade"`
	Branch       string  `json:"branch"`
	WorktreePath string  `json:"worktree_path"`
	PlanoMD      string  `json:"plano_md"`
	CustoUSD     float64 `json:"custo_usd"`
	BudgetUSD    float64 `json:"budget_usd"`
	Erro         string  `json:"erro"`
	CriadoEm     string  `json:"criado_em"`
	AtualizadoEm string  `json:"atualizado_em"`
}

// FiltroDemandas restringe ListarDemandas. Campos nulos/vazios não filtram.
type FiltroDemandas struct {
	ProjectID *int64 // filtra por projeto quando não-nil
	Status    string // filtra por status quando não-vazio
}

// colunasDemanda lista as colunas de demands na ordem esperada por scanDemanda.
const colunasDemanda = `id, project_id, titulo, origem, origem_ref, status, prioridade,
	branch, worktree_path, plano_md, custo_usd, budget_usd, erro, criado_em, atualizado_em`

// scanDemanda lê uma linha de demands (na ordem de colunasDemanda) para Demanda.
func scanDemanda(sc interface{ Scan(...any) error }) (Demanda, error) {
	var d Demanda
	if err := sc.Scan(&d.ID, &d.ProjectID, &d.Titulo, &d.Origem, &d.OrigemRef,
		&d.Status, &d.Prioridade, &d.Branch, &d.WorktreePath, &d.PlanoMD,
		&d.CustoUSD, &d.BudgetUSD, &d.Erro, &d.CriadoEm, &d.AtualizadoEm); err != nil {
		return Demanda{}, err
	}
	return d, nil
}

// CriarDemanda insere uma nova demanda e devolve a linha persistida (com id,
// criado_em e atualizado_em preenchidos pelo banco). Origem/status vazios caem
// nos defaults (ui/recebida). Projeto inexistente vira ErrNaoEncontrado.
func (d *DB) CriarDemanda(ctx context.Context, dem Demanda) (Demanda, error) {
	if strings.TrimSpace(dem.Origem) == "" {
		dem.Origem = OrigemUI
	}
	if strings.TrimSpace(dem.Status) == "" {
		dem.Status = StatusDemandaRecebida
	}
	row := d.Escritor.QueryRowContext(ctx, `
		INSERT INTO demands
			(project_id, titulo, origem, origem_ref, status, prioridade,
			 branch, worktree_path, plano_md, custo_usd, budget_usd, erro)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?)
		RETURNING id, criado_em, atualizado_em`,
		dem.ProjectID, dem.Titulo, dem.Origem, dem.OrigemRef, dem.Status, dem.Prioridade,
		dem.Branch, dem.WorktreePath, dem.PlanoMD, dem.CustoUSD, dem.BudgetUSD, dem.Erro,
	)
	if err := row.Scan(&dem.ID, &dem.CriadoEm, &dem.AtualizadoEm); err != nil {
		return Demanda{}, traduzirErroFK(err)
	}
	return dem, nil
}

// ListarDemandas devolve as demandas que casam com o filtro, ordenadas por
// prioridade e, em empate, por id decrescente (mais recentes antes). Slice
// não-nil.
func (d *DB) ListarDemandas(ctx context.Context, f FiltroDemandas) ([]Demanda, error) {
	sqlStr := `SELECT ` + colunasDemanda + ` FROM demands`
	cond := []string{}
	args := []any{}
	if f.ProjectID != nil {
		cond = append(cond, "project_id = ?")
		args = append(args, *f.ProjectID)
	}
	if strings.TrimSpace(f.Status) != "" {
		cond = append(cond, "status = ?")
		args = append(args, f.Status)
	}
	if len(cond) > 0 {
		sqlStr += " WHERE " + strings.Join(cond, " AND ")
	}
	sqlStr += " ORDER BY prioridade, id DESC"

	rows, err := d.Leitor.QueryContext(ctx, sqlStr, args...)
	if err != nil {
		return nil, fmt.Errorf("listar demandas: %w", err)
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
		return nil, fmt.Errorf("listar demandas: %w", err)
	}
	return demandas, nil
}

// ObterDemanda devolve a demanda de id. Se não existir, devolve ErrNaoEncontrado.
func (d *DB) ObterDemanda(ctx context.Context, id int64) (Demanda, error) {
	row := d.Leitor.QueryRowContext(ctx,
		`SELECT `+colunasDemanda+` FROM demands WHERE id = ?`, id)
	dem, err := scanDemanda(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Demanda{}, ErrNaoEncontrado
	}
	if err != nil {
		return Demanda{}, fmt.Errorf("obter demanda %d: %w", id, err)
	}
	return dem, nil
}

// AtualizarDemanda grava os campos editáveis da demanda identificada por dem.ID
// e carimba atualizado_em com o horário atual. Devolve a linha resultante.
// Demanda inexistente vira ErrNaoEncontrado. O project_id não é alterado (a
// demanda não migra de projeto).
func (d *DB) AtualizarDemanda(ctx context.Context, dem Demanda) (Demanda, error) {
	res, err := d.Escritor.ExecContext(ctx, `
		UPDATE demands SET
			titulo = ?, origem = ?, origem_ref = ?, status = ?, prioridade = ?,
			branch = ?, worktree_path = ?, plano_md = ?, custo_usd = ?, budget_usd = ?,
			erro = ?, atualizado_em = strftime('%Y-%m-%dT%H:%M:%fZ','now')
		WHERE id = ?`,
		dem.Titulo, dem.Origem, dem.OrigemRef, dem.Status, dem.Prioridade,
		dem.Branch, dem.WorktreePath, dem.PlanoMD, dem.CustoUSD, dem.BudgetUSD,
		dem.Erro, dem.ID,
	)
	if err != nil {
		return Demanda{}, fmt.Errorf("atualizar demanda %d: %w", dem.ID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return Demanda{}, fmt.Errorf("atualizar demanda %d: %w", dem.ID, err)
	}
	if n == 0 {
		return Demanda{}, ErrNaoEncontrado
	}
	return d.ObterDemanda(ctx, dem.ID)
}

// RemoverDemanda apaga a demanda de id (e, por cascata, suas fases, execuções e
// eventos). Demanda inexistente vira ErrNaoEncontrado.
func (d *DB) RemoverDemanda(ctx context.Context, id int64) error {
	res, err := d.Escritor.ExecContext(ctx, `DELETE FROM demands WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("remover demanda %d: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("remover demanda %d: %w", id, err)
	}
	if n == 0 {
		return ErrNaoEncontrado
	}
	return nil
}

// traduzirErroFK converte violação de chave estrangeira (referência a um id
// inexistente, ex.: project_id/demand_id) em ErrNaoEncontrado.
func traduzirErroFK(err error) error {
	if err == nil {
		return nil
	}
	if strings.Contains(err.Error(), "FOREIGN KEY") {
		return ErrNaoEncontrado
	}
	return fmt.Errorf("persistir: %w", err)
}

// nullInt converte um *int64 para o valor aceito pelo driver (nil → NULL SQL).
func nullInt(p *int64) any {
	if p == nil {
		return nil
	}
	return *p
}

// ptrDeNull converte um sql.NullInt64 lido do banco para *int64 (NULL → nil).
func ptrDeNull(n sql.NullInt64) *int64 {
	if !n.Valid {
		return nil
	}
	v := n.Int64
	return &v
}
