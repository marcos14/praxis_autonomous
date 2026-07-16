package db

import (
	"database/sql"
	"fmt"
	"net/url"
	"runtime"

	// Registra o driver "sqlite" (modernc.org/sqlite) em database/sql. É o
	// único driver de runtime do projeto — SQLite puro Go, sem cgo.
	_ "modernc.org/sqlite"
)

// nomeDriver é o nome com que o modernc.org/sqlite se registra em database/sql.
// Atenção: é "sqlite" (não "sqlite3", que é do driver mattn/cgo).
const nomeDriver = "sqlite"

// busyTimeoutMS é o tempo (ms) que uma conexão espera em SQLITE_BUSY antes de
// falhar. Combinado com WAL + escritor único, evita a maioria dos SQLITE_BUSY.
const busyTimeoutMS = 5000

// DB agrupa as conexões do Praxis conforme o desenho de concorrência do plano:
// um escritor único (SetMaxOpenConns(1)) serializa todas as escritas e um pool
// de leitura atende consultas concorrentes. WAL permite leituras simultâneas à
// escrita em andamento.
//
// Use Escritor para INSERT/UPDATE/DELETE e migrações; use Leitor para SELECT. O
// Leitor é aberto em modo query_only, portanto qualquer escrita por ele falha —
// é uma salvaguarda proposital contra uso incorreto.
type DB struct {
	Escritor *sql.DB
	Leitor   *sql.DB
	Caminho  string
}

// dsnEscritor monta a DSN da conexão de escrita. As PRAGMAs são passadas via
// parâmetros _pragma= (suportados pelo modernc.org/sqlite), garantindo que toda
// conexão criada pelo pool receba as mesmas garantias.
func dsnEscritor(caminho string) string {
	v := url.Values{}
	v.Add("_pragma", fmt.Sprintf("busy_timeout(%d)", busyTimeoutMS))
	v.Add("_pragma", "journal_mode(WAL)")
	v.Add("_pragma", "synchronous(NORMAL)")
	v.Add("_pragma", "foreign_keys(1)")
	return "file:" + caminho + "?" + v.Encode()
}

// dsnLeitor monta a DSN do pool de leitura. Não define journal_mode (WAL já fica
// persistido no arquivo pelo escritor) e adiciona query_only(1) para impedir
// escritas por engano.
func dsnLeitor(caminho string) string {
	v := url.Values{}
	v.Add("_pragma", fmt.Sprintf("busy_timeout(%d)", busyTimeoutMS))
	v.Add("_pragma", "foreign_keys(1)")
	v.Add("_pragma", "query_only(1)")
	return "file:" + caminho + "?" + v.Encode()
}

// Abrir abre (ou cria) o banco em caminho, aplica as migrações pendentes e
// devolve as conexões de escrita e leitura já configuradas. O escritor é aberto
// e migrado antes do leitor, garantindo que o arquivo exista e o schema esteja
// atualizado quando o pool de leitura começar a atender.
func Abrir(caminho string) (*DB, error) {
	escritor, err := sql.Open(nomeDriver, dsnEscritor(caminho))
	if err != nil {
		return nil, fmt.Errorf("abrir escritor %q: %w", caminho, err)
	}
	// Escritor único: serializa escritas e evita SQLITE_BUSY entre goroutines.
	escritor.SetMaxOpenConns(1)
	escritor.SetMaxIdleConns(1)
	escritor.SetConnMaxLifetime(0)
	if err := escritor.Ping(); err != nil {
		escritor.Close()
		return nil, fmt.Errorf("conectar escritor %q: %w", caminho, err)
	}
	if _, _, err := Migrar(escritor); err != nil {
		escritor.Close()
		return nil, err
	}

	leitor, err := sql.Open(nomeDriver, dsnLeitor(caminho))
	if err != nil {
		escritor.Close()
		return nil, fmt.Errorf("abrir leitor %q: %w", caminho, err)
	}
	maxLeitores := runtime.NumCPU()
	if maxLeitores < 4 {
		maxLeitores = 4
	}
	leitor.SetMaxOpenConns(maxLeitores)
	leitor.SetMaxIdleConns(maxLeitores)
	leitor.SetConnMaxLifetime(0)
	if err := leitor.Ping(); err != nil {
		leitor.Close()
		escritor.Close()
		return nil, fmt.Errorf("conectar leitor %q: %w", caminho, err)
	}

	return &DB{Escritor: escritor, Leitor: leitor, Caminho: caminho}, nil
}

// AbrirPadrao resolve o caminho de praxis.db a partir do PRAXIS_HOME (criando o
// diretório se preciso) e abre o banco. É a forma usada pelo `serve`.
func AbrirPadrao() (*DB, error) {
	caminho, err := CaminhoDB()
	if err != nil {
		return nil, err
	}
	return Abrir(caminho)
}

// Fechar encerra as duas conexões. Retorna o primeiro erro encontrado, mas
// sempre tenta fechar ambas.
func (d *DB) Fechar() error {
	var primeiro error
	if d.Leitor != nil {
		if err := d.Leitor.Close(); err != nil {
			primeiro = err
		}
	}
	if d.Escritor != nil {
		if err := d.Escritor.Close(); err != nil && primeiro == nil {
			primeiro = err
		}
	}
	return primeiro
}
