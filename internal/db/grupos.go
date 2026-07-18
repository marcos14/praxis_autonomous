package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// Grupo é uma linha de project_groups: uma "solução" composta por N repositórios
// (projetos). A relação é N:N (um projeto pode estar em vários grupos). Membros
// vem ordenado por ordem: o membro 0 é o repo principal (cwd do harness numa
// consulta de grupo); os demais entram como diretórios extras (--add-dir).
type Grupo struct {
	ID        int64        `json:"id"`
	Nome      string       `json:"nome"`
	Slug      string       `json:"slug"`
	Descricao string       `json:"descricao"`
	Ativo     bool         `json:"ativo"`
	CriadoEm  string       `json:"criado_em"`
	Membros   []MembroGrupo `json:"membros"`
}

// MembroGrupo é um projeto membro de um grupo, com os campos do projeto que os
// consumidores (UI e consultor) precisam sem novo lookup.
type MembroGrupo struct {
	ProjectID int64  `json:"project_id"`
	Ordem     int    `json:"ordem"`
	Nome      string `json:"nome"`
	Pasta     string `json:"pasta"`
}

// CriarGrupo insere um grupo e seus membros (na ordem do slice — o primeiro é o
// principal) numa única transação e devolve o grupo persistido. Slug duplicado
// vira ErrSlugDuplicado; projeto membro inexistente vira ErrNaoEncontrado.
func (d *DB) CriarGrupo(ctx context.Context, g Grupo, projectIDs []int64) (Grupo, error) {
	tx, err := d.Escritor.BeginTx(ctx, nil)
	if err != nil {
		return Grupo{}, fmt.Errorf("criar grupo: %w", err)
	}
	defer tx.Rollback()

	row := tx.QueryRowContext(ctx, `
		INSERT INTO project_groups (nome, slug, descricao, ativo)
		VALUES (?,?,?,?)
		RETURNING id, criado_em`,
		g.Nome, g.Slug, g.Descricao, booleanParaInt(g.Ativo),
	)
	if err := row.Scan(&g.ID, &g.CriadoEm); err != nil {
		return Grupo{}, traduzirErroGrupo(err)
	}
	if err := inserirMembros(ctx, tx, g.ID, projectIDs); err != nil {
		return Grupo{}, err
	}
	if err := tx.Commit(); err != nil {
		return Grupo{}, fmt.Errorf("criar grupo: %w", err)
	}
	return d.ObterGrupo(ctx, g.ID)
}

// ListarGrupos devolve todos os grupos (com membros) ordenados por nome
// (case-insensitive) e, em empate, por id.
func (d *DB) ListarGrupos(ctx context.Context) ([]Grupo, error) {
	rows, err := d.Leitor.QueryContext(ctx, `
		SELECT id, nome, slug, descricao, ativo, criado_em
		FROM project_groups ORDER BY nome COLLATE NOCASE, id`)
	if err != nil {
		return nil, fmt.Errorf("listar grupos: %w", err)
	}
	defer rows.Close()

	grupos := []Grupo{}
	for rows.Next() {
		g, err := scanGrupo(rows)
		if err != nil {
			return nil, err
		}
		grupos = append(grupos, g)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listar grupos: %w", err)
	}
	for i := range grupos {
		membros, err := d.membrosDoGrupo(ctx, grupos[i].ID)
		if err != nil {
			return nil, err
		}
		grupos[i].Membros = membros
	}
	return grupos, nil
}

// ObterGrupo devolve o grupo de id, com os membros ordenados (o primeiro é o
// principal). Se não existir, devolve ErrNaoEncontrado.
func (d *DB) ObterGrupo(ctx context.Context, id int64) (Grupo, error) {
	row := d.Leitor.QueryRowContext(ctx, `
		SELECT id, nome, slug, descricao, ativo, criado_em
		FROM project_groups WHERE id = ?`, id)
	g, err := scanGrupo(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Grupo{}, ErrNaoEncontrado
	}
	if err != nil {
		return Grupo{}, fmt.Errorf("obter grupo %d: %w", id, err)
	}
	g.Membros, err = d.membrosDoGrupo(ctx, id)
	if err != nil {
		return Grupo{}, err
	}
	return g, nil
}

// AtualizarGrupo grava os campos editáveis do grupo g.ID e redefine seus membros
// (projectIDs na ordem desejada — o primeiro é o principal), tudo numa transação.
// Grupo inexistente vira ErrNaoEncontrado; slug em uso vira ErrSlugDuplicado.
func (d *DB) AtualizarGrupo(ctx context.Context, g Grupo, projectIDs []int64) (Grupo, error) {
	tx, err := d.Escritor.BeginTx(ctx, nil)
	if err != nil {
		return Grupo{}, fmt.Errorf("atualizar grupo %d: %w", g.ID, err)
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx, `
		UPDATE project_groups SET nome = ?, slug = ?, descricao = ?, ativo = ?
		WHERE id = ?`,
		g.Nome, g.Slug, g.Descricao, booleanParaInt(g.Ativo), g.ID,
	)
	if err != nil {
		return Grupo{}, traduzirErroGrupo(err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return Grupo{}, fmt.Errorf("atualizar grupo %d: %w", g.ID, err)
	}
	if n == 0 {
		return Grupo{}, ErrNaoEncontrado
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM project_group_members WHERE group_id = ?`, g.ID); err != nil {
		return Grupo{}, fmt.Errorf("atualizar membros do grupo %d: %w", g.ID, err)
	}
	if err := inserirMembros(ctx, tx, g.ID, projectIDs); err != nil {
		return Grupo{}, err
	}
	if err := tx.Commit(); err != nil {
		return Grupo{}, fmt.Errorf("atualizar grupo %d: %w", g.ID, err)
	}
	return d.ObterGrupo(ctx, g.ID)
}

// ExcluirGrupo remove o grupo de id (cascade em membros e nas consultas do
// grupo). Grupo inexistente vira ErrNaoEncontrado.
func (d *DB) ExcluirGrupo(ctx context.Context, id int64) error {
	res, err := d.Escritor.ExecContext(ctx, `DELETE FROM project_groups WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("excluir grupo %d: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("excluir grupo %d: %w", id, err)
	}
	if n == 0 {
		return ErrNaoEncontrado
	}
	return nil
}

// membrosDoGrupo devolve os membros do grupo ordenados (ordem, project_id), com
// nome e pasta do projeto resolvidos por join. Slice não-nil.
func (d *DB) membrosDoGrupo(ctx context.Context, groupID int64) ([]MembroGrupo, error) {
	rows, err := d.Leitor.QueryContext(ctx, `
		SELECT m.project_id, m.ordem, p.nome, p.pasta
		FROM project_group_members m
		JOIN projects p ON p.id = m.project_id
		WHERE m.group_id = ?
		ORDER BY m.ordem, m.project_id`, groupID)
	if err != nil {
		return nil, fmt.Errorf("membros do grupo %d: %w", groupID, err)
	}
	defer rows.Close()

	membros := []MembroGrupo{}
	for rows.Next() {
		var m MembroGrupo
		if err := rows.Scan(&m.ProjectID, &m.Ordem, &m.Nome, &m.Pasta); err != nil {
			return nil, err
		}
		membros = append(membros, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("membros do grupo %d: %w", groupID, err)
	}
	return membros, nil
}

// inserirMembros grava os membros do grupo na transação, com ordem = índice no
// slice (deduplicando ids repetidos, mantendo a primeira posição).
func inserirMembros(ctx context.Context, tx *sql.Tx, groupID int64, projectIDs []int64) error {
	vistos := map[int64]bool{}
	ordem := 0
	for _, pid := range projectIDs {
		if vistos[pid] {
			continue
		}
		vistos[pid] = true
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO project_group_members (group_id, project_id, ordem)
			VALUES (?,?,?)`, groupID, pid, ordem); err != nil {
			return traduzirErroFK(err)
		}
		ordem++
	}
	return nil
}

// scanGrupo lê uma linha de project_groups (sem membros) para Grupo.
func scanGrupo(sc interface{ Scan(...any) error }) (Grupo, error) {
	var (
		g     Grupo
		ativo int
	)
	if err := sc.Scan(&g.ID, &g.Nome, &g.Slug, &g.Descricao, &ativo, &g.CriadoEm); err != nil {
		return Grupo{}, err
	}
	g.Ativo = ativo != 0
	return g, nil
}

// traduzirErroGrupo converte violações conhecidas em erros sentinela do pacote
// (mesma técnica de traduzirErroProjeto: checagem pela mensagem do driver).
func traduzirErroGrupo(err error) error {
	if err == nil {
		return nil
	}
	if strings.Contains(err.Error(), "project_groups.slug") {
		return ErrSlugDuplicado
	}
	return fmt.Errorf("persistir grupo: %w", err)
}
