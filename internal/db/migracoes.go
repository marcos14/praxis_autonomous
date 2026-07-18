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
	{
		versao: 3,
		nome:   "intake da demanda (chat_messages, questions)",
		sql:    schemaIntake,
	},
	{
		versao: 4,
		nome:   "prompts editáveis no banco (analista, planejador, …)",
		sql:    schemaPrompts,
	},
	{
		versao: 5,
		nome:   "tokens de API com papéis (api_tokens)",
		sql:    schemaTokens,
	},
	{
		versao: 6,
		nome:   "usuários, papéis customizáveis e segredo do JWT (users, roles, role_permissions, user_roles, auth_config)",
		sql:    schemaAuth,
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

// schemaIntake é a migração 3: as tabelas do intake da demanda (a demanda nasce
// como conversa). chat_messages guarda o PRD colado, os complementos do usuário e
// as falas do analista/planejador; questions guarda as perguntas estruturadas que
// o analista gera (preenchidas a partir da Fase 3b). Segue as convenções das
// migrações anteriores: datas em ISO-8601 UTC, JSON como TEXT com default válido,
// FKs com ON DELETE CASCADE.
//
// papel tem CHECK (conjunto pequeno e estável, ao contrário de status). questions
// já é criada aqui para a Fase 3b apenas persistir/consumir, sem nova migração.
const schemaIntake = `
CREATE TABLE chat_messages (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    demand_id  INTEGER NOT NULL REFERENCES demands(id) ON DELETE CASCADE,
    papel      TEXT    NOT NULL
                       CHECK (papel IN ('user','analista','planejador','sistema')),
    conteudo   TEXT    NOT NULL DEFAULT '',
    meta       TEXT    NOT NULL DEFAULT '{}',   -- JSON livre (ex.: custo/motor da fala do analista)
    criado_em  TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE INDEX ix_chat_messages_demand ON chat_messages (demand_id);

CREATE TABLE questions (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    demand_id     INTEGER NOT NULL REFERENCES demands(id) ON DELETE CASCADE,
    ordem         INTEGER NOT NULL DEFAULT 0,
    pergunta      TEXT    NOT NULL,
    contexto      TEXT    NOT NULL DEFAULT '',
    tipo          TEXT    NOT NULL DEFAULT '',   -- ex.: escolha|texto (livre; a UI decide o widget)
    opcoes        TEXT    NOT NULL DEFAULT '[]',  -- JSON: []string
    sugestao      TEXT    NOT NULL DEFAULT '',
    impacto       TEXT    NOT NULL DEFAULT '',   -- ex.: alto|medio|baixo
    resposta      TEXT    NOT NULL DEFAULT '',
    respondida_em TEXT    NOT NULL DEFAULT ''
);

CREATE INDEX ix_questions_demand ON questions (demand_id);
`

// schemaPrompts é a migração 4: os prompts dos harnesses (analista, planejador,
// executor/corretor/revisor) editáveis no banco. A tabela guarda apenas os
// OVERRIDES; quando uma linha não existe, o binário usa o default embutido
// (//go:embed no pacote intake) — daí "prompts no banco com default embutido"
// (Fase 3b). nome é a chave (ex.: 'analista'); conteudo é o template markdown com
// marcadores {VAR}. Segue as convenções: datas em ISO-8601 UTC.
const schemaPrompts = `
CREATE TABLE prompts (
    nome          TEXT    PRIMARY KEY,
    conteudo      TEXT    NOT NULL,
    atualizado_em TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);
`

// schemaAuth é a migração 6: usuários com login (users), papéis customizáveis
// (roles + role_permissions) e o vínculo usuário↔papel (user_roles), mais o
// segredo de assinatura do JWT (auth_config, linha única). Substitui, na prática,
// o modelo "loopback = admin": a partir do primeiro usuário criado a API passa a
// exigir autenticação (o middleware trata zero-usuários como modo bootstrap).
//
// Convenções das migrações anteriores: datas ISO-8601 UTC, FKs com ON DELETE
// CASCADE. senha_hash guarda um hash PBKDF2-HMAC-SHA256 self-describing (nunca a
// senha em claro). O catálogo de permissões é validado na aplicação (constantes
// Perm* em permissoes.go), não por CHECK — para não exigir migração a cada
// capacidade nova. O papel de sistema `admin` já nasce com a permissão curinga
// `*` e é vinculado ao primeiro usuário no /auth/setup.
const schemaAuth = `
CREATE TABLE users (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    nome          TEXT    NOT NULL,
    email         TEXT    NOT NULL UNIQUE,          -- login (comparado em minúsculas na app)
    senha_hash    TEXT    NOT NULL,
    ativo         INTEGER NOT NULL DEFAULT 1 CHECK (ativo IN (0,1)),
    criado_em     TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    atualizado_em TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE TABLE roles (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    nome       TEXT    NOT NULL UNIQUE,
    descricao  TEXT    NOT NULL DEFAULT '',
    sistema    INTEGER NOT NULL DEFAULT 0 CHECK (sistema IN (0,1)),  -- papel embutido não removível/renomeável
    criado_em  TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE TABLE role_permissions (
    role_id   INTEGER NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
    permissao TEXT    NOT NULL,
    PRIMARY KEY (role_id, permissao)
);

CREATE TABLE user_roles (
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role_id INTEGER NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
    PRIMARY KEY (user_id, role_id)
);

CREATE INDEX ix_user_roles_user ON user_roles (user_id);
CREATE INDEX ix_role_permissions_role ON role_permissions (role_id);

CREATE TABLE auth_config (
    id         INTEGER PRIMARY KEY CHECK (id = 1),   -- linha única (singleton)
    jwt_secret TEXT    NOT NULL
);

-- Papel de sistema "admin" com permissão curinga (todas). Vinculado ao 1º usuário
-- no /auth/setup. É o único papel com sistema=1 nascido aqui.
INSERT INTO roles (nome, descricao, sistema) VALUES ('admin', 'Acesso total ao Praxis', 1);
INSERT INTO role_permissions (role_id, permissao)
    SELECT id, '*' FROM roles WHERE nome = 'admin';
`

// schemaTokens é a migração 5: os tokens de API do sistema de chamados (Fase
// 5a), com papel (leitor/operador/admin). Guarda apenas o HASH do token
// (token_hash, SHA-256 hex) — o valor em claro é mostrado uma única vez na
// criação e nunca é persistido. revogado_em não-nulo = token revogado.
const schemaTokens = `
CREATE TABLE api_tokens (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    nome       TEXT    NOT NULL,
    token_hash TEXT    NOT NULL UNIQUE,
    papel      TEXT    NOT NULL CHECK (papel IN ('leitor','operador','admin')),
    criado_em  TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    revogado_em TEXT
);
`
