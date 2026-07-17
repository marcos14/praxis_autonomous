package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

// ArquivosProvaveisDemanda devolve os arquivos_provaveis apontados pelo analista
// para a demanda (Fase 5c): lê o campo meta.arquivos_provaveis da fala mais
// recente do analista no chat. Sem análise ainda → slice vazio (não é erro).
func (d *DB) ArquivosProvaveisDemanda(ctx context.Context, demandID int64) ([]string, error) {
	var meta string
	err := d.Leitor.QueryRowContext(ctx,
		`SELECT meta FROM chat_messages
		 WHERE demand_id = ? AND papel = ?
		 ORDER BY id DESC LIMIT 1`, demandID, PapelAnalista).Scan(&meta)
	if errors.Is(err, sql.ErrNoRows) {
		return []string{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("arquivos prováveis da demanda %d: %w", demandID, err)
	}
	var m struct {
		Arquivos []string `json:"arquivos_provaveis"`
	}
	if err := json.Unmarshal([]byte(meta), &m); err != nil {
		return []string{}, nil // meta malformado não deve quebrar a detecção
	}
	if m.Arquivos == nil {
		return []string{}, nil
	}
	return m.Arquivos, nil
}
