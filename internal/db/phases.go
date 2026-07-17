package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// ErrCodigoFaseDuplicado indica violação da unicidade phases(demand_id, codigo)
// — duas fases com o mesmo código na mesma demanda. Os handlers mapeiam para 409.
var ErrCodigoFaseDuplicado = errors.New("código de fase já em uso nesta demanda")

// Estados de uma fase (coluna phases.status). Livres no banco, validados aqui.
const (
	StatusFasePendente   = "pendente"
	StatusFaseExecutando = "executando"
	StatusFaseConcluida  = "concluida"
	StatusFaseFalhou     = "falhou"
	StatusFasePausada    = "pausada"
)

// Fase é uma linha da tabela phases. As tags JSON refletem o modelo de dados do
// plano (snake_case). DependeDe lista os códigos das fases das quais esta
// depende.
type Fase struct {
	ID           int64    `json:"id"`
	DemandID     int64    `json:"demand_id"`
	Codigo       string   `json:"codigo"`
	Titulo       string   `json:"titulo"`
	Status       string   `json:"status"`
	DependeDe    []string `json:"depende_de"`
	RequerHumano bool     `json:"requer_humano"`
	GateExtra    string   `json:"gate_extra"`
	Modelo       string   `json:"modelo"`
	Tentativas   int      `json:"tentativas"`
	CustoUSD     float64  `json:"custo_usd"`
	ConcluidoEm  string   `json:"concluido_em"`
	Observacao   string   `json:"observacao"`
	Ordem        int      `json:"ordem"`
}

// colunasFase lista as colunas de phases na ordem esperada por scanFase.
const colunasFase = `id, demand_id, codigo, titulo, status, depende_de, requer_humano,
	gate_extra, modelo, tentativas, custo_usd, concluido_em, observacao, ordem`

// scanFase lê uma linha de phases (na ordem de colunasFase) para Fase,
// desserializando depende_de (JSON) e requer_humano (0/1). DependeDe nunca fica
// nil.
func scanFase(sc interface{ Scan(...any) error }) (Fase, error) {
	var (
		f         Fase
		dependeDe string
		requer    int
	)
	if err := sc.Scan(&f.ID, &f.DemandID, &f.Codigo, &f.Titulo, &f.Status, &dependeDe,
		&requer, &f.GateExtra, &f.Modelo, &f.Tentativas, &f.CustoUSD, &f.ConcluidoEm,
		&f.Observacao, &f.Ordem); err != nil {
		return Fase{}, err
	}
	f.RequerHumano = requer != 0
	deps, err := decodificarLista(dependeDe)
	if err != nil {
		return Fase{}, fmt.Errorf("decodificar depende_de da fase %d: %w", f.ID, err)
	}
	f.DependeDe = deps
	return f, nil
}

// CriarFase insere uma fase na demanda f.DemandID e devolve a linha persistida
// (com id). Status vazio cai no default (pendente). Código repetido na mesma
// demanda vira ErrCodigoFaseDuplicado; demanda inexistente vira ErrNaoEncontrado.
func (d *DB) CriarFase(ctx context.Context, f Fase) (Fase, error) {
	if strings.TrimSpace(f.Status) == "" {
		f.Status = StatusFasePendente
	}
	f.DependeDe = normalizarLista(f.DependeDe)
	deps, err := json.Marshal(f.DependeDe)
	if err != nil {
		return Fase{}, fmt.Errorf("codificar depende_de: %w", err)
	}
	row := d.Escritor.QueryRowContext(ctx, `
		INSERT INTO phases
			(demand_id, codigo, titulo, status, depende_de, requer_humano,
			 gate_extra, modelo, tentativas, custo_usd, concluido_em, observacao, ordem)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)
		RETURNING id`,
		f.DemandID, f.Codigo, f.Titulo, f.Status, string(deps), booleanParaInt(f.RequerHumano),
		f.GateExtra, f.Modelo, f.Tentativas, f.CustoUSD, f.ConcluidoEm, f.Observacao, f.Ordem,
	)
	if err := row.Scan(&f.ID); err != nil {
		return Fase{}, traduzirErroFase(err)
	}
	return f, nil
}

// SubstituirFases troca TODO o conjunto de fases da demanda pelas dadas, numa
// única transação (o planejador reescreve as fases ao gerar o plano, e o usuário
// as reescreve ao editar/reordenar/remover na aba Plano & Fases — Fase 3c). A
// ordem é reatribuída sequencialmente (1..N) na ordem do slice, ignorando o
// campo Ordem de entrada. Devolve as fases persistidas (com id/ordem). Demanda
// inexistente vira ErrNaoEncontrado; código repetido vira ErrCodigoFaseDuplicado.
func (d *DB) SubstituirFases(ctx context.Context, demandID int64, fases []Fase) ([]Fase, error) {
	tx, err := d.Escritor.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("substituir fases: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM phases WHERE demand_id = ?`, demandID); err != nil {
		return nil, fmt.Errorf("limpar fases da demanda %d: %w", demandID, err)
	}

	criadas := make([]Fase, 0, len(fases))
	for i, f := range fases {
		f.DemandID = demandID
		f.Ordem = i + 1
		if strings.TrimSpace(f.Status) == "" {
			f.Status = StatusFasePendente
		}
		f.DependeDe = normalizarLista(f.DependeDe)
		deps, err := json.Marshal(f.DependeDe)
		if err != nil {
			return nil, fmt.Errorf("codificar depende_de da fase %q: %w", f.Codigo, err)
		}
		row := tx.QueryRowContext(ctx, `
			INSERT INTO phases
				(demand_id, codigo, titulo, status, depende_de, requer_humano,
				 gate_extra, modelo, tentativas, custo_usd, concluido_em, observacao, ordem)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)
			RETURNING id`,
			f.DemandID, f.Codigo, f.Titulo, f.Status, string(deps), booleanParaInt(f.RequerHumano),
			f.GateExtra, f.Modelo, f.Tentativas, f.CustoUSD, f.ConcluidoEm, f.Observacao, f.Ordem,
		)
		if err := row.Scan(&f.ID); err != nil {
			return nil, traduzirErroFase(err)
		}
		criadas = append(criadas, f)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("substituir fases: %w", err)
	}
	return criadas, nil
}

// ListarFases devolve as fases da demanda demandID ordenadas por ordem e, em
// empate, por id. Slice não-nil.
func (d *DB) ListarFases(ctx context.Context, demandID int64) ([]Fase, error) {
	rows, err := d.Leitor.QueryContext(ctx,
		`SELECT `+colunasFase+` FROM phases WHERE demand_id = ? ORDER BY ordem, id`, demandID)
	if err != nil {
		return nil, fmt.Errorf("listar fases da demanda %d: %w", demandID, err)
	}
	defer rows.Close()
	fases := []Fase{}
	for rows.Next() {
		f, err := scanFase(rows)
		if err != nil {
			return nil, err
		}
		fases = append(fases, f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listar fases da demanda %d: %w", demandID, err)
	}
	return fases, nil
}

// ObterFase devolve a fase de id. Se não existir, devolve ErrNaoEncontrado.
func (d *DB) ObterFase(ctx context.Context, id int64) (Fase, error) {
	row := d.Leitor.QueryRowContext(ctx,
		`SELECT `+colunasFase+` FROM phases WHERE id = ?`, id)
	f, err := scanFase(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Fase{}, ErrNaoEncontrado
	}
	if err != nil {
		return Fase{}, fmt.Errorf("obter fase %d: %w", id, err)
	}
	return f, nil
}

// AtualizarFase grava os campos editáveis da fase f.ID e devolve a linha
// resultante. Não altera demand_id. Fase inexistente vira ErrNaoEncontrado;
// código em uso por outra fase da mesma demanda vira ErrCodigoFaseDuplicado.
func (d *DB) AtualizarFase(ctx context.Context, f Fase) (Fase, error) {
	f.DependeDe = normalizarLista(f.DependeDe)
	deps, err := json.Marshal(f.DependeDe)
	if err != nil {
		return Fase{}, fmt.Errorf("codificar depende_de: %w", err)
	}
	res, err := d.Escritor.ExecContext(ctx, `
		UPDATE phases SET
			codigo = ?, titulo = ?, status = ?, depende_de = ?, requer_humano = ?,
			gate_extra = ?, modelo = ?, tentativas = ?, custo_usd = ?,
			concluido_em = ?, observacao = ?, ordem = ?
		WHERE id = ?`,
		f.Codigo, f.Titulo, f.Status, string(deps), booleanParaInt(f.RequerHumano),
		f.GateExtra, f.Modelo, f.Tentativas, f.CustoUSD, f.ConcluidoEm, f.Observacao,
		f.Ordem, f.ID,
	)
	if err != nil {
		return Fase{}, traduzirErroFase(err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return Fase{}, fmt.Errorf("atualizar fase %d: %w", f.ID, err)
	}
	if n == 0 {
		return Fase{}, ErrNaoEncontrado
	}
	return d.ObterFase(ctx, f.ID)
}

// RemoverFase apaga a fase de id. Fase inexistente vira ErrNaoEncontrado. As
// execuções que apontavam para ela ficam com phase_id NULL (ON DELETE SET NULL).
func (d *DB) RemoverFase(ctx context.Context, id int64) error {
	res, err := d.Escritor.ExecContext(ctx, `DELETE FROM phases WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("remover fase %d: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("remover fase %d: %w", id, err)
	}
	if n == 0 {
		return ErrNaoEncontrado
	}
	return nil
}

// traduzirErroFase converte violações conhecidas de phases em erros sentinela: a
// unicidade (demand_id, codigo) vira ErrCodigoFaseDuplicado; a FK (demanda
// inexistente) vira ErrNaoEncontrado.
func traduzirErroFase(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	// A restrição UNIQUE (demand_id, codigo) aparece como "phases.demand_id,
	// phases.codigo" na mensagem do modernc.org/sqlite.
	if strings.Contains(msg, "phases.codigo") {
		return ErrCodigoFaseDuplicado
	}
	if strings.Contains(msg, "FOREIGN KEY") {
		return ErrNaoEncontrado
	}
	return fmt.Errorf("persistir fase: %w", err)
}
