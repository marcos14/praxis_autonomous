package db

import (
	"context"
	"fmt"
	"strings"
)

// BackupPara grava uma cópia consistente do banco em `destino` via `VACUUM INTO`
// (backup online do SQLite — seguro mesmo com o serviço no ar, em WAL). O
// destino não pode existir (o SQLite recusa sobrescrever). Alimenta a rotina de
// backup periódico da Fase 5d.
func (d *DB) BackupPara(ctx context.Context, destino string) error {
	if strings.TrimSpace(destino) == "" {
		return fmt.Errorf("backup: destino vazio")
	}
	// VACUUM INTO não aceita placeholder; o caminho vem do serviço (não de
	// entrada externa). Escapa aspas simples para não quebrar o literal SQL.
	seguro := strings.ReplaceAll(destino, "'", "''")
	if _, err := d.Escritor.ExecContext(ctx, "VACUUM INTO '"+seguro+"'"); err != nil {
		return fmt.Errorf("backup do banco: %w", err)
	}
	return nil
}
