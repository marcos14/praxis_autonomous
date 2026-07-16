package db

import (
	"context"
	"errors"
	"testing"
)

func projetoExemplo() Projeto {
	return Projeto{
		Nome:            "Praxis",
		Slug:            "praxis",
		Pasta:           `C:\repo`,
		BranchPrincipal: "main",
		ModoIntegracao:  "merge_request",
		URLPlataforma:   "https://gitlab.com/acme/praxis",
		AddDirs:         []string{"../lib", "../shared"},
		Ativo:           true,
	}
}

func TestCriarProjetoPreencheIDeCriadoEm(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()

	p, err := d.CriarProjeto(ctx, projetoExemplo())
	if err != nil {
		t.Fatalf("CriarProjeto: %v", err)
	}
	if p.ID == 0 {
		t.Fatal("ID não preenchido")
	}
	if p.CriadoEm == "" {
		t.Fatal("CriadoEm não preenchido")
	}
	if len(p.AddDirs) != 2 {
		t.Fatalf("AddDirs = %v, quero 2 itens", p.AddDirs)
	}
}

func TestCriarProjetoSlugDuplicado(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()

	if _, err := d.CriarProjeto(ctx, projetoExemplo()); err != nil {
		t.Fatalf("primeira criação: %v", err)
	}
	outro := projetoExemplo()
	outro.Nome = "Outro"
	_, err := d.CriarProjeto(ctx, outro)
	if !errors.Is(err, ErrSlugDuplicado) {
		t.Fatalf("erro = %v, quero ErrSlugDuplicado", err)
	}
}

func TestCriarProjetoNormalizaAddDirs(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()

	p := projetoExemplo()
	p.AddDirs = []string{" ../lib ", "", "  ", "../x"}
	criado, err := d.CriarProjeto(ctx, p)
	if err != nil {
		t.Fatalf("CriarProjeto: %v", err)
	}
	if len(criado.AddDirs) != 2 || criado.AddDirs[0] != "../lib" || criado.AddDirs[1] != "../x" {
		t.Fatalf("AddDirs = %v, quero [../lib ../x]", criado.AddDirs)
	}
}

func TestCriarProjetoAddDirsVazioSerializaLista(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()

	p := projetoExemplo()
	p.AddDirs = nil
	criado, err := d.CriarProjeto(ctx, p)
	if err != nil {
		t.Fatalf("CriarProjeto: %v", err)
	}
	if criado.AddDirs == nil || len(criado.AddDirs) != 0 {
		t.Fatalf("AddDirs = %v, quero lista vazia não-nil", criado.AddDirs)
	}
	// Confere no banco que gravou "[]" (JSON válido) e relê corretamente.
	relido, err := d.ObterProjeto(ctx, criado.ID)
	if err != nil {
		t.Fatalf("ObterProjeto: %v", err)
	}
	if relido.AddDirs == nil {
		t.Fatal("AddDirs relido é nil, quero lista vazia")
	}
}

func TestListarProjetosOrdenadoPorNome(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()

	nomes := []struct{ nome, slug string }{
		{"Zeta", "zeta"}, {"alfa", "alfa"}, {"Beta", "beta"},
	}
	for _, n := range nomes {
		p := projetoExemplo()
		p.Nome, p.Slug = n.nome, n.slug
		if _, err := d.CriarProjeto(ctx, p); err != nil {
			t.Fatalf("CriarProjeto %s: %v", n.nome, err)
		}
	}
	lista, err := d.ListarProjetos(ctx)
	if err != nil {
		t.Fatalf("ListarProjetos: %v", err)
	}
	quero := []string{"alfa", "Beta", "Zeta"}
	if len(lista) != len(quero) {
		t.Fatalf("len = %d, quero %d", len(lista), len(quero))
	}
	for i, n := range quero {
		if lista[i].Nome != n {
			t.Fatalf("posição %d = %q, quero %q", i, lista[i].Nome, n)
		}
	}
}

func TestListarProjetosVazioNaoNil(t *testing.T) {
	d := abrirTemp(t)
	lista, err := d.ListarProjetos(context.Background())
	if err != nil {
		t.Fatalf("ListarProjetos: %v", err)
	}
	if lista == nil {
		t.Fatal("lista nil, quero slice vazio")
	}
}

func TestObterProjetoInexistente(t *testing.T) {
	d := abrirTemp(t)
	_, err := d.ObterProjeto(context.Background(), 999)
	if !errors.Is(err, ErrNaoEncontrado) {
		t.Fatalf("erro = %v, quero ErrNaoEncontrado", err)
	}
}

func TestAtualizarProjeto(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()

	criado, err := d.CriarProjeto(ctx, projetoExemplo())
	if err != nil {
		t.Fatalf("CriarProjeto: %v", err)
	}

	criado.Nome = "Praxis Renomeado"
	criado.ModoIntegracao = "merge_local"
	criado.Ativo = false
	criado.AddDirs = []string{"../novo"}
	atualizado, err := d.AtualizarProjeto(ctx, criado)
	if err != nil {
		t.Fatalf("AtualizarProjeto: %v", err)
	}
	if atualizado.Nome != "Praxis Renomeado" || atualizado.ModoIntegracao != "merge_local" {
		t.Fatalf("atualização não refletiu: %+v", atualizado)
	}
	if atualizado.Ativo {
		t.Fatal("Ativo deveria ser false")
	}
	if len(atualizado.AddDirs) != 1 || atualizado.AddDirs[0] != "../novo" {
		t.Fatalf("AddDirs = %v, quero [../novo]", atualizado.AddDirs)
	}
	if atualizado.CriadoEm != criado.CriadoEm {
		t.Fatalf("CriadoEm mudou: %q -> %q", criado.CriadoEm, atualizado.CriadoEm)
	}
}

func TestAtualizarProjetoInexistente(t *testing.T) {
	d := abrirTemp(t)
	p := projetoExemplo()
	p.ID = 999
	_, err := d.AtualizarProjeto(context.Background(), p)
	if !errors.Is(err, ErrNaoEncontrado) {
		t.Fatalf("erro = %v, quero ErrNaoEncontrado", err)
	}
}

func TestAtualizarProjetoSlugDuplicado(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()

	a := projetoExemplo()
	a.Nome, a.Slug = "A", "a"
	criadoA, err := d.CriarProjeto(ctx, a)
	if err != nil {
		t.Fatalf("criar A: %v", err)
	}
	b := projetoExemplo()
	b.Nome, b.Slug = "B", "b"
	if _, err := d.CriarProjeto(ctx, b); err != nil {
		t.Fatalf("criar B: %v", err)
	}

	// Tenta renomear o slug de A para o de B: deve colidir.
	criadoA.Slug = "b"
	_, err = d.AtualizarProjeto(ctx, criadoA)
	if !errors.Is(err, ErrSlugDuplicado) {
		t.Fatalf("erro = %v, quero ErrSlugDuplicado", err)
	}
}

func TestModoIntegracaoInvalidoRejeitadoPeloBanco(t *testing.T) {
	d := abrirTemp(t)
	p := projetoExemplo()
	p.ModoIntegracao = "invalido"
	_, err := d.CriarProjeto(context.Background(), p)
	if err == nil {
		t.Fatal("esperava erro do CHECK de modo_integracao, mas passou")
	}
}
