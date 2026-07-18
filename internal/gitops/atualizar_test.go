package gitops

import (
	"path/filepath"
	"strings"
	"testing"
)

// clonar cria um clone de origin em <base>/<nome> com identidade configurada.
func clonar(t *testing.T, origin, nome string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), nome)
	gitT(t, ".", "clone", origin, dir)
	gitT(t, dir, "config", "user.email", "outro@praxis.local")
	gitT(t, dir, "config", "user.name", "Outro Dev")
	return dir
}

// publicarNovoCommit cria um commit novo no origin (via um segundo clone), para
// o repo principal ficar defasado.
func publicarNovoCommit(t *testing.T, origin string) {
	t.Helper()
	outro := clonar(t, origin, "outro")
	escrever(t, outro, "novo.txt", "conteudo novo\n")
	gitT(t, outro, "add", "-A")
	gitT(t, outro, "commit", "-m", "commit remoto")
	gitT(t, outro, "push", "origin", "main")
}

func TestPosicionarBranchPrincipalAtualizaEPosiciona(t *testing.T) {
	repo, origin := repoComRemote(t)
	o := Novo()

	// Deixa o repo defasado E em outra branch (situação típica de servidor onde
	// alguém mexeu): o Praxis precisa voltar à main e puxar o commit novo.
	gitT(t, repo, "checkout", "-b", "outra-branch")
	publicarNovoCommit(t, origin)

	aviso, err := o.PosicionarBranchPrincipal(repo, "main")
	if err != nil {
		t.Fatalf("PosicionarBranchPrincipal: %v", err)
	}
	if aviso != "" {
		t.Fatalf("aviso inesperado: %q", aviso)
	}
	if got := gitT(t, repo, "rev-parse", "--abbrev-ref", "HEAD"); got != "main" {
		t.Fatalf("branch atual = %q, quero main", got)
	}
	// O commit remoto chegou (fast-forward aplicado).
	if log := gitT(t, repo, "log", "--oneline"); !strings.Contains(log, "commit remoto") {
		t.Fatalf("pull não aplicado; log:\n%s", log)
	}
}

func TestPosicionarBranchPrincipalNaoDescartaMudancasLocais(t *testing.T) {
	repo, origin := repoComRemote(t)
	o := Novo()

	gitT(t, repo, "checkout", "-b", "trabalho-local")
	escrever(t, repo, "sujo.txt", "não commitado\n")
	publicarNovoCommit(t, origin)

	aviso, err := o.PosicionarBranchPrincipal(repo, "main")
	if err != nil {
		t.Fatalf("PosicionarBranchPrincipal: %v", err)
	}
	if aviso == "" || !strings.Contains(aviso, "trabalho-local") {
		t.Fatalf("aviso = %q, quero menção à branch com mudanças locais", aviso)
	}
	// Nada foi descartado nem trocado.
	if got := gitT(t, repo, "rev-parse", "--abbrev-ref", "HEAD"); got != "trabalho-local" {
		t.Fatalf("branch mudou para %q — não deveria com mudanças locais", got)
	}
	if st := gitT(t, repo, "status", "--porcelain"); !strings.Contains(st, "sujo.txt") {
		t.Fatalf("mudança local sumiu; status:\n%s", st)
	}
}

func TestPosicionarBranchPrincipalDivergenciaViraAviso(t *testing.T) {
	repo, origin := repoComRemote(t)
	o := Novo()

	// Local e origin divergem: commit local na main + commit remoto diferente.
	escrever(t, repo, "local.txt", "commit local\n")
	gitT(t, repo, "add", "-A")
	gitT(t, repo, "commit", "-m", "commit local nao publicado")
	publicarNovoCommit(t, origin)

	aviso, err := o.PosicionarBranchPrincipal(repo, "main")
	if err != nil {
		t.Fatalf("PosicionarBranchPrincipal: %v", err)
	}
	if aviso == "" || !strings.Contains(aviso, "divergiu") {
		t.Fatalf("aviso = %q, quero menção à divergência", aviso)
	}
	// O commit local continua lá (nada de reset/rebase automático).
	if log := gitT(t, repo, "log", "--oneline"); !strings.Contains(log, "commit local nao publicado") {
		t.Fatalf("commit local sumiu; log:\n%s", log)
	}
}

func TestPosicionarBranchPrincipalSemRemote(t *testing.T) {
	repo, _ := repoComRemote(t)
	gitT(t, repo, "remote", "remove", "origin")
	gitT(t, repo, "checkout", "-b", "outra")
	o := Novo()

	aviso, err := o.PosicionarBranchPrincipal(repo, "main")
	if err != nil {
		t.Fatalf("PosicionarBranchPrincipal: %v", err)
	}
	if aviso != "" {
		t.Fatalf("aviso inesperado sem remote: %q", aviso)
	}
	if got := gitT(t, repo, "rev-parse", "--abbrev-ref", "HEAD"); got != "main" {
		t.Fatalf("branch atual = %q, quero main (posicionamento vale mesmo sem remote)", got)
	}
}
