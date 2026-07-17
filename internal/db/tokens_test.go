package db

import (
	"context"
	"errors"
	"testing"
)

func TestCriarAutenticarToken(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()

	tok, err := d.CriarToken(ctx, "sistema de chamados", PapelOperador)
	if err != nil {
		t.Fatalf("CriarToken: %v", err)
	}
	if tok.ID == 0 || tok.Token == "" {
		t.Fatalf("token sem id/valor: %+v", tok)
	}
	if tok.Papel != PapelOperador {
		t.Fatalf("papel = %q, quero operador", tok.Papel)
	}

	// autentica com o valor em claro → devolve o papel.
	got, err := d.AutenticarToken(ctx, tok.Token)
	if err != nil {
		t.Fatalf("AutenticarToken: %v", err)
	}
	if got.ID != tok.ID || got.Papel != PapelOperador {
		t.Fatalf("autenticado = %+v, quero id %d operador", got, tok.ID)
	}

	// valor errado → não encontrado.
	if _, err := d.AutenticarToken(ctx, "token-errado"); !errors.Is(err, ErrNaoEncontrado) {
		t.Fatalf("token errado: err = %v, quero ErrNaoEncontrado", err)
	}
}

func TestListarTokensNaoVazaSegredo(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()
	if _, err := d.CriarToken(ctx, "a", PapelLeitor); err != nil {
		t.Fatalf("criar: %v", err)
	}
	tokens, err := d.ListarTokens(ctx)
	if err != nil {
		t.Fatalf("listar: %v", err)
	}
	if len(tokens) != 1 {
		t.Fatalf("len = %d, quero 1", len(tokens))
	}
	if tokens[0].Token != "" {
		t.Fatalf("listagem não deve trazer o valor em claro: %q", tokens[0].Token)
	}
}

func TestRevogarToken(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()
	tok, err := d.CriarToken(ctx, "revogar", PapelAdmin)
	if err != nil {
		t.Fatalf("criar: %v", err)
	}
	if err := d.RevogarToken(ctx, tok.ID); err != nil {
		t.Fatalf("revogar: %v", err)
	}
	// após revogar, não autentica mais.
	if _, err := d.AutenticarToken(ctx, tok.Token); !errors.Is(err, ErrNaoEncontrado) {
		t.Fatalf("token revogado ainda autentica: err=%v", err)
	}
	// revogar de novo é no-op (sem erro).
	if err := d.RevogarToken(ctx, tok.ID); err != nil {
		t.Fatalf("revogar 2x: %v", err)
	}
	// inexistente → ErrNaoEncontrado.
	if err := d.RevogarToken(ctx, 9999); !errors.Is(err, ErrNaoEncontrado) {
		t.Fatalf("revogar inexistente: err=%v, quero ErrNaoEncontrado", err)
	}
}

func TestCriarTokenPapelInvalido(t *testing.T) {
	d := abrirTemp(t)
	if _, err := d.CriarToken(context.Background(), "x", "root"); err == nil {
		t.Fatal("papel inválido deveria falhar")
	}
}

func TestContarTokensAtivos(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()
	if n, _ := d.ContarTokensAtivos(ctx); n != 0 {
		t.Fatalf("inicial = %d, quero 0", n)
	}
	tok, _ := d.CriarToken(ctx, "a", PapelLeitor)
	if n, _ := d.ContarTokensAtivos(ctx); n != 1 {
		t.Fatalf("após criar = %d, quero 1", n)
	}
	_ = d.RevogarToken(ctx, tok.ID)
	if n, _ := d.ContarTokensAtivos(ctx); n != 0 {
		t.Fatalf("após revogar = %d, quero 0", n)
	}
}
