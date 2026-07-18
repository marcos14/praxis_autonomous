package gitops

import (
	"strings"
	"testing"
)

func TestDiffCompleto(t *testing.T) {
	repo, _ := repoComRemote(t)
	gitT(t, repo, "checkout", "-b", "praxis/d40-diff", "main")
	escrever(t, repo, "novo.txt", "linha nova\n")
	escrever(t, repo, "a.txt", "modificado\n") // a.txt já existe na main
	gitT(t, repo, "add", "-A")
	gitT(t, repo, "commit", "-m", "Fase 1a: muda arquivos")
	gitT(t, repo, "checkout", "main")

	diff, err := DiffCompleto(repo, "main", "praxis/d40-diff")
	if err != nil {
		t.Fatalf("DiffCompleto: %v", err)
	}
	if !strings.Contains(diff, "novo.txt") || !strings.Contains(diff, "a.txt") {
		t.Fatalf("diff não menciona os arquivos alterados:\n%s", diff)
	}
	if !strings.Contains(diff, "+linha nova") {
		t.Fatalf("diff não contém a adição esperada:\n%s", diff)
	}

	// base/branch vazia devolve erro.
	if _, err := DiffCompleto(repo, "", "praxis/d40-diff"); err == nil {
		t.Fatal("DiffCompleto com base vazia devia falhar")
	}
}

func TestDiffDaFase(t *testing.T) {
	repo, _ := repoComRemote(t)
	gitT(t, repo, "checkout", "-b", "praxis/d41-fase", "main")
	// Fase 1a commita b.txt.
	escrever(t, repo, "b.txt", "conteudo b\n")
	gitT(t, repo, "add", "-A")
	gitT(t, repo, "commit", "-m", "Fase 1a: cria b.txt [praxis]")
	// Fase 1b commita c.txt.
	escrever(t, repo, "c.txt", "conteudo c\n")
	gitT(t, repo, "add", "-A")
	gitT(t, repo, "commit", "-m", "Fase 1b: cria c.txt [praxis]")
	gitT(t, repo, "checkout", "main")

	// diff da fase 1a: só b.txt, não c.txt.
	diff, err := DiffDaFase(repo, "main", "praxis/d41-fase", "1a")
	if err != nil {
		t.Fatalf("DiffDaFase 1a: %v", err)
	}
	if !strings.Contains(diff, "b.txt") {
		t.Fatalf("diff da fase 1a não menciona b.txt:\n%s", diff)
	}
	if strings.Contains(diff, "c.txt") {
		t.Fatalf("diff da fase 1a não devia mencionar c.txt:\n%s", diff)
	}

	// fase sem commit → diff vazio, sem erro.
	vazio, err := DiffDaFase(repo, "main", "praxis/d41-fase", "9z")
	if err != nil {
		t.Fatalf("DiffDaFase de fase inexistente: %v", err)
	}
	if strings.TrimSpace(vazio) != "" {
		t.Fatalf("diff de fase sem commit devia ser vazio, veio:\n%s", vazio)
	}

	// código de fase vazio devolve erro.
	if _, err := DiffDaFase(repo, "main", "praxis/d41-fase", ""); err == nil {
		t.Fatal("DiffDaFase com código vazio devia falhar")
	}
}
