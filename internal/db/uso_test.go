package db

import (
	"context"
	"math"
	"testing"
	"time"
)

// aproximado compara custos somados em ponto flutuante.
func aproximado(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestUsoPraxisPorConta(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()
	dem := demandaTeste(t, d, "a")
	agora := time.Date(2026, 7, 23, 15, 0, 0, 0, time.UTC)

	inserirRun := func(iniciadoEm, engine, conta string, custo float64, tokIn, tokOut int64) {
		t.Helper()
		if _, err := d.Escritor.ExecContext(ctx, `
			INSERT INTO runs (demand_id, operacao, engine, conta, custo_usd, tokens_in, tokens_out, iniciado_em)
			VALUES (?,?,?,?,?,?,?,?)`,
			dem, OperacaoExecutor, engine, conta, custo, tokIn, tokOut, iniciadoEm); err != nil {
			t.Fatalf("inserir run: %v", err)
		}
	}

	// hoje (2 execuções), há 3 dias (1) e há 30 dias (1) no perfil principal…
	inserirRun("2026-07-23T10:00:00.000Z", "claude", "principal", 0.10, 100, 10)
	inserirRun("2026-07-23T12:00:00.000Z", "claude", "principal", 0.20, 200, 20)
	inserirRun("2026-07-20T12:00:00.000Z", "claude", "principal", 0.40, 400, 40)
	inserirRun("2026-06-23T12:00:00.000Z", "claude", "principal", 0.80, 800, 80)
	// …uma execução antiga sem perfil e uma de outro motor.
	inserirRun("2026-06-23T12:00:00.000Z", "claude", "", 0.05, 50, 5)
	inserirRun("2026-07-23T13:00:00.000Z", "codex", "x1", 0.30, 300, 30)

	// consulta_runs também conta (turno de consulta de hoje no perfil principal).
	pid := criarProjetoTeste(t, d, "uso")
	if _, err := d.Escritor.ExecContext(ctx, `
		INSERT INTO consulta_runs (project_id, operacao, engine, conta, custo_usd, tokens_in, tokens_out, iniciado_em)
		VALUES (?,?,?,?,?,?,?,?)`,
		pid, OperacaoOverview, "claude", "principal", 0.15, 150, 15, "2026-07-23T14:00:00.000Z"); err != nil {
		t.Fatalf("inserir consulta_run: %v", err)
	}

	usos, err := d.UsoPraxisPorConta(ctx, agora)
	if err != nil {
		t.Fatalf("UsoPraxisPorConta: %v", err)
	}
	porChave := map[[2]string]UsoPraxis{}
	for _, u := range usos {
		porChave[[2]string{u.Engine, u.Conta}] = u
	}

	principal, ok := porChave[[2]string{"claude", "principal"}]
	if !ok {
		t.Fatalf("sem agregação para claude/principal: %+v", usos)
	}
	// hoje: 2 runs + 1 consulta = 3 execuções, US$ 0.45, 450/45 tokens.
	if principal.Hoje.Execucoes != 3 || !aproximado(principal.Hoje.CustoUSD, 0.45) {
		t.Fatalf("hoje = %+v, quero 3 execuções / US$ 0.45", principal.Hoje)
	}
	if principal.Hoje.TokensIn != 450 || principal.Hoje.TokensOut != 45 {
		t.Fatalf("tokens de hoje = %+v", principal.Hoje)
	}
	// 7 dias: hoje + o run de 3 dias atrás.
	if principal.Ultimos7Dias.Execucoes != 4 || !aproximado(principal.Ultimos7Dias.CustoUSD, 0.85) {
		t.Fatalf("7 dias = %+v, quero 4 execuções / US$ 0.85", principal.Ultimos7Dias)
	}
	// total: tudo.
	if principal.Total.Execucoes != 5 || !aproximado(principal.Total.CustoUSD, 1.65) {
		t.Fatalf("total = %+v, quero 5 execuções / US$ 1.65", principal.Total)
	}

	semPerfil, ok := porChave[[2]string{"claude", ""}]
	if !ok || semPerfil.Total.Execucoes != 1 || semPerfil.Hoje.Execucoes != 0 {
		t.Fatalf("agregação sem perfil = %+v (ok=%v)", semPerfil, ok)
	}
	codex, ok := porChave[[2]string{"codex", "x1"}]
	if !ok || codex.Hoje.Execucoes != 1 || !aproximado(codex.Total.CustoUSD, 0.30) {
		t.Fatalf("agregação codex = %+v (ok=%v)", codex, ok)
	}
}

func TestUsoPraxisPorContaVazio(t *testing.T) {
	d := abrirTemp(t)
	usos, err := d.UsoPraxisPorConta(context.Background(), time.Now().UTC())
	if err != nil {
		t.Fatalf("UsoPraxisPorConta: %v", err)
	}
	if usos == nil || len(usos) != 0 {
		t.Fatalf("esperava slice vazio não-nil, veio %#v", usos)
	}
}
