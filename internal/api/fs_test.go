package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/marcos14/praxis-autonomous/internal/db"
)

func decodDirs(t *testing.T, rec *httptest.ResponseRecorder) respDirsFS {
	t.Helper()
	var d respDirsFS
	if err := json.Unmarshal(rec.Body.Bytes(), &d); err != nil {
		t.Fatalf("decodificar listagem: %v (corpo=%q)", err, rec.Body.String())
	}
	return d
}

// arvoreTeste monta uma pasta com duas subpastas (uma repo git), um arquivo
// solto e devolve a raiz.
func arvoreTeste(t *testing.T) string {
	t.Helper()
	raiz := t.TempDir()
	for _, nome := range []string{"beta", "Alfa"} {
		if err := os.Mkdir(filepath.Join(raiz, nome), 0o755); err != nil {
			t.Fatalf("criar %s: %v", nome, err)
		}
	}
	if err := os.Mkdir(filepath.Join(raiz, "Alfa", ".git"), 0o755); err != nil {
		t.Fatalf("criar .git: %v", err)
	}
	if err := os.WriteFile(filepath.Join(raiz, "leiame.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("criar arquivo: %v", err)
	}
	return raiz
}

func TestListarPastasOK(t *testing.T) {
	srv := Novo(Opcoes{Banco: abrirBancoTemp(t)})
	raiz := arvoreTeste(t)

	rec := fazerReq(t, srv, http.MethodGet, "/api/v1/fs/dirs?path="+url.QueryEscape(raiz), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, quero 200 (corpo=%q)", rec.Code, rec.Body.String())
	}
	d := decodDirs(t, rec)
	if d.Caminho != raiz {
		t.Fatalf("caminho = %q, quero %q", d.Caminho, raiz)
	}
	if d.Pai != filepath.Dir(raiz) {
		t.Fatalf("pai = %q, quero %q", d.Pai, filepath.Dir(raiz))
	}
	if len(d.Pastas) != 2 {
		t.Fatalf("pastas = %+v, quero só as duas subpastas (o arquivo solto fica de fora)", d.Pastas)
	}
	// Ordem alfabética sem diferenciar caixa: Alfa antes de beta.
	if d.Pastas[0].Nome != "Alfa" || d.Pastas[1].Nome != "beta" {
		t.Fatalf("ordem = %q, %q; quero Alfa, beta", d.Pastas[0].Nome, d.Pastas[1].Nome)
	}
	if !d.Pastas[0].RepoGit {
		t.Error("Alfa tem .git: deveria vir marcada como repositório")
	}
	if d.Pastas[1].RepoGit {
		t.Error("beta não tem .git: não deveria vir marcada como repositório")
	}
	if !d.Pastas[0].Acessivel || !d.Pastas[1].Acessivel {
		t.Error("as subpastas do teste deveriam ser acessíveis")
	}
	if d.RepoGit {
		t.Error("a raiz do teste não tem .git")
	}
	if len(d.Raizes) == 0 {
		t.Error("raizes vazio: a UI precisa de ao menos uma raiz para navegar")
	}
	if d.Separador != string(filepath.Separator) {
		t.Errorf("separador = %q, quero %q", d.Separador, string(filepath.Separator))
	}
	if d.Truncado {
		t.Error("listagem pequena não deveria vir truncada")
	}
}

// Pasta que É um repositório: a resposta marca repo_git na própria pasta aberta
// (a UI usa isso para avisar que o caminho serve para o cadastro).
func TestListarPastasMarcaRepoDaPastaAberta(t *testing.T) {
	srv := Novo(Opcoes{Banco: abrirBancoTemp(t)})
	repo := repoGitTemp(t)

	rec := fazerReq(t, srv, http.MethodGet, "/api/v1/fs/dirs?path="+url.QueryEscape(repo), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, quero 200 (corpo=%q)", rec.Code, rec.Body.String())
	}
	if d := decodDirs(t, rec); !d.RepoGit {
		t.Error("repo_git = false numa pasta com git init")
	}
}

// Sem ?path a rota começa por uma pasta útil (home do processo) em vez de falhar.
func TestListarPastasSemPathUsaInicial(t *testing.T) {
	srv := Novo(Opcoes{Banco: abrirBancoTemp(t)})

	rec := fazerReq(t, srv, http.MethodGet, "/api/v1/fs/dirs", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, quero 200 (corpo=%q)", rec.Code, rec.Body.String())
	}
	if d := decodDirs(t, rec); d.Caminho == "" {
		t.Error("caminho vazio: a listagem inicial precisa dizer onde está")
	}
}

func TestListarPastasInexistente(t *testing.T) {
	srv := Novo(Opcoes{Banco: abrirBancoTemp(t)})
	alvo := filepath.Join(t.TempDir(), "nao", "existe")

	rec := fazerReq(t, srv, http.MethodGet, "/api/v1/fs/dirs?path="+url.QueryEscape(alvo), nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, quero 404 (corpo=%q)", rec.Code, rec.Body.String())
	}
	if e := decodErro(t, rec); e.Erro.Codigo != "nao_encontrado" {
		t.Fatalf("codigo = %q, quero nao_encontrado", e.Erro.Codigo)
	}
}

func TestListarPastasCaminhoDeArquivo(t *testing.T) {
	srv := Novo(Opcoes{Banco: abrirBancoTemp(t)})
	arquivo := filepath.Join(arvoreTeste(t), "leiame.txt")

	rec := fazerReq(t, srv, http.MethodGet, "/api/v1/fs/dirs?path="+url.QueryEscape(arquivo), nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, quero 400 (corpo=%q)", rec.Code, rec.Body.String())
	}
	if e := decodErro(t, rec); e.Erro.Codigo != "invalido" {
		t.Fatalf("codigo = %q, quero invalido", e.Erro.Codigo)
	}
}

// A listagem do disco do servidor não é leitura pública da API: exige
// projetos.gerir, como o cadastro de projeto a que ela serve.
func TestListarPastasExigeProjetosGerir(t *testing.T) {
	publica, perm := requisitoRota(http.MethodGet, "/api/v1/fs/dirs")
	if publica {
		t.Fatal("a rota não pode ser pública")
	}
	if perm != db.PermProjetosGerir {
		t.Fatalf("permissão = %q, quero %q", perm, db.PermProjetosGerir)
	}
}
