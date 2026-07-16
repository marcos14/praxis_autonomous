package db

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
)

// Origens possíveis de uma chave na config efetiva.
const (
	OrigemGlobal  = "global"
	OrigemProjeto = "project"
)

// ValorEfetivo é o valor resolvido de uma chave na config efetiva de um projeto,
// junto com a origem (de onde ele veio: global ou override do projeto).
type ValorEfetivo struct {
	Valor  json.RawMessage `json:"valor"`
	Origem string          `json:"origem"` // OrigemGlobal | OrigemProjeto
}

// ObterConfigGlobal devolve as entradas de config do escopo global como um mapa
// chave→valor (JSON). Nunca devolve nil (mapa vazio quando não há entradas).
func (d *DB) ObterConfigGlobal(ctx context.Context) (map[string]json.RawMessage, error) {
	rows, err := d.Leitor.QueryContext(ctx,
		`SELECT chave, valor FROM config_entries WHERE escopo = 'global' ORDER BY chave`)
	if err != nil {
		return nil, fmt.Errorf("obter config global: %w", err)
	}
	defer rows.Close()
	return escanearConfig(rows)
}

// ObterConfigProjeto devolve as entradas de override do projeto (escopo project)
// como um mapa chave→valor (JSON). Não inclui as chaves globais — é só o override.
// Nunca devolve nil (mapa vazio quando não há overrides).
func (d *DB) ObterConfigProjeto(ctx context.Context, projectID int64) (map[string]json.RawMessage, error) {
	rows, err := d.Leitor.QueryContext(ctx,
		`SELECT chave, valor FROM config_entries WHERE escopo = 'project' AND project_id = ? ORDER BY chave`,
		projectID)
	if err != nil {
		return nil, fmt.Errorf("obter config do projeto %d: %w", projectID, err)
	}
	defer rows.Close()
	return escanearConfig(rows)
}

// ConfigEfetiva resolve a config efetiva do projeto: parte do global e sobrepõe
// as chaves com override do projeto. Cada chave carrega sua origem (global ou
// project). É determinística — a mesma entrada sempre produz o mesmo resultado.
func (d *DB) ConfigEfetiva(ctx context.Context, projectID int64) (map[string]ValorEfetivo, error) {
	global, err := d.ObterConfigGlobal(ctx)
	if err != nil {
		return nil, err
	}
	projeto, err := d.ObterConfigProjeto(ctx, projectID)
	if err != nil {
		return nil, err
	}
	efetiva := make(map[string]ValorEfetivo, len(global)+len(projeto))
	for chave, valor := range global {
		efetiva[chave] = ValorEfetivo{Valor: valor, Origem: OrigemGlobal}
	}
	for chave, valor := range projeto {
		efetiva[chave] = ValorEfetivo{Valor: valor, Origem: OrigemProjeto}
	}
	return efetiva, nil
}

// DefinirConfigGlobal substitui todas as entradas do escopo global pelo mapa
// dado (full replace, atômico). Chaves ausentes no mapa deixam de existir.
func (d *DB) DefinirConfigGlobal(ctx context.Context, entradas map[string]json.RawMessage) error {
	return d.substituirConfig(ctx, OrigemGlobal, nil, entradas)
}

// DefinirConfigProjeto substitui todas as entradas de override do projeto pelo
// mapa dado (full replace, atômico). Remover uma chave do mapa faz o projeto
// voltar a herdar o valor global daquela chave.
func (d *DB) DefinirConfigProjeto(ctx context.Context, projectID int64, entradas map[string]json.RawMessage) error {
	return d.substituirConfig(ctx, OrigemProjeto, &projectID, entradas)
}

// substituirConfig apaga as entradas do escopo (global, ou de um projeto) e
// reinsere as do mapa, tudo em uma transação. A ordem das inserções segue a
// ordem alfabética das chaves para ser determinística.
func (d *DB) substituirConfig(ctx context.Context, escopo string, projectID *int64, entradas map[string]json.RawMessage) error {
	tx, err := d.Escritor.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("definir config: %w", err)
	}
	defer tx.Rollback()

	if escopo == OrigemGlobal {
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM config_entries WHERE escopo = 'global'`); err != nil {
			return fmt.Errorf("definir config global: %w", err)
		}
	} else {
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM config_entries WHERE escopo = 'project' AND project_id = ?`, *projectID); err != nil {
			return fmt.Errorf("definir config do projeto: %w", err)
		}
	}

	for _, chave := range chavesOrdenadas(entradas) {
		valor, err := compactarJSON(entradas[chave])
		if err != nil {
			return fmt.Errorf("codificar valor da chave %q: %w", chave, err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO config_entries (escopo, project_id, chave, valor) VALUES (?,?,?,?)`,
			escopo, projectID, chave, valor); err != nil {
			return fmt.Errorf("gravar chave %q: %w", chave, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("definir config: %w", err)
	}
	return nil
}

// escanearConfig lê linhas (chave, valor) para um mapa não-nil.
func escanearConfig(rows interface {
	Next() bool
	Scan(...any) error
	Err() error
}) (map[string]json.RawMessage, error) {
	m := map[string]json.RawMessage{}
	for rows.Next() {
		var chave, valor string
		if err := rows.Scan(&chave, &valor); err != nil {
			return nil, err
		}
		m[chave] = json.RawMessage(valor)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return m, nil
}

// chavesOrdenadas devolve as chaves do mapa em ordem alfabética (para inserção
// determinística; a serialização JSON de mapa já ordena por conta própria).
func chavesOrdenadas(m map[string]json.RawMessage) []string {
	chaves := make([]string, 0, len(m))
	for k := range m {
		chaves = append(chaves, k)
	}
	sort.Strings(chaves)
	return chaves
}

// compactarJSON valida e compacta o JSON do valor (remove espaços). Valor vazio
// vira "null" (a coluna é NOT NULL e o default é 'null').
func compactarJSON(bruto json.RawMessage) (string, error) {
	if len(bytes.TrimSpace(bruto)) == 0 {
		return "null", nil
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, bruto); err != nil {
		return "", err
	}
	return buf.String(), nil
}
