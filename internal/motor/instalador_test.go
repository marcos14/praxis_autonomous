package motor

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// conteudoFalso é o "binário" servido pelos servidores de teste. No Windows um
// .exe de mentira não executa (--version falha), o que só zera o campo Versao —
// exatamente o comportamento best-effort esperado.
const conteudoFalso = "#!/bin/sh\necho praxis-teste 9.9.9\n"

func TestResolverCLICamadas(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PRAXIS_HOME", home)
	t.Setenv("PRAXIS_CLI_XPTO", "")

	// Sem nada: devolve o nome puro e CLIDisponivel = false.
	if got := ResolverCLI("xpto"); got != "xpto" {
		t.Fatalf("ResolverCLI sem nada = %q", got)
	}
	if _, ok := CLIDisponivel("xpto"); ok {
		t.Fatal("xpto não deveria estar disponível")
	}

	// Diretório gerenciado entra na resolução.
	dir := filepath.Join(home, "tools", "harness", "xpto", "bin")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(dir, nomeExeVendor("xpto"))
	if err := os.WriteFile(exe, []byte(conteudoFalso), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := ResolverCLI("xpto"); got != exe {
		t.Fatalf("ResolverCLI gerenciado = %q, quero %q", got, exe)
	}
	if _, ok := CLIDisponivel("xpto"); !ok {
		t.Fatal("xpto gerenciado deveria estar disponível")
	}

	// Override explícito vence tudo.
	t.Setenv("PRAXIS_CLI_XPTO", `C:\outro\xpto.exe`)
	if got := ResolverCLI("xpto"); got != `C:\outro\xpto.exe` {
		t.Fatalf("override = %q", got)
	}
}

// TestInstalarClaudeComServidorFake exercita o fluxo completo do formato "bin"
// (o do claude): /stable → versão → download → swap no diretório gerenciado.
func TestInstalarClaudeComServidorFake(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PRAXIS_HOME", home)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/stable"):
			_, _ = w.Write([]byte("9.9.9"))
		case strings.Contains(r.URL.Path, "/9.9.9/"):
			_, _ = w.Write([]byte(conteudoFalso))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	t.Setenv("PRAXIS_DOWNLOAD_BASE_CLAUDE", srv.URL)

	var etapas []string
	info, err := Instalar(context.Background(), "claude", func(m string) { etapas = append(etapas, m) })
	if err != nil {
		t.Fatalf("Instalar: %v", err)
	}
	if b, err := os.ReadFile(info.Caminho); err != nil || string(b) != conteudoFalso {
		t.Fatalf("binário instalado errado: %v / %q", err, b)
	}
	if len(etapas) == 0 {
		t.Error("nenhuma etapa de progresso reportada")
	}
	// Reinstala por cima (atualização): o corrente vira .old e o novo assume.
	if _, err := Instalar(context.Background(), "claude", nil); err != nil {
		t.Fatalf("reinstalar: %v", err)
	}

	// O ResolverCLI enxerga o gerenciado (a menos que haja um claude no PATH,
	// que tem precedência — só validamos o CLIDisponivel).
	if _, ok := CLIDisponivel("claude"); !ok {
		t.Fatal("claude instalado deveria estar disponível")
	}
}

// TestInstalarDeGitHubZipETarGz exercita a resolução por release do GitHub e a
// extração dos dois formatos de pacote.
func TestInstalarDeGitHubZipETarGz(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PRAXIS_HOME", home)

	// zip com o binário do opencode.
	var zbuf bytes.Buffer
	zw := zip.NewWriter(&zbuf)
	fw, _ := zw.Create("opencode" + map[string]string{"windows": ".exe"}[runtime.GOOS])
	_, _ = fw.Write([]byte(conteudoFalso))
	_ = zw.Close()

	// tar.gz com o binário do codex (nome com triple, como no release real).
	var tbuf bytes.Buffer
	gz := gzip.NewWriter(&tbuf)
	tw := tar.NewWriter(gz)
	nomeCodex := "codex-x86_64-teste"
	if runtime.GOOS == "windows" {
		nomeCodex += ".exe"
	}
	_ = tw.WriteHeader(&tar.Header{Name: nomeCodex, Mode: 0o755, Size: int64(len(conteudoFalso)), Typeflag: tar.TypeReg})
	_, _ = tw.Write([]byte(conteudoFalso))
	_ = tw.Close()
	_ = gz.Close()

	arqOpencode := "opencode-" + map[string]string{"windows": "windows", "linux": "linux", "darwin": "darwin"}[runtime.GOOS] +
		"-" + map[string]string{"amd64": "x64", "arm64": "arm64"}[runtime.GOARCH] + ".zip"
	arqCodex := "codex-" + map[string]string{"amd64": "x86_64", "arm64": "aarch64"}[runtime.GOARCH] + "-" +
		map[string]string{"windows": "pc-windows-msvc", "linux": "unknown-linux-musl", "darwin": "apple-darwin"}[runtime.GOOS] + ".tar.gz"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/latest-opencode"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"tag_name": "v1.2.3",
				"assets": []map[string]string{
					{"name": "outra-coisa.txt", "browser_download_url": "http://invalido"},
					{"name": arqOpencode, "browser_download_url": srvURL(r) + "/dl/opencode.zip"},
				},
			})
		case strings.HasSuffix(r.URL.Path, "/latest-codex"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"tag_name": "rust-v9",
				"assets": []map[string]string{
					{"name": arqCodex, "browser_download_url": srvURL(r) + "/dl/codex.tar.gz"},
				},
			})
		case r.URL.Path == "/dl/opencode.zip":
			_, _ = w.Write(zbuf.Bytes())
		case r.URL.Path == "/dl/codex.tar.gz":
			_, _ = w.Write(tbuf.Bytes())
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	t.Setenv("PRAXIS_DOWNLOAD_BASE_OPENCODE", srv.URL+"/latest-opencode")
	t.Setenv("PRAXIS_DOWNLOAD_BASE_CODEX", srv.URL+"/latest-codex")

	info, err := Instalar(context.Background(), "opencode", nil)
	if err != nil {
		t.Fatalf("instalar opencode: %v", err)
	}
	if b, _ := os.ReadFile(info.Caminho); string(b) != conteudoFalso {
		t.Fatalf("opencode extraído errado: %q", b)
	}

	info, err = Instalar(context.Background(), "codex", nil)
	if err != nil {
		t.Fatalf("instalar codex: %v", err)
	}
	if b, _ := os.ReadFile(info.Caminho); string(b) != conteudoFalso {
		t.Fatalf("codex extraído errado: %q", b)
	}
}

// srvURL reconstrói a URL base do servidor de teste a partir da requisição.
func srvURL(r *http.Request) string { return "http://" + r.Host }

func TestInstalarVendorDesconhecido(t *testing.T) {
	t.Setenv("PRAXIS_HOME", t.TempDir())
	if _, err := Instalar(context.Background(), "foobar", nil); err == nil {
		t.Fatal("vendor desconhecido deveria falhar")
	}
}

func TestEstadoCLIs(t *testing.T) {
	t.Setenv("PRAXIS_HOME", t.TempDir())
	estados := EstadoCLIs(context.Background())
	if len(estados) != 3 {
		t.Fatalf("EstadoCLIs = %d vendors, quero 3", len(estados))
	}
	for _, e := range estados {
		if !e.Instalavel {
			t.Errorf("%s deveria ser instalável", e.Vendor)
		}
	}
}
