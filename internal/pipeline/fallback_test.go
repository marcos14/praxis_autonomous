package pipeline

import (
	"errors"
	"testing"
	"time"

	"github.com/marcos14/praxis-autonomous/internal/motor"
)

func TestProximoMotorFallback(t *testing.T) {
	estado := NovoEstadoFallback()
	estado.marcarEsgotado("claude")
	if got := proximoMotorFallback([]string{"claude", "codex"}, "claude", estado); got != "codex" {
		t.Fatalf("proximo motor: %q", got)
	}
	estado.marcarEsgotado("codex")
	if got := proximoMotorFallback([]string{"claude", "codex"}, "claude", estado); got != "" {
		t.Fatalf("nao deveria haver fallback disponivel: %q", got)
	}
}

func TestProximoMotorFallbackComAlias(t *testing.T) {
	estado := NovoEstadoFallback()
	estado.marcarEsgotado("claude")
	ordem := []string{"claude", "claude_alt", "codex"}
	if got := proximoMotorFallback(ordem, "claude", estado); got != "claude_alt" {
		t.Fatalf("fallback deveria ir para claude_alt, veio %q", got)
	}
	estado.marcarEsgotado("claude_alt")
	if got := proximoMotorFallback(ordem, "claude", estado); got != "codex" {
		t.Fatalf("fallback deveria ir para codex, veio %q", got)
	}
}

// TestRodarComFallbackTrocaDeMotor: motor primario esgota, ha fallback ativo, e
// o segundo motor conclui — a operacao termina no motor de fallback.
func TestRodarComFallbackTrocaDeMotor(t *testing.T) {
	primario := stubMotor{nome: "claude", fn: func(motor.OpcoesRun) (*motor.ResultadoRun, error) {
		return &motor.ResultadoRun{LimiteSessao: true, DetalheLimite: "usage limit; reset em 1h"}, nil
	}}
	fallback := stubMotor{nome: "codex", fn: func(motor.OpcoesRun) (*motor.ResultadoRun, error) {
		return &motor.ResultadoRun{Resultado: "ok pelo fallback"}, nil
	}}
	c := &ContextoExec{
		Config:     Config{Fallback: Fallback{Ativo: true, Ordem: []string{"claude", "codex"}}},
		Selecionar: seletorStub(primario, fallback),
	}
	res, usado, err := c.rodarComFallback("executar", "claude", motor.OpcoesRun{}, NovoEstadoFallback())
	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	if usado != "codex" {
		t.Fatalf("motor usado = %q, esperava codex", usado)
	}
	if res == nil || res.Resultado != "ok pelo fallback" {
		t.Fatalf("resultado inesperado: %#v", res)
	}
}

// TestRodarComFallbackSemFallbackNaoBloqueia: franquia esgota e NAO ha fallback
// — em vez de dormir, devolve *ErroFranquia com o horario de retomada.
func TestRodarComFallbackSemFallbackNaoBloqueia(t *testing.T) {
	base := time.Date(2026, 7, 16, 10, 0, 0, 0, time.UTC)
	EsperaResetFranquiaOriginal := EsperaResetFranquia
	EsperaResetFranquia = 30 * time.Minute
	defer func() { EsperaResetFranquia = EsperaResetFranquiaOriginal }()

	m := stubMotor{nome: "claude", fn: func(motor.OpcoesRun) (*motor.ResultadoRun, error) {
		return &motor.ResultadoRun{LimiteSessao: true, DetalheLimite: "session limit; reset 11:00"}, nil
	}}
	c := &ContextoExec{
		Config:     Config{Fallback: Fallback{Ativo: false}},
		Selecionar: seletorStub(m),
		Agora:      func() time.Time { return base },
	}
	_, _, err := c.rodarComFallback("executar", "claude", motor.OpcoesRun{}, NovoEstadoFallback())
	var ef *ErroFranquia
	if err == nil {
		t.Fatal("esperava ErroFranquia, veio nil")
	}
	if !errors.As(err, &ef) {
		t.Fatalf("erro nao e *ErroFranquia: %v", err)
	}
	if !ef.RetomarEm.Equal(base.Add(30 * time.Minute)) {
		t.Fatalf("RetomarEm = %s, esperava %s", ef.RetomarEm, base.Add(30*time.Minute))
	}
	if ef.Motor != "claude" {
		t.Fatalf("motor = %q", ef.Motor)
	}
}
