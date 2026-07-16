// Package db concentra o acesso ao SQLite: abertura da conexão com as garantias
// de concorrência (WAL, busy_timeout, escritor único + pool de leitura),
// migrações versionadas por PRAGMA user_version e os stores de cada entidade.
//
// Implementado a partir da Fase 1a:
//   - Abrir/AbrirPadrao: conexões (escritor único + pool de leitura) — db.go
//   - Migrar: framework de migrações por PRAGMA user_version — migracoes.go
//   - PraxisHome/CaminhoDB: resolução de PRAXIS_HOME — paths.go
//
// O driver de runtime único do projeto — modernc.org/sqlite (SQLite puro Go,
// sem cgo) — é registrado via blank import em db.go.
package db
