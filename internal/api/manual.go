package api

import (
	"embed"
	"io/fs"
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/marcos14/praxis-autonomous/internal/i18n"
)

// manualFS embute as seções do manual (markdown), uma pasta por idioma
// (manual/<idioma>/NN-slug.md). Os arquivos são nomeados NN-slug.md; o prefixo
// NN dá a ordem e o slug é o identificador da rota. A primeira linha "# Título"
// de cada arquivo vira o título da seção.
//
// pt-BR é a FONTE DA VERDADE: a lista de seções (slugs e ordem) sai dela, e uma
// página ainda não traduzida cai no texto em pt-BR — é melhor ler a seção certa
// no idioma errado do que receber 404.
//
//go:embed manual/*/*.md
var manualFS embed.FS

// SecaoManual é uma seção do manual. Conteudo é o markdown (sem a linha de
// título). Na listagem, Conteudo vem vazio (só slug/titulo).
type SecaoManual struct {
	Slug     string `json:"slug"`
	Titulo   string `json:"titulo"`
	Conteudo string `json:"conteudo,omitempty"`
}

// manualCache guarda as seções já montadas por idioma (o embed é imutável, então
// o parse acontece uma vez por idioma).
var (
	manualMu    sync.Mutex
	manualCache = map[string][]SecaoManual{}
)

// carregarManual devolve as seções do manual no idioma pedido, na ordem de
// leitura. A ordem e o conjunto de slugs vêm SEMPRE de pt-BR (a fonte); para
// cada seção, usa-se a tradução quando o arquivo existe no idioma e o texto em
// pt-BR caso contrário — assim um idioma parcialmente traduzido continua com o
// manual inteiro navegável.
func carregarManual(lang string) []SecaoManual {
	lang = i18n.Normalizar(lang)
	if lang == "" {
		lang = i18n.Padrao
	}
	manualMu.Lock()
	defer manualMu.Unlock()
	if secoes, ok := manualCache[lang]; ok {
		return secoes
	}

	entradas, err := fs.ReadDir(manualFS, "manual/"+i18n.Padrao)
	if err != nil {
		return nil
	}
	nomes := make([]string, 0, len(entradas))
	for _, e := range entradas {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
			nomes = append(nomes, e.Name())
		}
	}
	sort.Strings(nomes)

	secoes := make([]SecaoManual, 0, len(nomes))
	for _, nome := range nomes {
		b, err := manualFS.ReadFile("manual/" + lang + "/" + nome)
		if err != nil {
			// Sem tradução desta página: cai na fonte pt-BR.
			if b, err = manualFS.ReadFile("manual/" + i18n.Padrao + "/" + nome); err != nil {
				continue
			}
		}
		titulo, corpo := separarTitulo(string(b))
		slug := strings.TrimSuffix(nome, ".md")
		if i := strings.IndexByte(slug, '-'); i >= 0 {
			slug = slug[i+1:] // remove o prefixo NN- da ordenação
		}
		secoes = append(secoes, SecaoManual{Slug: slug, Titulo: titulo, Conteudo: corpo})
	}
	manualCache[lang] = secoes
	return secoes
}

// separarTitulo extrai o título (primeira linha "# ...") e devolve o restante do
// conteúdo. Sem linha de título, o título fica vazio e o conteúdo é o texto todo.
func separarTitulo(md string) (titulo, corpo string) {
	linhas := strings.SplitN(md, "\n", 2)
	primeira := strings.TrimSpace(linhas[0])
	if strings.HasPrefix(primeira, "# ") {
		titulo = strings.TrimSpace(strings.TrimPrefix(primeira, "# "))
		if len(linhas) == 2 {
			corpo = strings.TrimLeft(linhas[1], "\n")
		}
		return titulo, corpo
	}
	return "", md
}

// registrarRotasManual registra as rotas do manual embutido (Fase 5b).
func (s *Servidor) registrarRotasManual(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/manual", s.handleListarManual)
	mux.HandleFunc("GET /api/v1/manual/{slug}", s.handleSecaoManual)
}

// handleListarManual devolve as seções do manual (slug + título, sem conteúdo),
// na ordem de leitura.
func (s *Servidor) handleListarManual(w http.ResponseWriter, r *http.Request) {
	secoes := carregarManual(idiomaDaRequisicao(r))
	lista := make([]SecaoManual, len(secoes))
	for i, sec := range secoes {
		lista[i] = SecaoManual{Slug: sec.Slug, Titulo: sec.Titulo}
	}
	responderJSON(w, http.StatusOK, lista)
}

// handleSecaoManual devolve uma seção do manual (com o markdown). Slug
// desconhecido → 404.
func (s *Servidor) handleSecaoManual(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	for _, sec := range carregarManual(idiomaDaRequisicao(r)) {
		if sec.Slug == slug {
			responderJSON(w, http.StatusOK, sec)
			return
		}
	}
	erroT(w, r, http.StatusNotFound, "nao_encontrado", "erro.manual_secao_nao_encontrada")
}
