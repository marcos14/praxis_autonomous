package api

// Navegação de pastas do SISTEMA DE ARQUIVOS DO SERVIDOR (GET /api/v1/fs/dirs).
// Existe para o cadastro de projeto: `pasta` é um caminho no SO onde o Praxis
// roda — não na máquina de quem usa o navegador —, então o seletor de arquivo
// nativo do HTML não serve (ele devolve o arquivo, nunca o caminho de origem).
// Esta rota deixa a UI percorrer as pastas do servidor e marcar quais já são
// repositórios git. Exige projetos.gerir (ver requisitoRota): listar o disco do
// servidor é informação sensível e vai junto de quem cadastra projetos.

import (
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// limiteEntradasFS limita quantas subpastas uma listagem devolve. Pastas com
// dezenas de milhares de filhos (node_modules, caches) não travam a UI; o
// truncado avisa a tela para o usuário digitar o caminho.
const limiteEntradasFS = 1000

// respPastaFS é uma subpasta na listagem.
type respPastaFS struct {
	Nome      string `json:"nome"`
	Caminho   string `json:"caminho"`
	RepoGit   bool   `json:"repo_git"`  // tem .git (arquivo ou pasta) — candidato a projeto
	Acessivel bool   `json:"acessivel"` // false = existe mas não deu para abrir (permissão)
}

// respDirsFS é o corpo de GET /fs/dirs.
type respDirsFS struct {
	Caminho   string        `json:"caminho"`   // pasta listada, absoluta e normalizada
	Pai       string        `json:"pai"`       // pasta acima ("" quando já é raiz)
	Separador string        `json:"separador"` // separador do SO, para a UI montar caminhos
	RepoGit   bool          `json:"repo_git"`  // a própria pasta listada é um repositório git
	Raizes    []string      `json:"raizes"`    // unidades (Windows) ou "/" (Unix)
	Pastas    []respPastaFS `json:"pastas"`
	Truncado  bool          `json:"truncado"` // havia mais que limiteEntradasFS subpastas
}

// registrarRotasFS registra a navegação de pastas do servidor.
func (s *Servidor) registrarRotasFS(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/fs/dirs", s.handleListarPastas)
}

// handleListarPastas devolve as subpastas de ?path. Sem path (ou com path em
// branco) começa pela pasta do usuário do processo — ponto de partida útil sem
// expor uma varredura do disco inteiro.
func (s *Servidor) handleListarPastas(w http.ResponseWriter, r *http.Request) {
	pedido := strings.TrimSpace(r.URL.Query().Get("path"))
	if pedido == "" {
		pedido = pastaInicialFS()
	}
	dir, err := filepath.Abs(filepath.Clean(expandirTil(pedido)))
	if err != nil {
		erroT(w, r, http.StatusBadRequest, "invalido", "erro.fs_caminho_invalido", "caminho", pedido)
		return
	}
	info, err := os.Stat(dir)
	switch {
	case os.IsNotExist(err):
		erroT(w, r, http.StatusNotFound, "nao_encontrado", "erro.fs_pasta_inexistente", "caminho", dir)
		return
	case os.IsPermission(err):
		erroT(w, r, http.StatusForbidden, "sem_permissao", "erro.fs_pasta_sem_acesso", "caminho", dir)
		return
	case err != nil:
		erroT(w, r, http.StatusBadRequest, "invalido", "erro.fs_pasta_invalida", "detalhe", err.Error())
		return
	case !info.IsDir():
		erroT(w, r, http.StatusBadRequest, "invalido", "erro.fs_nao_e_pasta", "caminho", dir)
		return
	}

	entradas, err := os.ReadDir(dir)
	if err != nil {
		if os.IsPermission(err) {
			erroT(w, r, http.StatusForbidden, "sem_permissao", "erro.fs_pasta_sem_acesso", "caminho", dir)
			return
		}
		erroT(w, r, http.StatusBadRequest, "invalido", "erro.fs_pasta_invalida", "detalhe", err.Error())
		return
	}

	resp := respDirsFS{
		Caminho:   dir,
		Pai:       paiFS(dir),
		Separador: string(filepath.Separator),
		RepoGit:   ehRepoGit(dir),
		Raizes:    raizesFS(),
		Pastas:    []respPastaFS{},
	}
	for _, e := range entradas {
		nome := e.Name()
		if !ehPastaFS(dir, e) {
			continue
		}
		if len(resp.Pastas) >= limiteEntradasFS {
			resp.Truncado = true
			break
		}
		filho := filepath.Join(dir, nome)
		resp.Pastas = append(resp.Pastas, respPastaFS{
			Nome:      nome,
			Caminho:   filho,
			RepoGit:   ehRepoGit(filho),
			Acessivel: acessivelFS(filho),
		})
	}
	sort.Slice(resp.Pastas, func(i, j int) bool {
		a, b := strings.ToLower(resp.Pastas[i].Nome), strings.ToLower(resp.Pastas[j].Nome)
		if a == b {
			return resp.Pastas[i].Nome < resp.Pastas[j].Nome
		}
		return a < b
	})
	responderJSON(w, http.StatusOK, resp)
}

// ehPastaFS informa se a entrada é uma pasta, resolvendo links simbólicos (um
// link para pasta é navegável e deve aparecer). Link quebrado fica de fora.
func ehPastaFS(dir string, e os.DirEntry) bool {
	if e.IsDir() {
		return true
	}
	if e.Type()&os.ModeSymlink == 0 {
		return false
	}
	info, err := os.Stat(filepath.Join(dir, e.Name()))
	return err == nil && info.IsDir()
}

// ehRepoGit informa se a pasta tem .git — pasta (repo comum) ou arquivo
// (worktree/submódulo). É a mesma marca que validarPastaRepoGit exige no
// cadastro, mas sem rodar o git: aqui é só uma dica visual na listagem.
func ehRepoGit(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil
}

// acessivelFS informa se dá para abrir a pasta (a UI desabilita as demais em
// vez de deixar o usuário entrar e receber 403). Só checa a abertura, sem ler
// as entradas.
func acessivelFS(dir string) bool {
	f, err := os.Open(dir)
	if err != nil {
		return false
	}
	_ = f.Close()
	return true
}

// paiFS devolve a pasta acima, ou "" quando dir já é a raiz do volume.
func paiFS(dir string) string {
	pai := filepath.Dir(dir)
	if pai == dir {
		return ""
	}
	return pai
}

// pastaInicialFS é o ponto de partida da navegação: a home do usuário do
// processo; sem ela, o diretório de trabalho; em último caso, a primeira raiz.
func pastaInicialFS() string {
	if h, err := os.UserHomeDir(); err == nil && h != "" {
		return h
	}
	if wd, err := os.Getwd(); err == nil && wd != "" {
		return wd
	}
	if raizes := raizesFS(); len(raizes) > 0 {
		return raizes[0]
	}
	return string(filepath.Separator)
}

// raizesFS lista as raízes navegáveis: as unidades existentes no Windows (C: a
// Z: — A:/B: são legado de disquete e sondá-las só gera espera) e "/" no resto.
func raizesFS() []string {
	if runtime.GOOS != "windows" {
		return []string{string(filepath.Separator)}
	}
	raizes := []string{}
	for letra := 'C'; letra <= 'Z'; letra++ {
		raiz := string(letra) + `:\`
		if _, err := os.Stat(raiz); err == nil {
			raizes = append(raizes, raiz)
		}
	}
	return raizes
}

// expandirTil traduz "~" e "~/algo" para a home do usuário do processo — a UI
// não gera esses caminhos, mas quem digita à mão espera que funcionem.
func expandirTil(caminho string) string {
	if caminho != "~" && !strings.HasPrefix(caminho, "~/") && !strings.HasPrefix(caminho, `~\`) {
		return caminho
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return caminho
	}
	return filepath.Join(home, caminho[1:]) // o Join normaliza a barra inicial
}
