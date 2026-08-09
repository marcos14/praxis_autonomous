package i18n

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// TestParidadeCatalogosServidor garante que os 4 catálogos do servidor têm
// exatamente o mesmo conjunto de chaves — chave faltando ou sobrando em
// qualquer idioma falha o build.
func TestParidadeCatalogosServidor(t *testing.T) {
	catalogos := map[string]map[string]string{}
	for _, lang := range Suportados {
		c := Catalogo(lang)
		if len(c) == 0 {
			t.Fatalf("catálogo %s vazio ou ausente", lang)
		}
		catalogos[lang] = c
	}
	verificarParidade(t, "servidor", catalogos)
}

// TestParidadeCatalogosWeb faz a mesma verificação nos catálogos do frontend
// (web/locales), que são consumidos pelo web/js/i18n.js.
func TestParidadeCatalogosWeb(t *testing.T) {
	verificarParidade(t, "web", carregarCatalogosWeb(t))
}

// TestPlaceholdersConsistentes garante que cada tradução usa exatamente os
// mesmos placeholders {nome} do texto no idioma padrão — um {erro} esquecido
// numa tradução viraria texto quebrado em produção.
func TestPlaceholdersConsistentes(t *testing.T) {
	re := regexp.MustCompile(`\{[a-z_]+\}`)

	verificar := func(origem string, catalogos map[string]map[string]string) {
		base := catalogos[Padrao]
		for chave, texto := range base {
			esperados := append([]string{}, re.FindAllString(texto, -1)...)
			sort.Strings(esperados)
			for _, lang := range Suportados {
				if lang == Padrao {
					continue
				}
				achados := append([]string{}, re.FindAllString(catalogos[lang][chave], -1)...)
				sort.Strings(achados)
				if len(achados) != len(esperados) {
					t.Errorf("%s: %s/%s: placeholders divergem do %s (%v ≠ %v)",
						origem, lang, chave, Padrao, achados, esperados)
					continue
				}
				for i := range esperados {
					if achados[i] != esperados[i] {
						t.Errorf("%s: %s/%s: placeholders divergem do %s (%v ≠ %v)",
							origem, lang, chave, Padrao, achados, esperados)
						break
					}
				}
			}
		}
	}

	servidor := map[string]map[string]string{}
	for _, lang := range Suportados {
		servidor[lang] = Catalogo(lang)
	}
	verificar("servidor", servidor)
	verificar("web", carregarCatalogosWeb(t))
}

// TestChavesDaUIExistem varre o frontend atrás de chaves referenciadas —
// data-i18n* no index.html e t("...")/tn("...") nos módulos JS — e exige que
// todas existam no catálogo web. Uma chave com typo apareceria crua na tela.
func TestChavesDaUIExistem(t *testing.T) {
	base := carregarCatalogosWeb(t)[Padrao]
	raizWeb := filepath.Join("..", "..", "web")

	usadas := map[string]string{} // chave → onde foi vista
	coletar := func(arquivo string, re *regexp.Regexp, plural bool) {
		b, err := os.ReadFile(arquivo)
		if err != nil {
			t.Fatalf("ler %s: %v", arquivo, err)
		}
		for _, m := range re.FindAllStringSubmatch(string(b), -1) {
			if plural {
				usadas[m[1]+".one"] = arquivo
				usadas[m[1]+".other"] = arquivo
			} else {
				usadas[m[1]] = arquivo
			}
		}
	}

	coletar(filepath.Join(raizWeb, "index.html"),
		regexp.MustCompile(`data-i18n(?:-placeholder|-title)?="([^"]+)"`), false)
	js, err := filepath.Glob(filepath.Join(raizWeb, "js", "*.js"))
	if err != nil || len(js) == 0 {
		t.Fatalf("listar web/js: %v (%d arquivos)", err, len(js))
	}
	reT := regexp.MustCompile(`\bt\(\s*"([^"]+)"`)
	reTN := regexp.MustCompile(`\btn\(\s*"([^"]+)"`)
	for _, arquivo := range js {
		coletar(arquivo, reT, false)
		coletar(arquivo, reTN, true)
	}

	for chave, onde := range usadas {
		if _, ok := base[chave]; !ok {
			t.Errorf("chave %q usada em %s não existe no catálogo web %s", chave, onde, Padrao)
		}
	}
}

// TestChavesDoServidorExistem varre os fontes Go (internal/, sem testes) atrás
// de chaves de catálogo usadas em erroT/i18n.T/i18n.TI/i18n.TN e exige que
// existam no catálogo do servidor. Linhas de comentário são ignoradas (os docs
// citam chaves de exemplo).
func TestChavesDoServidorExistem(t *testing.T) {
	base := Catalogo(Padrao)
	reChave := regexp.MustCompile(`"((?:erro|evento|chat|notif|consultor)\.[a-z0-9_]+)"`)
	usadas := map[string]string{}

	raiz := filepath.Join("..", "..")
	err := filepath.WalkDir(filepath.Join(raiz, "internal"), func(caminho string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(caminho, ".go") || strings.HasSuffix(caminho, "_test.go") {
			return err
		}
		b, err := os.ReadFile(caminho)
		if err != nil {
			return err
		}
		for _, linha := range strings.Split(string(b), "\n") {
			aparada := strings.TrimSpace(linha)
			if strings.HasPrefix(aparada, "//") {
				continue
			}
			if !strings.Contains(linha, "erroT(") && !strings.Contains(linha, "i18n.T") &&
				!strings.Contains(linha, "TI(") && !strings.Contains(linha, `"erro.`) &&
				!strings.Contains(linha, `"evento.`) && !strings.Contains(linha, `"chat.`) {
				continue
			}
			plural := strings.Contains(linha, "TN(")
			for _, m := range reChave.FindAllStringSubmatch(linha, -1) {
				if plural {
					usadas[m[1]+".one"] = caminho
					usadas[m[1]+".other"] = caminho
				} else {
					usadas[m[1]] = caminho
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("varrer fontes: %v", err)
	}

	for chave, onde := range usadas {
		if _, ok := base[chave]; !ok {
			t.Errorf("chave %q usada em %s não existe no catálogo do servidor %s", chave, onde, Padrao)
		}
	}
}

// TestNormalizar cobre o mapeamento de tags frouxas para os idiomas suportados.
func TestNormalizar(t *testing.T) {
	casos := map[string]string{
		"pt": "pt-BR", "pt-BR": "pt-BR", "pt_br": "pt-BR", "PT-PT": "pt-BR",
		"en": "en", "en-US": "en",
		"es": "es", "es-MX": "es",
		"zh": "zh-CN", "zh-Hans": "zh-CN", "zh_CN": "zh-CN",
		"fr": "", "": "", "de-DE": "",
	}
	for entrada, esperado := range casos {
		if got := Normalizar(entrada); got != esperado {
			t.Errorf("Normalizar(%q) = %q, esperado %q", entrada, got, esperado)
		}
	}
}

// TestDoAcceptLanguage cobre a escolha por preferência (fator q).
func TestDoAcceptLanguage(t *testing.T) {
	casos := map[string]string{
		"pt-BR,pt;q=0.9,en;q=0.8": "pt-BR",
		"en;q=0.8,zh-CN":          "zh-CN",
		"fr-FR,fr;q=0.9":          "",
		"":                        "",
		"es":                      "es",
	}
	for entrada, esperado := range casos {
		if got := DoAcceptLanguage(entrada); got != esperado {
			t.Errorf("DoAcceptLanguage(%q) = %q, esperado %q", entrada, got, esperado)
		}
	}
}

// TestTInterpolacaoEFallback cobre a interpolação e o fallback de chave ausente.
func TestTInterpolacaoEFallback(t *testing.T) {
	if got := T("en", "erro.corpo_invalido", "detalhe", "x"); got != "invalid JSON body: x" {
		t.Errorf("interpolação: %q", got)
	}
	if got := T("fr", "erro.interno"); got != T(Padrao, "erro.interno") {
		t.Errorf("idioma desconhecido deveria cair no padrão; veio %q", got)
	}
	if got := T("en", "chave.inexistente"); got != "chave.inexistente" {
		t.Errorf("chave ausente deveria voltar a própria chave; veio %q", got)
	}
}

// verificarParidade compara o conjunto de chaves de cada idioma com o padrão.
func verificarParidade(t *testing.T, origem string, catalogos map[string]map[string]string) {
	t.Helper()
	base := catalogos[Padrao]
	for _, lang := range Suportados {
		if lang == Padrao {
			continue
		}
		c := catalogos[lang]
		for chave := range base {
			if _, ok := c[chave]; !ok {
				t.Errorf("%s: catálogo %s: falta a chave %q (presente em %s)", origem, lang, chave, Padrao)
			}
		}
		for chave := range c {
			if _, ok := base[chave]; !ok {
				t.Errorf("%s: catálogo %s: chave %q sobrando (ausente em %s)", origem, lang, chave, Padrao)
			}
		}
	}
}

// carregarCatalogosWeb lê web/locales/<lang>.json relativo à raiz do repositório
// (dois níveis acima deste pacote).
func carregarCatalogosWeb(t *testing.T) map[string]map[string]string {
	t.Helper()
	catalogos := map[string]map[string]string{}
	for _, lang := range Suportados {
		caminho := filepath.Join("..", "..", "web", "locales", lang+".json")
		b, err := os.ReadFile(caminho)
		if err != nil {
			t.Fatalf("ler catálogo web %s: %v", lang, err)
		}
		m := map[string]string{}
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatalf("catálogo web %s inválido: %v", lang, err)
		}
		catalogos[lang] = m
	}
	return catalogos
}
