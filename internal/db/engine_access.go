package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Store da ACL de visibilidade de MOTORES (tabela engine_access, migração 16) —
// espelho da project_access. Semântica: motor sem nenhuma linha é público (todo
// usuário o vê e o scheduler pode usá-lo em qualquer trabalho); motor com ≥1
// linha só aparece — e só executa trabalho — para os usuários liberados,
// diretamente ou pelo grupo. Quem enxerga tudo na UI (config.gerir/admin) é
// decidido na camada da API; a EXECUÇÃO, porém, respeita sempre a ACL: um
// trabalho sem criador (token de API, bootstrap) usa apenas motores públicos.

// Valores de visibilidade resumida de projetos e motores (campo calculado a
// partir das linhas de ACL — não é coluna): sem linha = pública; qualquer linha
// de grupo = grupo; só linhas de usuário = privada.
const (
	VisibilidadePublica = "publica"
	VisibilidadePrivada = "privada"
	VisibilidadeGrupo   = "grupo"
)

// AcessoMotor é a ACL de um motor: os usuários e grupos liberados. Restrito
// informa se há alguma linha (false = motor público).
type AcessoMotor struct {
	Restrito bool        `json:"restrito"`
	Usuarios []RefAcesso `json:"usuarios"`
	Grupos   []RefAcesso `json:"grupos"`
}

// condAcessoMotor devolve a condição SQL "o usuário vê o motor cuja coluna de id
// é col" — mesma forma da condAcessoProjeto. Consome DOIS argumentos (userID,
// userID), na ordem de argsAcessoMotor.
func condAcessoMotor(col string) string {
	return `(NOT EXISTS (SELECT 1 FROM engine_access ea WHERE ea.engine_id = ` + col + `)
		OR EXISTS (SELECT 1 FROM engine_access ea
			WHERE ea.engine_id = ` + col + `
			  AND (ea.user_id = ?
			       OR ea.group_id IN (SELECT m.group_id FROM user_group_members m WHERE m.user_id = ?))))`
}

func argsAcessoMotor(userID int64) []any { return []any{userID, userID} }

// ObterAcessoMotor devolve a ACL do motor (listas ordenadas por nome). Motor
// inexistente vira ErrNaoEncontrado.
func (d *DB) ObterAcessoMotor(ctx context.Context, engineID int64) (AcessoMotor, error) {
	var existe int
	err := d.Leitor.QueryRowContext(ctx,
		`SELECT 1 FROM engines WHERE id = ?`, engineID).Scan(&existe)
	if errors.Is(err, sql.ErrNoRows) {
		return AcessoMotor{}, ErrNaoEncontrado
	}
	if err != nil {
		return AcessoMotor{}, fmt.Errorf("acesso do motor %d: %w", engineID, err)
	}

	acesso := AcessoMotor{Usuarios: []RefAcesso{}, Grupos: []RefAcesso{}}
	if acesso.Usuarios, err = d.listarRefs(ctx, `
		SELECT u.id, u.nome
		FROM engine_access ea JOIN users u ON u.id = ea.user_id
		WHERE ea.engine_id = ? ORDER BY u.nome COLLATE NOCASE`, engineID); err != nil {
		return AcessoMotor{}, fmt.Errorf("usuários do acesso do motor %d: %w", engineID, err)
	}
	if acesso.Grupos, err = d.listarRefs(ctx, `
		SELECT g.id, g.nome
		FROM engine_access ea JOIN user_groups g ON g.id = ea.group_id
		WHERE ea.engine_id = ? ORDER BY g.nome COLLATE NOCASE`, engineID); err != nil {
		return AcessoMotor{}, fmt.Errorf("grupos do acesso do motor %d: %w", engineID, err)
	}
	acesso.Restrito = len(acesso.Usuarios) > 0 || len(acesso.Grupos) > 0
	return acesso, nil
}

// DefinirAcessoMotor substitui a ACL do motor pelas listas informadas, numa única
// transação (listas vazias = motor público). Motor, usuário ou grupo inexistente
// vira ErrNaoEncontrado.
func (d *DB) DefinirAcessoMotor(ctx context.Context, engineID int64, usuarios, grupos []int64) error {
	tx, err := d.Escritor.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("definir acesso do motor %d: %w", engineID, err)
	}
	defer tx.Rollback()

	var existe int
	err = tx.QueryRowContext(ctx, `SELECT 1 FROM engines WHERE id = ?`, engineID).Scan(&existe)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNaoEncontrado
	}
	if err != nil {
		return fmt.Errorf("definir acesso do motor %d: %w", engineID, err)
	}

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM engine_access WHERE engine_id = ?`, engineID); err != nil {
		return fmt.Errorf("definir acesso do motor %d: %w", engineID, err)
	}
	for _, uid := range usuarios {
		if _, err := tx.ExecContext(ctx, `
			INSERT OR IGNORE INTO engine_access (engine_id, user_id) VALUES (?,?)`,
			engineID, uid); err != nil {
			return traduzirErroFK(err)
		}
	}
	for _, gid := range grupos {
		if _, err := tx.ExecContext(ctx, `
			INSERT OR IGNORE INTO engine_access (engine_id, group_id) VALUES (?,?)`,
			engineID, gid); err != nil {
			return traduzirErroFK(err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("definir acesso do motor %d: %w", engineID, err)
	}
	return nil
}

// IDsMotoresVisiveis devolve o conjunto de motores visíveis para um usuário.
// userID nil = a visão de quem NÃO tem usuário (token de API, bootstrap): só os
// motores públicos (sem nenhuma linha de ACL). É o filtro que o scheduler, o
// intake, o consultor e o estrategista aplicam antes de escolher motor — a ACL
// vale para EXECUTAR, não só para listar.
func (d *DB) IDsMotoresVisiveis(ctx context.Context, userID *int64) (map[int64]bool, error) {
	var (
		rows *sql.Rows
		err  error
	)
	if userID == nil {
		rows, err = d.Leitor.QueryContext(ctx, `
			SELECT e.id FROM engines e
			WHERE NOT EXISTS (SELECT 1 FROM engine_access ea WHERE ea.engine_id = e.id)`)
	} else {
		rows, err = d.Leitor.QueryContext(ctx,
			`SELECT e.id FROM engines e WHERE `+condAcessoMotor("e.id"),
			argsAcessoMotor(*userID)...)
	}
	if err != nil {
		return nil, fmt.Errorf("motores visíveis: %w", err)
	}
	defer rows.Close()

	ids := map[int64]bool{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids[id] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("motores visíveis: %w", err)
	}
	return ids, nil
}

// FiltrarMotoresVisiveis devolve só os motores de `motores` visíveis a userID
// (nil = só públicos), preservando a ordem. É o açúcar sobre IDsMotoresVisiveis
// usado pelos resolvedores de motor.
func (d *DB) FiltrarMotoresVisiveis(ctx context.Context, motores []Motor, userID *int64) ([]Motor, error) {
	visiveis, err := d.IDsMotoresVisiveis(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]Motor, 0, len(motores))
	for _, m := range motores {
		if visiveis[m.ID] {
			out = append(out, m)
		}
	}
	return out, nil
}

// MotorVisivelPara informa se UM motor é visível a userID (nil = só se público).
// Usado nos caminhos que apontam para um motor específico (motor do grupo de
// usuários nas consultas/planejamentos) — fail-closed: se a ACL esconde o motor
// do criador do trabalho, o chamador cai na cadeia padrão.
func (d *DB) MotorVisivelPara(ctx context.Context, engineID int64, userID *int64) (bool, error) {
	visiveis, err := d.IDsMotoresVisiveis(ctx, userID)
	if err != nil {
		return false, err
	}
	return visiveis[engineID], nil
}
