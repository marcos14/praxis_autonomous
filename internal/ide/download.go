// Download automático do CLI oficial do VS Code (`code`), usado pelo Gerente
// para subir o `code serve-web`. O binário NÃO é redistribuído pelo Praxis: é
// baixado do endpoint oficial da Microsoft na máquina do operador, que aceita a
// licença via --accept-server-license-terms na subida do serve-web.
//
// O endpoint https://update.code.visualstudio.com/latest/<plataforma>/stable é
// estável e devolve um zip (Windows/macOS) ou tar.gz (Linux) contendo apenas o
// executável `code`. Tudo em stdlib (net/http + archive/zip + archive/tar).
package ide

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// urlDownloadCLI monta a URL de download do CLI para um SO/arquitetura. Devolve
// erro para combinações sem build oficial.
func urlDownloadCLI(goos, goarch string) (string, error) {
	plataformas := map[string]string{
		"windows/amd64": "cli-win32-x64",
		"windows/arm64": "cli-win32-arm64",
		"linux/amd64":   "cli-linux-x64",
		"linux/arm64":   "cli-linux-arm64",
		"linux/arm":     "cli-linux-armhf",
		"darwin/amd64":  "cli-darwin-x64",
		"darwin/arm64":  "cli-darwin-arm64",
	}
	p, ok := plataformas[goos+"/"+goarch]
	if !ok {
		return "", fmt.Errorf("sem build do CLI do VS Code para %s/%s", goos, goarch)
	}
	return "https://update.code.visualstudio.com/latest/" + p + "/stable", nil
}

// nomeExeCLI é o nome do executável do CLI no SO corrente.
func nomeExeCLI(goos string) string {
	if goos == "windows" {
		return "code.exe"
	}
	return "code"
}

// garantirCLI devolve o caminho de um executável `code` utilizável: o já baixado
// em <home>/tools/vscode-cli, ou baixa e extrai agora do endpoint oficial. Se o
// download falhar (ex.: servidor sem internet), cai para um `code` no PATH do
// servidor; sem nenhum dos dois, devolve o erro do download com a orientação.
func garantirCLI(home string, logf func(string)) (string, error) {
	dir := filepath.Join(home, "tools", "vscode-cli")
	exe := filepath.Join(dir, nomeExeCLI(runtime.GOOS))
	if _, err := os.Stat(exe); err == nil {
		return exe, nil
	}

	logf("IDE web: baixando o CLI do VS Code (primeira execução)")
	if err := baixarCLI(dir, exe); err != nil {
		if noPath, errPath := exec.LookPath("code"); errPath == nil {
			logf("IDE web: download falhou (" + err.Error() + "); usando o `code` do PATH")
			return noPath, nil
		}
		return "", fmt.Errorf("baixar CLI do VS Code: %w (sem internet? instale o VS Code CLI e deixe `code` no PATH do servidor)", err)
	}
	logf("IDE web: CLI do VS Code pronto em " + exe)
	return exe, nil
}

// baixarCLI baixa o arquivo da plataforma corrente para um temporário em dir e
// extrai o executável para exe.
func baixarCLI(dir, exe string) error {
	url, err := urlDownloadCLI(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	cliente := &http.Client{Timeout: 10 * time.Minute}
	resp, err := cliente.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download devolveu HTTP %d", resp.StatusCode)
	}

	// Salva o pacote em um temporário (o zip exige leitura com ReaderAt/tamanho).
	tmp, err := os.CreateTemp(dir, "download-*")
	if err != nil {
		return err
	}
	defer func() { tmp.Close(); os.Remove(tmp.Name()) }()
	if _, err := io.Copy(tmp, resp.Body); err != nil {
		return err
	}

	if runtime.GOOS == "linux" {
		err = extrairDeTarGz(tmp.Name(), filepath.Base(exe), exe)
	} else {
		err = extrairDeZip(tmp.Name(), filepath.Base(exe), exe)
	}
	if err != nil {
		return fmt.Errorf("extrair %q do pacote: %w", filepath.Base(exe), err)
	}
	return nil
}

// extrairDeZip extrai o membro de nome-base `nome` do zip para destino.
func extrairDeZip(zipPath, nome, destino string) error {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer zr.Close()
	for _, f := range zr.File {
		if filepath.Base(strings.TrimSuffix(f.Name, "/")) != nome || f.FileInfo().IsDir() {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		defer rc.Close()
		return gravarExecutavel(destino, rc)
	}
	return fmt.Errorf("membro %q não encontrado no zip", nome)
}

// extrairDeTarGz extrai o membro de nome-base `nome` do tar.gz para destino.
func extrairDeTarGz(tarPath, nome, destino string) error {
	f, err := os.Open(tarPath)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if hdr.Typeflag != tar.TypeReg || filepath.Base(hdr.Name) != nome {
			continue
		}
		return gravarExecutavel(destino, tr)
	}
	return fmt.Errorf("membro %q não encontrado no tar.gz", nome)
}

// gravarExecutavel grava o conteúdo em destino com bit de execução, via um
// temporário + rename para nunca deixar um executável meio-escrito no lugar.
func gravarExecutavel(destino string, r io.Reader) error {
	tmp, err := os.CreateTemp(filepath.Dir(destino), "exe-*")
	if err != nil {
		return err
	}
	if _, err := io.Copy(tmp, r); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o755); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), destino)
}
