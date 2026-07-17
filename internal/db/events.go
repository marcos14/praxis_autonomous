package db

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// Evento é uma linha da tabela events (alimenta o SSE e o histórico).
// ProjectID/DemandID são opcionais: um evento pode ser global do projeto, de
// uma demanda, ou de ambos.
type Evento struct {
	ID        int64  `json:"id"`
	ProjectID *int64 `json:"project_id"`
	DemandID  *int64 `json:"demand_id"`
	Tipo      string `json:"tipo"`
	Titulo    string `json:"titulo"`
	Detalhe   string `json:"detalhe"`
	CriadoEm  string `json:"criado_em"`
}

// FiltroEventos restringe ListarEventos. Campos nil não filtram; Limite <= 0
// devolve todos.
type FiltroEventos struct {
	ProjectID *int64 // filtra por projeto quando não-nil
	DemandID  *int64 // filtra por demanda quando não-nil
	Limite    int    // máximo de linhas (mais recentes primeiro); <= 0 = sem limite
}

// colunasEvento lista as colunas de events na ordem esperada por scanEvento.
const colunasEvento = `id, project_id, demand_id, tipo, titulo, detalhe, criado_em`

// scanEvento lê uma linha de events (na ordem de colunasEvento) para Evento,
// tratando project_id/demand_id (nullable).
func scanEvento(sc interface{ Scan(...any) error }) (Evento, error) {
	var (
		e         Evento
		projectID sql.NullInt64
		demandID  sql.NullInt64
	)
	if err := sc.Scan(&e.ID, &projectID, &demandID, &e.Tipo, &e.Titulo,
		&e.Detalhe, &e.CriadoEm); err != nil {
		return Evento{}, err
	}
	e.ProjectID = ptrDeNull(projectID)
	e.DemandID = ptrDeNull(demandID)
	return e, nil
}

// RegistrarEvento insere um evento e devolve a linha persistida (com id e
// criado_em preenchidos pelo banco). Projeto/demanda inexistente vira
// ErrNaoEncontrado (violação de FK).
func (d *DB) RegistrarEvento(ctx context.Context, e Evento) (Evento, error) {
	row := d.Escritor.QueryRowContext(ctx, `
		INSERT INTO events (project_id, demand_id, tipo, titulo, detalhe)
		VALUES (?,?,?,?,?)
		RETURNING id, criado_em`,
		nullInt(e.ProjectID), nullInt(e.DemandID), e.Tipo, e.Titulo, e.Detalhe,
	)
	if err := row.Scan(&e.ID, &e.CriadoEm); err != nil {
		return Evento{}, traduzirErroFK(err)
	}
	return e, nil
}

// EventosApos devolve os eventos com id > aposID em ordem CRESCENTE (id
// crescente), para o tailing incremental do SSE global (Fase 4a). limite <= 0
// aplica um teto de segurança (evita despejar um backlog enorme num cliente que
// acabou de conectar com aposID=0). Slice não-nil.
func (d *DB) EventosApos(ctx context.Context, aposID int64, limite int) ([]Evento, error) {
	if limite <= 0 {
		limite = 500
	}
	rows, err := d.Leitor.QueryContext(ctx,
		`SELECT `+colunasEvento+` FROM events WHERE id > ? ORDER BY id ASC LIMIT ?`,
		aposID, limite)
	if err != nil {
		return nil, fmt.Errorf("eventos após %d: %w", aposID, err)
	}
	defer rows.Close()
	eventos := []Evento{}
	for rows.Next() {
		e, err := scanEvento(rows)
		if err != nil {
			return nil, err
		}
		eventos = append(eventos, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("eventos após %d: %w", aposID, err)
	}
	return eventos, nil
}

// RemoverEventosAntesDe apaga os eventos com criado_em anterior a corte
// (ISO-8601, ex.: '2026-06-01T00:00:00Z') — a rotina de retenção (Fase 5d).
// Devolve quantas linhas foram removidas.
func (d *DB) RemoverEventosAntesDe(ctx context.Context, corte string) (int64, error) {
	res, err := d.Escritor.ExecContext(ctx,
		`DELETE FROM events WHERE criado_em < ?`, corte)
	if err != nil {
		return 0, fmt.Errorf("remover eventos antigos: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("remover eventos antigos: %w", err)
	}
	return n, nil
}

// UltimoEventoID devolve o maior id da tabela events (0 se vazia). O SSE global
// usa como cursor inicial para transmitir só os eventos novos após a conexão.
func (d *DB) UltimoEventoID(ctx context.Context) (int64, error) {
	var id sql.NullInt64
	if err := d.Leitor.QueryRowContext(ctx, `SELECT MAX(id) FROM events`).Scan(&id); err != nil {
		return 0, fmt.Errorf("último evento: %w", err)
	}
	return id.Int64, nil
}

// ListarEventos devolve os eventos que casam com o filtro, dos mais recentes
// para os mais antigos (id decrescente). Slice não-nil.
func (d *DB) ListarEventos(ctx context.Context, f FiltroEventos) ([]Evento, error) {
	sqlStr := `SELECT ` + colunasEvento + ` FROM events`
	cond := []string{}
	args := []any{}
	if f.ProjectID != nil {
		cond = append(cond, "project_id = ?")
		args = append(args, *f.ProjectID)
	}
	if f.DemandID != nil {
		cond = append(cond, "demand_id = ?")
		args = append(args, *f.DemandID)
	}
	if len(cond) > 0 {
		sqlStr += " WHERE " + strings.Join(cond, " AND ")
	}
	sqlStr += " ORDER BY id DESC"
	if f.Limite > 0 {
		sqlStr += " LIMIT ?"
		args = append(args, f.Limite)
	}

	rows, err := d.Leitor.QueryContext(ctx, sqlStr, args...)
	if err != nil {
		return nil, fmt.Errorf("listar eventos: %w", err)
	}
	defer rows.Close()
	eventos := []Evento{}
	for rows.Next() {
		e, err := scanEvento(rows)
		if err != nil {
			return nil, err
		}
		eventos = append(eventos, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listar eventos: %w", err)
	}
	return eventos, nil
}
