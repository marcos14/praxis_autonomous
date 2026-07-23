package db

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func motorExemplo() Motor {
	return Motor{
		Nome:          "claude",
		Ativo:         true,
		Fallback:      true,
		ModeloExec:    "opus",
		ModeloAnalise: "sonnet",
		BudgetFaseUSD: 6.0,
		TimeoutMin:    45,
		Params:        json.RawMessage(`{"foo":"bar"}`),
	}
}

// TestMotorFallbackRoundTrip: o switch de participação no fallback persiste na
// criação e na atualização.
func TestMotorFallbackRoundTrip(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()

	m, err := d.CriarMotor(ctx, motorExemplo())
	if err != nil {
		t.Fatalf("CriarMotor: %v", err)
	}
	lido, err := d.ObterMotor(ctx, m.ID)
	if err != nil {
		t.Fatalf("ObterMotor: %v", err)
	}
	if !lido.Fallback {
		t.Fatal("fallback deveria vir true")
	}

	lido.Fallback = false
	atualizado, err := d.AtualizarMotor(ctx, lido)
	if err != nil {
		t.Fatalf("AtualizarMotor: %v", err)
	}
	if atualizado.Fallback {
		t.Fatal("fallback deveria persistir false (motor de uso manual)")
	}
}

func criarMotorTeste(t *testing.T, d *DB, nome string) Motor {
	t.Helper()
	m := motorExemplo()
	m.Nome = nome
	prox, err := d.ProximaPrioridadeMotor(context.Background())
	if err != nil {
		t.Fatalf("ProximaPrioridadeMotor: %v", err)
	}
	m.Prioridade = prox
	criado, err := d.CriarMotor(context.Background(), m)
	if err != nil {
		t.Fatalf("CriarMotor(%s): %v", nome, err)
	}
	return criado
}

func TestCriarMotorPreencheID(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()

	m, err := d.CriarMotor(ctx, motorExemplo())
	if err != nil {
		t.Fatalf("CriarMotor: %v", err)
	}
	if m.ID == 0 {
		t.Fatal("ID não preenchido")
	}
	if m.Contas == nil {
		t.Fatal("Contas deve ser slice não-nil")
	}
	if string(m.Params) != `{"foo":"bar"}` {
		t.Fatalf("Params = %s", m.Params)
	}
}

func TestCriarMotorParamsVazioViraObjeto(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()

	m := motorExemplo()
	m.Params = nil
	criado, err := d.CriarMotor(ctx, m)
	if err != nil {
		t.Fatalf("CriarMotor: %v", err)
	}
	if string(criado.Params) != "{}" {
		t.Fatalf("Params = %s, quero {}", criado.Params)
	}
}

func TestCriarMotorNomeDuplicado(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()

	if _, err := d.CriarMotor(ctx, motorExemplo()); err != nil {
		t.Fatalf("primeira criação: %v", err)
	}
	_, err := d.CriarMotor(ctx, motorExemplo())
	if !errors.Is(err, ErrNomeDuplicado) {
		t.Fatalf("erro = %v, quero ErrNomeDuplicado", err)
	}
}

func TestProximaPrioridadeMotor(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()

	prox, err := d.ProximaPrioridadeMotor(ctx)
	if err != nil {
		t.Fatalf("ProximaPrioridadeMotor: %v", err)
	}
	if prox != 0 {
		t.Fatalf("prioridade inicial = %d, quero 0", prox)
	}
	criarMotorTeste(t, d, "claude")
	criarMotorTeste(t, d, "codex")
	prox, err = d.ProximaPrioridadeMotor(ctx)
	if err != nil {
		t.Fatalf("ProximaPrioridadeMotor: %v", err)
	}
	if prox != 2 {
		t.Fatalf("próxima prioridade = %d, quero 2", prox)
	}
}

func TestListarMotoresOrdenadoPorPrioridade(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()

	criarMotorTeste(t, d, "claude")   // prioridade 0
	criarMotorTeste(t, d, "codex")    // prioridade 1
	criarMotorTeste(t, d, "opencode") // prioridade 2

	motores, err := d.ListarMotores(ctx)
	if err != nil {
		t.Fatalf("ListarMotores: %v", err)
	}
	if len(motores) != 3 {
		t.Fatalf("len = %d, quero 3", len(motores))
	}
	quer := []string{"claude", "codex", "opencode"}
	for i, nome := range quer {
		if motores[i].Nome != nome {
			t.Fatalf("motores[%d] = %s, quero %s", i, motores[i].Nome, nome)
		}
		if motores[i].Contas == nil {
			t.Fatalf("motores[%d].Contas nil", i)
		}
	}
}

func TestListarMotoresVazioNaoNil(t *testing.T) {
	d := abrirTemp(t)
	motores, err := d.ListarMotores(context.Background())
	if err != nil {
		t.Fatalf("ListarMotores: %v", err)
	}
	if motores == nil {
		t.Fatal("lista deve ser não-nil")
	}
	if len(motores) != 0 {
		t.Fatalf("len = %d, quero 0", len(motores))
	}
}

func TestObterMotorInexistente(t *testing.T) {
	d := abrirTemp(t)
	_, err := d.ObterMotor(context.Background(), 999)
	if !errors.Is(err, ErrNaoEncontrado) {
		t.Fatalf("erro = %v, quero ErrNaoEncontrado", err)
	}
}

func TestAtualizarMotorNaoAlteraPrioridade(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()

	criarMotorTeste(t, d, "claude") // prioridade 0
	m := criarMotorTeste(t, d, "codex")
	if m.Prioridade != 1 {
		t.Fatalf("prioridade = %d, quero 1", m.Prioridade)
	}

	m.Nome = "codex-x"
	m.BudgetFaseUSD = 9.5
	atualizado, err := d.AtualizarMotor(ctx, m)
	if err != nil {
		t.Fatalf("AtualizarMotor: %v", err)
	}
	if atualizado.Nome != "codex-x" {
		t.Fatalf("Nome = %s", atualizado.Nome)
	}
	if atualizado.BudgetFaseUSD != 9.5 {
		t.Fatalf("Budget = %v", atualizado.BudgetFaseUSD)
	}
	if atualizado.Prioridade != 1 {
		t.Fatalf("prioridade mudou para %d, deveria manter 1", atualizado.Prioridade)
	}
}

func TestAtualizarMotorInexistente(t *testing.T) {
	d := abrirTemp(t)
	m := motorExemplo()
	m.ID = 999
	_, err := d.AtualizarMotor(context.Background(), m)
	if !errors.Is(err, ErrNaoEncontrado) {
		t.Fatalf("erro = %v, quero ErrNaoEncontrado", err)
	}
}

func TestReordenarMotores(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()

	a := criarMotorTeste(t, d, "claude")   // prioridade 0
	b := criarMotorTeste(t, d, "codex")    // prioridade 1
	c := criarMotorTeste(t, d, "opencode") // prioridade 2

	// Inverte a ordem: opencode, codex, claude.
	if err := d.ReordenarMotores(ctx, []int64{c.ID, b.ID, a.ID}); err != nil {
		t.Fatalf("ReordenarMotores: %v", err)
	}
	motores, err := d.ListarMotores(ctx)
	if err != nil {
		t.Fatalf("ListarMotores: %v", err)
	}
	quer := []string{"opencode", "codex", "claude"}
	for i, nome := range quer {
		if motores[i].Nome != nome {
			t.Fatalf("motores[%d] = %s, quero %s", i, motores[i].Nome, nome)
		}
		if motores[i].Prioridade != i {
			t.Fatalf("motores[%d].Prioridade = %d, quero %d", i, motores[i].Prioridade, i)
		}
	}
}

func TestReordenarMotoresListaIncompleta(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()
	a := criarMotorTeste(t, d, "claude")
	criarMotorTeste(t, d, "codex")

	if err := d.ReordenarMotores(ctx, []int64{a.ID}); !errors.Is(err, ErrOrdemInvalida) {
		t.Fatalf("erro = %v, quero ErrOrdemInvalida", err)
	}
}

func TestReordenarMotoresIDDesconhecido(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()
	a := criarMotorTeste(t, d, "claude")
	b := criarMotorTeste(t, d, "codex")

	if err := d.ReordenarMotores(ctx, []int64{a.ID, 999}); !errors.Is(err, ErrOrdemInvalida) {
		t.Fatalf("erro = %v, quero ErrOrdemInvalida", err)
	}
	// id repetido também é inválido.
	if err := d.ReordenarMotores(ctx, []int64{a.ID, a.ID}); !errors.Is(err, ErrOrdemInvalida) {
		t.Fatalf("erro (repetido) = %v, quero ErrOrdemInvalida", err)
	}
	_ = b
}

func TestContasCRUD(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()
	m := criarMotorTeste(t, d, "claude")

	c1, err := d.CriarConta(ctx, Conta{EngineID: m.ID, Alias: "principal", ConfigDir: `C:\cfg\p`, Ativo: true})
	if err != nil {
		t.Fatalf("CriarConta: %v", err)
	}
	if c1.ID == 0 {
		t.Fatal("conta sem ID")
	}
	if _, err := d.CriarConta(ctx, Conta{EngineID: m.ID, Alias: "secundaria", Ativo: true}); err != nil {
		t.Fatalf("CriarConta 2: %v", err)
	}

	// ObterMotor traz as contas.
	obtido, err := d.ObterMotor(ctx, m.ID)
	if err != nil {
		t.Fatalf("ObterMotor: %v", err)
	}
	if len(obtido.Contas) != 2 {
		t.Fatalf("contas = %d, quero 2", len(obtido.Contas))
	}

	// Atualiza.
	c1.Alias = "principal-2"
	c1.Ativo = false
	atualizada, err := d.AtualizarConta(ctx, c1)
	if err != nil {
		t.Fatalf("AtualizarConta: %v", err)
	}
	if atualizada.Alias != "principal-2" || atualizada.Ativo {
		t.Fatalf("conta não atualizada: %+v", atualizada)
	}

	// Remove.
	if err := d.RemoverConta(ctx, m.ID, c1.ID); err != nil {
		t.Fatalf("RemoverConta: %v", err)
	}
	obtido, err = d.ObterMotor(ctx, m.ID)
	if err != nil {
		t.Fatalf("ObterMotor pós-remoção: %v", err)
	}
	if len(obtido.Contas) != 1 {
		t.Fatalf("contas pós-remoção = %d, quero 1", len(obtido.Contas))
	}
}

func TestCriarContaAliasDuplicado(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()
	m := criarMotorTeste(t, d, "claude")

	if _, err := d.CriarConta(ctx, Conta{EngineID: m.ID, Alias: "principal"}); err != nil {
		t.Fatalf("primeira conta: %v", err)
	}
	_, err := d.CriarConta(ctx, Conta{EngineID: m.ID, Alias: "principal"})
	if !errors.Is(err, ErrAliasDuplicado) {
		t.Fatalf("erro = %v, quero ErrAliasDuplicado", err)
	}
}

func TestCriarContaMotorInexistente(t *testing.T) {
	d := abrirTemp(t)
	_, err := d.CriarConta(context.Background(), Conta{EngineID: 999, Alias: "x"})
	if !errors.Is(err, ErrNaoEncontrado) {
		t.Fatalf("erro = %v, quero ErrNaoEncontrado", err)
	}
}

func TestAliasDuplicadoEmMotoresDiferentesOK(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()
	m1 := criarMotorTeste(t, d, "claude")
	m2 := criarMotorTeste(t, d, "codex")

	if _, err := d.CriarConta(ctx, Conta{EngineID: m1.ID, Alias: "principal"}); err != nil {
		t.Fatalf("conta m1: %v", err)
	}
	if _, err := d.CriarConta(ctx, Conta{EngineID: m2.ID, Alias: "principal"}); err != nil {
		t.Fatalf("conta m2 (alias igual, motor diferente) deveria ser OK: %v", err)
	}
}

func TestContaAtivaParaDistribuiPorAfinidade(t *testing.T) {
	m := Motor{Contas: []Conta{
		{ID: 1, Alias: "inativa", Ativo: false},
		{ID: 2, Alias: "a", ConfigDir: "perfil-a", Ativo: true},
		{ID: 3, Alias: "b", ConfigDir: "perfil-b", Ativo: true},
	}}
	casos := []struct {
		afinidade int64
		quer      int64
	}{{0, 2}, {1, 2}, {2, 3}, {3, 2}}
	for _, tc := range casos {
		got, ok := ContaAtivaPara(m, tc.afinidade)
		if !ok || got.ID != tc.quer {
			t.Fatalf("afinidade %d: conta=%+v ok=%v, quero id=%d", tc.afinidade, got, ok, tc.quer)
		}
	}
}

func TestRemoverContaInexistente(t *testing.T) {
	d := abrirTemp(t)
	m := criarMotorTeste(t, d, "claude")
	if err := d.RemoverConta(context.Background(), m.ID, 999); !errors.Is(err, ErrNaoEncontrado) {
		t.Fatalf("erro = %v, quero ErrNaoEncontrado", err)
	}
}

func TestRemoverMotorCascadeiaContas(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()
	m := criarMotorTeste(t, d, "claude")
	if _, err := d.CriarConta(ctx, Conta{EngineID: m.ID, Alias: "principal"}); err != nil {
		t.Fatalf("CriarConta: %v", err)
	}
	// Deleta o motor direto (não há método público; testa o ON DELETE CASCADE do schema).
	if _, err := d.Escritor.ExecContext(ctx, `DELETE FROM engines WHERE id = ?`, m.ID); err != nil {
		t.Fatalf("delete motor: %v", err)
	}
	var n int
	if err := d.Leitor.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM engine_accounts WHERE engine_id = ?`, m.ID).Scan(&n); err != nil {
		t.Fatalf("contar contas: %v", err)
	}
	if n != 0 {
		t.Fatalf("contas órfãs = %d, quero 0 (cascade)", n)
	}
}
