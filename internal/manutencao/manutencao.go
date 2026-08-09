// Package manutencao cuida da operação de longo prazo do serviço (Fase 5d):
// backup periódico do banco, rotação dos backups e retenção de logs/eventos.
package manutencao

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Store é o mínimo do banco que a manutenção usa.
type Store interface {
	BackupPara(ctx context.Context, destino string) error
	RemoverEventosAntesDe(ctx context.Context, corte string) (int64, error)
}

// Opcoes configura a manutenção.
type Opcoes struct {
	Store         Store
	DirBackups    string        // onde gravar os backups (ex.: PRAXIS_HOME/backups)
	DirLogs       string        // onde ficam os .jsonl a reter (ex.: PRAXIS_HOME/logs)
	ManterBackups int           // quantos backups manter (default 7)
	RetencaoDias  int           // idade máxima de logs/eventos em dias (default 30)
	Intervalo     time.Duration // periodicidade do ciclo (default 24h)
	Log           func(string)
}

// Manutencao executa os ciclos de backup/retenção.
type Manutencao struct {
	o Opcoes
}

// Nova monta a manutenção aplicando os defaults.
func Nova(o Opcoes) *Manutencao {
	if o.ManterBackups <= 0 {
		o.ManterBackups = 7
	}
	if o.RetencaoDias <= 0 {
		o.RetencaoDias = 30
	}
	if o.Intervalo <= 0 {
		o.Intervalo = 24 * time.Hour
	}
	if o.Log == nil {
		o.Log = func(string) {}
	}
	return &Manutencao{o: o}
}

// Rodar executa um ciclo imediatamente e depois a cada Intervalo, até ctx ser
// cancelado.
func (m *Manutencao) Rodar(ctx context.Context) {
	m.Ciclo(ctx, time.Now())
	ticker := time.NewTicker(m.o.Intervalo)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case t := <-ticker.C:
			m.Ciclo(ctx, t)
		}
	}
}

// Ciclo executa um único ciclo de manutenção (backup + rotação + retenção),
// carimbando com `agora`. Best-effort: cada etapa loga e segue. Exposto para os
// testes exercitarem um ciclo determinístico.
func (m *Manutencao) Ciclo(ctx context.Context, agora time.Time) {
	if m.o.DirBackups != "" && m.o.Store != nil {
		if caminho, err := m.Backup(ctx, agora); err != nil {
			m.o.Log("manutenção: backup falhou: " + err.Error())
		} else {
			m.o.Log("manutenção: backup em " + caminho)
			if err := RotacionarBackups(m.o.DirBackups, m.o.ManterBackups); err != nil {
				m.o.Log("manutenção: rotação de backups falhou: " + err.Error())
			}
		}
	}

	corte := agora.AddDate(0, 0, -m.o.RetencaoDias)
	if m.o.Store != nil {
		if n, err := m.o.Store.RemoverEventosAntesDe(ctx, corte.UTC().Format(time.RFC3339)); err != nil {
			m.o.Log("manutenção: retenção de eventos falhou: " + err.Error())
		} else if n > 0 {
			m.o.Log(fmt.Sprintf("manutenção: %d eventos antigos removidos", n))
		}
	}
	if m.o.DirLogs != "" {
		if n, err := LimparLogs(m.o.DirLogs, corte); err != nil {
			m.o.Log("manutenção: retenção de logs falhou: " + err.Error())
		} else if n > 0 {
			m.o.Log(fmt.Sprintf("manutenção: %d arquivos de log antigos removidos", n))
		}
	}
}

// Backup grava um backup do banco em DirBackups com nome carimbado por `agora`.
// Devolve o caminho gravado.
func (m *Manutencao) Backup(ctx context.Context, agora time.Time) (string, error) {
	if err := os.MkdirAll(m.o.DirBackups, 0o755); err != nil {
		return "", err
	}
	nome := fmt.Sprintf("praxis-%s.db", agora.UTC().Format("20060102-150405"))
	destino := filepath.Join(m.o.DirBackups, nome)
	if err := m.o.Store.BackupPara(ctx, destino); err != nil {
		return "", err
	}
	return destino, nil
}

// RotacionarBackups mantém apenas os `manter` backups mais recentes em dir
// (arquivos praxis-*.db), apagando os excedentes mais antigos.
func RotacionarBackups(dir string, manter int) error {
	if manter <= 0 {
		return nil
	}
	entradas, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	nomes := []string{}
	for _, e := range entradas {
		if !e.IsDir() && strings.HasPrefix(e.Name(), "praxis-") && strings.HasSuffix(e.Name(), ".db") {
			nomes = append(nomes, e.Name())
		}
	}
	if len(nomes) <= manter {
		return nil
	}
	sort.Strings(nomes) // o carimbo YYYYMMDD-HHMMSS ordena cronologicamente
	// remove os mais antigos (do início), preservando os `manter` finais.
	for _, nome := range nomes[:len(nomes)-manter] {
		if err := os.Remove(filepath.Join(dir, nome)); err != nil {
			return err
		}
	}
	return nil
}

// LimparLogs remove os arquivos em dir (recursivamente) cuja data de modificação
// é anterior a `corte`. Devolve quantos foram removidos. Diretórios vazios são
// mantidos. Usada para reter os .jsonl de execução.
func LimparLogs(dir string, corte time.Time) (int, error) {
	entradas, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	removidos := 0
	for _, e := range entradas {
		caminho := filepath.Join(dir, e.Name())
		if e.IsDir() {
			n, err := LimparLogs(caminho, corte)
			if err != nil {
				return removidos, err
			}
			removidos += n
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(corte) {
			if err := os.Remove(caminho); err == nil {
				removidos++
			}
		}
	}
	return removidos, nil
}
