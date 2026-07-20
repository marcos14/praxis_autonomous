package ide

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestURLDownloadCLI(t *testing.T) {
	casos := []struct {
		goos, goarch, quer string
	}{
		{"windows", "amd64", "cli-win32-x64"},
		{"windows", "arm64", "cli-win32-arm64"},
		{"linux", "amd64", "cli-linux-x64"},
		{"linux", "arm64", "cli-linux-arm64"},
		{"darwin", "arm64", "cli-darwin-arm64"},
	}
	for _, c := range casos {
		u, err := urlDownloadCLI(c.goos, c.goarch)
		if err != nil {
			t.Fatalf("%s/%s: %v", c.goos, c.goarch, err)
		}
		if !strings.Contains(u, c.quer) {
			t.Errorf("%s/%s: url %q não contém %q", c.goos, c.goarch, u, c.quer)
		}
	}
	if _, err := urlDownloadCLI("plan9", "386"); err == nil {
		t.Error("plataforma sem build deveria dar erro")
	}
}

func TestExtrairDeZip(t *testing.T) {
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "pacote.zip")

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	f, _ := zw.Create("code.exe")
	f.Write([]byte("binario-fake"))
	zw.Close()
	if err := os.WriteFile(zipPath, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}

	destino := filepath.Join(dir, "code.exe")
	if err := extrairDeZip(zipPath, "code.exe", destino); err != nil {
		t.Fatalf("extrair: %v", err)
	}
	b, err := os.ReadFile(destino)
	if err != nil || string(b) != "binario-fake" {
		t.Fatalf("conteúdo extraído = %q, err=%v", b, err)
	}

	if err := extrairDeZip(zipPath, "inexistente", destino); err == nil {
		t.Error("membro inexistente deveria dar erro")
	}
}

func TestExtrairDeTarGz(t *testing.T) {
	dir := t.TempDir()
	tarPath := filepath.Join(dir, "pacote.tar.gz")

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	conteudo := []byte("binario-fake")
	tw.WriteHeader(&tar.Header{Name: "code", Mode: 0o755, Size: int64(len(conteudo)), Typeflag: tar.TypeReg})
	tw.Write(conteudo)
	tw.Close()
	gz.Close()
	if err := os.WriteFile(tarPath, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}

	destino := filepath.Join(dir, "code")
	if err := extrairDeTarGz(tarPath, "code", destino); err != nil {
		t.Fatalf("extrair: %v", err)
	}
	b, err := os.ReadFile(destino)
	if err != nil || string(b) != "binario-fake" {
		t.Fatalf("conteúdo extraído = %q, err=%v", b, err)
	}
}

func TestGarantirCLIReusaExistente(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "tools", "vscode-cli")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(dir, nomeExeCLI(runtime.GOOS))
	if err := os.WriteFile(exe, []byte("fake"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := garantirCLI(home, func(string) {})
	if err != nil {
		t.Fatalf("garantirCLI: %v", err)
	}
	if got != exe {
		t.Fatalf("caminho = %q, quero %q (não deveria baixar nada)", got, exe)
	}
}

func TestGerenteEstadoInicial(t *testing.T) {
	g := Novo(Opcoes{Home: t.TempDir()})
	if estado, _ := g.Estado(); estado != EstadoParado {
		t.Fatalf("estado inicial = %q, quero %q", estado, EstadoParado)
	}
	if _, _, ok := g.Alvo(); ok {
		t.Fatal("Alvo de gerente parado deveria devolver ok=false")
	}
	var nulo *Gerente
	if estado, _ := nulo.Estado(); estado != "desligado" {
		t.Fatalf("estado de gerente nil = %q, quero desligado", estado)
	}
}

func TestNovoToken(t *testing.T) {
	a, err := novoToken()
	if err != nil || len(a) != 32 {
		t.Fatalf("token = %q (len %d), err=%v", a, len(a), err)
	}
	b, _ := novoToken()
	if a == b {
		t.Fatal("dois tokens iguais")
	}
}

func TestPortaLivre(t *testing.T) {
	p, err := portaLivre()
	if err != nil || p <= 0 {
		t.Fatalf("porta = %d, err=%v", p, err)
	}
}
