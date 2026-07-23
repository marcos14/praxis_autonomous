// Package uso mantém a leitura periódica da franquia dos perfis dos motores.
// O monitor roda em background (mesma vida do serviço), consulta o CLI de cada
// perfil ativo e guarda apenas o último snapshot sanitizado em memória — a
// fonte de verdade continua sendo o vendor; reiniciar o serviço só zera o cache
// até o próximo ciclo.
package uso

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/marcos14/praxis-autonomous/internal/db"
	"github.com/marcos14/praxis-autonomous/internal/motor"
)

// IntervaloPadraoMin é o intervalo default entre verificações, em minutos. A
// chave global `uso_intervalo_min` sobrepõe (relida a cada ciclo — ajustar não
// exige reinício).
const IntervaloPadraoMin = 5

// ChaveIntervalo é a chave da config global que ajusta o intervalo.
const ChaveIntervalo = "uso_intervalo_min"

// Opcoes configura o Monitor.
type Opcoes struct {
	Store *db.DB
	// Consultar é o seam de leitura da franquia (default motor.ConsultarFranquia).
	Consultar func(ctx context.Context, vendor, perfilDir string) motor.UsoFranquia
	Log       func(msg string)
}

// Monitor verifica periodicamente a franquia de cada perfil ativo e guarda o
// último snapshot por conta (id de engine_accounts).
type Monitor struct {
	store     *db.DB
	consultar func(ctx context.Context, vendor, perfilDir string) motor.UsoFranquia
	log       func(string)

	mu        sync.Mutex
	snapshots map[int64]motor.UsoFranquia
}

// Novo cria o monitor (não inicia o laço — chame Rodar numa goroutine).
func Novo(o Opcoes) *Monitor {
	consultar := o.Consultar
	if consultar == nil {
		consultar = motor.ConsultarFranquia
	}
	logf := o.Log
	if logf == nil {
		logf = func(string) {}
	}
	return &Monitor{store: o.Store, consultar: consultar, log: logf,
		snapshots: map[int64]motor.UsoFranquia{}}
}

// Rodar executa o laço do monitor até ctx ser cancelado: verifica todos os
// perfis, dorme o intervalo configurado (relido a cada ciclo) e repete.
func (m *Monitor) Rodar(ctx context.Context) {
	for {
		m.VerificarAgora(ctx)
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Duration(m.IntervaloMin(ctx)) * time.Minute):
		}
	}
}

// VerificarAgora percorre os motores ativos com perfil isolado e atualiza o
// snapshot de franquia de cada conta ativa. Erros de listagem são logados e o
// ciclo segue (o snapshot anterior permanece).
func (m *Monitor) VerificarAgora(ctx context.Context) {
	if m == nil || m.store == nil {
		return
	}
	motores, err := m.store.ListarMotores(ctx)
	if err != nil {
		m.log("monitor de uso: listar motores: " + err.Error())
		return
	}
	for _, mo := range motores {
		if !mo.Ativo || !motor.VendorComPerfilIsolado(mo.Nome) {
			continue
		}
		for _, c := range mo.Contas {
			if !c.Ativo {
				continue
			}
			u := m.consultar(ctx, mo.Nome, c.ConfigDir)
			m.mu.Lock()
			m.snapshots[c.ID] = u
			m.mu.Unlock()
			if ctx.Err() != nil {
				return
			}
		}
	}
}

// Snapshot devolve a última leitura de franquia da conta (ok=false antes do
// primeiro ciclo alcançá-la).
func (m *Monitor) Snapshot(contaID int64) (motor.UsoFranquia, bool) {
	if m == nil {
		return motor.UsoFranquia{}, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.snapshots[contaID]
	return u, ok
}

// IntervaloMin lê o intervalo configurado em minutos (config global
// `uso_intervalo_min`), caindo no default e nunca abaixo de 1.
func (m *Monitor) IntervaloMin(ctx context.Context) int {
	if m == nil || m.store == nil {
		return IntervaloPadraoMin
	}
	entradas, err := m.store.ObterConfigGlobal(ctx)
	if err != nil {
		m.log("monitor de uso: ler config global: " + err.Error())
		return IntervaloPadraoMin
	}
	bruto, ok := entradas[ChaveIntervalo]
	if !ok {
		return IntervaloPadraoMin
	}
	var v float64
	if err := json.Unmarshal(bruto, &v); err != nil || v <= 0 {
		return IntervaloPadraoMin
	}
	if v < 1 {
		return 1
	}
	return int(v)
}
