package db

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// Notificacao é uma linha de notificacoes: a caixa de entrada de um usuário
// (M4 do PLANO_INTERNET). O despachante grava uma por evento destinado ao
// usuário; o SSE por usuário e o sino da UI leem daqui. Rota é o hash da UI
// que abre o item ("#demandas/12"); PushEm carimba quando o Web Push saiu.
type Notificacao struct {
	ID       int64  `json:"id"`
	UserID   int64  `json:"user_id"`
	EventID  *int64 `json:"event_id"`
	Tipo     string `json:"tipo"`
	Titulo   string `json:"titulo"`
	Detalhe  string `json:"detalhe"`
	Rota     string `json:"rota"`
	LidaEm   string `json:"lida_em,omitempty"`
	PushEm   string `json:"push_em,omitempty"`
	CriadoEm string `json:"criado_em"`
}

// colunasNotificacao lista as colunas na ordem esperada por scanNotificacao.
const colunasNotificacao = `id, user_id, event_id, tipo, titulo, detalhe, rota,
	COALESCE(lida_em, ''), COALESCE(push_em, ''), criado_em`

func scanNotificacao(sc interface{ Scan(...any) error }) (Notificacao, error) {
	var (
		n       Notificacao
		eventID sql.NullInt64
	)
	if err := sc.Scan(&n.ID, &n.UserID, &eventID, &n.Tipo, &n.Titulo, &n.Detalhe, &n.Rota,
		&n.LidaEm, &n.PushEm, &n.CriadoEm); err != nil {
		return Notificacao{}, err
	}
	n.EventID = ptrDeNull(eventID)
	return n, nil
}

// CriarNotificacao insere uma notificação e devolve a linha persistida.
// Usuário inexistente vira ErrNaoEncontrado (FK).
func (d *DB) CriarNotificacao(ctx context.Context, n Notificacao) (Notificacao, error) {
	if n.UserID <= 0 || strings.TrimSpace(n.Tipo) == "" {
		return Notificacao{}, ErrValorInvalido
	}
	row := d.Escritor.QueryRowContext(ctx, `
		INSERT INTO notificacoes (user_id, event_id, tipo, titulo, detalhe, rota)
		VALUES (?,?,?,?,?,?)
		RETURNING id, criado_em`,
		n.UserID, nullInt(n.EventID), n.Tipo, n.Titulo, n.Detalhe, n.Rota)
	if err := row.Scan(&n.ID, &n.CriadoEm); err != nil {
		return Notificacao{}, traduzirErroFK(err)
	}
	return n, nil
}

// ListarNotificacoes devolve as notificações do usuário, mais recentes
// primeiro; soNaoLidas filtra; limite <= 0 aplica 100. Slice não-nil.
func (d *DB) ListarNotificacoes(ctx context.Context, userID int64, soNaoLidas bool, limite int) ([]Notificacao, error) {
	if limite <= 0 {
		limite = 100
	}
	cond := "user_id = ?"
	if soNaoLidas {
		cond += " AND lida_em IS NULL"
	}
	rows, err := d.Leitor.QueryContext(ctx,
		`SELECT `+colunasNotificacao+` FROM notificacoes WHERE `+cond+` ORDER BY id DESC LIMIT ?`,
		userID, limite)
	if err != nil {
		return nil, fmt.Errorf("listar notificações: %w", err)
	}
	defer rows.Close()
	return lerNotificacoes(rows)
}

// NotificacoesApos devolve as notificações do usuário com id > aposID em ordem
// crescente — o tailing do SSE por usuário. limite <= 0 aplica 200.
func (d *DB) NotificacoesApos(ctx context.Context, userID, aposID int64, limite int) ([]Notificacao, error) {
	if limite <= 0 {
		limite = 200
	}
	rows, err := d.Leitor.QueryContext(ctx,
		`SELECT `+colunasNotificacao+` FROM notificacoes WHERE user_id = ? AND id > ? ORDER BY id ASC LIMIT ?`,
		userID, aposID, limite)
	if err != nil {
		return nil, fmt.Errorf("notificações após %d: %w", aposID, err)
	}
	defer rows.Close()
	return lerNotificacoes(rows)
}

func lerNotificacoes(rows *sql.Rows) ([]Notificacao, error) {
	lista := []Notificacao{}
	for rows.Next() {
		n, err := scanNotificacao(rows)
		if err != nil {
			return nil, err
		}
		lista = append(lista, n)
	}
	return lista, rows.Err()
}

// UltimaNotificacaoID devolve o maior id de notificação do usuário (0 se
// nenhuma) — cursor inicial do SSE (só transmite o que vier depois).
func (d *DB) UltimaNotificacaoID(ctx context.Context, userID int64) (int64, error) {
	var id sql.NullInt64
	if err := d.Leitor.QueryRowContext(ctx,
		`SELECT MAX(id) FROM notificacoes WHERE user_id = ?`, userID).Scan(&id); err != nil {
		return 0, fmt.Errorf("última notificação: %w", err)
	}
	return id.Int64, nil
}

// ContarNaoLidas devolve quantas notificações do usuário ainda não foram lidas
// (o badge do sino).
func (d *DB) ContarNaoLidas(ctx context.Context, userID int64) (int, error) {
	var n int
	if err := d.Leitor.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM notificacoes WHERE user_id = ? AND lida_em IS NULL`, userID).Scan(&n); err != nil {
		return 0, fmt.Errorf("contar não lidas: %w", err)
	}
	return n, nil
}

// MarcarNotificacaoLida marca a notificação id do usuário como lida.
// Inexistente — ou de outro usuário — vira ErrNaoEncontrado; já lida é no-op.
func (d *DB) MarcarNotificacaoLida(ctx context.Context, id, userID int64) error {
	res, err := d.Escritor.ExecContext(ctx,
		`UPDATE notificacoes SET lida_em = ? WHERE id = ? AND user_id = ? AND lida_em IS NULL`,
		agoraISO(), id, userID)
	if err != nil {
		return fmt.Errorf("marcar notificação %d lida: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("marcar notificação %d lida: %w", id, err)
	}
	if n == 0 {
		var existe int
		if err := d.Leitor.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM notificacoes WHERE id = ? AND user_id = ?`, id, userID).Scan(&existe); err != nil {
			return fmt.Errorf("marcar notificação %d lida: %w", id, err)
		}
		if existe == 0 {
			return ErrNaoEncontrado
		}
	}
	return nil
}

// MarcarTodasLidas marca todas as notificações do usuário como lidas e devolve
// quantas mudaram.
func (d *DB) MarcarTodasLidas(ctx context.Context, userID int64) (int64, error) {
	res, err := d.Escritor.ExecContext(ctx,
		`UPDATE notificacoes SET lida_em = ? WHERE user_id = ? AND lida_em IS NULL`, agoraISO(), userID)
	if err != nil {
		return 0, fmt.Errorf("marcar todas lidas: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("marcar todas lidas: %w", err)
	}
	return n, nil
}

// MarcarPushEnviado carimba push_em (auditoria de que o Web Push saiu).
func (d *DB) MarcarPushEnviado(ctx context.Context, id int64) error {
	if _, err := d.Escritor.ExecContext(ctx,
		`UPDATE notificacoes SET push_em = ? WHERE id = ?`, agoraISO(), id); err != nil {
		return fmt.Errorf("marcar push da notificação %d: %w", id, err)
	}
	return nil
}

// RemoverNotificacoesAntigas apaga as lidas criadas antes de corteLidas e as
// não lidas criadas antes de corteNaoLidas (retenção). Devolve quantas saíram.
func (d *DB) RemoverNotificacoesAntigas(ctx context.Context, corteLidas, corteNaoLidas time.Time) (int64, error) {
	res, err := d.Escritor.ExecContext(ctx, `
		DELETE FROM notificacoes
		WHERE (lida_em IS NOT NULL AND criado_em < ?)
		   OR (lida_em IS NULL AND criado_em < ?)`,
		corteLidas.UTC().Format(formatoISO), corteNaoLidas.UTC().Format(formatoISO))
	if err != nil {
		return 0, fmt.Errorf("remover notificações antigas: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("remover notificações antigas: %w", err)
	}
	return n, nil
}
