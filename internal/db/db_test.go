package db

import (
	"path/filepath"
	"strings"
	"testing"
)

// abrirTemp abre um banco novo num arquivo temporário e agenda o fechamento.
func abrirTemp(t *testing.T) *DB {
	t.Helper()
	caminho := filepath.Join(t.TempDir(), "praxis.db")
	d, err := Abrir(caminho)
	if err != nil {
		t.Fatalf("Abrir: %v", err)
	}
	t.Cleanup(func() {
		if err := d.Fechar(); err != nil {
			t.Errorf("Fechar: %v", err)
		}
	})
	return d
}

func TestAbrirConfiguraEscritorUnico(t *testing.T) {
	d := abrirTemp(t)
	if got := d.Escritor.Stats().MaxOpenConnections; got != 1 {
		t.Fatalf("escritor MaxOpenConnections = %d, quero 1", got)
	}
	if got := d.Leitor.Stats().MaxOpenConnections; got < 1 {
		t.Fatalf("leitor MaxOpenConnections = %d, quero >= 1", got)
	}
}

func TestAbrirAplicaPragmas(t *testing.T) {
	d := abrirTemp(t)

	var journal string
	if err := d.Escritor.QueryRow("PRAGMA journal_mode").Scan(&journal); err != nil {
		t.Fatalf("ler journal_mode: %v", err)
	}
	if !strings.EqualFold(journal, "wal") {
		t.Fatalf("journal_mode = %q, quero wal", journal)
	}

	var busy int
	if err := d.Escritor.QueryRow("PRAGMA busy_timeout").Scan(&busy); err != nil {
		t.Fatalf("ler busy_timeout: %v", err)
	}
	if busy != busyTimeoutMS {
		t.Fatalf("busy_timeout = %d, quero %d", busy, busyTimeoutMS)
	}

	var fk int
	if err := d.Escritor.QueryRow("PRAGMA foreign_keys").Scan(&fk); err != nil {
		t.Fatalf("ler foreign_keys: %v", err)
	}
	if fk != 1 {
		t.Fatalf("foreign_keys = %d, quero 1", fk)
	}
}

func TestLeitorEhSomenteLeitura(t *testing.T) {
	d := abrirTemp(t)

	var qo int
	if err := d.Leitor.QueryRow("PRAGMA query_only").Scan(&qo); err != nil {
		t.Fatalf("ler query_only: %v", err)
	}
	if qo != 1 {
		t.Fatalf("query_only do leitor = %d, quero 1", qo)
	}

	// Escrita pelo leitor deve falhar (salvaguarda proposital).
	_, err := d.Leitor.Exec(`INSERT INTO projects (nome, slug, pasta) VALUES ('x','x','x')`)
	if err == nil {
		t.Fatal("escrita pelo leitor deveria falhar, mas passou")
	}
}

func TestEscreveNoEscritorLeNoLeitor(t *testing.T) {
	d := abrirTemp(t)

	if _, err := d.Escritor.Exec(
		`INSERT INTO projects (nome, slug, pasta) VALUES (?,?,?)`,
		"Praxis", "praxis", `C:\repo`,
	); err != nil {
		t.Fatalf("inserir projeto: %v", err)
	}

	var nome string
	if err := d.Leitor.QueryRow(`SELECT nome FROM projects WHERE slug = ?`, "praxis").Scan(&nome); err != nil {
		t.Fatalf("ler projeto pelo leitor: %v", err)
	}
	if nome != "Praxis" {
		t.Fatalf("nome = %q, quero Praxis", nome)
	}
}

func TestReabrirPreservaDadosEVersao(t *testing.T) {
	caminho := filepath.Join(t.TempDir(), "praxis.db")

	d1, err := Abrir(caminho)
	if err != nil {
		t.Fatalf("Abrir (1): %v", err)
	}
	if _, err := d1.Escritor.Exec(
		`INSERT INTO engines (nome, prioridade) VALUES (?,?)`, "claude", 0,
	); err != nil {
		t.Fatalf("inserir engine: %v", err)
	}
	if err := d1.Fechar(); err != nil {
		t.Fatalf("Fechar (1): %v", err)
	}

	d2, err := Abrir(caminho)
	if err != nil {
		t.Fatalf("Abrir (2): %v", err)
	}
	defer d2.Fechar()

	var n int
	if err := d2.Leitor.QueryRow(`SELECT COUNT(*) FROM engines WHERE nome = ?`, "claude").Scan(&n); err != nil {
		t.Fatalf("contar engines: %v", err)
	}
	if n != 1 {
		t.Fatalf("engines com nome claude = %d, quero 1", n)
	}
	v, err := VersaoAtual(d2.Escritor)
	if err != nil {
		t.Fatalf("VersaoAtual: %v", err)
	}
	if v != VersaoSchema() {
		t.Fatalf("user_version = %d, quero %d", v, VersaoSchema())
	}
}
