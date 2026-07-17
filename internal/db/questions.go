package db

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// Impactos sugeridos de uma pergunta (coluna questions.impacto). Livres no banco
// (TEXT), estáveis aqui só para a UI destacar as de alto impacto.
const (
	ImpactoAlto  = "alto"
	ImpactoMedio = "medio"
	ImpactoBaixo = "baixo"
)

// Pergunta é uma linha da tabela questions: uma pergunta que o analista gerou
// para a demanda (Fase 3b). Opcoes lista as alternativas de resposta (chips na
// UI); vazio = resposta em texto livre. Resposta/RespondidaEm ficam vazios até o
// usuário responder.
type Pergunta struct {
	ID           int64    `json:"id"`
	DemandID     int64    `json:"demand_id"`
	Ordem        int      `json:"ordem"`
	Pergunta     string   `json:"pergunta"`
	Contexto     string   `json:"contexto"`
	Tipo         string   `json:"tipo"`
	Opcoes       []string `json:"opcoes"`
	Sugestao     string   `json:"sugestao"`
	Impacto      string   `json:"impacto"`
	Resposta     string   `json:"resposta"`
	RespondidaEm string   `json:"respondida_em"`
}

// colunasPergunta lista as colunas de questions na ordem esperada por scanPergunta.
const colunasPergunta = `id, demand_id, ordem, pergunta, contexto, tipo, opcoes,
	sugestao, impacto, resposta, respondida_em`

// scanPergunta lê uma linha de questions (na ordem de colunasPergunta),
// desserializando opcoes (JSON). Opcoes nunca fica nil.
func scanPergunta(sc interface{ Scan(...any) error }) (Pergunta, error) {
	var (
		q      Pergunta
		opcoes string
	)
	if err := sc.Scan(&q.ID, &q.DemandID, &q.Ordem, &q.Pergunta, &q.Contexto,
		&q.Tipo, &opcoes, &q.Sugestao, &q.Impacto, &q.Resposta, &q.RespondidaEm); err != nil {
		return Pergunta{}, err
	}
	ops, err := decodificarLista(opcoes)
	if err != nil {
		return Pergunta{}, fmt.Errorf("decodificar opcoes da pergunta %d: %w", q.ID, err)
	}
	q.Opcoes = ops
	return q, nil
}

// SubstituirPerguntas troca TODO o conjunto de perguntas da demanda pelas dadas,
// numa única transação (o analista reescreve as perguntas a cada análise — Fase
// 3b). A ordem é reatribuída sequencialmente (1..N) na ordem do slice, ignorando
// o campo Ordem de entrada. Devolve as perguntas persistidas (com id/ordem).
// Demanda inexistente vira ErrNaoEncontrado.
func (d *DB) SubstituirPerguntas(ctx context.Context, demandID int64, perguntas []Pergunta) ([]Pergunta, error) {
	tx, err := d.Escritor.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("substituir perguntas: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM questions WHERE demand_id = ?`, demandID); err != nil {
		return nil, fmt.Errorf("limpar perguntas da demanda %d: %w", demandID, err)
	}

	criadas := make([]Pergunta, 0, len(perguntas))
	for i, q := range perguntas {
		q.DemandID = demandID
		q.Ordem = i + 1
		q.Opcoes = normalizarLista(q.Opcoes)
		ops, err := json.Marshal(q.Opcoes)
		if err != nil {
			return nil, fmt.Errorf("codificar opcoes da pergunta %d: %w", q.Ordem, err)
		}
		row := tx.QueryRowContext(ctx, `
			INSERT INTO questions
				(demand_id, ordem, pergunta, contexto, tipo, opcoes, sugestao, impacto, resposta, respondida_em)
			VALUES (?,?,?,?,?,?,?,?,?,?)
			RETURNING id`,
			q.DemandID, q.Ordem, q.Pergunta, q.Contexto, q.Tipo, string(ops),
			q.Sugestao, q.Impacto, q.Resposta, q.RespondidaEm,
		)
		if err := row.Scan(&q.ID); err != nil {
			return nil, traduzirErroFK(err)
		}
		criadas = append(criadas, q)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("substituir perguntas: %w", err)
	}
	return criadas, nil
}

// ListarPerguntas devolve as perguntas da demanda em ordem (ordem crescente,
// desempate por id). Slice não-nil.
func (d *DB) ListarPerguntas(ctx context.Context, demandID int64) ([]Pergunta, error) {
	rows, err := d.Leitor.QueryContext(ctx,
		`SELECT `+colunasPergunta+` FROM questions WHERE demand_id = ? ORDER BY ordem, id`, demandID)
	if err != nil {
		return nil, fmt.Errorf("listar perguntas da demanda %d: %w", demandID, err)
	}
	defer rows.Close()

	perguntas := []Pergunta{}
	for rows.Next() {
		q, err := scanPergunta(rows)
		if err != nil {
			return nil, err
		}
		perguntas = append(perguntas, q)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listar perguntas da demanda %d: %w", demandID, err)
	}
	return perguntas, nil
}

// RespostaPergunta associa uma resposta a uma pergunta pelo id (usado por
// ResponderPerguntas).
type RespostaPergunta struct {
	ID       int64  `json:"id"`
	Resposta string `json:"resposta"`
}

// ResponderPerguntas grava as respostas do usuário nas perguntas dadas, numa
// única transação, carimbando respondida_em com o horário atual. Só afeta
// perguntas que pertencem à demanda (guarda contra ids de outra demanda).
// Devolve quantas perguntas foram efetivamente atualizadas.
func (d *DB) ResponderPerguntas(ctx context.Context, demandID int64, respostas []RespostaPergunta) (int, error) {
	tx, err := d.Escritor.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("responder perguntas: %w", err)
	}
	defer tx.Rollback()

	total := 0
	for _, r := range respostas {
		res, err := tx.ExecContext(ctx, `
			UPDATE questions
			   SET resposta = ?, respondida_em = strftime('%Y-%m-%dT%H:%M:%fZ','now')
			 WHERE id = ? AND demand_id = ?`,
			strings.TrimSpace(r.Resposta), r.ID, demandID)
		if err != nil {
			return 0, fmt.Errorf("responder pergunta %d: %w", r.ID, err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return 0, fmt.Errorf("responder pergunta %d: %w", r.ID, err)
		}
		total += int(n)
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("responder perguntas: %w", err)
	}
	return total, nil
}
