// Package db concentra o acesso ao SQLite: abertura da conexão com as garantias
// de concorrência (WAL, busy_timeout, escritor único + pool de leitura),
// migrações versionadas por PRAGMA user_version e os stores de cada entidade.
//
// A implementação começa na Fase 1a. Aqui o pacote apenas ancora o driver de
// runtime único do projeto: modernc.org/sqlite (SQLite puro Go, sem cgo).
package db

// O blank import registra o driver "sqlite" em database/sql e mantém a
// dependência declarada no go.mod desde a fundação do repositório (Fase 0).
import _ "modernc.org/sqlite"
