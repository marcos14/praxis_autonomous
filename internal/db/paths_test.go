package db

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestPraxisHomeUsaVariavelDeAmbiente(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PRAXIS_HOME", dir)

	got, err := PraxisHome()
	if err != nil {
		t.Fatalf("PraxisHome: %v", err)
	}
	if got != dir {
		t.Fatalf("PraxisHome = %q, quero %q", got, dir)
	}
}

func TestPraxisHomeIgnoraValorEmBranco(t *testing.T) {
	// PRAXIS_HOME="   " deve cair no default do SO, não virar caminho vazio.
	t.Setenv("PRAXIS_HOME", "   ")
	got, err := PraxisHome()
	if err != nil {
		t.Fatalf("PraxisHome: %v", err)
	}
	if strings.TrimSpace(got) == "" {
		t.Fatal("PraxisHome devolveu caminho vazio")
	}
	if filepath.Base(got) != "praxis" {
		t.Fatalf("default deveria terminar em 'praxis', veio %q", got)
	}
}

func TestPraxisHomeDefaultWindowsUsaLocalAppData(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("default específico do Windows")
	}
	t.Setenv("PRAXIS_HOME", "")
	base := t.TempDir()
	t.Setenv("LOCALAPPDATA", base)

	got, err := PraxisHome()
	if err != nil {
		t.Fatalf("PraxisHome: %v", err)
	}
	if want := filepath.Join(base, "praxis"); got != want {
		t.Fatalf("PraxisHome = %q, quero %q", got, want)
	}
}

func TestCaminhoDBCriaDiretorioERetornaArquivo(t *testing.T) {
	base := t.TempDir()
	home := filepath.Join(base, "sub", "praxis") // subdir ainda inexistente
	t.Setenv("PRAXIS_HOME", home)

	caminho, err := CaminhoDB()
	if err != nil {
		t.Fatalf("CaminhoDB: %v", err)
	}
	if want := filepath.Join(home, nomeArquivoDB); caminho != want {
		t.Fatalf("CaminhoDB = %q, quero %q", caminho, want)
	}
	fi, err := os.Stat(home)
	if err != nil || !fi.IsDir() {
		t.Fatalf("PRAXIS_HOME %q não foi criado como diretório (err=%v)", home, err)
	}
}
