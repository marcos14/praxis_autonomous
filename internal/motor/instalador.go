package motor

// Instalador de harnesses (Fase D do PLANO_MULTIUSUARIO.md): baixa o binário
// STANDALONE oficial de cada vendor para o diretório gerenciado
// (PRAXIS_HOME/tools/harness/<vendor>/bin) — mesmo molde do download do VS Code
// CLI do IDE web. Sem npm/Node no host: os três vendors publicam binários
// nativos por plataforma. O ResolverCLI (cli.go) já enxerga o diretório
// gerenciado, então instalar aqui deixa o motor utilizável na sequência (login
// pela web incluído).
//
// Atualizar = instalar de novo: o binário corrente vira .old (o Windows não
// deixa sobrescrever um exe em uso, mas deixa renomeá-lo) e o novo assume.
//
// As URLs oficiais podem ser sobrescritas por variável de ambiente
// (PRAXIS_DOWNLOAD_BASE_<VENDOR>) — é o que os testes usam, e serve de escape
// para mirrors corporativos.

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
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

// timeoutDownload limita o download de um binário de harness.
const timeoutDownload = 10 * time.Minute

// urlBaseClaude é a distribuição nativa oficial do Claude Code (a mesma que o
// install.sh/install.ps1 do vendor usa): /stable devolve a versão corrente e
// /<versão>/<plataforma>/claude(.exe) é o binário.
const urlBaseClaude = "https://storage.googleapis.com/claude-code-dist-86c565f3-f756-42ad-8dfa-d59b1c096819/claude-code-releases"

// URLs da API de releases do GitHub (codex e opencode publicam binários lá).
const (
	urlReleasesCodex    = "https://api.github.com/repos/openai/codex/releases/latest"
	urlReleasesOpencode = "https://api.github.com/repos/sst/opencode/releases/latest"
)

// Formatos de pacote que o instalador sabe abrir.
const (
	formatoBin   = "bin"
	formatoZip   = "zip"
	formatoTarGz = "targz"
)

// alvoDownload é o resultado da resolução: de onde baixar e como abrir.
type alvoDownload struct {
	URL     string
	Formato string
	Versao  string
}

// InfoInstalacao é o resultado de Instalar.
type InfoInstalacao struct {
	Vendor  string `json:"vendor"`
	Caminho string `json:"caminho"`
	Versao  string `json:"versao,omitempty"`
}

// VendorsInstalaveis lista os harnesses que o instalador sabe baixar.
func VendorsInstalaveis() []string { return []string{"claude", "codex", "opencode"} }

// VendorInstalavel informa se o instalador cobre o vendor.
func VendorInstalavel(vendor string) bool {
	switch normalizarNomeMotor(vendor) {
	case "claude", "codex", "opencode":
		return true
	}
	return false
}

// baseVendor devolve a URL base do vendor, respeitando o override
// PRAXIS_DOWNLOAD_BASE_<VENDOR> (testes e mirrors).
func baseVendor(vendor, padrao string) string {
	if v := strings.TrimSpace(os.Getenv("PRAXIS_DOWNLOAD_BASE_" + strings.ToUpper(normalizarNomeMotor(vendor)))); v != "" {
		return v
	}
	return padrao
}

// resolverAlvo descobre a URL de download do vendor para GOOS/GOARCH correntes.
func resolverAlvo(ctx context.Context, cliente *http.Client, vendor string) (alvoDownload, error) {
	switch normalizarNomeMotor(vendor) {
	case "claude":
		return alvoClaude(ctx, cliente)
	case "codex":
		return alvoGitHub(ctx, cliente, baseVendor("codex", urlReleasesCodex), casaAssetCodex)
	case "opencode":
		return alvoGitHub(ctx, cliente, baseVendor("opencode", urlReleasesOpencode), casaAssetOpencode)
	}
	return alvoDownload{}, fmt.Errorf("não sei instalar o harness %q (suportados: %s)",
		vendor, strings.Join(VendorsInstalaveis(), ", "))
}

// alvoClaude resolve a distribuição nativa do Claude Code: GET /stable devolve
// a versão; o binário fica em /<versão>/<plataforma>/claude(.exe).
func alvoClaude(ctx context.Context, cliente *http.Client) (alvoDownload, error) {
	plataforma := map[string]string{
		"windows/amd64": "win32-x64",
		"linux/amd64":   "linux-x64",
		"linux/arm64":   "linux-arm64",
		"darwin/amd64":  "darwin-x64",
		"darwin/arm64":  "darwin-arm64",
	}[runtime.GOOS+"/"+runtime.GOARCH]
	if plataforma == "" {
		return alvoDownload{}, fmt.Errorf("plataforma %s/%s sem build do claude", runtime.GOOS, runtime.GOARCH)
	}
	base := baseVendor("claude", urlBaseClaude)
	versao, err := lerTexto(ctx, cliente, base+"/stable")
	if err != nil {
		return alvoDownload{}, fmt.Errorf("descobrir a versão estável do claude: %w", err)
	}
	nome := "claude"
	if runtime.GOOS == "windows" {
		nome = "claude.exe"
	}
	return alvoDownload{
		URL:     base + "/" + versao + "/" + plataforma + "/" + nome,
		Formato: formatoBin,
		Versao:  versao,
	}, nil
}

// releaseGitHub é o subconjunto da resposta da API de releases que interessa.
type releaseGitHub struct {
	TagName string `json:"tag_name"`
	Assets  []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

// alvoGitHub resolve o release mais recente e escolhe o asset da plataforma
// pelo seletor do vendor.
func alvoGitHub(ctx context.Context, cliente *http.Client, url string,
	casa func(nome string) bool) (alvoDownload, error) {

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return alvoDownload{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := cliente.Do(req)
	if err != nil {
		return alvoDownload{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return alvoDownload{}, fmt.Errorf("consultar %s: HTTP %d", url, resp.StatusCode)
	}
	var rel releaseGitHub
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&rel); err != nil {
		return alvoDownload{}, fmt.Errorf("decodificar o release: %w", err)
	}
	for _, a := range rel.Assets {
		if !casa(strings.ToLower(a.Name)) {
			continue
		}
		formato := formatoBin
		switch {
		case strings.HasSuffix(a.Name, ".zip"):
			formato = formatoZip
		case strings.HasSuffix(a.Name, ".tar.gz"), strings.HasSuffix(a.Name, ".tgz"):
			formato = formatoTarGz
		}
		return alvoDownload{URL: a.URL, Formato: formato, Versao: rel.TagName}, nil
	}
	return alvoDownload{}, fmt.Errorf("o release %s não tem binário para %s/%s",
		rel.TagName, runtime.GOOS, runtime.GOARCH)
}

// casaAssetCodex reconhece o asset do codex (triples Rust) da plataforma.
func casaAssetCodex(nome string) bool {
	if !strings.HasPrefix(nome, "codex-") {
		return false
	}
	if !strings.HasSuffix(nome, ".zip") && !strings.HasSuffix(nome, ".tar.gz") {
		return false
	}
	// Assets de utilitários (codex-responses-api-proxy-…) não são o CLI.
	if strings.Contains(nome, "proxy") {
		return false
	}
	arq := map[string]string{"amd64": "x86_64", "arm64": "aarch64"}[runtime.GOARCH]
	if arq == "" || !strings.Contains(nome, arq) {
		return false
	}
	switch runtime.GOOS {
	case "windows":
		return strings.Contains(nome, "windows")
	case "darwin":
		return strings.Contains(nome, "apple-darwin")
	default:
		// linux: o build musl roda em qualquer distro; gnu também serve.
		return strings.Contains(nome, "linux")
	}
}

// casaAssetOpencode reconhece o asset do opencode da plataforma.
func casaAssetOpencode(nome string) bool {
	if !strings.HasPrefix(nome, "opencode-") || !strings.HasSuffix(nome, ".zip") {
		return false
	}
	so := map[string]string{"windows": "windows", "linux": "linux", "darwin": "darwin"}[runtime.GOOS]
	arq := map[string]string{"amd64": "x64", "arm64": "arm64"}[runtime.GOARCH]
	return so != "" && arq != "" && strings.Contains(nome, so) && strings.Contains(nome, arq)
}

// Instalar baixa o binário oficial do vendor para o diretório gerenciado e
// devolve caminho + versão. progresso (opcional) recebe mensagens de etapa.
func Instalar(ctx context.Context, vendor string, progresso func(string)) (InfoInstalacao, error) {
	avisar := func(msg string) {
		if progresso != nil {
			progresso(msg)
		}
	}
	vendor = normalizarNomeMotor(vendor)
	if !VendorInstalavel(vendor) {
		return InfoInstalacao{}, fmt.Errorf("não sei instalar o harness %q (suportados: %s)",
			vendor, strings.Join(VendorsInstalaveis(), ", "))
	}
	dir := dirHarnessGerenciado(vendor)
	if dir == "" {
		return InfoInstalacao{}, errors.New("PRAXIS_HOME não resolvido: sem destino para o download")
	}
	ctx, cancelar := context.WithTimeout(ctx, timeoutDownload)
	defer cancelar()
	cliente := &http.Client{}

	avisar("resolvendo a versão mais recente…")
	alvo, err := resolverAlvo(ctx, cliente, vendor)
	if err != nil {
		return InfoInstalacao{}, err
	}

	avisar("baixando " + alvo.Versao + "…")
	tmp, err := baixarParaTemp(ctx, cliente, alvo.URL)
	if err != nil {
		return InfoInstalacao{}, fmt.Errorf("baixar %s: %w", alvo.URL, err)
	}
	defer os.Remove(tmp)

	avisar("instalando…")
	destino := filepath.Join(dir, nomeExeVendor(vendor))
	if err := instalarBinario(tmp, alvo.Formato, vendor, destino); err != nil {
		return InfoInstalacao{}, err
	}

	info := InfoInstalacao{Vendor: vendor, Caminho: destino, Versao: alvo.Versao}
	if v := versaoCLI(ctx, destino); v != "" {
		info.Versao = v
	}
	return info, nil
}

// lerTexto faz um GET e devolve o corpo aparado (endpoints de versão).
func lerTexto(ctx context.Context, cliente *http.Client, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := cliente.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GET %s: HTTP %d", url, resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return "", err
	}
	v := strings.TrimSpace(string(b))
	if v == "" {
		return "", fmt.Errorf("GET %s: resposta vazia", url)
	}
	return v, nil
}

// baixarParaTemp baixa a URL para um arquivo temporário e devolve o caminho.
func baixarParaTemp(ctx context.Context, cliente *http.Client, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := cliente.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	tmp, err := os.CreateTemp("", "praxis-harness-*")
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(tmp, resp.Body); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return "", err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return "", err
	}
	return tmp.Name(), nil
}

// instalarBinario extrai/copia o executável do pacote baixado para destino,
// preservando um binário em uso via renomeio para .old.
func instalarBinario(pacote, formato, vendor, destino string) error {
	if err := os.MkdirAll(filepath.Dir(destino), 0o755); err != nil {
		return err
	}
	// tmp ao lado do destino: o rename final é atômico no mesmo volume.
	tmp := destino + ".novo"
	defer os.Remove(tmp)

	var err error
	switch formato {
	case formatoBin:
		err = copiarArquivo(pacote, tmp)
	case formatoZip:
		err = extrairExecutavelZip(pacote, vendor, tmp)
	case formatoTarGz:
		err = extrairExecutavelTarGz(pacote, vendor, tmp)
	default:
		err = fmt.Errorf("formato de pacote desconhecido: %q", formato)
	}
	if err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o755); err != nil {
		return err
	}
	// Binário corrente (possivelmente em execução) vira .old.
	if _, err := os.Stat(destino); err == nil {
		_ = os.Remove(destino + ".old")
		if err := os.Rename(destino, destino+".old"); err != nil {
			return fmt.Errorf("liberar o binário atual (%s em uso?): %w", destino, err)
		}
	}
	if err := os.Rename(tmp, destino); err != nil {
		return err
	}
	_ = os.Remove(destino + ".old") // best-effort: sai quando ninguém mais o segura
	return nil
}

// pareceExecutavelDoVendor decide se uma entrada de pacote é o CLI do vendor.
func pareceExecutavelDoVendor(nome, vendor string) bool {
	base := strings.ToLower(filepath.Base(filepath.ToSlash(nome)))
	base = strings.TrimSuffix(base, ".exe")
	return base == vendor || strings.HasPrefix(base, vendor+"-") || strings.HasPrefix(base, vendor+"_")
}

// extrairExecutavelZip localiza o CLI do vendor dentro do zip e o grava em destino.
func extrairExecutavelZip(pacote, vendor, destino string) error {
	zr, err := zip.OpenReader(pacote)
	if err != nil {
		return fmt.Errorf("abrir o zip: %w", err)
	}
	defer zr.Close()
	for _, f := range zr.File {
		if f.FileInfo().IsDir() || !pareceExecutavelDoVendor(f.Name, vendor) {
			continue
		}
		r, err := f.Open()
		if err != nil {
			return err
		}
		defer r.Close()
		return gravarArquivo(destino, r)
	}
	return fmt.Errorf("o zip não contém o executável do %s", vendor)
}

// extrairExecutavelTarGz localiza o CLI do vendor dentro do tar.gz.
func extrairExecutavelTarGz(pacote, vendor, destino string) error {
	f, err := os.Open(pacote)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("abrir o tar.gz: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		if h.Typeflag != tar.TypeReg || !pareceExecutavelDoVendor(h.Name, vendor) {
			continue
		}
		return gravarArquivo(destino, tr)
	}
	return fmt.Errorf("o tar.gz não contém o executável do %s", vendor)
}

func copiarArquivo(origem, destino string) error {
	in, err := os.Open(origem)
	if err != nil {
		return err
	}
	defer in.Close()
	return gravarArquivo(destino, in)
}

func gravarArquivo(destino string, r io.Reader) error {
	out, err := os.OpenFile(destino, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, r); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// versaoCLI pergunta a versão ao binário recém-instalado (best-effort).
func versaoCLI(ctx context.Context, exe string) string {
	ctx, cancelar := context.WithTimeout(ctx, 30*time.Second)
	defer cancelar()
	out, err := exec.CommandContext(ctx, exe, "--version").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(strings.Split(strings.TrimSpace(string(out)), "\n")[0])
}

// InfoCLI descreve o estado do CLI de um vendor para a tela Motores.
type InfoCLI struct {
	Vendor     string `json:"vendor"`
	Instalavel bool   `json:"instalavel"`
	Instalado  bool   `json:"instalado"`
	// Origem: "path" (PATH/override do operador) ou "gerenciado" (instalado
	// pelo Praxis). Vazia quando não instalado.
	Origem  string `json:"origem,omitempty"`
	Caminho string `json:"caminho,omitempty"`
	Versao  string `json:"versao,omitempty"`
}

// EstadoCLIs devolve o estado dos CLIs dos harnesses conhecidos.
func EstadoCLIs(ctx context.Context) []InfoCLI {
	out := make([]InfoCLI, 0, len(VendorsInstalaveis()))
	for _, vendor := range VendorsInstalaveis() {
		info := InfoCLI{Vendor: vendor, Instalavel: VendorInstalavel(vendor)}
		if caminho, ok := CLIDisponivel(vendor); ok {
			info.Instalado = true
			info.Caminho = caminho
			info.Origem = "path"
			if ger, ok := caminhoCLIGerenciado(vendor); ok && ger == caminho {
				info.Origem = "gerenciado"
			}
			info.Versao = versaoCLI(ctx, caminho)
		}
		out = append(out, info)
	}
	return out
}
