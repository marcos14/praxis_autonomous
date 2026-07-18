package db

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"
)

// ErrEmailDuplicado indica violação da unicidade de users.email. Os handlers
// mapeiam para HTTP 409.
var ErrEmailDuplicado = errors.New("já existe um usuário com esse e-mail")

// ErrCredenciais é o erro genérico de autenticação (e-mail inexistente, senha
// errada ou usuário inativo). É deliberadamente único: o login nunca revela qual
// dos três ocorreu. Os handlers mapeiam para HTTP 401.
var ErrCredenciais = errors.New("credenciais inválidas")

// ErrPapelInexistente indica que um dos papéis informados ao criar/atualizar um
// usuário não existe. Os handlers mapeiam para HTTP 400.
var ErrPapelInexistente = errors.New("papel informado não existe")

// Usuario é uma linha de users já com os papéis vinculados (id/nome/sistema — as
// permissões dos papéis não são carregadas aqui). senhaHash nunca é serializado.
type Usuario struct {
	ID           int64   `json:"id"`
	Nome         string  `json:"nome"`
	Email        string  `json:"email"`
	Ativo        bool    `json:"ativo"`
	CriadoEm     string  `json:"criado_em"`
	AtualizadoEm string  `json:"atualizado_em"`
	Papeis       []Papel `json:"papeis"`
	// Grupo de usuários (feature de consultas): define o motor/modelo das
	// consultas do usuário. Nulo = sem grupo (motor padrão).
	GrupoID   *int64 `json:"grupo_id"`
	GrupoNome string `json:"grupo_nome,omitempty"`

	senhaHash string // interno; não serializa
}

// normalizarEmail deixa o e-mail em minúsculas e sem espaços nas pontas — o login
// é case-insensitive.
func normalizarEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// ContarUsuarios devolve o número de usuários cadastrados. O gate de auth usa
// para decidir o modo bootstrap (zero usuários → acesso local livre até criar o
// primeiro admin; ≥1 usuário → autenticação exigida).
func (d *DB) ContarUsuarios(ctx context.Context) (int, error) {
	var n int
	if err := d.Leitor.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n); err != nil {
		return 0, fmt.Errorf("contar usuários: %w", err)
	}
	return n, nil
}

// CriarUsuario insere um usuário (senha em claro é hasheada) e vincula os papéis
// dados. Devolve a linha persistida (sem hash). E-mail em uso → ErrEmailDuplicado;
// papel inexistente → ErrPapelInexistente; senha vazia → ErrSenhaVazia.
func (d *DB) CriarUsuario(ctx context.Context, nome, email, senha string, roleIDs []int64) (Usuario, error) {
	nome = strings.TrimSpace(nome)
	email = normalizarEmail(email)
	if nome == "" {
		return Usuario{}, errors.New("nome do usuário é obrigatório")
	}
	if email == "" {
		return Usuario{}, errors.New("e-mail do usuário é obrigatório")
	}
	hash, err := HashSenha(senha)
	if err != nil {
		return Usuario{}, err
	}
	tx, err := d.Escritor.BeginTx(ctx, nil)
	if err != nil {
		return Usuario{}, fmt.Errorf("criar usuário: %w", err)
	}
	defer tx.Rollback()

	var id int64
	if err := tx.QueryRowContext(ctx,
		`INSERT INTO users (nome, email, senha_hash) VALUES (?,?,?) RETURNING id`,
		nome, email, hash).Scan(&id); err != nil {
		return Usuario{}, traduzirErroUsuario(err)
	}
	if err := definirPapeisTx(ctx, tx, id, roleIDs); err != nil {
		return Usuario{}, err
	}
	if err := tx.Commit(); err != nil {
		return Usuario{}, fmt.Errorf("criar usuário: %w", err)
	}
	return d.ObterUsuario(ctx, id)
}

// ObterUsuario devolve o usuário id com seus papéis. Inexistente →
// ErrNaoEncontrado. Não carrega o hash da senha.
func (d *DB) ObterUsuario(ctx context.Context, id int64) (Usuario, error) {
	u, err := scanUsuario(d.Leitor.QueryRowContext(ctx,
		`SELECT id, nome, email, ativo, criado_em, atualizado_em FROM users WHERE id = ?`, id))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Usuario{}, ErrNaoEncontrado
		}
		return Usuario{}, fmt.Errorf("obter usuário: %w", err)
	}
	papeis, err := d.papeisDoUsuario(ctx, id)
	if err != nil {
		return Usuario{}, err
	}
	u.Papeis = papeis
	if g, ok, err := d.GrupoDoUsuario(ctx, id); err != nil {
		return Usuario{}, err
	} else if ok {
		u.GrupoID = &g.ID
		u.GrupoNome = g.Nome
	}
	return u, nil
}

// obterUsuarioPorEmail devolve o usuário (com hash) para autenticação. É interno
// — o hash nunca sai do pacote db.
func (d *DB) obterUsuarioPorEmail(ctx context.Context, email string) (Usuario, error) {
	email = normalizarEmail(email)
	u, err := scanUsuarioComHash(d.Leitor.QueryRowContext(ctx,
		`SELECT id, nome, email, ativo, criado_em, atualizado_em, senha_hash FROM users WHERE email = ?`, email))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Usuario{}, ErrNaoEncontrado
		}
		return Usuario{}, fmt.Errorf("obter usuário por e-mail: %w", err)
	}
	return u, nil
}

// AutenticarUsuario valida e-mail + senha e devolve o usuário (com papéis). Falha
// de qualquer natureza (inexistente, senha errada, inativo) → ErrCredenciais.
func (d *DB) AutenticarUsuario(ctx context.Context, email, senha string) (Usuario, error) {
	u, err := d.obterUsuarioPorEmail(ctx, email)
	if err != nil {
		if errors.Is(err, ErrNaoEncontrado) {
			return Usuario{}, ErrCredenciais
		}
		return Usuario{}, err
	}
	if !u.Ativo || !VerificarSenha(u.senhaHash, senha) {
		return Usuario{}, ErrCredenciais
	}
	return d.ObterUsuario(ctx, u.ID)
}

// IDUsuarioPorEmail devolve o id de um usuário pelo e-mail (normalizado). Usado
// pela CLI de recuperação (reset de senha). Inexistente → ErrNaoEncontrado.
func (d *DB) IDUsuarioPorEmail(ctx context.Context, email string) (int64, error) {
	var id int64
	err := d.Leitor.QueryRowContext(ctx, `SELECT id FROM users WHERE email = ?`, normalizarEmail(email)).Scan(&id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, ErrNaoEncontrado
		}
		return 0, fmt.Errorf("id do usuário por e-mail: %w", err)
	}
	return id, nil
}

// ListarUsuarios devolve todos os usuários (mais antigos primeiro) com seus
// papéis. Slice não-nil, sem hash.
func (d *DB) ListarUsuarios(ctx context.Context) ([]Usuario, error) {
	rows, err := d.Leitor.QueryContext(ctx,
		`SELECT id, nome, email, ativo, criado_em, atualizado_em FROM users ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("listar usuários: %w", err)
	}
	defer rows.Close()
	usuarios := []Usuario{}
	for rows.Next() {
		u, err := scanUsuario(rows)
		if err != nil {
			return nil, err
		}
		usuarios = append(usuarios, u)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listar usuários: %w", err)
	}
	for i := range usuarios {
		papeis, err := d.papeisDoUsuario(ctx, usuarios[i].ID)
		if err != nil {
			return nil, err
		}
		usuarios[i].Papeis = papeis
		if g, ok, err := d.GrupoDoUsuario(ctx, usuarios[i].ID); err != nil {
			return nil, err
		} else if ok {
			usuarios[i].GrupoID = &g.ID
			usuarios[i].GrupoNome = g.Nome
		}
	}
	return usuarios, nil
}

// AtualizarUsuario altera nome, e-mail, ativo e o conjunto de papéis (full
// replace). Não mexe na senha (ver DefinirSenha). Inexistente → ErrNaoEncontrado;
// e-mail em uso por outro → ErrEmailDuplicado.
func (d *DB) AtualizarUsuario(ctx context.Context, id int64, nome, email string, ativo bool, roleIDs []int64) (Usuario, error) {
	nome = strings.TrimSpace(nome)
	email = normalizarEmail(email)
	if nome == "" || email == "" {
		return Usuario{}, errors.New("nome e e-mail são obrigatórios")
	}
	if _, err := d.ObterUsuario(ctx, id); err != nil {
		return Usuario{}, err
	}
	tx, err := d.Escritor.BeginTx(ctx, nil)
	if err != nil {
		return Usuario{}, fmt.Errorf("atualizar usuário: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx,
		`UPDATE users SET nome = ?, email = ?, ativo = ?, atualizado_em = strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE id = ?`,
		nome, email, booleanParaInt(ativo), id); err != nil {
		return Usuario{}, traduzirErroUsuario(err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM user_roles WHERE user_id = ?`, id); err != nil {
		return Usuario{}, fmt.Errorf("atualizar usuário: %w", err)
	}
	if err := definirPapeisTx(ctx, tx, id, roleIDs); err != nil {
		return Usuario{}, err
	}
	if err := tx.Commit(); err != nil {
		return Usuario{}, fmt.Errorf("atualizar usuário: %w", err)
	}
	return d.ObterUsuario(ctx, id)
}

// DefinirSenha troca a senha do usuário id (hasheando a nova). Inexistente →
// ErrNaoEncontrado; senha vazia → ErrSenhaVazia.
func (d *DB) DefinirSenha(ctx context.Context, id int64, nova string) error {
	hash, err := HashSenha(nova)
	if err != nil {
		return err
	}
	res, err := d.Escritor.ExecContext(ctx,
		`UPDATE users SET senha_hash = ?, atualizado_em = strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE id = ?`,
		hash, id)
	if err != nil {
		return fmt.Errorf("definir senha: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNaoEncontrado
	}
	return nil
}

// ExcluirUsuario remove o usuário id (CASCADE limpa user_roles). Inexistente →
// ErrNaoEncontrado.
func (d *DB) ExcluirUsuario(ctx context.Context, id int64) error {
	res, err := d.Escritor.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("excluir usuário: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNaoEncontrado
	}
	return nil
}

// PermissoesDoUsuario devolve o conjunto (união) de permissões do usuário a
// partir de todos os seus papéis. Mapa não-nil (vazio se o usuário não tem papéis
// ou só papéis sem permissões). Não inclui PermVisualizar (implícita na app).
func (d *DB) PermissoesDoUsuario(ctx context.Context, userID int64) (map[string]bool, error) {
	rows, err := d.Leitor.QueryContext(ctx, `
		SELECT DISTINCT rp.permissao
		FROM user_roles ur
		JOIN role_permissions rp ON rp.role_id = ur.role_id
		WHERE ur.user_id = ?`, userID)
	if err != nil {
		return nil, fmt.Errorf("permissões do usuário: %w", err)
	}
	defer rows.Close()
	perms := map[string]bool{}
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		perms[p] = true
	}
	return perms, rows.Err()
}

// ContarAdmins devolve quantos usuários ATIVOS possuem a permissão curinga (`*`),
// i.e. são administradores plenos. Os handlers usam para impedir a remoção/
// desativação do último admin (anti-lockout).
func (d *DB) ContarAdmins(ctx context.Context) (int, error) {
	var n int
	if err := d.Leitor.QueryRowContext(ctx, `
		SELECT COUNT(DISTINCT ur.user_id)
		FROM user_roles ur
		JOIN role_permissions rp ON rp.role_id = ur.role_id
		JOIN users u ON u.id = ur.user_id
		WHERE rp.permissao = ? AND u.ativo = 1`, PermCuringa).Scan(&n); err != nil {
		return 0, fmt.Errorf("contar admins: %w", err)
	}
	return n, nil
}

// papeisDoUsuario devolve os papéis vinculados ao usuário (id/nome/descrição/
// sistema; sem as permissões). Slice não-nil.
func (d *DB) papeisDoUsuario(ctx context.Context, userID int64) ([]Papel, error) {
	rows, err := d.Leitor.QueryContext(ctx, `
		SELECT r.id, r.nome, r.descricao, r.sistema, r.criado_em
		FROM user_roles ur JOIN roles r ON r.id = ur.role_id
		WHERE ur.user_id = ? ORDER BY r.id`, userID)
	if err != nil {
		return nil, fmt.Errorf("papéis do usuário: %w", err)
	}
	defer rows.Close()
	papeis := []Papel{}
	for rows.Next() {
		p, err := scanPapel(rows)
		if err != nil {
			return nil, err
		}
		papeis = append(papeis, p)
	}
	return papeis, rows.Err()
}

// definirPapeisTx vincula o usuário aos papéis dados, dentro de uma transação.
// Assume que os vínculos antigos já foram removidos (ou que é uma criação). Papel
// inexistente (violação de FK) → ErrPapelInexistente. Ids repetidos são ignorados.
func definirPapeisTx(ctx context.Context, tx *sql.Tx, userID int64, roleIDs []int64) error {
	visto := map[int64]bool{}
	for _, rid := range roleIDs {
		if rid <= 0 || visto[rid] {
			continue
		}
		visto[rid] = true
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO user_roles (user_id, role_id) VALUES (?,?)`, userID, rid); err != nil {
			if strings.Contains(err.Error(), "FOREIGN KEY") {
				return ErrPapelInexistente
			}
			return fmt.Errorf("vincular papel %d: %w", rid, err)
		}
	}
	return nil
}

// scanUsuario lê uma linha de users (sem hash).
func scanUsuario(sc interface{ Scan(...any) error }) (Usuario, error) {
	var (
		u     Usuario
		ativo int
	)
	if err := sc.Scan(&u.ID, &u.Nome, &u.Email, &ativo, &u.CriadoEm, &u.AtualizadoEm); err != nil {
		return Usuario{}, err
	}
	u.Ativo = ativo != 0
	u.Papeis = []Papel{}
	return u, nil
}

// scanUsuarioComHash é scanUsuario com a coluna senha_hash ao final (uso interno
// de autenticação).
func scanUsuarioComHash(sc interface{ Scan(...any) error }) (Usuario, error) {
	var (
		u     Usuario
		ativo int
	)
	if err := sc.Scan(&u.ID, &u.Nome, &u.Email, &ativo, &u.CriadoEm, &u.AtualizadoEm, &u.senhaHash); err != nil {
		return Usuario{}, err
	}
	u.Ativo = ativo != 0
	u.Papeis = []Papel{}
	return u, nil
}

// traduzirErroUsuario converte a violação de unicidade de users.email em sentinela.
func traduzirErroUsuario(err error) error {
	if err == nil {
		return nil
	}
	if strings.Contains(err.Error(), "users.email") {
		return ErrEmailDuplicado
	}
	return fmt.Errorf("persistir usuário: %w", err)
}

// nomeEnvJWTSecret é a variável de ambiente que, se definida, sobrescreve o
// segredo de assinatura do JWT gravado no banco (útil para rotação ou para
// compartilhar o segredo entre instâncias).
const nomeEnvJWTSecret = "PRAXIS_JWT_SECRET"

// ObterOuGerarJWTSecret devolve o segredo de assinatura do JWT. Precedência:
//  1. variável de ambiente PRAXIS_JWT_SECRET (usada verbatim, em bytes);
//  2. o segredo persistido em auth_config (linha única);
//  3. um novo segredo aleatório de 32 bytes, gerado e persistido na primeira vez.
func (d *DB) ObterOuGerarJWTSecret(ctx context.Context) ([]byte, error) {
	if v := strings.TrimSpace(os.Getenv(nomeEnvJWTSecret)); v != "" {
		return []byte(v), nil
	}
	var b64 string
	err := d.Leitor.QueryRowContext(ctx, `SELECT jwt_secret FROM auth_config WHERE id = 1`).Scan(&b64)
	if err == nil {
		if secret, derr := base64.StdEncoding.DecodeString(b64); derr == nil && len(secret) > 0 {
			return secret, nil
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("ler segredo do jwt: %w", err)
	}
	// Gera e persiste um segredo novo (idempotente: INSERT OR IGNORE na linha 1).
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil, fmt.Errorf("gerar segredo do jwt: %w", err)
	}
	novo := base64.StdEncoding.EncodeToString(secret)
	if _, err := d.Escritor.ExecContext(ctx,
		`INSERT INTO auth_config (id, jwt_secret) VALUES (1, ?)
		 ON CONFLICT(id) DO NOTHING`, novo); err != nil {
		return nil, fmt.Errorf("gravar segredo do jwt: %w", err)
	}
	// Relê para cobrir a corrida em que outra instância inseriu primeiro.
	if err := d.Leitor.QueryRowContext(ctx, `SELECT jwt_secret FROM auth_config WHERE id = 1`).Scan(&b64); err != nil {
		return nil, fmt.Errorf("reler segredo do jwt: %w", err)
	}
	secret, err = base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, fmt.Errorf("decodificar segredo do jwt: %w", err)
	}
	return secret, nil
}
