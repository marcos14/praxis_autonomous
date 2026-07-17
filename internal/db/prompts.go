package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// Prompt é um template de prompt de harness guardado no banco (override do
// default embutido). Nome é a chave estável (ex.: "analista", "planejador");
// Conteudo é o markdown com marcadores {VAR} renderizados na hora do run.
type Prompt struct {
	Nome         string `json:"nome"`
	Conteudo     string `json:"conteudo"`
	AtualizadoEm string `json:"atualizado_em"`
}

// ObterPrompt devolve o override do prompt de nome. Ausente → ErrNaoEncontrado
// (o chamador cai no default embutido). O nome é normalizado (minúsculas, sem
// espaços) para casar com como os prompts são salvos.
func (d *DB) ObterPrompt(ctx context.Context, nome string) (Prompt, error) {
	nome = normalizarNomePrompt(nome)
	row := d.Leitor.QueryRowContext(ctx,
		`SELECT nome, conteudo, atualizado_em FROM prompts WHERE nome = ?`, nome)
	var p Prompt
	err := row.Scan(&p.Nome, &p.Conteudo, &p.AtualizadoEm)
	if errors.Is(err, sql.ErrNoRows) {
		return Prompt{}, ErrNaoEncontrado
	}
	if err != nil {
		return Prompt{}, fmt.Errorf("obter prompt %q: %w", nome, err)
	}
	return p, nil
}

// SalvarPrompt cria ou atualiza o override do prompt de nome (upsert) e devolve a
// linha persistida. Conteúdo vazio é rejeitado (para apagar um override, use
// RemoverPrompt e volte ao default embutido).
func (d *DB) SalvarPrompt(ctx context.Context, nome, conteudo string) (Prompt, error) {
	nome = normalizarNomePrompt(nome)
	if nome == "" {
		return Prompt{}, fmt.Errorf("nome do prompt é obrigatório")
	}
	if strings.TrimSpace(conteudo) == "" {
		return Prompt{}, fmt.Errorf("conteúdo do prompt é obrigatório")
	}
	_, err := d.Escritor.ExecContext(ctx, `
		INSERT INTO prompts (nome, conteudo, atualizado_em)
		VALUES (?, ?, strftime('%Y-%m-%dT%H:%M:%fZ','now'))
		ON CONFLICT(nome) DO UPDATE SET
			conteudo = excluded.conteudo,
			atualizado_em = excluded.atualizado_em`,
		nome, conteudo)
	if err != nil {
		return Prompt{}, fmt.Errorf("salvar prompt %q: %w", nome, err)
	}
	return d.ObterPrompt(ctx, nome)
}

// RemoverPrompt apaga o override do prompt de nome (volta ao default embutido).
// Prompt inexistente vira ErrNaoEncontrado.
func (d *DB) RemoverPrompt(ctx context.Context, nome string) error {
	nome = normalizarNomePrompt(nome)
	res, err := d.Escritor.ExecContext(ctx, `DELETE FROM prompts WHERE nome = ?`, nome)
	if err != nil {
		return fmt.Errorf("remover prompt %q: %w", nome, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("remover prompt %q: %w", nome, err)
	}
	if n == 0 {
		return ErrNaoEncontrado
	}
	return nil
}

// ListarPrompts devolve os overrides de prompt cadastrados, em ordem alfabética
// de nome. Slice não-nil (a tela de Configurações combina com os defaults).
func (d *DB) ListarPrompts(ctx context.Context) ([]Prompt, error) {
	rows, err := d.Leitor.QueryContext(ctx,
		`SELECT nome, conteudo, atualizado_em FROM prompts ORDER BY nome`)
	if err != nil {
		return nil, fmt.Errorf("listar prompts: %w", err)
	}
	defer rows.Close()
	prompts := []Prompt{}
	for rows.Next() {
		var p Prompt
		if err := rows.Scan(&p.Nome, &p.Conteudo, &p.AtualizadoEm); err != nil {
			return nil, err
		}
		prompts = append(prompts, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listar prompts: %w", err)
	}
	return prompts, nil
}

// normalizarNomePrompt padroniza a chave do prompt (minúsculas, sem espaços).
func normalizarNomePrompt(nome string) string {
	return strings.ToLower(strings.TrimSpace(nome))
}
