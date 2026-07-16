package motor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidarAlias(t *testing.T) {
	if err := ValidarAlias("claude_alt"); err != nil {
		t.Fatalf("alias valido rejeitado: %v", err)
	}
	if err := ValidarAlias("123errado"); err == nil {
		t.Fatal("esperava erro para alias comecando por numero")
	}
	if err := ValidarAlias("claude"); err == nil {
		t.Fatal("esperava erro para alias reservado 'claude'")
	}
	if err := ValidarAlias("codex"); err == nil {
		t.Fatal("esperava erro para alias que conflita com motor existente")
	}
}

func TestSugerirConfigDir(t *testing.T) {
	got := SugerirConfigDir("claude_alt")
	if got == "" {
		t.Skip("sem HOME resolvivel neste ambiente")
	}
	if filepath.Base(got) != ".claude-alt" {
		t.Fatalf("sufixo do config dir inesperado: %q", got)
	}
}

func TestNomesComandoShell(t *testing.T) {
	got := NomesComandoShell("claude_alt")
	if len(got) != 2 || got[0] != "claude-alt" || got[1] != "claude_alt" {
		t.Fatalf("nomes de comando inesperados: %+v", got)
	}
	// Alias sem underscore nao deve duplicar.
	if got := NomesComandoShell("claudealt"); len(got) != 1 || got[0] != "claudealt" {
		t.Fatalf("nao deveria duplicar alias sem underscore: %+v", got)
	}
}

func TestUpsertBlocoMarcadoIdempotente(t *testing.T) {
	start := "# >>> teste >>>"
	end := "# <<< teste <<<"
	bloco := start + "\nlinha\n" + end
	primeiro := upsertBlocoMarcado("", start, end, bloco)
	segundo := upsertBlocoMarcado(primeiro, start, end, bloco)
	if primeiro != segundo {
		t.Fatalf("upsert deveria ser idempotente\nprimeiro:\n%s\nsegundo:\n%s", primeiro, segundo)
	}
}

func TestUpsertBlocoAliasPowerShellIdempotente(t *testing.T) {
	perfil := filepath.Join(t.TempDir(), "profile.ps1")
	if err := os.WriteFile(perfil, []byte("# perfil existente\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	const cfgDir = `C:\Users\dev\.claude-alt`
	if err := upsertBlocoAliasPowerShell(perfil, "claude-alt", cfgDir); err != nil {
		t.Fatalf("primeira escrita: %v", err)
	}
	b1, _ := os.ReadFile(perfil)
	if !strings.Contains(string(b1), cfgDir) || !strings.Contains(string(b1), "function claude-alt") {
		t.Fatalf("bloco nao contem a funcao/config dir: %s", b1)
	}
	if !strings.Contains(string(b1), "# perfil existente") {
		t.Fatalf("conteudo previo do perfil foi perdido: %s", b1)
	}
	// Uma vez presente, o bloco marcado estabiliza: reaplicar nao altera mais o
	// perfil (a substituicao do bloco existente e idempotente).
	if err := upsertBlocoAliasPowerShell(perfil, "claude-alt", cfgDir); err != nil {
		t.Fatalf("segunda escrita: %v", err)
	}
	b2, _ := os.ReadFile(perfil)
	if err := upsertBlocoAliasPowerShell(perfil, "claude-alt", cfgDir); err != nil {
		t.Fatalf("terceira escrita: %v", err)
	}
	b3, _ := os.ReadFile(perfil)
	if string(b2) != string(b3) {
		t.Fatalf("reaplicar o bloco existente deveria ser idempotente\n2:\n%s\n3:\n%s", b2, b3)
	}
	if strings.Count(string(b3), "function claude-alt") != 1 {
		t.Fatalf("bloco duplicado no perfil: %s", b3)
	}
}

func TestCaminhosPerfilPowerShellIncluiOneDriveDocumentos(t *testing.T) {
	home := `C:\Users\marco`
	got := caminhosPerfilPowerShell(home)
	if len(got) < 4 {
		t.Fatalf("lista de perfis deveria ter varios candidatos, veio: %+v", got)
	}
	temDocPt := false
	temOneDrivePt := false
	for _, p := range got {
		if p == `C:\Users\marco\Documentos\WindowsPowerShell\Microsoft.PowerShell_profile.ps1` {
			temDocPt = true
		}
		if p == `C:\Users\marco\OneDrive\Documentos\WindowsPowerShell\Microsoft.PowerShell_profile.ps1` {
			temOneDrivePt = true
		}
	}
	if !temDocPt || !temOneDrivePt {
		t.Fatalf("faltou caminho em portugues para perfil do PowerShell: %+v", got)
	}
}
