package db

import (
	"context"
	"errors"
	"testing"
)

func TestObterPromptInexistente(t *testing.T) {
	d := abrirTemp(t)
	if _, err := d.ObterPrompt(context.Background(), "analista"); !errors.Is(err, ErrNaoEncontrado) {
		t.Fatalf("ObterPrompt ausente: err = %v, quero ErrNaoEncontrado", err)
	}
}

func TestSalvarEObterPrompt(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()

	p, err := d.SalvarPrompt(ctx, "Analista", "conteúdo v1 {PRD}")
	if err != nil {
		t.Fatalf("SalvarPrompt: %v", err)
	}
	if p.Nome != "analista" { // normalizado (minúsculas)
		t.Fatalf("nome = %q, quero normalizado 'analista'", p.Nome)
	}
	if p.AtualizadoEm == "" {
		t.Fatal("atualizado_em não preenchido")
	}

	got, err := d.ObterPrompt(ctx, "analista")
	if err != nil {
		t.Fatalf("ObterPrompt: %v", err)
	}
	if got.Conteudo != "conteúdo v1 {PRD}" {
		t.Fatalf("conteudo = %q", got.Conteudo)
	}

	// upsert: salvar de novo atualiza o conteúdo (uma linha só).
	if _, err := d.SalvarPrompt(ctx, "analista", "conteúdo v2"); err != nil {
		t.Fatalf("SalvarPrompt v2: %v", err)
	}
	got, _ = d.ObterPrompt(ctx, "analista")
	if got.Conteudo != "conteúdo v2" {
		t.Fatalf("conteudo após upsert = %q, quero v2", got.Conteudo)
	}
	prompts, err := d.ListarPrompts(ctx)
	if err != nil {
		t.Fatalf("ListarPrompts: %v", err)
	}
	if len(prompts) != 1 {
		t.Fatalf("len prompts = %d, quero 1 (upsert, não insert duplicado)", len(prompts))
	}
}

func TestSalvarPromptValida(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()
	if _, err := d.SalvarPrompt(ctx, "", "x"); err == nil {
		t.Fatal("nome vazio deveria falhar")
	}
	if _, err := d.SalvarPrompt(ctx, "analista", "   "); err == nil {
		t.Fatal("conteúdo em branco deveria falhar")
	}
}

func TestRemoverPrompt(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()
	if _, err := d.SalvarPrompt(ctx, "analista", "x"); err != nil {
		t.Fatalf("SalvarPrompt: %v", err)
	}
	if err := d.RemoverPrompt(ctx, "analista"); err != nil {
		t.Fatalf("RemoverPrompt: %v", err)
	}
	if _, err := d.ObterPrompt(ctx, "analista"); !errors.Is(err, ErrNaoEncontrado) {
		t.Fatalf("após remover: err = %v, quero ErrNaoEncontrado", err)
	}
	if err := d.RemoverPrompt(ctx, "analista"); !errors.Is(err, ErrNaoEncontrado) {
		t.Fatalf("remover inexistente: err = %v, quero ErrNaoEncontrado", err)
	}
}
