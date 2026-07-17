package api

import (
	"embed"
	"io/fs"
	"net/http"
	"sort"
	"strings"
)

// manualFS embute as seções do manual (markdown). Os arquivos são nomeados
// NN-slug.md; o prefixo NN dá a ordem e o slug é o identificador da rota. A
// primeira linha "# Título" de cada arquivo vira o título da seção.
//
//go:embed manual/*.md
var manualFS embed.FS

// SecaoManual é uma seção do manual. Conteudo é o markdown (sem a linha de
// título). Na listagem, Conteudo vem vazio (só slug/titulo).
type SecaoManual struct {
	Slug     string `json:"slug"`
	Titulo   string `json:"titulo"`
	Conteudo string `json:"conteudo,omitempty"`
}

// manualSecoes lê e ordena as seções embutidas uma vez (no primeiro uso).
var manualCache []SecaoManual

// carregarManual lê as seções do embed, ordenadas por nome de arquivo (o prefixo
// NN garante a ordem). Faz o parse do título (primeira linha "# ...").
func carregarManual() []SecaoManual {
	if manualCache != nil {
		return manualCache
	}
	entradas, err := fs.ReadDir(manualFS, "manual")
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
		b, err := manualFS.ReadFile("manual/" + nome)
		if err != nil {
			continue
		}
		conteudo := string(b)
		titulo, corpo := separarTitulo(conteudo)
		slug := strings.TrimSuffix(nome, ".md")
		if i := strings.IndexByte(slug, '-'); i >= 0 {
			slug = slug[i+1:] // remove o prefixo NN- da ordenação
		}
		secoes = append(secoes, SecaoManual{Slug: slug, Titulo: titulo, Conteudo: corpo})
	}
	manualCache = secoes
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
	secoes := carregarManual()
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
	for _, sec := range carregarManual() {
		if sec.Slug == slug {
			responderJSON(w, http.StatusOK, sec)
			return
		}
	}
	responderErro(w, http.StatusNotFound, "nao_encontrado", "seção do manual não encontrada")
}
