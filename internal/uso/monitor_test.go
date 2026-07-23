package uso

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/marcos14/praxis-autonomous/internal/db"
	"github.com/marcos14/praxis-autonomous/internal/motor"
)

func abrirTempDB(t *testing.T) *db.DB {
	t.Helper()
	d, err := db.Abrir(filepath.Join(t.TempDir(), "praxis.db"))
	if err != nil {
		t.Fatalf("abrir banco: %v", err)
	}
	t.Cleanup(func() { _ = d.Fechar() })
	return d
}

func TestMonitorVerificaSoPerfisAtivos(t *testing.T) {
	d := abrirTempDB(t)
	ctx := context.Background()

	claude, err := d.CriarMotor(ctx, db.Motor{Nome: "claude", Ativo: true, Fallback: true})
	if err != nil {
		t.Fatal(err)
	}
	inativo, err := d.CriarMotor(ctx, db.Motor{Nome: "codex", Ativo: false, Fallback: true})
	if err != nil {
		t.Fatal(err)
	}
	ativa, err := d.CriarConta(ctx, db.Conta{EngineID: claude.ID, Alias: "principal", ConfigDir: t.TempDir(), Ativo: true})
	if err != nil {
		t.Fatal(err)
	}
	desligada, err := d.CriarConta(ctx, db.Conta{EngineID: claude.ID, Alias: "reserva", ConfigDir: t.TempDir(), Ativo: false})
	if err != nil {
		t.Fatal(err)
	}
	contaInativo, err := d.CriarConta(ctx, db.Conta{EngineID: inativo.ID, Alias: "x1", ConfigDir: t.TempDir(), Ativo: true})
	if err != nil {
		t.Fatal(err)
	}

	var consultados []string
	m := Novo(Opcoes{Store: d, Consultar: func(_ context.Context, vendor, dir string) motor.UsoFranquia {
		consultados = append(consultados, vendor)
		return motor.UsoFranquia{Vendor: vendor, Disponivel: true, VerificadoEm: time.Now().UTC(),
			Janelas: []motor.JanelaFranquia{{Rotulo: "5h", UsadoPct: 42}}}
	}})
	m.VerificarAgora(ctx)

	if len(consultados) != 1 || consultados[0] != "claude" {
		t.Fatalf("consultados = %v, quero só o perfil ativo do motor ativo", consultados)
	}
	u, ok := m.Snapshot(ativa.ID)
	if !ok || !u.Disponivel || len(u.Janelas) != 1 || u.Janelas[0].UsadoPct != 42 {
		t.Fatalf("snapshot da conta ativa = %+v (ok=%v)", u, ok)
	}
	if _, ok := m.Snapshot(desligada.ID); ok {
		t.Fatal("conta desativada não deveria ter snapshot")
	}
	if _, ok := m.Snapshot(contaInativo.ID); ok {
		t.Fatal("conta de motor inativo não deveria ter snapshot")
	}
}

func TestMonitorIntervaloConfiguravel(t *testing.T) {
	d := abrirTempDB(t)
	ctx := context.Background()
	m := Novo(Opcoes{Store: d})

	if got := m.IntervaloMin(ctx); got != IntervaloPadraoMin {
		t.Fatalf("intervalo default = %d, quero %d", got, IntervaloPadraoMin)
	}
	if err := d.DefinirConfigGlobal(ctx, map[string]json.RawMessage{ChaveIntervalo: json.RawMessage(`2`)}); err != nil {
		t.Fatalf("definir config: %v", err)
	}
	if got := m.IntervaloMin(ctx); got != 2 {
		t.Fatalf("intervalo configurado = %d, quero 2", got)
	}
	// valor inválido cai no default; fração abaixo de 1 vira o mínimo de 1.
	if err := d.DefinirConfigGlobal(ctx, map[string]json.RawMessage{ChaveIntervalo: json.RawMessage(`"abc"`)}); err != nil {
		t.Fatalf("definir config: %v", err)
	}
	if got := m.IntervaloMin(ctx); got != IntervaloPadraoMin {
		t.Fatalf("intervalo inválido = %d, quero o default %d", got, IntervaloPadraoMin)
	}
	if err := d.DefinirConfigGlobal(ctx, map[string]json.RawMessage{ChaveIntervalo: json.RawMessage(`0.5`)}); err != nil {
		t.Fatalf("definir config: %v", err)
	}
	if got := m.IntervaloMin(ctx); got != 1 {
		t.Fatalf("intervalo fracionário = %d, quero o mínimo 1", got)
	}
}
