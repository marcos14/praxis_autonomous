package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Status possíveis de uma consulta (coluna consultas.status — validado na app,
// como o status de demands). A consulta alterna entre ociosa (aguardando o
// usuário) e pensando (o consultor está rodando); falhou registra erro de
// infraestrutura do último turno (o usuário pode reenviar).
const (
	StatusConsultaOciosa   = "ociosa"
	StatusConsultaPensando = "pensando"
	StatusConsultaFalhou   = "falhou"
)

// Papéis de uma fala da consulta (coluna consulta_messages.papel — CHECK no
// banco). Conjunto próprio, separado do chat de demandas: user pergunta, o
// consultor responde (ou pede clarificação), o sistema registra avisos.
const (
	PapelConsultaUser      = "user"
	PapelConsultaConsultor = "consultor"
	PapelConsultaSistema   = "sistema"
)

// PapelConsultaValido informa se p é um papel de fala de consulta conhecido.
func PapelConsultaValido(p string) bool {
	switch p {
	case PapelConsultaUser, PapelConsultaConsultor, PapelConsultaSistema:
		return true
	}
	return false
}

// Consulta é uma linha da tabela consultas: uma conversa de análise de código
// para produto/suporte, vinculada a UM projeto OU UM grupo (exclusivo — CHECK no
// banco). ProjetoNome/GrupoNome são resolvidos por join nas listagens (vazios
// fora delas).
type Consulta struct {
	ID           int64   `json:"id"`
	ProjectID    *int64  `json:"project_id"`
	GroupID      *int64  `json:"group_id"`
	Titulo       string  `json:"titulo"`
	Status       string  `json:"status"`
	CustoUSD     float64 `json:"custo_usd"`
	CriadoPor    *int64  `json:"criado_por"`
	Erro         string  `json:"erro"`
	CriadoEm     string  `json:"criado_em"`
	AtualizadoEm string  `json:"atualizado_em"`
	ProjetoNome  string  `json:"projeto_nome,omitempty"`
	GrupoNome    string  `json:"grupo_nome,omitempty"`
}

// MensagemConsulta é uma linha de consulta_messages. Meta é JSON livre (objeto
// vazio por padrão) — o consultor anexa tipo da fala (perguntas|resposta|recusa),
// custo, motor e a flag de redação do pós-filtro.
type MensagemConsulta struct {
	ID         int64           `json:"id"`
	ConsultaID int64           `json:"consulta_id"`
	Papel      string          `json:"papel"`
	Conteudo   string          `json:"conteudo"`
	Meta       json.RawMessage `json:"meta"`
	CriadoEm   string          `json:"criado_em"`
}

// colunasConsulta lista as colunas de consultas na ordem esperada por scanConsulta.
const colunasConsulta = `id, project_id, group_id, titulo, status, custo_usd,
	criado_por, erro, criado_em, atualizado_em`

// scanConsulta lê uma linha de consultas (na ordem de colunasConsulta) para
// Consulta, tratando as FKs opcionais.
func scanConsulta(sc interface{ Scan(...any) error }) (Consulta, error) {
	var (
		c                  Consulta
		projID, grpID, por sql.NullInt64
	)
	if err := sc.Scan(&c.ID, &projID, &grpID, &c.Titulo, &c.Status, &c.CustoUSD,
		&por, &c.Erro, &c.CriadoEm, &c.AtualizadoEm); err != nil {
		return Consulta{}, err
	}
	c.ProjectID = ptrDeNull(projID)
	c.GroupID = ptrDeNull(grpID)
	c.CriadoPor = ptrDeNull(por)
	return c, nil
}

// CriarConsultaComChat insere a consulta e a primeira fala do usuário numa única
// transação (a consulta nasce como conversa, padrão de CriarDemandaComChat) e
// devolve ambas persistidas. Status em branco cai em ociosa. Projeto/grupo
// inexistente vira ErrNaoEncontrado (violação de FK).
func (d *DB) CriarConsultaComChat(ctx context.Context, c Consulta, primeira MensagemConsulta) (Consulta, MensagemConsulta, error) {
	if strings.TrimSpace(c.Status) == "" {
		c.Status = StatusConsultaOciosa
	}
	if strings.TrimSpace(primeira.Papel) == "" {
		primeira.Papel = PapelConsultaUser
	}
	if !PapelConsultaValido(primeira.Papel) {
		return Consulta{}, MensagemConsulta{}, ErrPapelInvalido
	}

	tx, err := d.Escritor.BeginTx(ctx, nil)
	if err != nil {
		return Consulta{}, MensagemConsulta{}, fmt.Errorf("criar consulta: %w", err)
	}
	defer tx.Rollback()

	row := tx.QueryRowContext(ctx, `
		INSERT INTO consultas (project_id, group_id, titulo, status, custo_usd, criado_por, erro)
		VALUES (?,?,?,?,?,?,?)
		RETURNING id, criado_em, atualizado_em`,
		nullInt(c.ProjectID), nullInt(c.GroupID), c.Titulo, c.Status,
		c.CustoUSD, nullInt(c.CriadoPor), c.Erro,
	)
	if err := row.Scan(&c.ID, &c.CriadoEm, &c.AtualizadoEm); err != nil {
		return Consulta{}, MensagemConsulta{}, traduzirErroFK(err)
	}

	primeira.ConsultaID = c.ID
	meta := normalizarMeta(primeira.Meta)
	mrow := tx.QueryRowContext(ctx, `
		INSERT INTO consulta_messages (consulta_id, papel, conteudo, meta)
		VALUES (?,?,?,?)
		RETURNING id, criado_em`,
		primeira.ConsultaID, primeira.Papel, primeira.Conteudo, meta,
	)
	if err := mrow.Scan(&primeira.ID, &primeira.CriadoEm); err != nil {
		return Consulta{}, MensagemConsulta{}, traduzirErroFK(err)
	}
	primeira.Meta = json.RawMessage(meta)

	if err := tx.Commit(); err != nil {
		return Consulta{}, MensagemConsulta{}, fmt.Errorf("criar consulta: %w", err)
	}
	return c, primeira, nil
}

// ListarConsultas devolve as consultas em ordem de atividade (atualizado_em
// decrescente, id decrescente no empate), com nome do projeto/grupo resolvido
// por join. projectID/groupID > 0 filtram. Slice não-nil.
func (d *DB) ListarConsultas(ctx context.Context, projectID, groupID int64) ([]Consulta, error) {
	where, args := "", []any{}
	switch {
	case projectID > 0:
		where, args = "WHERE c.project_id = ?", []any{projectID}
	case groupID > 0:
		where, args = "WHERE c.group_id = ?", []any{groupID}
	}
	rows, err := d.Leitor.QueryContext(ctx, `
		SELECT c.id, c.project_id, c.group_id, c.titulo, c.status, c.custo_usd,
		       c.criado_por, c.erro, c.criado_em, c.atualizado_em,
		       COALESCE(p.nome, ''), COALESCE(g.nome, '')
		FROM consultas c
		LEFT JOIN projects p       ON p.id = c.project_id
		LEFT JOIN project_groups g ON g.id = c.group_id
		`+where+`
		ORDER BY c.atualizado_em DESC, c.id DESC`, args...)
	if err != nil {
		return nil, fmt.Errorf("listar consultas: %w", err)
	}
	defer rows.Close()

	consultas := []Consulta{}
	for rows.Next() {
		var (
			c                  Consulta
			projID, grpID, por sql.NullInt64
		)
		if err := rows.Scan(&c.ID, &projID, &grpID, &c.Titulo, &c.Status, &c.CustoUSD,
			&por, &c.Erro, &c.CriadoEm, &c.AtualizadoEm, &c.ProjetoNome, &c.GrupoNome); err != nil {
			return nil, err
		}
		c.ProjectID = ptrDeNull(projID)
		c.GroupID = ptrDeNull(grpID)
		c.CriadoPor = ptrDeNull(por)
		consultas = append(consultas, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listar consultas: %w", err)
	}
	return consultas, nil
}

// ObterConsulta devolve a consulta de id. Se não existir, devolve ErrNaoEncontrado.
func (d *DB) ObterConsulta(ctx context.Context, id int64) (Consulta, error) {
	row := d.Leitor.QueryRowContext(ctx,
		`SELECT `+colunasConsulta+` FROM consultas WHERE id = ?`, id)
	c, err := scanConsulta(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Consulta{}, ErrNaoEncontrado
	}
	if err != nil {
		return Consulta{}, fmt.Errorf("obter consulta %d: %w", id, err)
	}
	return c, nil
}

// AtualizarConsulta grava os campos mutáveis da consulta c.ID (título, status,
// custo acumulado, erro) e carimba atualizado_em. Consulta inexistente vira
// ErrNaoEncontrado. Vínculo projeto/grupo e criado_por são imutáveis.
func (d *DB) AtualizarConsulta(ctx context.Context, c Consulta) (Consulta, error) {
	res, err := d.Escritor.ExecContext(ctx, `
		UPDATE consultas SET
			titulo = ?, status = ?, custo_usd = ?, erro = ?,
			atualizado_em = strftime('%Y-%m-%dT%H:%M:%fZ','now')
		WHERE id = ?`,
		c.Titulo, c.Status, c.CustoUSD, c.Erro, c.ID,
	)
	if err != nil {
		return Consulta{}, fmt.Errorf("atualizar consulta %d: %w", c.ID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return Consulta{}, fmt.Errorf("atualizar consulta %d: %w", c.ID, err)
	}
	if n == 0 {
		return Consulta{}, ErrNaoEncontrado
	}
	return d.ObterConsulta(ctx, c.ID)
}

// ExcluirConsulta remove a consulta de id (cascade em mensagens e execuções).
// Consulta inexistente vira ErrNaoEncontrado.
func (d *DB) ExcluirConsulta(ctx context.Context, id int64) error {
	res, err := d.Escritor.ExecContext(ctx, `DELETE FROM consultas WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("excluir consulta %d: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("excluir consulta %d: %w", id, err)
	}
	if n == 0 {
		return ErrNaoEncontrado
	}
	return nil
}

// CriarMensagemConsulta insere uma fala na consulta e devolve a linha persistida.
// Papel em branco cai em user; papel desconhecido vira ErrPapelInvalido; consulta
// inexistente vira ErrNaoEncontrado (violação de FK).
func (d *DB) CriarMensagemConsulta(ctx context.Context, m MensagemConsulta) (MensagemConsulta, error) {
	if strings.TrimSpace(m.Papel) == "" {
		m.Papel = PapelConsultaUser
	}
	if !PapelConsultaValido(m.Papel) {
		return MensagemConsulta{}, ErrPapelInvalido
	}
	meta := normalizarMeta(m.Meta)
	row := d.Escritor.QueryRowContext(ctx, `
		INSERT INTO consulta_messages (consulta_id, papel, conteudo, meta)
		VALUES (?,?,?,?)
		RETURNING id, criado_em`,
		m.ConsultaID, m.Papel, m.Conteudo, meta,
	)
	if err := row.Scan(&m.ID, &m.CriadoEm); err != nil {
		return MensagemConsulta{}, traduzirErroFK(err)
	}
	m.Meta = json.RawMessage(meta)
	return m, nil
}

// ListarMensagensConsulta devolve as falas da consulta em ordem cronológica (id
// crescente). Slice não-nil.
func (d *DB) ListarMensagensConsulta(ctx context.Context, consultaID int64) ([]MensagemConsulta, error) {
	rows, err := d.Leitor.QueryContext(ctx, `
		SELECT id, consulta_id, papel, conteudo, meta, criado_em
		FROM consulta_messages WHERE consulta_id = ? ORDER BY id`, consultaID)
	if err != nil {
		return nil, fmt.Errorf("listar chat da consulta %d: %w", consultaID, err)
	}
	defer rows.Close()

	msgs := []MensagemConsulta{}
	for rows.Next() {
		var (
			m    MensagemConsulta
			meta string
		)
		if err := rows.Scan(&m.ID, &m.ConsultaID, &m.Papel, &m.Conteudo, &meta, &m.CriadoEm); err != nil {
			return nil, err
		}
		if strings.TrimSpace(meta) == "" {
			meta = "{}"
		}
		m.Meta = json.RawMessage(meta)
		msgs = append(msgs, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listar chat da consulta %d: %w", consultaID, err)
	}
	return msgs, nil
}
