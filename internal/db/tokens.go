package db

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// Papéis dos tokens de API (coluna api_tokens.papel — CHECK no banco). Ordem de
// privilégio crescente: leitor < operador < admin.
const (
	PapelLeitor   = "leitor"
	PapelOperador = "operador"
	PapelAdmin    = "admin"
)

// PapelTokenValido informa se p é um papel conhecido.
func PapelTokenValido(p string) bool {
	switch p {
	case PapelLeitor, PapelOperador, PapelAdmin:
		return true
	}
	return false
}

// Token é uma linha de api_tokens. TokenHash nunca é serializado (json:"-"); o
// valor em claro só existe no momento da criação (campo Token, transitório).
type Token struct {
	ID         int64  `json:"id"`
	Nome       string `json:"nome"`
	Papel      string `json:"papel"`
	CriadoEm   string `json:"criado_em"`
	RevogadoEm string `json:"revogado_em,omitempty"`
	Token      string `json:"token,omitempty"` // preenchido só na criação (valor em claro, uma vez)
}

// GerarTokenAPI gera um token aleatório url-safe (32 bytes → ~43 chars). Portado
// da ideia do auth.go do Praxis atual (rand + base64 url-safe).
func GerarTokenAPI() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("gerar token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// HashToken devolve o SHA-256 (hex) do token em claro — o que é persistido.
func HashToken(tokenPlano string) string {
	soma := sha256.Sum256([]byte(strings.TrimSpace(tokenPlano)))
	return hex.EncodeToString(soma[:])
}

// CriarToken gera um novo token com o papel dado, persiste apenas o hash e
// devolve a linha com o valor em CLARO no campo Token (mostrado uma única vez).
// Papel inválido → erro.
func (d *DB) CriarToken(ctx context.Context, nome, papel string) (Token, error) {
	nome = strings.TrimSpace(nome)
	if nome == "" {
		return Token{}, errors.New("nome do token é obrigatório")
	}
	if !PapelTokenValido(papel) {
		return Token{}, fmt.Errorf("papel inválido: %q (use leitor, operador ou admin)", papel)
	}
	plano, err := GerarTokenAPI()
	if err != nil {
		return Token{}, err
	}
	hash := HashToken(plano)
	row := d.Escritor.QueryRowContext(ctx, `
		INSERT INTO api_tokens (nome, token_hash, papel)
		VALUES (?,?,?)
		RETURNING id, criado_em`,
		nome, hash, papel)
	var t Token
	if err := row.Scan(&t.ID, &t.CriadoEm); err != nil {
		return Token{}, fmt.Errorf("criar token: %w", err)
	}
	t.Nome = nome
	t.Papel = papel
	t.Token = plano
	return t, nil
}

// ListarTokens devolve os tokens (sem o hash nem o valor em claro), mais recentes
// primeiro. Slice não-nil.
func (d *DB) ListarTokens(ctx context.Context) ([]Token, error) {
	rows, err := d.Leitor.QueryContext(ctx,
		`SELECT id, nome, papel, criado_em, COALESCE(revogado_em,'') FROM api_tokens ORDER BY id DESC`)
	if err != nil {
		return nil, fmt.Errorf("listar tokens: %w", err)
	}
	defer rows.Close()
	tokens := []Token{}
	for rows.Next() {
		var t Token
		if err := rows.Scan(&t.ID, &t.Nome, &t.Papel, &t.CriadoEm, &t.RevogadoEm); err != nil {
			return nil, err
		}
		tokens = append(tokens, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listar tokens: %w", err)
	}
	return tokens, nil
}

// RevogarToken marca o token como revogado (revogado_em = agora). Token
// inexistente → ErrNaoEncontrado. Revogar um já revogado é no-op (200).
func (d *DB) RevogarToken(ctx context.Context, id int64) error {
	res, err := d.Escritor.ExecContext(ctx,
		`UPDATE api_tokens SET revogado_em = strftime('%Y-%m-%dT%H:%M:%fZ','now')
		 WHERE id = ? AND revogado_em IS NULL`, id)
	if err != nil {
		return fmt.Errorf("revogar token %d: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("revogar token %d: %w", id, err)
	}
	if n == 0 {
		// ou não existe, ou já estava revogado: distingue com uma leitura.
		var existe int
		if err := d.Leitor.QueryRowContext(ctx, `SELECT COUNT(*) FROM api_tokens WHERE id = ?`, id).Scan(&existe); err != nil {
			return fmt.Errorf("revogar token %d: %w", id, err)
		}
		if existe == 0 {
			return ErrNaoEncontrado
		}
	}
	return nil
}

// AutenticarToken resolve um token em claro para sua linha ATIVA (não revogada).
// Devolve ErrNaoEncontrado quando não há token ativo com aquele hash. Faz a busca
// pelo hash (o valor em claro nunca é persistido).
func (d *DB) AutenticarToken(ctx context.Context, tokenPlano string) (Token, error) {
	tokenPlano = strings.TrimSpace(tokenPlano)
	if tokenPlano == "" {
		return Token{}, ErrNaoEncontrado
	}
	hash := HashToken(tokenPlano)
	row := d.Leitor.QueryRowContext(ctx,
		`SELECT id, nome, papel, criado_em FROM api_tokens
		 WHERE token_hash = ? AND revogado_em IS NULL`, hash)
	var t Token
	if err := row.Scan(&t.ID, &t.Nome, &t.Papel, &t.CriadoEm); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Token{}, ErrNaoEncontrado
		}
		return Token{}, fmt.Errorf("autenticar token: %w", err)
	}
	return t, nil
}

// ContarTokensAtivos devolve o número de tokens não revogados. O gate de auth usa
// para decidir se a API está em "modo aberto" (nenhum token → acesso local livre)
// ou "modo protegido" (há tokens → papéis são exigidos).
func (d *DB) ContarTokensAtivos(ctx context.Context) (int, error) {
	var n int
	if err := d.Leitor.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM api_tokens WHERE revogado_em IS NULL`).Scan(&n); err != nil {
		return 0, fmt.Errorf("contar tokens ativos: %w", err)
	}
	return n, nil
}
