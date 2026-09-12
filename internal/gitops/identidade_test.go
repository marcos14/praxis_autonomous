package gitops

import (
	"testing"
)

func TestAutorNomeEEmail(t *testing.T) {
	casos := []struct {
		nome      string
		id        Identidade
		wantNome  string
		wantEmail string
	}{
		{
			nome:      "usuario com sufixo (default)",
			id:        Identidade{Nome: "Joao Teste", Email: "joao@exemplo.com.br", Sufixo: true},
			wantNome:  "Joao Teste - Praxis",
			wantEmail: "joao@exemplo.com.br",
		},
		{
			nome:      "usuario sem sufixo (admin desligou)",
			id:        Identidade{Nome: "Joao Teste", Email: "joao@exemplo.com.br"},
			wantNome:  "Joao Teste",
			wantEmail: "joao@exemplo.com.br",
		},
		{
			nome:      "valor zero cai no Praxis",
			id:        Identidade{},
			wantNome:  PraxisNome,
			wantEmail: PraxisEmail,
		},
		{
			nome:      "identidade do Praxis nao ganha sufixo duplicado",
			id:        Identidade{Nome: PraxisNome, Email: PraxisEmail, Sufixo: true},
			wantNome:  PraxisNome,
			wantEmail: PraxisEmail,
		},
		{
			// < > e quebras de linha permitiriam falsificar o ident/trailers.
			nome:      "nome malicioso e saneado",
			id:        Identidade{Nome: "Ana <root>\nCo-Authored-By: X", Email: "ana@x.com\n", Sufixo: true},
			wantNome:  "Ana rootCo-Authored-By: X - Praxis",
			wantEmail: "ana@x.com",
		},
		{
			nome:      "nome so de caracteres invalidos cai no Praxis",
			id:        Identidade{Nome: "<>\n", Email: "  ", Sufixo: true},
			wantNome:  PraxisNome,
			wantEmail: PraxisEmail,
		},
	}
	for _, c := range casos {
		if got := c.id.AutorNome(); got != c.wantNome {
			t.Errorf("%s: AutorNome = %q, esperava %q", c.nome, got, c.wantNome)
		}
		if got := c.id.AutorEmail(); got != c.wantEmail {
			t.Errorf("%s: AutorEmail = %q, esperava %q", c.nome, got, c.wantEmail)
		}
	}
}

// TestCommitIdentidade garante que o commit do orquestrador usa a identidade
// injetada por env — autor = usuario da plataforma, committer = Praxis — e
// IGNORA a config user.name/user.email do repo (que num servidor multiusuario
// seria a da maquina, nao a de quem criou a demanda).
func TestCommitIdentidade(t *testing.T) {
	repo, _ := repoComRemote(t) // repoComRemote configura user.name "Praxis Teste"
	o := Novo()

	escrever(t, repo, "b.txt", "novo\n")
	autor := Identidade{Nome: "Joao Teste", Email: "joao@exemplo.com.br", Sufixo: true}
	if err := o.Commit(repo, "commit com autor", autor); err != nil {
		t.Fatal(err)
	}
	ident := gitT(t, repo, "log", "-1", "--pretty=%an|%ae|%cn|%ce")
	want := "Joao Teste - Praxis|joao@exemplo.com.br|" + PraxisNome + "|" + PraxisEmail
	if ident != want {
		t.Fatalf("ident do commit = %q, esperava %q", ident, want)
	}

	// sem autor (valor zero): autor e committer sao o Praxis, nunca a config do repo.
	escrever(t, repo, "c.txt", "novo\n")
	if err := o.Commit(repo, "commit sem autor", Identidade{}); err != nil {
		t.Fatal(err)
	}
	ident = gitT(t, repo, "log", "-1", "--pretty=%an|%ae|%cn|%ce")
	want = PraxisNome + "|" + PraxisEmail + "|" + PraxisNome + "|" + PraxisEmail
	if ident != want {
		t.Fatalf("ident do commit sem autor = %q, esperava %q", ident, want)
	}
}

// TestMergeNoFFIdentidade cobre o mesmo contrato no commit de merge da
// integracao local (acao integrar): autor = usuario que disparou, committer =
// Praxis.
func TestMergeNoFFIdentidade(t *testing.T) {
	repo, _ := repoComRemote(t)
	o := Novo()

	gitT(t, repo, "checkout", "-b", "praxis/d7-ident")
	escrever(t, repo, "d.txt", "conteudo\n")
	if err := o.Commit(repo, "fase da d7", Identidade{}); err != nil {
		t.Fatal(err)
	}
	autor := Identidade{Nome: "Ana Souza", Email: "ana@exemplo.com.br", Sufixo: true}
	if err := o.MergeNoFF(repo, "main", "praxis/d7-ident", "merge da d7", autor); err != nil {
		t.Fatal(err)
	}
	ident := gitT(t, repo, "log", "-1", "--pretty=%an|%ae|%cn|%ce")
	want := "Ana Souza - Praxis|ana@exemplo.com.br|" + PraxisNome + "|" + PraxisEmail
	if ident != want {
		t.Fatalf("ident do merge = %q, esperava %q", ident, want)
	}
}
