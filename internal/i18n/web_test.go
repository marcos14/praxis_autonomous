package i18n

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestModulosWebParseiam valida a SINTAXE de cada módulo do frontend como ES
// module. É o gate que faltava quando uma conversão automática de i18n corrompeu
// um literal e a página quebrou no navegador com SyntaxError: `node --check`
// sozinho não serve — sem --input-type=module ele avalia como CommonJS e o
// resultado não é confiável. Sem node no PATH, o teste é pulado (o CI que tiver
// node cobre; o build Go continua verde em máquinas sem ele).
func TestModulosWebParseiam(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node não está no PATH: pulando a validação de sintaxe do frontend")
	}
	dir := filepath.Join("..", "..", "web", "js")
	entradas, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ler %s: %v", dir, err)
	}
	for _, e := range entradas {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".js") {
			continue
		}
		fonte, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("ler %s: %v", e.Name(), err)
		}
		cmd := exec.Command(node, "--input-type=module", "--check")
		cmd.Stdin = strings.NewReader(string(fonte))
		if saida, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("web/js/%s não parseia como ES module:\n%s", e.Name(), saida)
		}
	}
}

// TestRenderMarkdownNaoQuebra roda o renderMarkdown real do frontend contra
// casos-limite e contra TODAS as seções do manual, em todos os idiomas. Guarda
// um incidente real: os arquivos pt-BR estavam com CRLF, o "\r" desancorava o
// padrão de título (`$` não casa antes de \r), a linha caía no ramo de parágrafo
// sem ser consumida e a tela do Manual morria com "Cannot read properties of
// undefined". Como o mesmo caminho renderiza PRDs colados pelo usuário — que
// vêm com CRLF o tempo todo no Windows —, o parser precisa aguentar qualquer
// entrada.
func TestRenderMarkdownNaoQuebra(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node não está no PATH: pulando a validação do renderMarkdown")
	}
	raiz, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolver raiz: %v", err)
	}
	args := []string{
		filepath.Join("testdata", "render_markdown.mjs"),
		// import() dinâmico exige URL file:// com barras normais.
		"file:///" + filepath.ToSlash(filepath.Join(raiz, "web", "js", "ui.js")),
	}
	manuais, err := filepath.Glob(filepath.Join(raiz, "internal", "api", "manual", "*", "*.md"))
	if err != nil {
		t.Fatalf("listar manual: %v", err)
	}
	if len(manuais) == 0 {
		t.Fatal("nenhum arquivo de manual encontrado")
	}
	args = append(args, manuais...)

	saida, err := exec.Command(node, args...).CombinedOutput()
	if err != nil {
		t.Errorf("renderMarkdown quebrou:\n%s", saida)
		return
	}
	t.Log(strings.TrimSpace(string(saida)))
}

// TestImportsDoI18nNoWeb garante que todo módulo que CHAMA t()/tn()/idiomaAtivo()
// também as importa de ./i18n.js — uma chamada sem import passa no parser e só
// falha no navegador (ReferenceError), derrubando a tela inteira.
func TestImportsDoI18nNoWeb(t *testing.T) {
	dir := filepath.Join("..", "..", "web", "js")
	entradas, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ler %s: %v", dir, err)
	}
	reImport := regexp.MustCompile(`import\s*\{([^}]+)\}\s*from\s*"\./i18n\.js"`)
	funcoes := []string{"t", "tn", "idiomaAtivo", "aplicarTraducoes", "seletorIdioma", "adotarIdiomaDoUsuario"}

	for _, e := range entradas {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".js") || e.Name() == "i18n.js" {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("ler %s: %v", e.Name(), err)
		}
		fonte := string(b)
		importados := map[string]bool{}
		if m := reImport.FindStringSubmatch(fonte); m != nil {
			for _, nome := range strings.Split(m[1], ",") {
				importados[strings.TrimSpace(nome)] = true
			}
		}
		for _, fn := range funcoes {
			// Chamada da função sem estar precedida por letra/ponto (evita casar
			// split(, set(, insertAdjacentElement( etc.).
			usa := regexp.MustCompile(`(^|[^\w.$])` + fn + `\s*\(`).MatchString(fonte)
			if usa && !importados[fn] {
				t.Errorf("web/js/%s chama %s() mas não a importa de ./i18n.js", e.Name(), fn)
			}
		}
	}
}
