package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// ErrPapelDuplicado indica violação da unicidade de roles.nome. Os handlers
// mapeiam para HTTP 409.
var ErrPapelDuplicado = errors.New("já existe um papel com esse nome")

// ErrPapelSistema indica tentativa de editar ou excluir um papel de sistema
// (sistema=1, ex.: o papel `admin`). Os handlers mapeiam para HTTP 409.
var ErrPapelSistema = errors.New("papel de sistema não pode ser alterado ou removido")

// ErrPermissaoInvalida indica que uma permissão fora do catálogo foi informada
// na criação/edição de um papel. Os handlers mapeiam para HTTP 400.
var ErrPermissaoInvalida = errors.New("permissão desconhecida")

// Papel é uma linha de roles já com suas permissões (union de role_permissions).
type Papel struct {
	ID         int64    `json:"id"`
	Nome       string   `json:"nome"`
	Descricao  string   `json:"descricao"`
	Sistema    bool     `json:"sistema"`
	CriadoEm   string   `json:"criado_em"`
	Permissoes []string `json:"permissoes"`
}

// validarPermissoes confere que toda permissão pertence ao catálogo (ou é o
// curinga) e devolve a lista deduplicada. Lista vazia é válida (papel só-leitura).
func validarPermissoes(perms []string) ([]string, error) {
	visto := map[string]bool{}
	out := []string{}
	for _, p := range perms {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if !PermissaoValida(p) {
			return nil, fmt.Errorf("%w: %q", ErrPermissaoInvalida, p)
		}
		if !visto[p] {
			visto[p] = true
			out = append(out, p)
		}
	}
	return out, nil
}

// ListarPapeis devolve todos os papéis (mais antigos primeiro) com suas
// permissões carregadas. Slice não-nil.
func (d *DB) ListarPapeis(ctx context.Context) ([]Papel, error) {
	rows, err := d.Leitor.QueryContext(ctx,
		`SELECT id, nome, descricao, sistema, criado_em FROM roles ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("listar papéis: %w", err)
	}
	defer rows.Close()
	papeis := []Papel{}
	indice := map[int64]int{}
	for rows.Next() {
		p, err := scanPapel(rows)
		if err != nil {
			return nil, err
		}
		indice[p.ID] = len(papeis)
		papeis = append(papeis, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listar papéis: %w", err)
	}
	if len(papeis) == 0 {
		return papeis, nil
	}
	// Carrega todas as permissões de uma vez e agrupa por papel (evita N+1).
	prows, err := d.Leitor.QueryContext(ctx,
		`SELECT role_id, permissao FROM role_permissions ORDER BY role_id, permissao`)
	if err != nil {
		return nil, fmt.Errorf("listar permissões: %w", err)
	}
	defer prows.Close()
	for prows.Next() {
		var rid int64
		var perm string
		if err := prows.Scan(&rid, &perm); err != nil {
			return nil, err
		}
		if i, ok := indice[rid]; ok {
			papeis[i].Permissoes = append(papeis[i].Permissoes, perm)
		}
	}
	if err := prows.Err(); err != nil {
		return nil, fmt.Errorf("listar permissões: %w", err)
	}
	return papeis, nil
}

// IDPapelPorNome devolve o id de um papel pelo nome (usado, p.ex., para vincular
// o papel de sistema `admin` ao primeiro usuário no setup). Inexistente →
// ErrNaoEncontrado.
func (d *DB) IDPapelPorNome(ctx context.Context, nome string) (int64, error) {
	var id int64
	err := d.Leitor.QueryRowContext(ctx, `SELECT id FROM roles WHERE nome = ?`, strings.TrimSpace(nome)).Scan(&id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, ErrNaoEncontrado
		}
		return 0, fmt.Errorf("id do papel %q: %w", nome, err)
	}
	return id, nil
}

// ObterPapel devolve um papel com suas permissões. Inexistente → ErrNaoEncontrado.
func (d *DB) ObterPapel(ctx context.Context, id int64) (Papel, error) {
	p, err := scanPapel(d.Leitor.QueryRowContext(ctx,
		`SELECT id, nome, descricao, sistema, criado_em FROM roles WHERE id = ?`, id))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Papel{}, ErrNaoEncontrado
		}
		return Papel{}, fmt.Errorf("obter papel: %w", err)
	}
	perms, err := d.permissoesDoPapel(ctx, id)
	if err != nil {
		return Papel{}, err
	}
	p.Permissoes = perms
	return p, nil
}

// permissoesDoPapel devolve as permissões de um papel (slice não-nil, ordenada).
func (d *DB) permissoesDoPapel(ctx context.Context, roleID int64) ([]string, error) {
	rows, err := d.Leitor.QueryContext(ctx,
		`SELECT permissao FROM role_permissions WHERE role_id = ? ORDER BY permissao`, roleID)
	if err != nil {
		return nil, fmt.Errorf("permissões do papel: %w", err)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// scanPapel lê uma linha de roles (sem as permissões).
func scanPapel(sc interface{ Scan(...any) error }) (Papel, error) {
	var (
		p       Papel
		sistema int
	)
	if err := sc.Scan(&p.ID, &p.Nome, &p.Descricao, &sistema, &p.CriadoEm); err != nil {
		return Papel{}, err
	}
	p.Sistema = sistema != 0
	p.Permissoes = []string{}
	return p, nil
}

// CriarPapel insere um novo papel com as permissões dadas (validadas) e devolve a
// linha persistida. Nome duplicado → ErrPapelDuplicado. Permissão fora do
// catálogo → ErrPermissaoInvalida.
func (d *DB) CriarPapel(ctx context.Context, nome, descricao string, perms []string) (Papel, error) {
	nome = strings.TrimSpace(nome)
	if nome == "" {
		return Papel{}, errors.New("nome do papel é obrigatório")
	}
	limpas, err := validarPermissoes(perms)
	if err != nil {
		return Papel{}, err
	}
	tx, err := d.Escritor.BeginTx(ctx, nil)
	if err != nil {
		return Papel{}, fmt.Errorf("criar papel: %w", err)
	}
	defer tx.Rollback()

	var id int64
	if err := tx.QueryRowContext(ctx,
		`INSERT INTO roles (nome, descricao, sistema) VALUES (?,?,0) RETURNING id`,
		nome, strings.TrimSpace(descricao)).Scan(&id); err != nil {
		return Papel{}, traduzirErroPapel(err)
	}
	if err := inserirPermissoes(ctx, tx, id, limpas); err != nil {
		return Papel{}, err
	}
	if err := tx.Commit(); err != nil {
		return Papel{}, fmt.Errorf("criar papel: %w", err)
	}
	return d.ObterPapel(ctx, id)
}

// AtualizarPapel substitui nome, descrição e permissões de um papel (full
// replace das permissões). Papel de sistema → ErrPapelSistema. Inexistente →
// ErrNaoEncontrado. Nome em uso por outro → ErrPapelDuplicado.
func (d *DB) AtualizarPapel(ctx context.Context, id int64, nome, descricao string, perms []string) (Papel, error) {
	nome = strings.TrimSpace(nome)
	if nome == "" {
		return Papel{}, errors.New("nome do papel é obrigatório")
	}
	limpas, err := validarPermissoes(perms)
	if err != nil {
		return Papel{}, err
	}
	existente, err := d.ObterPapel(ctx, id)
	if err != nil {
		return Papel{}, err
	}
	if existente.Sistema {
		return Papel{}, ErrPapelSistema
	}
	tx, err := d.Escritor.BeginTx(ctx, nil)
	if err != nil {
		return Papel{}, fmt.Errorf("atualizar papel: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx,
		`UPDATE roles SET nome = ?, descricao = ? WHERE id = ?`,
		nome, strings.TrimSpace(descricao), id); err != nil {
		return Papel{}, traduzirErroPapel(err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM role_permissions WHERE role_id = ?`, id); err != nil {
		return Papel{}, fmt.Errorf("atualizar papel: %w", err)
	}
	if err := inserirPermissoes(ctx, tx, id, limpas); err != nil {
		return Papel{}, err
	}
	if err := tx.Commit(); err != nil {
		return Papel{}, fmt.Errorf("atualizar papel: %w", err)
	}
	return d.ObterPapel(ctx, id)
}

// ExcluirPapel remove um papel (e, por CASCADE, seus vínculos em user_roles e
// role_permissions). Papel de sistema → ErrPapelSistema. Inexistente →
// ErrNaoEncontrado.
func (d *DB) ExcluirPapel(ctx context.Context, id int64) error {
	existente, err := d.ObterPapel(ctx, id)
	if err != nil {
		return err
	}
	if existente.Sistema {
		return ErrPapelSistema
	}
	if _, err := d.Escritor.ExecContext(ctx, `DELETE FROM roles WHERE id = ?`, id); err != nil {
		return fmt.Errorf("excluir papel: %w", err)
	}
	return nil
}

// inserirPermissoes grava as permissões de um papel dentro de uma transação.
func inserirPermissoes(ctx context.Context, tx *sql.Tx, roleID int64, perms []string) error {
	for _, p := range perms {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO role_permissions (role_id, permissao) VALUES (?,?)`, roleID, p); err != nil {
			return fmt.Errorf("gravar permissão %q: %w", p, err)
		}
	}
	return nil
}

// traduzirErroPapel converte a violação de unicidade de roles.nome em sentinela.
func traduzirErroPapel(err error) error {
	if err == nil {
		return nil
	}
	if strings.Contains(err.Error(), "roles.nome") {
		return ErrPapelDuplicado
	}
	return fmt.Errorf("persistir papel: %w", err)
}
