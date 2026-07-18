package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// ErrGrupoUsuariosDuplicado indica violação da unicidade de user_groups.nome.
// Os handlers mapeiam para HTTP 409.
var ErrGrupoUsuariosDuplicado = errors.New("já existe um grupo de usuários com esse nome")

// GrupoUsuarios é uma linha de user_groups: um conjunto de usuários que
// compartilha o motor/modelo das CONSULTAS (o rigor da consulta pode ser menor
// que o de análise/execução — o grupo permite, por exemplo, dar um modelo mais
// barato ao time de suporte). EngineID nulo = motor padrão (ordem de fallback);
// Modelo vazio = modelo_consulta do motor. Usuarios traz os nomes dos membros
// (para a UI), resolvido por join nas listagens.
type GrupoUsuarios struct {
	ID        int64    `json:"id"`
	Nome      string   `json:"nome"`
	Descricao string   `json:"descricao"`
	EngineID  *int64   `json:"engine_id"`
	Modelo    string   `json:"modelo"`
	CriadoEm  string   `json:"criado_em"`
	Usuarios  []string `json:"usuarios"`
}

// colunasGrupoUsuarios lista as colunas de user_groups na ordem esperada por
// scanGrupoUsuarios.
const colunasGrupoUsuarios = `id, nome, descricao, engine_id, modelo, criado_em`

func scanGrupoUsuarios(sc interface{ Scan(...any) error }) (GrupoUsuarios, error) {
	var (
		g   GrupoUsuarios
		eng sql.NullInt64
	)
	if err := sc.Scan(&g.ID, &g.Nome, &g.Descricao, &eng, &g.Modelo, &g.CriadoEm); err != nil {
		return GrupoUsuarios{}, err
	}
	g.EngineID = ptrDeNull(eng)
	g.Usuarios = []string{}
	return g, nil
}

// CriarGrupoUsuarios insere um grupo e devolve a linha persistida. Nome em uso
// vira ErrGrupoUsuariosDuplicado; motor inexistente vira ErrNaoEncontrado (FK).
func (d *DB) CriarGrupoUsuarios(ctx context.Context, g GrupoUsuarios) (GrupoUsuarios, error) {
	row := d.Escritor.QueryRowContext(ctx, `
		INSERT INTO user_groups (nome, descricao, engine_id, modelo)
		VALUES (?,?,?,?)
		RETURNING id, criado_em`,
		g.Nome, g.Descricao, nullInt(g.EngineID), g.Modelo,
	)
	if err := row.Scan(&g.ID, &g.CriadoEm); err != nil {
		return GrupoUsuarios{}, traduzirErroGrupoUsuarios(err)
	}
	g.Usuarios = []string{}
	return g, nil
}

// ListarGruposUsuarios devolve todos os grupos (com os nomes dos membros)
// ordenados por nome. Slice não-nil.
func (d *DB) ListarGruposUsuarios(ctx context.Context) ([]GrupoUsuarios, error) {
	rows, err := d.Leitor.QueryContext(ctx,
		`SELECT `+colunasGrupoUsuarios+` FROM user_groups ORDER BY nome COLLATE NOCASE, id`)
	if err != nil {
		return nil, fmt.Errorf("listar grupos de usuários: %w", err)
	}
	defer rows.Close()

	grupos := []GrupoUsuarios{}
	indice := map[int64]int{}
	for rows.Next() {
		g, err := scanGrupoUsuarios(rows)
		if err != nil {
			return nil, err
		}
		indice[g.ID] = len(grupos)
		grupos = append(grupos, g)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listar grupos de usuários: %w", err)
	}
	if len(grupos) == 0 {
		return grupos, nil
	}

	mrows, err := d.Leitor.QueryContext(ctx, `
		SELECT m.group_id, u.nome
		FROM user_group_members m JOIN users u ON u.id = m.user_id
		ORDER BY u.nome COLLATE NOCASE`)
	if err != nil {
		return nil, fmt.Errorf("membros dos grupos de usuários: %w", err)
	}
	defer mrows.Close()
	for mrows.Next() {
		var (
			gid  int64
			nome string
		)
		if err := mrows.Scan(&gid, &nome); err != nil {
			return nil, err
		}
		if i, ok := indice[gid]; ok {
			grupos[i].Usuarios = append(grupos[i].Usuarios, nome)
		}
	}
	return grupos, mrows.Err()
}

// ObterGrupoUsuarios devolve o grupo de id (sem a lista de membros — use a
// listagem para a UI). Inexistente → ErrNaoEncontrado.
func (d *DB) ObterGrupoUsuarios(ctx context.Context, id int64) (GrupoUsuarios, error) {
	row := d.Leitor.QueryRowContext(ctx,
		`SELECT `+colunasGrupoUsuarios+` FROM user_groups WHERE id = ?`, id)
	g, err := scanGrupoUsuarios(row)
	if errors.Is(err, sql.ErrNoRows) {
		return GrupoUsuarios{}, ErrNaoEncontrado
	}
	if err != nil {
		return GrupoUsuarios{}, fmt.Errorf("obter grupo de usuários %d: %w", id, err)
	}
	return g, nil
}

// AtualizarGrupoUsuarios grava os campos editáveis do grupo g.ID. Inexistente →
// ErrNaoEncontrado; nome em uso → ErrGrupoUsuariosDuplicado.
func (d *DB) AtualizarGrupoUsuarios(ctx context.Context, g GrupoUsuarios) (GrupoUsuarios, error) {
	res, err := d.Escritor.ExecContext(ctx, `
		UPDATE user_groups SET nome = ?, descricao = ?, engine_id = ?, modelo = ?
		WHERE id = ?`,
		g.Nome, g.Descricao, nullInt(g.EngineID), g.Modelo, g.ID,
	)
	if err != nil {
		return GrupoUsuarios{}, traduzirErroGrupoUsuarios(err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return GrupoUsuarios{}, fmt.Errorf("atualizar grupo de usuários %d: %w", g.ID, err)
	}
	if n == 0 {
		return GrupoUsuarios{}, ErrNaoEncontrado
	}
	return d.ObterGrupoUsuarios(ctx, g.ID)
}

// ExcluirGrupoUsuarios remove o grupo (cascade limpa os vínculos; os usuários
// ficam sem grupo e voltam ao motor padrão). Inexistente → ErrNaoEncontrado.
func (d *DB) ExcluirGrupoUsuarios(ctx context.Context, id int64) error {
	res, err := d.Escritor.ExecContext(ctx, `DELETE FROM user_groups WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("excluir grupo de usuários %d: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("excluir grupo de usuários %d: %w", id, err)
	}
	if n == 0 {
		return ErrNaoEncontrado
	}
	return nil
}

// DefinirGrupoDoUsuario vincula o usuário ao grupo (substituindo o vínculo
// anterior — um usuário pertence a no máximo um grupo) ou remove o vínculo
// quando groupID é nil. Usuário/grupo inexistente → ErrNaoEncontrado (FK).
func (d *DB) DefinirGrupoDoUsuario(ctx context.Context, userID int64, groupID *int64) error {
	if groupID == nil {
		if _, err := d.Escritor.ExecContext(ctx,
			`DELETE FROM user_group_members WHERE user_id = ?`, userID); err != nil {
			return fmt.Errorf("desvincular grupo do usuário %d: %w", userID, err)
		}
		return nil
	}
	if _, err := d.Escritor.ExecContext(ctx, `
		INSERT INTO user_group_members (user_id, group_id) VALUES (?,?)
		ON CONFLICT(user_id) DO UPDATE SET group_id = excluded.group_id`,
		userID, *groupID); err != nil {
		return traduzirErroFK(err)
	}
	return nil
}

// GrupoDoUsuario devolve o grupo do usuário (ok=false quando o usuário não tem
// grupo). É a consulta que o consultor usa para resolver motor/modelo.
func (d *DB) GrupoDoUsuario(ctx context.Context, userID int64) (GrupoUsuarios, bool, error) {
	row := d.Leitor.QueryRowContext(ctx, `
		SELECT `+prefixarColunas("g", colunasGrupoUsuarios)+`
		FROM user_group_members m JOIN user_groups g ON g.id = m.group_id
		WHERE m.user_id = ?`, userID)
	g, err := scanGrupoUsuarios(row)
	if errors.Is(err, sql.ErrNoRows) {
		return GrupoUsuarios{}, false, nil
	}
	if err != nil {
		return GrupoUsuarios{}, false, fmt.Errorf("grupo do usuário %d: %w", userID, err)
	}
	return g, true, nil
}

// prefixarColunas prefixa cada coluna de uma lista "a, b, c" com o alias da
// tabela ("g.a, g.b, g.c") para joins.
func prefixarColunas(alias, colunas string) string {
	partes := strings.Split(colunas, ",")
	for i, p := range partes {
		partes[i] = alias + "." + strings.TrimSpace(p)
	}
	return strings.Join(partes, ", ")
}

// traduzirErroGrupoUsuarios converte violações conhecidas em erros sentinela.
func traduzirErroGrupoUsuarios(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	if strings.Contains(msg, "user_groups.nome") {
		return ErrGrupoUsuariosDuplicado
	}
	if strings.Contains(msg, "FOREIGN KEY") {
		return ErrNaoEncontrado
	}
	return fmt.Errorf("persistir grupo de usuários: %w", err)
}
