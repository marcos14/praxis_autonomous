package chavessh

import (
	"context"
	"os/exec"
	"strings"
	"testing"
)

// temSSHKeygen pula os testes que exigem o OpenSSH quando ele não está na
// máquina de CI (o Praxis exige git; o ssh-keygen costuma vir junto).
func temSSHKeygen(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("ssh-keygen não encontrado no PATH")
	}
}

func TestGerarLerETestarChave(t *testing.T) {
	temSSHKeygen(t)
	g := Novo(t.TempDir())
	ctx := context.Background()

	if g.Existe(7) {
		t.Fatal("usuário novo não deveria ter chave")
	}
	if _, err := g.Chave(7); err != ErrNaoExiste {
		t.Fatalf("Chave sem gerar: err = %v, quero ErrNaoExiste", err)
	}

	c, err := g.Gerar(ctx, 7, "praxis-ana@x.com")
	if err != nil {
		t.Fatalf("Gerar: %v", err)
	}
	if !strings.HasPrefix(c.Publica, "ssh-ed25519 ") {
		t.Fatalf("pública inesperada: %q", c.Publica)
	}
	if !strings.Contains(c.Publica, "praxis-ana@x.com") {
		t.Fatalf("comentário ausente da pública: %q", c.Publica)
	}
	if c.Fingerprint == "" {
		t.Error("fingerprint vazio")
	}

	// Segunda geração é recusada — regenerar invalidaria o cadastro na plataforma.
	if _, err := g.Gerar(ctx, 7, ""); err != ErrJaExiste {
		t.Fatalf("segunda geração: err = %v, quero ErrJaExiste", err)
	}

	// A leitura devolve a mesma pública.
	lida, err := g.Chave(7)
	if err != nil || lida.Publica != c.Publica {
		t.Fatalf("Chave releu %q err=%v", lida.Publica, err)
	}
}

func TestComandoGitEAmbiente(t *testing.T) {
	temSSHKeygen(t)
	g := Novo(t.TempDir())
	if _, err := g.Gerar(context.Background(), 3, ""); err != nil {
		t.Fatal(err)
	}

	cmd := g.ComandoGit(3)
	for _, quer := range []string{"ssh -i ", "IdentitiesOnly=yes", "StrictHostKeyChecking=accept-new", "UserKnownHostsFile="} {
		if !strings.Contains(cmd, quer) {
			t.Errorf("ComandoGit sem %q: %s", quer, cmd)
		}
	}
	// Caminhos com barras normais (o git interpreta o comando com sh).
	if strings.Contains(cmd, `\`) {
		t.Errorf("ComandoGit com backslash (quebra no sh do git): %s", cmd)
	}

	env := g.AmbienteGit(3)
	if len(env) != 1 || !strings.HasPrefix(env[0], "GIT_SSH_COMMAND=") {
		t.Fatalf("AmbienteGit = %v", env)
	}
	// Usuário sem chave (ou inválido) → nil: as credenciais do SO seguem valendo.
	if env := g.AmbienteGit(99); env != nil {
		t.Fatalf("AmbienteGit sem chave = %v, quero nil", env)
	}
	if env := g.AmbienteGit(0); env != nil {
		t.Fatalf("AmbienteGit uid 0 = %v, quero nil", env)
	}
}

func TestTestarValidacoes(t *testing.T) {
	g := Novo(t.TempDir())
	ctx := context.Background()

	if err := g.Testar(ctx, 1, ""); err == nil {
		t.Fatal("URL vazia deveria falhar")
	}
	if err := g.Testar(ctx, 1, "git@github.com:org/repo.git"); err != ErrNaoExiste {
		t.Fatalf("teste sem chave: err = %v, quero ErrNaoExiste", err)
	}
}
