package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// ErrValorInvalido sinaliza um valor de enum fora do domínio (foco, nível
// visual, arquivo vazio…) — o handler traduz para 400.
var ErrValorInvalido = errors.New("valor inválido")

// Status possíveis de um planejamento (coluna planejamentos.status — validado na
// app, como o status de consultas). O planejamento alterna entre ocioso
// (aguardando o usuário) e pensando (o estrategista está rodando); falhou
// registra erro de infraestrutura do último turno (o usuário pode reenviar).
const (
	StatusPlanejamentoOcioso   = "ocioso"
	StatusPlanejamentoPensando = "pensando"
	StatusPlanejamentoFalhou   = "falhou"
)

// Papéis de uma fala do planejamento (coluna planejamento_messages.papel —
// CHECK no banco). Conjunto próprio: user pede, o estrategista trabalha os
// documentos e responde, o sistema registra avisos.
const (
	PapelPlanejamentoUser         = "user"
	PapelPlanejamentoEstrategista = "estrategista"
	PapelPlanejamentoSistema      = "sistema"
)

// Focos de um planejamento (coluna planejamentos.foco — CHECK no banco): o que
// o estrategista produz — PRD de negócio, ADRs arquiteturais ou ambos.
const (
	FocoPlanejamentoPRD   = "prd"
	FocoPlanejamentoADR   = "adr"
	FocoPlanejamentoAmbos = "ambos"
)

// Níveis visuais de um planejamento (coluna planejamentos.nivel_visual — CHECK
// no banco): documento (só .md), apresentacao (+ html com infográficos) ou
// prototipo (+ mockup navegável).
const (
	NivelVisualDocumento    = "documento"
	NivelVisualApresentacao = "apresentacao"
	NivelVisualPrototipo    = "prototipo"
)

// PapelPlanejamentoValido informa se p é um papel de fala de planejamento conhecido.
func PapelPlanejamentoValido(p string) bool {
	switch p {
	case PapelPlanejamentoUser, PapelPlanejamentoEstrategista, PapelPlanejamentoSistema:
		return true
	}
	return false
}

// FocoPlanejamentoValido informa se f é um foco de planejamento conhecido.
func FocoPlanejamentoValido(f string) bool {
	switch f {
	case FocoPlanejamentoPRD, FocoPlanejamentoADR, FocoPlanejamentoAmbos:
		return true
	}
	return false
}

// NivelVisualValido informa se n é um nível visual conhecido.
func NivelVisualValido(n string) bool {
	switch n {
	case NivelVisualDocumento, NivelVisualApresentacao, NivelVisualPrototipo:
		return true
	}
	return false
}

// Planejamento é uma linha da tabela planejamentos: uma sessão iterativa do
// estrategista (PRD/ADR) vinculada a UM projeto OU UM grupo (exclusivo — CHECK
// no banco). As demandas geradas nos handoffs vivem em planejamento_demandas
// (um planejamento gera N demandas); DemandasCriadas é a contagem, resolvida
// por subconsulta nas leituras. ProjetoNome/GrupoNome vêm de join nas listagens.
type Planejamento struct {
	ID              int64   `json:"id"`
	ProjectID       *int64  `json:"project_id"`
	GroupID         *int64  `json:"group_id"`
	Titulo          string  `json:"titulo"`
	Foco            string  `json:"foco"`
	NivelVisual     string  `json:"nivel_visual"`
	Status          string  `json:"status"`
	CustoUSD        float64 `json:"custo_usd"`
	CriadoPor       *int64  `json:"criado_por"`
	DemandasCriadas int64   `json:"demandas_criadas"`
	Erro            string  `json:"erro"`
	CriadoEm        string  `json:"criado_em"`
	AtualizadoEm    string  `json:"atualizado_em"`
	ProjetoNome     string  `json:"projeto_nome,omitempty"`
	GrupoNome       string  `json:"grupo_nome,omitempty"`
}

// MensagemPlanejamento é uma linha de planejamento_messages. Meta é JSON livre
// (objeto vazio por padrão) — o estrategista anexa tipo da fala, custo, motor,
// confiança e os documentos/artefatos alterados no turno.
type MensagemPlanejamento struct {
	ID             int64           `json:"id"`
	PlanejamentoID int64           `json:"planejamento_id"`
	Papel          string          `json:"papel"`
	Conteudo       string          `json:"conteudo"`
	Meta           json.RawMessage `json:"meta"`
	CriadoEm       string          `json:"criado_em"`
}

// DocumentoPlanejamento é uma revisão de um documento canônico (.md) do
// planejamento — linha de planejamento_documentos. Cada turno que altera o
// arquivo grava uma revisão nova (histórico completo no banco).
type DocumentoPlanejamento struct {
	ID             int64  `json:"id"`
	PlanejamentoID int64  `json:"planejamento_id"`
	Arquivo        string `json:"arquivo"`
	Revisao        int64  `json:"revisao"`
	Conteudo       string `json:"conteudo"`
	CriadoEm       string `json:"criado_em"`
}

// ArtefatoPlanejamento é o índice de um artefato visual (.html autocontido)
// gravado na pasta do planejamento — linha de planejamento_artefatos. Só a
// versão corrente fica no disco; revisao conta as regravações.
type ArtefatoPlanejamento struct {
	ID             int64  `json:"id"`
	PlanejamentoID int64  `json:"planejamento_id"`
	Arquivo        string `json:"arquivo"`
	Titulo         string `json:"titulo"`
	Descricao      string `json:"descricao"`
	Tamanho        int64  `json:"tamanho"`
	Hash           string `json:"hash"`
	Revisao        int64  `json:"revisao"`
	CriadoEm       string `json:"criado_em"`
	AtualizadoEm   string `json:"atualizado_em"`
}

// colunasPlanejamento lista as colunas de planejamentos na ordem esperada por
// scanPlanejamento (a última é a contagem de demandas geradas; demand_id é
// legado da migração 13 e não é mais lido — a verdade está em
// planejamento_demandas).
const colunasPlanejamento = `id, project_id, group_id, titulo, foco, nivel_visual, status,
	custo_usd, criado_por, erro, criado_em, atualizado_em,
	(SELECT COUNT(*) FROM planejamento_demandas pd WHERE pd.planejamento_id = planejamentos.id)`

// scanPlanejamento lê uma linha de planejamentos (na ordem de
// colunasPlanejamento) para Planejamento, tratando as FKs opcionais.
func scanPlanejamento(sc interface{ Scan(...any) error }) (Planejamento, error) {
	var (
		p                  Planejamento
		projID, grpID, por sql.NullInt64
	)
	if err := sc.Scan(&p.ID, &projID, &grpID, &p.Titulo, &p.Foco, &p.NivelVisual,
		&p.Status, &p.CustoUSD, &por, &p.Erro, &p.CriadoEm, &p.AtualizadoEm,
		&p.DemandasCriadas); err != nil {
		return Planejamento{}, err
	}
	p.ProjectID = ptrDeNull(projID)
	p.GroupID = ptrDeNull(grpID)
	p.CriadoPor = ptrDeNull(por)
	return p, nil
}

// CriarPlanejamentoComChat insere o planejamento e a primeira fala do usuário
// numa única transação (nasce como conversa, padrão de CriarConsultaComChat) e
// devolve ambos persistidos. Status em branco cai em ocioso; foco e nível visual
// em branco caem nos defaults do schema (prd/apresentacao); valores inválidos
// viram ErrValorInvalido. Projeto/grupo inexistente vira ErrNaoEncontrado.
func (d *DB) CriarPlanejamentoComChat(ctx context.Context, p Planejamento, primeira MensagemPlanejamento) (Planejamento, MensagemPlanejamento, error) {
	if strings.TrimSpace(p.Status) == "" {
		p.Status = StatusPlanejamentoOcioso
	}
	if strings.TrimSpace(p.Foco) == "" {
		p.Foco = FocoPlanejamentoPRD
	}
	if strings.TrimSpace(p.NivelVisual) == "" {
		p.NivelVisual = NivelVisualApresentacao
	}
	if !FocoPlanejamentoValido(p.Foco) || !NivelVisualValido(p.NivelVisual) {
		return Planejamento{}, MensagemPlanejamento{}, ErrValorInvalido
	}
	if strings.TrimSpace(primeira.Papel) == "" {
		primeira.Papel = PapelPlanejamentoUser
	}
	if !PapelPlanejamentoValido(primeira.Papel) {
		return Planejamento{}, MensagemPlanejamento{}, ErrPapelInvalido
	}

	tx, err := d.Escritor.BeginTx(ctx, nil)
	if err != nil {
		return Planejamento{}, MensagemPlanejamento{}, fmt.Errorf("criar planejamento: %w", err)
	}
	defer tx.Rollback()

	row := tx.QueryRowContext(ctx, `
		INSERT INTO planejamentos (project_id, group_id, titulo, foco, nivel_visual, status, custo_usd, criado_por, erro)
		VALUES (?,?,?,?,?,?,?,?,?)
		RETURNING id, criado_em, atualizado_em`,
		nullInt(p.ProjectID), nullInt(p.GroupID), p.Titulo, p.Foco, p.NivelVisual,
		p.Status, p.CustoUSD, nullInt(p.CriadoPor), p.Erro,
	)
	if err := row.Scan(&p.ID, &p.CriadoEm, &p.AtualizadoEm); err != nil {
		return Planejamento{}, MensagemPlanejamento{}, traduzirErroFK(err)
	}

	primeira.PlanejamentoID = p.ID
	meta := normalizarMeta(primeira.Meta)
	mrow := tx.QueryRowContext(ctx, `
		INSERT INTO planejamento_messages (planejamento_id, papel, conteudo, meta)
		VALUES (?,?,?,?)
		RETURNING id, criado_em`,
		primeira.PlanejamentoID, primeira.Papel, primeira.Conteudo, meta,
	)
	if err := mrow.Scan(&primeira.ID, &primeira.CriadoEm); err != nil {
		return Planejamento{}, MensagemPlanejamento{}, traduzirErroFK(err)
	}
	primeira.Meta = json.RawMessage(meta)

	if err := tx.Commit(); err != nil {
		return Planejamento{}, MensagemPlanejamento{}, fmt.Errorf("criar planejamento: %w", err)
	}
	return p, primeira, nil
}

// ListarPlanejamentos devolve os planejamentos em ordem de atividade
// (atualizado_em decrescente, id decrescente no empate), com nome do
// projeto/grupo resolvido por join. projectID/groupID > 0 filtram. Slice não-nil.
func (d *DB) ListarPlanejamentos(ctx context.Context, projectID, groupID int64) ([]Planejamento, error) {
	where, args := "", []any{}
	switch {
	case projectID > 0:
		where, args = "WHERE pl.project_id = ?", []any{projectID}
	case groupID > 0:
		where, args = "WHERE pl.group_id = ?", []any{groupID}
	}
	rows, err := d.Leitor.QueryContext(ctx, `
		SELECT pl.id, pl.project_id, pl.group_id, pl.titulo, pl.foco, pl.nivel_visual,
		       pl.status, pl.custo_usd, pl.criado_por, pl.erro,
		       pl.criado_em, pl.atualizado_em,
		       (SELECT COUNT(*) FROM planejamento_demandas pd WHERE pd.planejamento_id = pl.id),
		       COALESCE(p.nome, ''), COALESCE(g.nome, '')
		FROM planejamentos pl
		LEFT JOIN projects p       ON p.id = pl.project_id
		LEFT JOIN project_groups g ON g.id = pl.group_id
		`+where+`
		ORDER BY pl.atualizado_em DESC, pl.id DESC`, args...)
	if err != nil {
		return nil, fmt.Errorf("listar planejamentos: %w", err)
	}
	defer rows.Close()

	planejamentos := []Planejamento{}
	for rows.Next() {
		var (
			p                  Planejamento
			projID, grpID, por sql.NullInt64
		)
		if err := rows.Scan(&p.ID, &projID, &grpID, &p.Titulo, &p.Foco, &p.NivelVisual,
			&p.Status, &p.CustoUSD, &por, &p.Erro, &p.CriadoEm, &p.AtualizadoEm,
			&p.DemandasCriadas, &p.ProjetoNome, &p.GrupoNome); err != nil {
			return nil, err
		}
		p.ProjectID = ptrDeNull(projID)
		p.GroupID = ptrDeNull(grpID)
		p.CriadoPor = ptrDeNull(por)
		planejamentos = append(planejamentos, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listar planejamentos: %w", err)
	}
	return planejamentos, nil
}

// ObterPlanejamento devolve o planejamento de id. Se não existir, devolve
// ErrNaoEncontrado.
func (d *DB) ObterPlanejamento(ctx context.Context, id int64) (Planejamento, error) {
	row := d.Leitor.QueryRowContext(ctx,
		`SELECT `+colunasPlanejamento+` FROM planejamentos WHERE id = ?`, id)
	p, err := scanPlanejamento(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Planejamento{}, ErrNaoEncontrado
	}
	if err != nil {
		return Planejamento{}, fmt.Errorf("obter planejamento %d: %w", id, err)
	}
	return p, nil
}

// AtualizarPlanejamento grava os campos mutáveis do planejamento p.ID (título,
// foco, nível visual, status, custo acumulado, erro) e carimba atualizado_em.
// Foco/nível inválidos viram ErrValorInvalido; planejamento inexistente vira
// ErrNaoEncontrado. Vínculo projeto/grupo e criado_por são imutáveis; as
// demandas geradas vivem em planejamento_demandas.
func (d *DB) AtualizarPlanejamento(ctx context.Context, p Planejamento) (Planejamento, error) {
	if !FocoPlanejamentoValido(p.Foco) || !NivelVisualValido(p.NivelVisual) {
		return Planejamento{}, ErrValorInvalido
	}
	res, err := d.Escritor.ExecContext(ctx, `
		UPDATE planejamentos SET
			titulo = ?, foco = ?, nivel_visual = ?, status = ?, custo_usd = ?, erro = ?,
			atualizado_em = strftime('%Y-%m-%dT%H:%M:%fZ','now')
		WHERE id = ?`,
		p.Titulo, p.Foco, p.NivelVisual, p.Status, p.CustoUSD, p.Erro, p.ID,
	)
	if err != nil {
		return Planejamento{}, fmt.Errorf("atualizar planejamento %d: %w", p.ID, traduzirErroFK(err))
	}
	n, err := res.RowsAffected()
	if err != nil {
		return Planejamento{}, fmt.Errorf("atualizar planejamento %d: %w", p.ID, err)
	}
	if n == 0 {
		return Planejamento{}, ErrNaoEncontrado
	}
	return d.ObterPlanejamento(ctx, p.ID)
}

// ExcluirPlanejamento remove o planejamento de id (cascade em mensagens,
// documentos, artefatos e execuções). A pasta no disco é responsabilidade do
// chamador. Planejamento inexistente vira ErrNaoEncontrado.
func (d *DB) ExcluirPlanejamento(ctx context.Context, id int64) error {
	res, err := d.Escritor.ExecContext(ctx, `DELETE FROM planejamentos WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("excluir planejamento %d: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("excluir planejamento %d: %w", id, err)
	}
	if n == 0 {
		return ErrNaoEncontrado
	}
	return nil
}

// CriarMensagemPlanejamento insere uma fala no planejamento e devolve a linha
// persistida. Papel em branco cai em user; papel desconhecido vira
// ErrPapelInvalido; planejamento inexistente vira ErrNaoEncontrado.
func (d *DB) CriarMensagemPlanejamento(ctx context.Context, m MensagemPlanejamento) (MensagemPlanejamento, error) {
	if strings.TrimSpace(m.Papel) == "" {
		m.Papel = PapelPlanejamentoUser
	}
	if !PapelPlanejamentoValido(m.Papel) {
		return MensagemPlanejamento{}, ErrPapelInvalido
	}
	meta := normalizarMeta(m.Meta)
	row := d.Escritor.QueryRowContext(ctx, `
		INSERT INTO planejamento_messages (planejamento_id, papel, conteudo, meta)
		VALUES (?,?,?,?)
		RETURNING id, criado_em`,
		m.PlanejamentoID, m.Papel, m.Conteudo, meta,
	)
	if err := row.Scan(&m.ID, &m.CriadoEm); err != nil {
		return MensagemPlanejamento{}, traduzirErroFK(err)
	}
	m.Meta = json.RawMessage(meta)
	return m, nil
}

// ListarMensagensPlanejamento devolve as falas do planejamento em ordem
// cronológica (id crescente). Slice não-nil.
func (d *DB) ListarMensagensPlanejamento(ctx context.Context, planejamentoID int64) ([]MensagemPlanejamento, error) {
	rows, err := d.Leitor.QueryContext(ctx, `
		SELECT id, planejamento_id, papel, conteudo, meta, criado_em
		FROM planejamento_messages WHERE planejamento_id = ? ORDER BY id`, planejamentoID)
	if err != nil {
		return nil, fmt.Errorf("listar chat do planejamento %d: %w", planejamentoID, err)
	}
	defer rows.Close()

	msgs := []MensagemPlanejamento{}
	for rows.Next() {
		var (
			m    MensagemPlanejamento
			meta string
		)
		if err := rows.Scan(&m.ID, &m.PlanejamentoID, &m.Papel, &m.Conteudo, &meta, &m.CriadoEm); err != nil {
			return nil, err
		}
		if strings.TrimSpace(meta) == "" {
			meta = "{}"
		}
		m.Meta = json.RawMessage(meta)
		msgs = append(msgs, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listar chat do planejamento %d: %w", planejamentoID, err)
	}
	return msgs, nil
}

// SalvarRevisaoDocumento grava uma revisão nova do documento (revisao = última
// + 1, começando em 1) e a devolve persistida. A comparação "mudou de verdade?"
// é responsabilidade do chamador (estrategista) — o store sempre grava.
func (d *DB) SalvarRevisaoDocumento(ctx context.Context, planejamentoID int64, arquivo, conteudo string) (DocumentoPlanejamento, error) {
	arquivo = strings.TrimSpace(arquivo)
	if arquivo == "" {
		return DocumentoPlanejamento{}, ErrValorInvalido
	}
	doc := DocumentoPlanejamento{PlanejamentoID: planejamentoID, Arquivo: arquivo, Conteudo: conteudo}
	row := d.Escritor.QueryRowContext(ctx, `
		INSERT INTO planejamento_documentos (planejamento_id, arquivo, revisao, conteudo)
		VALUES (?,?,
			COALESCE((SELECT MAX(revisao) FROM planejamento_documentos
			          WHERE planejamento_id = ? AND arquivo = ?), 0) + 1,
			?)
		RETURNING id, revisao, criado_em`,
		planejamentoID, arquivo, planejamentoID, arquivo, conteudo,
	)
	if err := row.Scan(&doc.ID, &doc.Revisao, &doc.CriadoEm); err != nil {
		return DocumentoPlanejamento{}, traduzirErroFK(err)
	}
	return doc, nil
}

// ObterDocumentoPlanejamento devolve a revisão pedida do documento — revisao <= 0
// devolve a mais recente. Documento inexistente vira ErrNaoEncontrado.
func (d *DB) ObterDocumentoPlanejamento(ctx context.Context, planejamentoID int64, arquivo string, revisao int64) (DocumentoPlanejamento, error) {
	consulta := `SELECT id, planejamento_id, arquivo, revisao, conteudo, criado_em
		FROM planejamento_documentos WHERE planejamento_id = ? AND arquivo = ?`
	args := []any{planejamentoID, arquivo}
	if revisao > 0 {
		consulta += ` AND revisao = ?`
		args = append(args, revisao)
	}
	consulta += ` ORDER BY revisao DESC LIMIT 1`

	var doc DocumentoPlanejamento
	err := d.Leitor.QueryRowContext(ctx, consulta, args...).Scan(
		&doc.ID, &doc.PlanejamentoID, &doc.Arquivo, &doc.Revisao, &doc.Conteudo, &doc.CriadoEm)
	if errors.Is(err, sql.ErrNoRows) {
		return DocumentoPlanejamento{}, ErrNaoEncontrado
	}
	if err != nil {
		return DocumentoPlanejamento{}, fmt.Errorf("obter documento %q do planejamento %d: %w", arquivo, planejamentoID, err)
	}
	return doc, nil
}

// ListarDocumentosPlanejamento devolve a revisão MAIS RECENTE de cada documento
// do planejamento (ordem por arquivo). Slice não-nil.
func (d *DB) ListarDocumentosPlanejamento(ctx context.Context, planejamentoID int64) ([]DocumentoPlanejamento, error) {
	rows, err := d.Leitor.QueryContext(ctx, `
		SELECT dp.id, dp.planejamento_id, dp.arquivo, dp.revisao, dp.conteudo, dp.criado_em
		FROM planejamento_documentos dp
		JOIN (SELECT arquivo, MAX(revisao) AS rev FROM planejamento_documentos
		      WHERE planejamento_id = ? GROUP BY arquivo) ult
		  ON ult.arquivo = dp.arquivo AND ult.rev = dp.revisao
		WHERE dp.planejamento_id = ?
		ORDER BY dp.arquivo`, planejamentoID, planejamentoID)
	if err != nil {
		return nil, fmt.Errorf("listar documentos do planejamento %d: %w", planejamentoID, err)
	}
	defer rows.Close()

	docs := []DocumentoPlanejamento{}
	for rows.Next() {
		var doc DocumentoPlanejamento
		if err := rows.Scan(&doc.ID, &doc.PlanejamentoID, &doc.Arquivo, &doc.Revisao,
			&doc.Conteudo, &doc.CriadoEm); err != nil {
			return nil, err
		}
		docs = append(docs, doc)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listar documentos do planejamento %d: %w", planejamentoID, err)
	}
	return docs, nil
}

// UpsertArtefatoPlanejamento cria ou atualiza o índice do artefato (chave
// planejamento_id + arquivo). Quando o hash muda, incrementa revisao e carimba
// atualizado_em; título/descrição são sempre atualizados quando não-vazios
// (vêm da declaração do estrategista no turno). Devolve a linha persistida.
func (d *DB) UpsertArtefatoPlanejamento(ctx context.Context, a ArtefatoPlanejamento) (ArtefatoPlanejamento, error) {
	a.Arquivo = strings.TrimSpace(a.Arquivo)
	if a.Arquivo == "" {
		return ArtefatoPlanejamento{}, ErrValorInvalido
	}
	row := d.Escritor.QueryRowContext(ctx, `
		INSERT INTO planejamento_artefatos
			(planejamento_id, arquivo, titulo, descricao, tamanho, hash, revisao)
		VALUES (?,?,?,?,?,?,1)
		ON CONFLICT (planejamento_id, arquivo) DO UPDATE SET
			titulo    = CASE WHEN excluded.titulo    != '' THEN excluded.titulo    ELSE titulo    END,
			descricao = CASE WHEN excluded.descricao != '' THEN excluded.descricao ELSE descricao END,
			tamanho   = excluded.tamanho,
			revisao   = CASE WHEN hash != excluded.hash THEN revisao + 1 ELSE revisao END,
			atualizado_em = CASE WHEN hash != excluded.hash
				THEN strftime('%Y-%m-%dT%H:%M:%fZ','now') ELSE atualizado_em END,
			hash = excluded.hash
		RETURNING id, planejamento_id, arquivo, titulo, descricao, tamanho, hash,
		          revisao, criado_em, atualizado_em`,
		a.PlanejamentoID, a.Arquivo, a.Titulo, a.Descricao, a.Tamanho, a.Hash,
	)
	var out ArtefatoPlanejamento
	if err := row.Scan(&out.ID, &out.PlanejamentoID, &out.Arquivo, &out.Titulo,
		&out.Descricao, &out.Tamanho, &out.Hash, &out.Revisao, &out.CriadoEm,
		&out.AtualizadoEm); err != nil {
		return ArtefatoPlanejamento{}, traduzirErroFK(err)
	}
	return out, nil
}

// ListarArtefatosPlanejamento devolve os artefatos do planejamento em ordem por
// arquivo. Slice não-nil.
func (d *DB) ListarArtefatosPlanejamento(ctx context.Context, planejamentoID int64) ([]ArtefatoPlanejamento, error) {
	rows, err := d.Leitor.QueryContext(ctx, `
		SELECT id, planejamento_id, arquivo, titulo, descricao, tamanho, hash,
		       revisao, criado_em, atualizado_em
		FROM planejamento_artefatos WHERE planejamento_id = ? ORDER BY arquivo`, planejamentoID)
	if err != nil {
		return nil, fmt.Errorf("listar artefatos do planejamento %d: %w", planejamentoID, err)
	}
	defer rows.Close()

	artefatos := []ArtefatoPlanejamento{}
	for rows.Next() {
		var a ArtefatoPlanejamento
		if err := rows.Scan(&a.ID, &a.PlanejamentoID, &a.Arquivo, &a.Titulo, &a.Descricao,
			&a.Tamanho, &a.Hash, &a.Revisao, &a.CriadoEm, &a.AtualizadoEm); err != nil {
			return nil, err
		}
		artefatos = append(artefatos, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listar artefatos do planejamento %d: %w", planejamentoID, err)
	}
	return artefatos, nil
}

// ObterArtefatoPlanejamento devolve o artefato do planejamento pelo nome do
// arquivo. Se não existir, devolve ErrNaoEncontrado.
func (d *DB) ObterArtefatoPlanejamento(ctx context.Context, planejamentoID int64, arquivo string) (ArtefatoPlanejamento, error) {
	row := d.Leitor.QueryRowContext(ctx, `
		SELECT id, planejamento_id, arquivo, titulo, descricao, tamanho, hash,
		       revisao, criado_em, atualizado_em
		FROM planejamento_artefatos WHERE planejamento_id = ? AND arquivo = ?`,
		planejamentoID, arquivo)
	var a ArtefatoPlanejamento
	err := row.Scan(&a.ID, &a.PlanejamentoID, &a.Arquivo, &a.Titulo, &a.Descricao,
		&a.Tamanho, &a.Hash, &a.Revisao, &a.CriadoEm, &a.AtualizadoEm)
	if errors.Is(err, sql.ErrNoRows) {
		return ArtefatoPlanejamento{}, ErrNaoEncontrado
	}
	if err != nil {
		return ArtefatoPlanejamento{}, fmt.Errorf("obter artefato %q do planejamento %d: %w", arquivo, planejamentoID, err)
	}
	return a, nil
}

// RemoverArtefatosAusentes apaga do índice os artefatos cujo arquivo não está
// mais na pasta (o estrategista pode remover um html obsoleto). presentes é a
// lista de arquivos que EXISTEM no disco após o turno.
func (d *DB) RemoverArtefatosAusentes(ctx context.Context, planejamentoID int64, presentes []string) error {
	marcadores := make([]string, 0, len(presentes))
	args := []any{planejamentoID}
	for _, p := range presentes {
		marcadores = append(marcadores, "?")
		args = append(args, p)
	}
	consulta := `DELETE FROM planejamento_artefatos WHERE planejamento_id = ?`
	if len(marcadores) > 0 {
		consulta += ` AND arquivo NOT IN (` + strings.Join(marcadores, ",") + `)`
	}
	if _, err := d.Escritor.ExecContext(ctx, consulta, args...); err != nil {
		return fmt.Errorf("remover artefatos ausentes do planejamento %d: %w", planejamentoID, err)
	}
	return nil
}
