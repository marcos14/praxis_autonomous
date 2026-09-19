package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// ErrPapelInvalido indica um papel de mensagem de chat fora do conjunto aceito
// (CHECK no banco). Os handlers mapeiam para 400.
var ErrPapelInvalido = errors.New("papel de mensagem inválido")

// Papéis possíveis de uma fala do chat da demanda (coluna chat_messages.papel —
// CHECK no banco). A demanda nasce como conversa: o usuário cola o PRD e
// complementa (user); o analista e o planejador respondem; o sistema registra
// marcos (criada, análise iniciada…).
const (
	PapelUser       = "user"
	PapelAnalista   = "analista"
	PapelPlanejador = "planejador"
	PapelSistema    = "sistema"
)

// PapelValido informa se p é um papel de chat conhecido.
func PapelValido(p string) bool {
	switch p {
	case PapelUser, PapelAnalista, PapelPlanejador, PapelSistema:
		return true
	}
	return false
}

// MensagemChat é uma linha da tabela chat_messages. Meta é JSON livre (objeto
// vazio por padrão) usado para anexar contexto de uma fala (ex.: custo/motor da
// análise); nunca fica nil na serialização.
type MensagemChat struct {
	ID       int64           `json:"id"`
	DemandID int64           `json:"demand_id"`
	Papel    string          `json:"papel"`
	Conteudo string          `json:"conteudo"`
	Meta     json.RawMessage `json:"meta"`
	CriadoEm string          `json:"criado_em"`
}

// colunasChat lista as colunas de chat_messages na ordem esperada por scanChat.
const colunasChat = `id, demand_id, papel, conteudo, meta, criado_em`

// scanChat lê uma linha de chat_messages (na ordem de colunasChat) para
// MensagemChat, mantendo Meta como JSON cru (default "{}").
func scanChat(sc interface{ Scan(...any) error }) (MensagemChat, error) {
	var (
		m    MensagemChat
		meta string
	)
	if err := sc.Scan(&m.ID, &m.DemandID, &m.Papel, &m.Conteudo, &meta, &m.CriadoEm); err != nil {
		return MensagemChat{}, err
	}
	if strings.TrimSpace(meta) == "" {
		meta = "{}"
	}
	m.Meta = json.RawMessage(meta)
	return m, nil
}

// normalizarMeta devolve o JSON de meta pronto para persistir (objeto vazio
// quando ausente/em branco).
func normalizarMeta(meta json.RawMessage) string {
	s := strings.TrimSpace(string(meta))
	if s == "" {
		return "{}"
	}
	return s
}

// CriarMensagemChat insere uma fala no chat da demanda e devolve a linha
// persistida (com id e criado_em preenchidos pelo banco). Papel em branco cai em
// user; papel desconhecido vira ErrPapelInvalido; demanda inexistente vira
// ErrNaoEncontrado (violação de FK).
func (d *DB) CriarMensagemChat(ctx context.Context, m MensagemChat) (MensagemChat, error) {
	if strings.TrimSpace(m.Papel) == "" {
		m.Papel = PapelUser
	}
	if !PapelValido(m.Papel) {
		return MensagemChat{}, ErrPapelInvalido
	}
	meta := normalizarMeta(m.Meta)
	row := d.Escritor.QueryRowContext(ctx, `
		INSERT INTO chat_messages (demand_id, papel, conteudo, meta)
		VALUES (?,?,?,?)
		RETURNING id, criado_em`,
		m.DemandID, m.Papel, m.Conteudo, meta,
	)
	if err := row.Scan(&m.ID, &m.CriadoEm); err != nil {
		return MensagemChat{}, traduzirErroFK(err)
	}
	m.Meta = json.RawMessage(meta)
	return m, nil
}

// ListarMensagensChat devolve as falas do chat da demanda em ordem cronológica
// (id crescente — a conversa como o usuário a lê). Slice não-nil.
func (d *DB) ListarMensagensChat(ctx context.Context, demandID int64) ([]MensagemChat, error) {
	rows, err := d.Leitor.QueryContext(ctx,
		`SELECT `+colunasChat+` FROM chat_messages WHERE demand_id = ? ORDER BY id`, demandID)
	if err != nil {
		return nil, fmt.Errorf("listar chat da demanda %d: %w", demandID, err)
	}
	defer rows.Close()

	msgs := []MensagemChat{}
	for rows.Next() {
		m, err := scanChat(rows)
		if err != nil {
			return nil, err
		}
		msgs = append(msgs, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listar chat da demanda %d: %w", demandID, err)
	}
	return msgs, nil
}

// CriarDemandaComChat insere uma demanda e a primeira fala do seu chat numa única
// transação (tudo ou nada) e devolve ambas persistidas. É o caminho canônico de
// criação de uma demanda pela tela "Nova demanda" (Fase 3a): a demanda nasce como
// conversa, com o PRD colado como a primeira mensagem.
//
// Origem/status vazios caem nos defaults (ui/recebida). Papel da mensagem em
// branco cai em user; papel desconhecido vira ErrPapelInvalido. Projeto
// inexistente vira ErrNaoEncontrado.
func (d *DB) CriarDemandaComChat(ctx context.Context, dem Demanda, primeira MensagemChat) (Demanda, MensagemChat, error) {
	if strings.TrimSpace(dem.Origem) == "" {
		dem.Origem = OrigemUI
	}
	if strings.TrimSpace(dem.Status) == "" {
		dem.Status = StatusDemandaRecebida
	}
	if strings.TrimSpace(primeira.Papel) == "" {
		primeira.Papel = PapelUser
	}
	if !PapelValido(primeira.Papel) {
		return Demanda{}, MensagemChat{}, ErrPapelInvalido
	}
	if err := normalizarVisibilidade(&dem); err != nil {
		return Demanda{}, MensagemChat{}, err
	}

	tx, err := d.Escritor.BeginTx(ctx, nil)
	if err != nil {
		return Demanda{}, MensagemChat{}, fmt.Errorf("criar demanda com chat: %w", err)
	}
	defer tx.Rollback()

	row := tx.QueryRowContext(ctx, `
		INSERT INTO demands
			(project_id, titulo, origem, origem_ref, status, prioridade,
			 branch, worktree_path, plano_md, custo_usd, budget_usd, erro, criado_por, visibilidade)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		RETURNING id, criado_em, atualizado_em`,
		dem.ProjectID, dem.Titulo, dem.Origem, dem.OrigemRef, dem.Status, dem.Prioridade,
		dem.Branch, dem.WorktreePath, dem.PlanoMD, dem.CustoUSD, dem.BudgetUSD, dem.Erro,
		nullInt(dem.CriadoPor), dem.Visibilidade,
	)
	if err := row.Scan(&dem.ID, &dem.CriadoEm, &dem.AtualizadoEm); err != nil {
		return Demanda{}, MensagemChat{}, traduzirErroFK(err)
	}

	primeira.DemandID = dem.ID
	meta := normalizarMeta(primeira.Meta)
	mrow := tx.QueryRowContext(ctx, `
		INSERT INTO chat_messages (demand_id, papel, conteudo, meta)
		VALUES (?,?,?,?)
		RETURNING id, criado_em`,
		primeira.DemandID, primeira.Papel, primeira.Conteudo, meta,
	)
	if err := mrow.Scan(&primeira.ID, &primeira.CriadoEm); err != nil {
		return Demanda{}, MensagemChat{}, traduzirErroFK(err)
	}
	primeira.Meta = json.RawMessage(meta)

	if err := tx.Commit(); err != nil {
		return Demanda{}, MensagemChat{}, fmt.Errorf("criar demanda com chat: %w", err)
	}
	return dem, primeira, nil
}
