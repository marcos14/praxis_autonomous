package db

import (
	"database/sql"
	"fmt"
)

// migracao é um passo de evolução do schema. As migrações são aplicadas em
// ordem crescente de versao; cada uma roda em uma única transação junto com o
// avanço de PRAGMA user_version, tornando a aplicação atômica e idempotente
// (reaplicar um banco já atualizado é no-op).
type migracao struct {
	versao int
	nome   string
	sql    string
}

// migracoes é a lista ordenada de migrações do projeto. Cada nova migração é
// acrescentada ao fim com versao = anterior + 1; nunca edite uma migração já
// liberada (crie outra). A versão corrente do schema é len(migracoes).
var migracoes = []migracao{
	{
		versao: 1,
		nome:   "schema núcleo (projects, engines, engine_accounts, config_entries)",
		sql:    schemaNucleo,
	},
	{
		versao: 2,
		nome:   "ciclo de execução (demands, phases, runs, events, metrics_dia)",
		sql:    schemaCicloExecucao,
	},
}

// VersaoSchema é a versão de schema que o binário espera (a última migração
// conhecida). Útil para diagnósticos e para detectar bancos de versão futura.
func VersaoSchema() int { return len(migracoes) }

// versaoUser lê o PRAGMA user_version do banco.
func versaoUser(exec interface {
	QueryRow(string, ...any) *sql.Row
}) (int, error) {
	var v int
	if err := exec.QueryRow("PRAGMA user_version").Scan(&v); err != nil {
		return 0, fmt.Errorf("ler user_version: %w", err)
	}
	return v, nil
}

// VersaoAtual devolve a versão de schema gravada no banco (PRAGMA user_version).
func VersaoAtual(db *sql.DB) (int, error) { return versaoUser(db) }

// Migrar aplica, em transações, todas as migrações com versao maior que a
// user_version atual. Devolve a versão final e quantas migrações foram
// efetivamente aplicadas (0 quando o banco já estava atualizado).
//
// Se o banco estiver numa versão maior que a conhecida pelo binário (downgrade),
// Migrar retorna erro em vez de tentar "desmigrar".
func Migrar(db *sql.DB) (versaoFinal int, aplicadas int, err error) {
	atual, err := versaoUser(db)
	if err != nil {
		return 0, 0, err
	}
	if atual > VersaoSchema() {
		return atual, 0, fmt.Errorf(
			"banco na versão %d, mais nova que a suportada (%d): atualize o binário",
			atual, VersaoSchema())
	}
	for _, m := range migracoes {
		if m.versao <= atual {
			continue
		}
		if err := aplicarMigracao(db, m); err != nil {
			return atual, aplicadas, err
		}
		atual = m.versao
		aplicadas++
	}
	return atual, aplicadas, nil
}

// aplicarMigracao roda o SQL da migração e avança o user_version na mesma
// transação. Em erro, o defer Rollback desfaz tudo (schema não fica pela metade).
func aplicarMigracao(db *sql.DB, m migracao) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("migração %d (%s): iniciar transação: %w", m.versao, m.nome, err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec(m.sql); err != nil {
		return fmt.Errorf("migração %d (%s): %w", m.versao, m.nome, err)
	}
	// PRAGMA user_version não aceita placeholders; a versão vem de um int nosso,
	// nunca de entrada externa, então a interpolação é segura.
	if _, err := tx.Exec(fmt.Sprintf("PRAGMA user_version = %d", m.versao)); err != nil {
		return fmt.Errorf("migração %d (%s): gravar user_version: %w", m.versao, m.nome, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("migração %d (%s): commit: %w", m.versao, m.nome, err)
	}
	return nil
}

// schemaNucleo é a migração 1: as quatro tabelas de cadastro (config em
// camadas) definidas no modelo de dados do plano. Datas em ISO-8601 UTC; campos
// JSON guardados como TEXT com default válido.
const schemaNucleo = `
CREATE TABLE projects (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    nome              TEXT    NOT NULL,
    slug              TEXT    NOT NULL UNIQUE,
    pasta             TEXT    NOT NULL,
    branch_principal  TEXT    NOT NULL DEFAULT 'main',
    modo_integracao   TEXT    NOT NULL DEFAULT 'merge_request'
                              CHECK (modo_integracao IN ('merge_request','merge_local')),
    url_plataforma    TEXT    NOT NULL DEFAULT '',
    add_dirs          TEXT    NOT NULL DEFAULT '[]',   -- JSON: []string
    ativo             INTEGER NOT NULL DEFAULT 1 CHECK (ativo IN (0,1)),
    criado_em         TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE TABLE engines (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    nome            TEXT    NOT NULL UNIQUE,
    prioridade      INTEGER NOT NULL DEFAULT 0,   -- ordem de fallback (menor = tentado antes)
    ativo           INTEGER NOT NULL DEFAULT 1 CHECK (ativo IN (0,1)),
    modelo_exec     TEXT    NOT NULL DEFAULT '',
    modelo_analise  TEXT    NOT NULL DEFAULT '',
    budget_fase_usd REAL    NOT NULL DEFAULT 0,
    timeout_min     INTEGER NOT NULL DEFAULT 0,
    params          TEXT    NOT NULL DEFAULT '{}'   -- JSON: parâmetros livres do motor
);

CREATE TABLE engine_accounts (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    engine_id  INTEGER NOT NULL REFERENCES engines(id) ON DELETE CASCADE,
    alias      TEXT    NOT NULL,
    config_dir TEXT    NOT NULL DEFAULT '',   -- CLAUDE_CONFIG_DIR da conta
    ativo      INTEGER NOT NULL DEFAULT 1 CHECK (ativo IN (0,1)),
    UNIQUE (engine_id, alias)
);

CREATE TABLE config_entries (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    escopo     TEXT    NOT NULL CHECK (escopo IN ('global','project')),
    project_id INTEGER REFERENCES projects(id) ON DELETE CASCADE,
    chave      TEXT    NOT NULL,
    valor      TEXT    NOT NULL DEFAULT 'null',   -- JSON
    -- global: sem projeto; project: exige projeto
    CHECK (
        (escopo = 'global'  AND project_id IS NULL) OR
        (escopo = 'project' AND project_id IS NOT NULL)
    )
);

-- Uma entrada por chave no escopo global e uma por (projeto, chave) no override.
CREATE UNIQUE INDEX ux_config_global  ON config_entries (chave)             WHERE escopo = 'global';
CREATE UNIQUE INDEX ux_config_project ON config_entries (project_id, chave) WHERE escopo = 'project';

CREATE INDEX ix_engine_accounts_engine ON engine_accounts (engine_id);
`

// schemaCicloExecucao é a migração 2: as tabelas do ciclo de execução de uma
// demanda (demanda → fases → execuções → eventos) mais o agregado diário de
// métricas para a Home. Segue as convenções da migração 1: datas em ISO-8601
// UTC, campos JSON como TEXT com default válido, FKs com ON DELETE CASCADE.
//
// status (de demands e phases) é TEXT livre com default — a máquina de estados
// é validada na camada de aplicação (constantes StatusDemanda*/StatusFase*),
// não por CHECK, para não exigir migração a cada estado novo das fases futuras.
const schemaCicloExecucao = `
CREATE TABLE demands (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id     INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    titulo         TEXT    NOT NULL,
    origem         TEXT    NOT NULL DEFAULT 'ui' CHECK (origem IN ('ui','api')),
    origem_ref     TEXT    NOT NULL DEFAULT '',     -- referência externa (ex.: id do chamado)
    status         TEXT    NOT NULL DEFAULT 'recebida',
    prioridade     INTEGER NOT NULL DEFAULT 0,      -- ordem no kanban (arrastar reordena)
    branch         TEXT    NOT NULL DEFAULT '',      -- praxis/d<id>-<slug> (preenchida ao executar)
    worktree_path  TEXT    NOT NULL DEFAULT '',
    plano_md       TEXT    NOT NULL DEFAULT '',
    custo_usd      REAL    NOT NULL DEFAULT 0,
    budget_usd     REAL    NOT NULL DEFAULT 0,
    erro           TEXT    NOT NULL DEFAULT '',
    criado_em      TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    atualizado_em  TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE INDEX ix_demands_project ON demands (project_id);
CREATE INDEX ix_demands_status  ON demands (status);

CREATE TABLE phases (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    demand_id     INTEGER NOT NULL REFERENCES demands(id) ON DELETE CASCADE,
    codigo        TEXT    NOT NULL,                  -- ex.: "2a" (único dentro da demanda)
    titulo        TEXT    NOT NULL,
    status        TEXT    NOT NULL DEFAULT 'pendente',
    depende_de    TEXT    NOT NULL DEFAULT '[]',     -- JSON: []string (códigos de outras fases)
    requer_humano INTEGER NOT NULL DEFAULT 0 CHECK (requer_humano IN (0,1)),
    gate_extra    TEXT    NOT NULL DEFAULT '',
    modelo        TEXT    NOT NULL DEFAULT '',
    tentativas    INTEGER NOT NULL DEFAULT 0,
    custo_usd     REAL    NOT NULL DEFAULT 0,
    concluido_em  TEXT    NOT NULL DEFAULT '',
    observacao    TEXT    NOT NULL DEFAULT '',
    ordem         INTEGER NOT NULL DEFAULT 0,
    UNIQUE (demand_id, codigo)
);

CREATE INDEX ix_phases_demand ON phases (demand_id);

CREATE TABLE runs (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    demand_id    INTEGER NOT NULL REFERENCES demands(id) ON DELETE CASCADE,
    -- fase opcional: execuções de analista/planejador não têm fase. Ao remover
    -- uma fase (edição do plano) as execuções ficam com phase_id NULL, não somem.
    phase_id     INTEGER REFERENCES phases(id) ON DELETE SET NULL,
    operacao     TEXT    NOT NULL,                   -- executor|corretor|revisor|gates|analista|planejador
    engine       TEXT    NOT NULL DEFAULT '',
    modelo       TEXT    NOT NULL DEFAULT '',
    custo_usd    REAL    NOT NULL DEFAULT 0,
    tokens_in    INTEGER NOT NULL DEFAULT 0,
    tokens_out   INTEGER NOT NULL DEFAULT 0,
    is_error     INTEGER NOT NULL DEFAULT 0 CHECK (is_error IN (0,1)),
    log_ref      TEXT    NOT NULL DEFAULT '',        -- ponteiro para o .jsonl em PRAXIS_HOME/logs
    iniciado_em  TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    terminado_em TEXT    NOT NULL DEFAULT ''
);

CREATE INDEX ix_runs_demand ON runs (demand_id);
CREATE INDEX ix_runs_phase  ON runs (phase_id);

-- events alimenta o SSE e o histórico. project_id/demand_id são opcionais: um
-- evento pode ser global do projeto, de uma demanda, ou de ambos.
CREATE TABLE events (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    project_id INTEGER REFERENCES projects(id) ON DELETE CASCADE,
    demand_id  INTEGER REFERENCES demands(id)  ON DELETE CASCADE,
    tipo       TEXT    NOT NULL,
    titulo     TEXT    NOT NULL DEFAULT '',
    detalhe    TEXT    NOT NULL DEFAULT '',
    criado_em  TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE INDEX ix_events_demand  ON events (demand_id);
CREATE INDEX ix_events_project ON events (project_id);
CREATE INDEX ix_events_criado  ON events (criado_em);

-- metrics_dia é o agregado diário (dia × projeto × motor) que alimenta os tiles
-- e o gráfico da Home; escrito incrementalmente conforme as fases concluem.
CREATE TABLE metrics_dia (
    dia              TEXT    NOT NULL,               -- 'YYYY-MM-DD' (UTC)
    project_id       INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    engine           TEXT    NOT NULL DEFAULT '',
    custo_usd        REAL    NOT NULL DEFAULT 0,
    fases_concluidas INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (dia, project_id, engine)
);
`
