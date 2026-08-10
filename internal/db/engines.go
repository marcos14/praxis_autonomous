package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// ErrNomeDuplicado indica violação da unicidade de engines.nome. Os handlers
// mapeiam para HTTP 409.
var ErrNomeDuplicado = errors.New("nome já em uso")

// ErrAliasDuplicado indica violação da unicidade de engine_accounts(engine_id,
// alias). Os handlers mapeiam para HTTP 409.
var ErrAliasDuplicado = errors.New("alias já em uso neste motor")

// ErrOrdemInvalida indica que a lista de ids passada para ReordenarMotores não é
// uma permutação exata dos motores existentes (id desconhecido, repetido ou
// faltando). Os handlers mapeiam para HTTP 400.
var ErrOrdemInvalida = errors.New("ordem inválida: informe exatamente os ids de todos os motores, sem repetir")

// Motor é uma linha da tabela engines já com as contas associadas. As tags JSON
// refletem o modelo de dados do plano (snake_case) e são a forma serializada
// pela API. Prioridade é a ordem de fallback (menor = tentado antes).
type Motor struct {
	ID         int64  `json:"id"`
	Nome       string `json:"nome"`
	Prioridade int    `json:"prioridade"`
	Ativo      bool   `json:"ativo"`
	// Fallback indica se o motor participa da cadeia de fallback automático.
	// false = o motor só roda onde for definido manualmente (motor preferido do
	// projeto ou motor do grupo de usuários nas consultas).
	Fallback      bool   `json:"fallback"`
	ModeloExec    string `json:"modelo_exec"`
	ModeloAnalise string `json:"modelo_analise"`
	// ModeloConsulta é o modelo da feature de consultas (chat de produto/
	// suporte); o rigor pode ser menor que o de análise/execução. Vazio → as
	// consultas caem no ModeloAnalise.
	ModeloConsulta string          `json:"modelo_consulta"`
	BudgetFaseUSD  float64         `json:"budget_fase_usd"`
	TimeoutMin     int             `json:"timeout_min"`
	Params         json.RawMessage `json:"params"`
	Contas         []Conta         `json:"contas"`
	// OwnerUserID é o usuário que criou o motor (NULL nos legados e nos criados
	// por token/bootstrap). Informativo + âncora da visibilidade "privada"; a
	// gestão de motores continua exigindo config.gerir.
	OwnerUserID *int64 `json:"owner_user_id,omitempty"`
	// Campos CALCULADOS da ACL (engine_access) — preenchidos na leitura, nunca
	// persistidos: DonoNome é o nome do owner; Visibilidade resume a ACL
	// (publica|privada|grupo); GrupoID é o grupo liberado (quando grupo).
	DonoNome     string `json:"dono_nome,omitempty"`
	Visibilidade string `json:"visibilidade,omitempty"`
	GrupoID      *int64 `json:"grupo_id,omitempty"`
}

// Conta é uma linha da tabela engine_accounts (uma conta do motor, com seu
// CLAUDE_CONFIG_DIR).
type Conta struct {
	ID        int64  `json:"id"`
	EngineID  int64  `json:"engine_id"`
	Alias     string `json:"alias"`
	ConfigDir string `json:"config_dir"`
	Ativo     bool   `json:"ativo"`
}

// ContaAtivaPara escolhe de forma determinística uma conta ativa do motor.
// A afinidade faz demandas/consultas diferentes se distribuírem pelos perfis,
// mantendo a mesma escolha entre etapas do mesmo fluxo. Afinidade <= 0 escolhe
// a primeira e preserva o comportamento histórico.
func ContaAtivaPara(m Motor, afinidade int64) (Conta, bool) {
	ativas := make([]Conta, 0, len(m.Contas))
	for _, c := range m.Contas {
		if c.Ativo {
			ativas = append(ativas, c)
		}
	}
	if len(ativas) == 0 {
		return Conta{}, false
	}
	if afinidade <= 0 {
		return ativas[0], true
	}
	return ativas[(afinidade-1)%int64(len(ativas))], true
}

// colunasMotor lista as colunas de engines na ordem esperada por scanMotor.
const colunasMotor = `id, nome, prioridade, ativo, fallback, modelo_exec, modelo_analise,
	modelo_consulta, budget_fase_usd, timeout_min, params, owner_user_id`

// colunasConta lista as colunas de engine_accounts na ordem esperada por
// scanConta.
const colunasConta = `id, engine_id, alias, config_dir, ativo`

// scanMotor lê uma linha de engines (na ordem de colunasMotor) para Motor. Não
// carrega as contas — os métodos de consulta cuidam disso. Contas nunca fica nil.
func scanMotor(sc interface{ Scan(...any) error }) (Motor, error) {
	var (
		m        Motor
		ativo    int
		fallback int
		params   string
		owner    sql.NullInt64
	)
	if err := sc.Scan(&m.ID, &m.Nome, &m.Prioridade, &ativo, &fallback, &m.ModeloExec,
		&m.ModeloAnalise, &m.ModeloConsulta, &m.BudgetFaseUSD, &m.TimeoutMin, &params, &owner); err != nil {
		return Motor{}, err
	}
	m.Ativo = ativo != 0
	m.Fallback = fallback != 0
	m.Params = normalizarParams(params)
	m.Contas = []Conta{}
	m.OwnerUserID = ptrDeNull(owner)
	return m, nil
}

// scanConta lê uma linha de engine_accounts (na ordem de colunasConta).
func scanConta(sc interface{ Scan(...any) error }) (Conta, error) {
	var (
		c     Conta
		ativo int
	)
	if err := sc.Scan(&c.ID, &c.EngineID, &c.Alias, &c.ConfigDir, &ativo); err != nil {
		return Conta{}, err
	}
	c.Ativo = ativo != 0
	return c, nil
}

// ProximaPrioridadeMotor devolve a prioridade a atribuir a um novo motor: o
// maior valor existente + 1 (0 quando não há motores). Assim, motores novos
// entram no fim da ordem de fallback.
func (d *DB) ProximaPrioridadeMotor(ctx context.Context) (int, error) {
	var max sql.NullInt64
	if err := d.Leitor.QueryRowContext(ctx,
		`SELECT MAX(prioridade) FROM engines`).Scan(&max); err != nil {
		return 0, fmt.Errorf("calcular próxima prioridade: %w", err)
	}
	if !max.Valid {
		return 0, nil
	}
	return int(max.Int64) + 1, nil
}

// CriarMotor insere um novo motor e devolve a linha persistida (com id
// preenchido). Nome duplicado vira ErrNomeDuplicado. As contas de m são
// ignoradas aqui (são gerenciadas pelos métodos de conta).
func (d *DB) CriarMotor(ctx context.Context, m Motor) (Motor, error) {
	params := normalizarParams(string(m.Params))
	row := d.Escritor.QueryRowContext(ctx, `
		INSERT INTO engines
			(nome, prioridade, ativo, fallback, modelo_exec, modelo_analise, modelo_consulta,
			 budget_fase_usd, timeout_min, params, owner_user_id)
		VALUES (?,?,?,?,?,?,?,?,?,?,?)
		RETURNING id`,
		m.Nome, m.Prioridade, booleanParaInt(m.Ativo), booleanParaInt(m.Fallback), m.ModeloExec,
		m.ModeloAnalise, m.ModeloConsulta, m.BudgetFaseUSD, m.TimeoutMin, string(params),
		nullInt(m.OwnerUserID),
	)
	if err := row.Scan(&m.ID); err != nil {
		return Motor{}, traduzirErroMotor(err)
	}
	m.Params = params
	m.Contas = []Conta{}
	return m, nil
}

// ListarMotores devolve todos os motores ordenados por prioridade (fallback) e,
// em empate, por id. Cada motor vem com suas contas (ordenadas por id).
func (d *DB) ListarMotores(ctx context.Context) ([]Motor, error) {
	rows, err := d.Leitor.QueryContext(ctx,
		`SELECT `+colunasMotor+` FROM engines ORDER BY prioridade, id`)
	if err != nil {
		return nil, fmt.Errorf("listar motores: %w", err)
	}
	defer rows.Close()

	motores := []Motor{}
	indice := map[int64]int{}
	for rows.Next() {
		m, err := scanMotor(rows)
		if err != nil {
			return nil, err
		}
		indice[m.ID] = len(motores)
		motores = append(motores, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listar motores: %w", err)
	}
	if len(motores) == 0 {
		return motores, nil
	}

	// Carrega todas as contas de uma vez e agrupa por motor (evita N+1).
	crows, err := d.Leitor.QueryContext(ctx,
		`SELECT `+colunasConta+` FROM engine_accounts ORDER BY engine_id, id`)
	if err != nil {
		return nil, fmt.Errorf("listar contas: %w", err)
	}
	defer crows.Close()
	for crows.Next() {
		c, err := scanConta(crows)
		if err != nil {
			return nil, err
		}
		if i, ok := indice[c.EngineID]; ok {
			motores[i].Contas = append(motores[i].Contas, c)
		}
	}
	if err := crows.Err(); err != nil {
		return nil, fmt.Errorf("listar contas: %w", err)
	}
	if err := d.decorarMotores(ctx, motores); err != nil {
		return nil, err
	}
	return motores, nil
}

// ObterMotor devolve o motor de id com suas contas. Se não existir, devolve
// ErrNaoEncontrado.
func (d *DB) ObterMotor(ctx context.Context, id int64) (Motor, error) {
	row := d.Leitor.QueryRowContext(ctx,
		`SELECT `+colunasMotor+` FROM engines WHERE id = ?`, id)
	m, err := scanMotor(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Motor{}, ErrNaoEncontrado
	}
	if err != nil {
		return Motor{}, fmt.Errorf("obter motor %d: %w", id, err)
	}
	contas, err := d.ListarContas(ctx, id)
	if err != nil {
		return Motor{}, err
	}
	m.Contas = contas
	ms := []Motor{m}
	if err := d.decorarMotores(ctx, ms); err != nil {
		return Motor{}, err
	}
	return ms[0], nil
}

// AtualizarMotor grava os campos editáveis do motor identificado por m.ID e
// devolve a linha resultante (com contas). Não altera a prioridade — a ordem de
// fallback é gerenciada por ReordenarMotores. Motor inexistente vira
// ErrNaoEncontrado; nome em uso por outro motor vira ErrNomeDuplicado.
func (d *DB) AtualizarMotor(ctx context.Context, m Motor) (Motor, error) {
	params := normalizarParams(string(m.Params))
	res, err := d.Escritor.ExecContext(ctx, `
		UPDATE engines SET
			nome = ?, ativo = ?, fallback = ?, modelo_exec = ?, modelo_analise = ?, modelo_consulta = ?,
			budget_fase_usd = ?, timeout_min = ?, params = ?
		WHERE id = ?`,
		m.Nome, booleanParaInt(m.Ativo), booleanParaInt(m.Fallback), m.ModeloExec, m.ModeloAnalise,
		m.ModeloConsulta, m.BudgetFaseUSD, m.TimeoutMin, string(params), m.ID,
	)
	if err != nil {
		return Motor{}, traduzirErroMotor(err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return Motor{}, fmt.Errorf("atualizar motor %d: %w", m.ID, err)
	}
	if n == 0 {
		return Motor{}, ErrNaoEncontrado
	}
	return d.ObterMotor(ctx, m.ID)
}

// ReordenarMotores redefine a prioridade dos motores conforme a ordem dos ids
// (posição 0 = prioridade 0 = primeiro no fallback). A lista deve ser uma
// permutação exata dos motores existentes (todos, sem repetir); caso contrário
// devolve ErrOrdemInvalida. A gravação é atômica (uma transação).
func (d *DB) ReordenarMotores(ctx context.Context, ids []int64) error {
	rows, err := d.Leitor.QueryContext(ctx, `SELECT id FROM engines`)
	if err != nil {
		return fmt.Errorf("reordenar motores: %w", err)
	}
	existentes := map[int64]bool{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return fmt.Errorf("reordenar motores: %w", err)
		}
		existentes[id] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("reordenar motores: %w", err)
	}

	if len(ids) != len(existentes) {
		return ErrOrdemInvalida
	}
	vistos := map[int64]bool{}
	for _, id := range ids {
		if !existentes[id] || vistos[id] {
			return ErrOrdemInvalida
		}
		vistos[id] = true
	}

	tx, err := d.Escritor.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("reordenar motores: %w", err)
	}
	defer tx.Rollback()
	for pos, id := range ids {
		if _, err := tx.ExecContext(ctx,
			`UPDATE engines SET prioridade = ? WHERE id = ?`, pos, id); err != nil {
			return fmt.Errorf("reordenar motores: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("reordenar motores: %w", err)
	}
	return nil
}

// ListarContas devolve as contas do motor engineID ordenadas por id (slice
// não-nil).
func (d *DB) ListarContas(ctx context.Context, engineID int64) ([]Conta, error) {
	rows, err := d.Leitor.QueryContext(ctx,
		`SELECT `+colunasConta+` FROM engine_accounts WHERE engine_id = ? ORDER BY id`, engineID)
	if err != nil {
		return nil, fmt.Errorf("listar contas do motor %d: %w", engineID, err)
	}
	defer rows.Close()
	contas := []Conta{}
	for rows.Next() {
		c, err := scanConta(rows)
		if err != nil {
			return nil, err
		}
		contas = append(contas, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("listar contas do motor %d: %w", engineID, err)
	}
	return contas, nil
}

// CriarConta insere uma conta no motor c.EngineID e devolve a linha persistida.
// Alias duplicado no mesmo motor vira ErrAliasDuplicado; motor inexistente vira
// ErrNaoEncontrado.
func (d *DB) CriarConta(ctx context.Context, c Conta) (Conta, error) {
	row := d.Escritor.QueryRowContext(ctx, `
		INSERT INTO engine_accounts (engine_id, alias, config_dir, ativo)
		VALUES (?,?,?,?)
		RETURNING id`,
		c.EngineID, c.Alias, c.ConfigDir, booleanParaInt(c.Ativo),
	)
	if err := row.Scan(&c.ID); err != nil {
		return Conta{}, traduzirErroConta(err)
	}
	return c, nil
}

// AtualizarConta grava os campos editáveis da conta c.ID pertencente a
// c.EngineID. Conta inexistente (ou de outro motor) vira ErrNaoEncontrado; alias
// em uso por outra conta do mesmo motor vira ErrAliasDuplicado.
func (d *DB) AtualizarConta(ctx context.Context, c Conta) (Conta, error) {
	res, err := d.Escritor.ExecContext(ctx, `
		UPDATE engine_accounts SET alias = ?, config_dir = ?, ativo = ?
		WHERE id = ? AND engine_id = ?`,
		c.Alias, c.ConfigDir, booleanParaInt(c.Ativo), c.ID, c.EngineID,
	)
	if err != nil {
		return Conta{}, traduzirErroConta(err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return Conta{}, fmt.Errorf("atualizar conta %d: %w", c.ID, err)
	}
	if n == 0 {
		return Conta{}, ErrNaoEncontrado
	}
	return c, nil
}

// RemoverConta apaga a conta contaID pertencente a engineID. Conta inexistente
// (ou de outro motor) vira ErrNaoEncontrado.
func (d *DB) RemoverConta(ctx context.Context, engineID, contaID int64) error {
	res, err := d.Escritor.ExecContext(ctx,
		`DELETE FROM engine_accounts WHERE id = ? AND engine_id = ?`, contaID, engineID)
	if err != nil {
		return fmt.Errorf("remover conta %d: %w", contaID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("remover conta %d: %w", contaID, err)
	}
	if n == 0 {
		return ErrNaoEncontrado
	}
	return nil
}

// normalizarParams devolve o JSON de params como json.RawMessage, caindo em "{}"
// quando vazio/nulo (mantém a coluna sempre com um objeto válido).
func normalizarParams(bruto string) json.RawMessage {
	bruto = strings.TrimSpace(bruto)
	if bruto == "" || bruto == "null" {
		return json.RawMessage("{}")
	}
	return json.RawMessage(bruto)
}

// traduzirErroMotor converte violações conhecidas de engines em erros sentinela.
func traduzirErroMotor(err error) error {
	if err == nil {
		return nil
	}
	if strings.Contains(err.Error(), "engines.nome") {
		return ErrNomeDuplicado
	}
	return fmt.Errorf("persistir motor: %w", err)
}

// traduzirErroConta converte violações conhecidas de engine_accounts em erros
// sentinela. A violação de FK (engine_id inexistente) vira ErrNaoEncontrado.
func traduzirErroConta(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	if strings.Contains(msg, "engine_accounts.alias") {
		return ErrAliasDuplicado
	}
	if strings.Contains(msg, "FOREIGN KEY") {
		return ErrNaoEncontrado
	}
	return fmt.Errorf("persistir conta: %w", err)
}
