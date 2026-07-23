package db

import (
	"database/sql"
	"path/filepath"
	"strconv"
	"testing"
)

// abrirBruto abre uma única conexão de escrita sem migrar, para exercitar Migrar
// diretamente.
func abrirBruto(t *testing.T) *sql.DB {
	t.Helper()
	caminho := filepath.Join(t.TempDir(), "praxis.db")
	db, err := sql.Open(nomeDriver, dsnEscritor(caminho))
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	return db
}

func TestMigrarAplicaEAvancaUserVersion(t *testing.T) {
	db := abrirBruto(t)

	v0, err := VersaoAtual(db)
	if err != nil {
		t.Fatalf("VersaoAtual inicial: %v", err)
	}
	if v0 != 0 {
		t.Fatalf("user_version inicial = %d, quero 0", v0)
	}

	final, aplicadas, err := Migrar(db)
	if err != nil {
		t.Fatalf("Migrar: %v", err)
	}
	if final != VersaoSchema() {
		t.Fatalf("versão final = %d, quero %d", final, VersaoSchema())
	}
	if aplicadas != len(migracoes) {
		t.Fatalf("aplicadas = %d, quero %d", aplicadas, len(migracoes))
	}

	v1, err := VersaoAtual(db)
	if err != nil {
		t.Fatalf("VersaoAtual pós-migração: %v", err)
	}
	if v1 != VersaoSchema() {
		t.Fatalf("user_version = %d, quero %d", v1, VersaoSchema())
	}
}

func TestMigrarReaplicarEhNoOp(t *testing.T) {
	db := abrirBruto(t)

	if _, _, err := Migrar(db); err != nil {
		t.Fatalf("Migrar (1): %v", err)
	}
	final, aplicadas, err := Migrar(db)
	if err != nil {
		t.Fatalf("Migrar (2): %v", err)
	}
	if aplicadas != 0 {
		t.Fatalf("reaplicar aplicou %d migrações, quero 0", aplicadas)
	}
	if final != VersaoSchema() {
		t.Fatalf("versão após no-op = %d, quero %d", final, VersaoSchema())
	}
}

func TestMigrarRejeitaVersaoFutura(t *testing.T) {
	db := abrirBruto(t)
	futura := VersaoSchema() + 5
	if _, err := db.Exec("PRAGMA user_version = " + strconv.Itoa(futura)); err != nil {
		t.Fatalf("forçar versão futura: %v", err)
	}
	if _, _, err := Migrar(db); err == nil {
		t.Fatal("Migrar deveria recusar banco de versão futura")
	}
}

// TestMigracaoContaPreservaRunsExistentes garante o critério da migração 11:
// runs gravados antes dela continuam legíveis (conta = '') — os relatórios
// atuais não quebram no upgrade.
func TestMigracaoContaPreservaRunsExistentes(t *testing.T) {
	db := abrirBruto(t)

	// aplica só até a migração 10 e grava um run "legado" (sem coluna conta).
	for _, m := range migracoes {
		if m.versao > 10 {
			break
		}
		if err := aplicarMigracao(db, m); err != nil {
			t.Fatalf("migração %d: %v", m.versao, err)
		}
	}
	if _, err := db.Exec(`INSERT INTO projects (nome, slug, pasta) VALUES ('p','p','x')`); err != nil {
		t.Fatalf("projeto legado: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO demands (project_id, titulo) VALUES (1,'d')`); err != nil {
		t.Fatalf("demanda legada: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO runs (demand_id, operacao, engine) VALUES (1,'executor','claude')`); err != nil {
		t.Fatalf("run legado: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO engines (nome) VALUES ('claude')`); err != nil {
		t.Fatalf("motor legado: %v", err)
	}

	if _, _, err := Migrar(db); err != nil {
		t.Fatalf("Migrar até a versão corrente: %v", err)
	}

	var engine, conta string
	if err := db.QueryRow(`SELECT engine, conta FROM runs WHERE id = 1`).Scan(&engine, &conta); err != nil {
		t.Fatalf("ler run legado pós-migração: %v", err)
	}
	if engine != "claude" || conta != "" {
		t.Fatalf("run legado = engine %q / conta %q, quero claude / ''", engine, conta)
	}
	var contaConsulta string
	if err := db.QueryRow(`SELECT COALESCE(MAX(conta), '') FROM consulta_runs`).Scan(&contaConsulta); err != nil {
		t.Fatalf("consulta_runs sem coluna conta: %v", err)
	}
	// migração 12: motor criado antes dela continua participando do fallback.
	var fallback int
	if err := db.QueryRow(`SELECT fallback FROM engines WHERE nome = 'claude'`).Scan(&fallback); err != nil {
		t.Fatalf("ler fallback do motor legado: %v", err)
	}
	if fallback != 1 {
		t.Fatalf("fallback do motor legado = %d, quero 1 (default preserva o comportamento)", fallback)
	}
}

func TestSchemaNucleoCriaTabelasEIndices(t *testing.T) {
	db := abrirBruto(t)
	if _, _, err := Migrar(db); err != nil {
		t.Fatalf("Migrar: %v", err)
	}

	tabelas := []string{"projects", "engines", "engine_accounts", "config_entries"}
	for _, tab := range tabelas {
		if !existeNoSchema(t, db, "table", tab) {
			t.Errorf("tabela %q não foi criada", tab)
		}
	}
	indices := []string{"ux_config_global", "ux_config_project", "ix_engine_accounts_engine"}
	for _, idx := range indices {
		if !existeNoSchema(t, db, "index", idx) {
			t.Errorf("índice %q não foi criado", idx)
		}
	}
}

func TestSchemaCicloExecucaoCriaTabelasEIndices(t *testing.T) {
	db := abrirBruto(t)
	if _, _, err := Migrar(db); err != nil {
		t.Fatalf("Migrar: %v", err)
	}

	tabelas := []string{"demands", "phases", "runs", "events", "metrics_dia"}
	for _, tab := range tabelas {
		if !existeNoSchema(t, db, "table", tab) {
			t.Errorf("tabela %q não foi criada", tab)
		}
	}
	indices := []string{
		"ix_demands_project", "ix_demands_status", "ix_phases_demand",
		"ix_runs_demand", "ix_runs_phase",
		"ix_events_demand", "ix_events_project", "ix_events_criado",
	}
	for _, idx := range indices {
		if !existeNoSchema(t, db, "index", idx) {
			t.Errorf("índice %q não foi criado", idx)
		}
	}
}

func TestSchemaIntakeCriaTabelasEIndices(t *testing.T) {
	db := abrirBruto(t)
	if _, _, err := Migrar(db); err != nil {
		t.Fatalf("Migrar: %v", err)
	}

	tabelas := []string{"chat_messages", "questions"}
	for _, tab := range tabelas {
		if !existeNoSchema(t, db, "table", tab) {
			t.Errorf("tabela %q não foi criada", tab)
		}
	}
	indices := []string{"ix_chat_messages_demand", "ix_questions_demand"}
	for _, idx := range indices {
		if !existeNoSchema(t, db, "index", idx) {
			t.Errorf("índice %q não foi criado", idx)
		}
	}
}

func TestSchemaPromptsCriaTabela(t *testing.T) {
	db := abrirBruto(t)
	if _, _, err := Migrar(db); err != nil {
		t.Fatalf("Migrar: %v", err)
	}
	if !existeNoSchema(t, db, "table", "prompts") {
		t.Error("tabela \"prompts\" não foi criada")
	}
}

func TestChatMessagesRejeitaPapelInvalido(t *testing.T) {
	db := abrirBruto(t)
	if _, _, err := Migrar(db); err != nil {
		t.Fatalf("Migrar: %v", err)
	}
	// precisa de um projeto e uma demanda para satisfazer as FKs.
	if _, err := db.Exec(`INSERT INTO projects (nome, slug, pasta) VALUES ('p','p','p')`); err != nil {
		t.Fatalf("inserir projeto: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO demands (project_id, titulo) VALUES (1, 't')`); err != nil {
		t.Fatalf("inserir demanda: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO chat_messages (demand_id, papel, conteudo) VALUES (1, 'robo', 'x')`,
	); err == nil {
		t.Fatal("CHECK de papel deveria rejeitar valor inválido")
	}
}

func TestProjectsRejeitaModoIntegracaoInvalido(t *testing.T) {
	db := abrirBruto(t)
	if _, _, err := Migrar(db); err != nil {
		t.Fatalf("Migrar: %v", err)
	}
	_, err := db.Exec(
		`INSERT INTO projects (nome, slug, pasta, modo_integracao) VALUES (?,?,?,?)`,
		"x", "x", "x", "modo_invalido",
	)
	if err == nil {
		t.Fatal("CHECK de modo_integracao deveria rejeitar valor inválido")
	}
}

func TestEngineAccountsExigeFK(t *testing.T) {
	// Foreign keys precisam estar ligadas para o INSERT órfão falhar.
	caminho := filepath.Join(t.TempDir(), "praxis.db")
	d, err := Abrir(caminho)
	if err != nil {
		t.Fatalf("Abrir: %v", err)
	}
	defer d.Fechar()

	_, err = d.Escritor.Exec(
		`INSERT INTO engine_accounts (engine_id, alias) VALUES (?,?)`, 999, "conta",
	)
	if err == nil {
		t.Fatal("FK deveria rejeitar engine_id inexistente")
	}
}

func TestConfigEntriesRespeitaEscopo(t *testing.T) {
	db := abrirBruto(t)
	if _, _, err := Migrar(db); err != nil {
		t.Fatalf("Migrar: %v", err)
	}
	// global exige project_id NULL
	if _, err := db.Exec(
		`INSERT INTO config_entries (escopo, project_id, chave, valor) VALUES ('global', 1, 'k', 'null')`,
	); err == nil {
		t.Fatal("CHECK deveria rejeitar global com project_id")
	}
	// global válido
	if _, err := db.Exec(
		`INSERT INTO config_entries (escopo, chave, valor) VALUES ('global', 'motor', '"claude"')`,
	); err != nil {
		t.Fatalf("global válido falhou: %v", err)
	}
	// chave global duplicada viola o índice único parcial
	if _, err := db.Exec(
		`INSERT INTO config_entries (escopo, chave, valor) VALUES ('global', 'motor', '"codex"')`,
	); err == nil {
		t.Fatal("índice único global deveria rejeitar chave duplicada")
	}
}

// existeNoSchema consulta sqlite_master pela existência de um objeto.
func existeNoSchema(t *testing.T, db *sql.DB, tipo, nome string) bool {
	t.Helper()
	var n int
	err := db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type = ? AND name = ?`, tipo, nome,
	).Scan(&n)
	if err != nil {
		t.Fatalf("consultar sqlite_master (%s %s): %v", tipo, nome, err)
	}
	return n > 0
}
