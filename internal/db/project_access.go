package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Store da ACL de visibilidade de projetos (tabela project_access, migração 9).
// Semântica: projeto sem nenhuma linha é aberto a todos os usuários autenticados;
// projeto com ≥1 linha só aparece para os usuários liberados — diretamente ou
// pelo grupo de usuários (user_groups) a que pertencem. Quem enxerga tudo
// (admin/projetos.gerir, tokens de API, modo bootstrap) é decidido na camada da
// API, que simplesmente não aplica o filtro.

// RefAcesso é um par id+nome para as listas da ACL (e os seletores da UI).
type RefAcesso struct {
	ID   int64  `json:"id"`
	Nome string `json:"nome"`
}

// AcessoProjeto é a ACL de um projeto: os usuários e grupos liberados. Restrito
// informa se há alguma linha (false = projeto aberto a todos).
type AcessoProjeto struct {
	Restrito bool        `json:"restrito"`
	Usuarios []RefAcesso `json:"usuarios"`
	Grupos   []RefAcesso `json:"grupos"`
}

// condAcessoProjeto devolve a condição SQL "o usuário vê o projeto cuja coluna
// de id é col": sem ACL é aberto; com ACL exige vínculo direto do usuário ou do
// grupo dele. A condição consome DOIS argumentos (userID, userID) — acrescente-os
// na ordem com argsAcessoProjeto.
func condAcessoProjeto(col string) string {
	return `(NOT EXISTS (SELECT 1 FROM project_access pa WHERE pa.project_id = ` + col + `)
		OR EXISTS (SELECT 1 FROM project_access pa
			WHERE pa.project_id = ` + col + `
			  AND (pa.user_id = ?
			       OR pa.group_id IN (SELECT m.group_id FROM user_group_members m WHERE m.user_id = ?))))`
}

// argsAcessoProjeto são os argumentos consumidos por condAcessoProjeto.
func argsAcessoProjeto(userID int64) []any { return []any{userID, userID} }

// ObterAcessoProjeto devolve a ACL do projeto (listas ordenadas por nome).
// Projeto inexistente vira ErrNaoEncontrado.
func (d *DB) ObterAcessoProjeto(ctx context.Context, projectID int64) (AcessoProjeto, error) {
	var existe int
	err := d.Leitor.QueryRowContext(ctx,
		`SELECT 1 FROM projects WHERE id = ?`, projectID).Scan(&existe)
	if errors.Is(err, sql.ErrNoRows) {
		return AcessoProjeto{}, ErrNaoEncontrado
	}
	if err != nil {
		return AcessoProjeto{}, fmt.Errorf("acesso do projeto %d: %w", projectID, err)
	}

	acesso := AcessoProjeto{Usuarios: []RefAcesso{}, Grupos: []RefAcesso{}}
	if acesso.Usuarios, err = d.listarRefs(ctx, `
		SELECT u.id, u.nome
		FROM project_access pa JOIN users u ON u.id = pa.user_id
		WHERE pa.project_id = ? ORDER BY u.nome COLLATE NOCASE`, projectID); err != nil {
		return AcessoProjeto{}, fmt.Errorf("usuários do acesso do projeto %d: %w", projectID, err)
	}
	if acesso.Grupos, err = d.listarRefs(ctx, `
		SELECT g.id, g.nome
		FROM project_access pa JOIN user_groups g ON g.id = pa.group_id
		WHERE pa.project_id = ? ORDER BY g.nome COLLATE NOCASE`, projectID); err != nil {
		return AcessoProjeto{}, fmt.Errorf("grupos do acesso do projeto %d: %w", projectID, err)
	}
	acesso.Restrito = len(acesso.Usuarios) > 0 || len(acesso.Grupos) > 0
	return acesso, nil
}

// DefinirAcessoProjeto substitui a ACL do projeto pelas listas informadas, numa
// única transação (listas vazias = projeto aberto a todos). Projeto, usuário ou
// grupo inexistente vira ErrNaoEncontrado.
func (d *DB) DefinirAcessoProjeto(ctx context.Context, projectID int64, usuarios, grupos []int64) error {
	tx, err := d.Escritor.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("definir acesso do projeto %d: %w", projectID, err)
	}
	defer tx.Rollback()

	var existe int
	err = tx.QueryRowContext(ctx, `SELECT 1 FROM projects WHERE id = ?`, projectID).Scan(&existe)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNaoEncontrado
	}
	if err != nil {
		return fmt.Errorf("definir acesso do projeto %d: %w", projectID, err)
	}

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM project_access WHERE project_id = ?`, projectID); err != nil {
		return fmt.Errorf("definir acesso do projeto %d: %w", projectID, err)
	}
	for _, uid := range usuarios {
		if _, err := tx.ExecContext(ctx, `
			INSERT OR IGNORE INTO project_access (project_id, user_id) VALUES (?,?)`,
			projectID, uid); err != nil {
			return traduzirErroFK(err)
		}
	}
	for _, gid := range grupos {
		if _, err := tx.ExecContext(ctx, `
			INSERT OR IGNORE INTO project_access (project_id, group_id) VALUES (?,?)`,
			projectID, gid); err != nil {
			return traduzirErroFK(err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("definir acesso do projeto %d: %w", projectID, err)
	}
	return nil
}

// UsuarioVeProjeto informa se o projeto é visível ao usuário pela ACL (projeto
// sem ACL é visível a todos). Projeto inexistente devolve false.
func (d *DB) UsuarioVeProjeto(ctx context.Context, userID, projectID int64) (bool, error) {
	var ve bool
	args := append([]any{}, argsAcessoProjeto(userID)...)
	args = append(args, projectID)
	err := d.Leitor.QueryRowContext(ctx,
		`SELECT `+condAcessoProjeto("p.id")+` FROM projects p WHERE p.id = ?`, args...).Scan(&ve)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("visibilidade do projeto %d para usuário %d: %w", projectID, userID, err)
	}
	return ve, nil
}

// UsuarioVeDemanda informa se a demanda pertence a um projeto visível ao
// usuário. Demanda inexistente devolve TRUE — o chamador (handler) é quem
// responde 404 sem revelar se a demanda existe ou não.
func (d *DB) UsuarioVeDemanda(ctx context.Context, userID, demandID int64) (bool, error) {
	var ve bool
	args := append([]any{}, argsAcessoProjeto(userID)...)
	args = append(args, demandID)
	err := d.Leitor.QueryRowContext(ctx,
		`SELECT `+condAcessoProjeto("dm.project_id")+` FROM demands dm WHERE dm.id = ?`, args...).Scan(&ve)
	if errors.Is(err, sql.ErrNoRows) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("visibilidade da demanda %d para usuário %d: %w", demandID, userID, err)
	}
	return ve, nil
}

// UsuarioVeGrupoProjetos informa se um grupo de PROJETOS (project_groups, feature
// de consultas) é visível ao usuário: todos os projetos-membros precisam ser
// visíveis (fail-closed — um único projeto restrito esconde o grupo inteiro).
// Grupo inexistente devolve TRUE (o handler responde 404).
func (d *DB) UsuarioVeGrupoProjetos(ctx context.Context, userID, groupID int64) (bool, error) {
	var existe int
	err := d.Leitor.QueryRowContext(ctx,
		`SELECT 1 FROM project_groups WHERE id = ?`, groupID).Scan(&existe)
	if errors.Is(err, sql.ErrNoRows) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("visibilidade do grupo %d para usuário %d: %w", groupID, userID, err)
	}

	var ve bool
	args := []any{groupID}
	args = append(args, argsAcessoProjeto(userID)...)
	if err := d.Leitor.QueryRowContext(ctx, `
		SELECT NOT EXISTS (
			SELECT 1 FROM project_group_members m
			WHERE m.group_id = ? AND NOT `+condAcessoProjeto("m.project_id")+`
		)`, args...).Scan(&ve); err != nil {
		return false, fmt.Errorf("visibilidade do grupo %d para usuário %d: %w", groupID, userID, err)
	}
	return ve, nil
}

// UsuarioVeConsulta informa se a consulta é visível ao usuário: consulta de
// projeto segue a ACL do projeto; consulta de grupo segue UsuarioVeGrupoProjetos.
// Consulta inexistente devolve TRUE (o handler responde 404).
func (d *DB) UsuarioVeConsulta(ctx context.Context, userID, consultaID int64) (bool, error) {
	var projectID, groupID sql.NullInt64
	err := d.Leitor.QueryRowContext(ctx,
		`SELECT project_id, group_id FROM consultas WHERE id = ?`, consultaID).
		Scan(&projectID, &groupID)
	if errors.Is(err, sql.ErrNoRows) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("visibilidade da consulta %d para usuário %d: %w", consultaID, userID, err)
	}
	switch {
	case projectID.Valid:
		return d.UsuarioVeProjeto(ctx, userID, projectID.Int64)
	case groupID.Valid:
		return d.UsuarioVeGrupoProjetos(ctx, userID, groupID.Int64)
	}
	return true, nil
}

// ListarProjetosVisiveis devolve os projetos visíveis ao usuário pela ACL, na
// mesma ordem de ListarProjetos (nome case-insensitive, id). Slice não-nil.
func (d *DB) ListarProjetosVisiveis(ctx context.Context, userID int64) ([]Projeto, error) {
	rows, err := d.Leitor.QueryContext(ctx,
		`SELECT `+colunasProjeto+` FROM projects p
		 WHERE `+condAcessoProjeto("p.id")+`
		 ORDER BY nome COLLATE NOCASE, id`, argsAcessoProjeto(userID)...)
	if err != nil {
		return nil, fmt.Errorf("listar projetos visíveis: %w", err)
	}
	defer rows.Close()

	projetos := []Projeto{}
	for rows.Next() {
		p, err := scanProjeto(rows)
		if err != nil {
			return nil, err
		}
		projetos = append(projetos, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listar projetos visíveis: %w", err)
	}
	return projetos, nil
}

// ListarRefsUsuarios devolve id+nome dos usuários ativos (opções do seletor da
// ACL na UI — sem e-mail nem papéis). Slice não-nil.
func (d *DB) ListarRefsUsuarios(ctx context.Context) ([]RefAcesso, error) {
	return d.listarRefs(ctx,
		`SELECT id, nome FROM users WHERE ativo = 1 ORDER BY nome COLLATE NOCASE, id`)
}

// ListarRefsGruposUsuarios devolve id+nome dos grupos de usuários (opções do
// seletor da ACL na UI). Slice não-nil.
func (d *DB) ListarRefsGruposUsuarios(ctx context.Context) ([]RefAcesso, error) {
	return d.listarRefs(ctx,
		`SELECT id, nome FROM user_groups ORDER BY nome COLLATE NOCASE, id`)
}

// listarRefs executa uma consulta de duas colunas (id, nome) e devolve os pares.
func (d *DB) listarRefs(ctx context.Context, sqlStr string, args ...any) ([]RefAcesso, error) {
	rows, err := d.Leitor.QueryContext(ctx, sqlStr, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	refs := []RefAcesso{}
	for rows.Next() {
		var ref RefAcesso
		if err := rows.Scan(&ref.ID, &ref.Nome); err != nil {
			return nil, err
		}
		refs = append(refs, ref)
	}
	return refs, rows.Err()
}

// anexarCondAcesso junta a condição de acesso ao WHERE de uma listagem quando
// visiveisPara não é nil (nil = quem enxerga tudo — sem filtro).
func anexarCondAcesso(cond []string, args []any, col string, visiveisPara *int64) ([]string, []any) {
	if visiveisPara == nil {
		return cond, args
	}
	cond = append(cond, condAcessoProjeto(col))
	args = append(args, argsAcessoProjeto(*visiveisPara)...)
	return cond, args
}
