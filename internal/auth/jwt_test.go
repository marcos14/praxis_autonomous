package auth

import (
	"strings"
	"testing"
	"time"
)

var segredo = []byte("segredo-de-teste-nao-use-em-prod")

func TestAssinarValidarIdaEVolta(t *testing.T) {
	tok, err := Assinar(42, time.Hour, segredo)
	if err != nil {
		t.Fatalf("assinar: %v", err)
	}
	sub, err := Validar(tok, segredo)
	if err != nil {
		t.Fatalf("validar: %v", err)
	}
	if sub != 42 {
		t.Fatalf("sub = %d, quero 42", sub)
	}
}

func TestAssinarClaimsExpBateComOToken(t *testing.T) {
	antes := time.Now()
	tok, emitidos, err := AssinarClaims(9, 90*time.Second, segredo)
	if err != nil {
		t.Fatalf("assinar: %v", err)
	}
	lidos, err := ValidarClaims(tok, segredo)
	if err != nil {
		t.Fatalf("validar: %v", err)
	}
	if lidos.Sub != 9 || emitidos.Sub != 9 {
		t.Fatalf("sub = %d/%d, quero 9", emitidos.Sub, lidos.Sub)
	}
	// O exp devolvido na emissão é exatamente o gravado no token (segundos).
	if !lidos.Exp.Equal(emitidos.Exp) {
		t.Fatalf("exp emitido %v ≠ exp lido %v", emitidos.Exp, lidos.Exp)
	}
	if lidos.Exp.Before(antes.Add(89*time.Second)) || lidos.Exp.After(antes.Add(92*time.Second)) {
		t.Fatalf("exp = %v, quero ~90s após %v", lidos.Exp, antes)
	}
}

func TestValidarClaimsTokenInvalidoDevolveZero(t *testing.T) {
	c, err := ValidarClaims("a.b.c", segredo)
	if err != ErrTokenInvalido {
		t.Fatalf("erro = %v, quero ErrTokenInvalido", err)
	}
	if c.Sub != 0 || !c.Exp.IsZero() {
		t.Fatalf("claims de token inválido = %+v, quero zerados", c)
	}
}

func TestValidarTokenExpirado(t *testing.T) {
	// Emitido no passado, já expirado.
	passado := time.Now().Add(-2 * time.Hour)
	tok, err := assinarEm(7, passado, passado.Add(time.Hour), segredo)
	if err != nil {
		t.Fatalf("assinar: %v", err)
	}
	if _, err := Validar(tok, segredo); err != ErrTokenInvalido {
		t.Fatalf("erro = %v, quero ErrTokenInvalido", err)
	}
}

func TestValidarNaExpiracaoExata(t *testing.T) {
	base := time.Unix(1_000_000, 0)
	tok, err := assinarEm(1, base.Add(-time.Hour), base, segredo)
	if err != nil {
		t.Fatalf("assinar: %v", err)
	}
	// Um segundo antes do exp: válido.
	if _, err := validarEm(tok, segredo, base.Add(-time.Second)); err != nil {
		t.Fatalf("antes do exp deveria valer: %v", err)
	}
	// No exp: inválido (>=).
	if _, err := validarEm(tok, segredo, base); err != ErrTokenInvalido {
		t.Fatalf("no exp deveria expirar, erro = %v", err)
	}
}

func TestValidarAssinaturaAdulterada(t *testing.T) {
	tok, err := Assinar(5, time.Hour, segredo)
	if err != nil {
		t.Fatalf("assinar: %v", err)
	}
	// Segredo diferente → assinatura não confere.
	if _, err := Validar(tok, []byte("outro-segredo-totalmente-diferente")); err != ErrTokenInvalido {
		t.Fatalf("segredo errado deveria falhar, erro = %v", err)
	}
	// Payload trocado mantendo a assinatura antiga.
	partes := strings.Split(tok, ".")
	forjado := partes[0] + "." + codificar([]byte(`{"sub":999,"iat":0,"exp":9999999999}`)) + "." + partes[2]
	if _, err := Validar(forjado, segredo); err != ErrTokenInvalido {
		t.Fatalf("payload adulterado deveria falhar, erro = %v", err)
	}
}

func TestValidarMalformado(t *testing.T) {
	casos := []string{"", "a.b", "a.b.c.d", "não-é-jwt", "..", "a.b."}
	for _, c := range casos {
		if _, err := Validar(c, segredo); err != ErrTokenInvalido {
			t.Fatalf("token %q: erro = %v, quero ErrTokenInvalido", c, err)
		}
	}
}

func TestValidarAlgNone(t *testing.T) {
	// Cabeçalho com alg:"none" e sem assinatura não deve ser aceito.
	forjado := codificar([]byte(`{"alg":"none","typ":"JWT"}`)) + "." +
		codificar([]byte(`{"sub":1,"iat":0,"exp":9999999999}`)) + "."
	if _, err := Validar(forjado, segredo); err != ErrTokenInvalido {
		t.Fatalf("alg none deveria falhar, erro = %v", err)
	}
}
