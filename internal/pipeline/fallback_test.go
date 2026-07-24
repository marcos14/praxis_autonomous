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

// TestProximoMotorFallbackMotorForaDaCadeia: um motor de uso manual (fora da
// ordem) cai no PRIMEIRO motor livre da cadeia — o uso manual tem fallback, so
// nao e alvo dele.
func TestProximoMotorFallbackMotorForaDaCadeia(t *testing.T) {
	estado := NovoEstadoFallback()
	ordem := []string{"claude", "codex"}
	if got := proximoMotorFallback(ordem, "exclusivo", estado); got != "claude" {
		t.Fatalf("fallback do motor manual deveria ir para claude, veio %q", got)
	}
	estado.marcarEsgotado("claude")
	if got := proximoMotorFallback(ordem, "exclusivo", estado); got != "codex" {
		t.Fatalf("fallback do motor manual deveria pular para codex, veio %q", got)
	}
}

// TestRodarComFallbackEsgotaPerfisAntesDoMotor: com dois perfis no motor
// primario, o limite do primeiro perfil troca para o SEGUNDO PERFIL do mesmo
// motor — o proximo motor da cadeia nem e chamado.
func TestRodarComFallbackEsgotaPerfisAntesDoMotor(t *testing.T) {
	var dirs []string
	claude := stubMotor{nome: "claude", fn: func(op motor.OpcoesRun) (*motor.ResultadoRun, error) {
		dirs = append(dirs, op.PerfilDir)
		if op.PerfilDir == "dir-1" {
			return &motor.ResultadoRun{LimiteSessao: true, DetalheLimite: "usage limit"}, nil
		}
		return &motor.ResultadoRun{Resultado: "ok pelo segundo perfil"}, nil
	}}
	codex := stubMotor{nome: "codex", fn: func(motor.OpcoesRun) (*motor.ResultadoRun, error) {
		t.Fatal("codex nao deveria ser chamado enquanto o claude tem perfil livre")
		return nil, nil
	}}
	c := &ContextoExec{
		Config: Config{
			Fallback: Fallback{Ativo: true, Ordem: []string{"claude", "codex"}},
			Perfis: map[string][]PerfilMotor{
				"claude": {{Conta: "p1", Dir: "dir-1"}, {Conta: "p2", Dir: "dir-2"}},
			},
		},
		Selecionar: seletorStub(claude, codex),
	}
	res, usado, conta, err := c.rodarComFallback("executar", "claude", motor.OpcoesRun{}, NovoEstadoFallback())
	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	if usado != "claude" || conta != "p2" {
		t.Fatalf("usado = %s:%s, esperava claude:p2", usado, conta)
	}
	if res == nil || res.Resultado != "ok pelo segundo perfil" {
		t.Fatalf("resultado inesperado: %#v", res)
	}
	if len(dirs) != 2 || dirs[0] != "dir-1" || dirs[1] != "dir-2" {
		t.Fatalf("perfis aplicados = %v, esperava [dir-1 dir-2]", dirs)
	}
}

// TestRodarComFallbackTrocaDeMotorAposTodosOsPerfis: só depois de TODOS os
// perfis do motor primario esgotarem a troca de motor acontece — e o run
// devolve o perfil do motor de fallback.
func TestRodarComFallbackTrocaDeMotorAposTodosOsPerfis(t *testing.T) {
	chamadasClaude := 0
	claude := stubMotor{nome: "claude", fn: func(motor.OpcoesRun) (*motor.ResultadoRun, error) {
		chamadasClaude++
		return &motor.ResultadoRun{LimiteSessao: true, DetalheLimite: "usage limit"}, nil
	}}
	codex := stubMotor{nome: "codex", fn: func(op motor.OpcoesRun) (*motor.ResultadoRun, error) {
		if op.PerfilDir != "codex-dir" {
			t.Fatalf("codex sem o perfil isolado dele: %q", op.PerfilDir)
		}
		return &motor.ResultadoRun{Resultado: "ok pelo codex"}, nil
	}}
	c := &ContextoExec{
		Config: Config{
			Fallback: Fallback{Ativo: true, Ordem: []string{"claude", "codex"}},
			Perfis: map[string][]PerfilMotor{
				"claude": {{Conta: "p1", Dir: "dir-1"}, {Conta: "p2", Dir: "dir-2"}},
				"codex":  {{Conta: "x1", Dir: "codex-dir"}},
			},
		},
		Selecionar: seletorStub(claude, codex),
	}
	res, usado, conta, err := c.rodarComFallback("executar", "claude", motor.OpcoesRun{}, NovoEstadoFallback())
	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	if chamadasClaude != 2 {
		t.Fatalf("claude chamado %d vezes, esperava 2 (um por perfil)", chamadasClaude)
	}
	if usado != "codex" || conta != "x1" {
		t.Fatalf("usado = %s:%s, esperava codex:x1", usado, conta)
	}
	if res == nil || res.Resultado != "ok pelo codex" {
		t.Fatalf("resultado inesperado: %#v", res)
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
	res, usado, _, err := c.rodarComFallback("executar", "claude", motor.OpcoesRun{}, NovoEstadoFallback())
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

// TestRodarComFallbackContaDeslogadaGiraPerfil: uma conta deslogada ("Not
// logged in") nao derruba a fase de imediato — o fallback pula para o proximo
// perfil do mesmo motor, como faz com a franquia esgotada.
func TestRodarComFallbackContaDeslogadaGiraPerfil(t *testing.T) {
	claude := stubMotor{nome: "claude", fn: func(op motor.OpcoesRun) (*motor.ResultadoRun, error) {
		if op.PerfilDir == "dir-1" {
			return &motor.ResultadoRun{IsError: true, Subtipo: "success",
				Resultado: "Not logged in · Please run /login", FalhaAutenticacao: true}, nil
		}
		return &motor.ResultadoRun{Resultado: "ok pelo segundo perfil"}, nil
	}}
	c := &ContextoExec{
		Config: Config{
			Perfis: map[string][]PerfilMotor{
				"claude": {{Conta: "p1", Dir: "dir-1"}, {Conta: "p2", Dir: "dir-2"}},
			},
		},
		Selecionar: seletorStub(claude),
	}
	res, usado, conta, err := c.rodarComFallback("executar", "claude", motor.OpcoesRun{}, NovoEstadoFallback())
	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	if usado != "claude" || conta != "p2" {
		t.Fatalf("usado = %s:%s, esperava claude:p2", usado, conta)
	}
	if res == nil || res.Resultado != "ok pelo segundo perfil" {
		t.Fatalf("resultado inesperado: %#v", res)
	}
}

// TestRodarComFallbackContaDeslogadaSemFallbackDevolveResultado: esgotada a
// cadeia com uma falha de autenticacao, o resultado volta como esta (SEM
// *ErroFranquia): esperar nao resolve conta deslogada — a fase falha com a
// mensagem real e um humano reloga o perfil.
func TestRodarComFallbackContaDeslogadaSemFallbackDevolveResultado(t *testing.T) {
	m := stubMotor{nome: "claude", fn: func(motor.OpcoesRun) (*motor.ResultadoRun, error) {
		return &motor.ResultadoRun{IsError: true, Subtipo: "success",
			Resultado: "Not logged in · Please run /login", FalhaAutenticacao: true}, nil
	}}
	c := &ContextoExec{
		Config:     Config{Fallback: Fallback{Ativo: false}},
		Selecionar: seletorStub(m),
	}
	res, _, _, err := c.rodarComFallback("executar", "claude", motor.OpcoesRun{}, NovoEstadoFallback())
	if err != nil {
		t.Fatalf("esperava err nil (resultado devolvido como esta), veio: %v", err)
	}
	if res == nil || !res.IsError || !res.FalhaAutenticacao {
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
	_, _, _, err := c.rodarComFallback("executar", "claude", motor.OpcoesRun{}, NovoEstadoFallback())
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
