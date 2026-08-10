package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"sync"
	"testing"

	"github.com/marcos14/praxis-autonomous/internal/db"
)

// repoGitTemp cria um repositório git isolado num diretório temporário e devolve
// seu caminho. Mesma abordagem dos testes do Praxis atual.
func repoGitTemp(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	cmd := exec.Command("git", "-C", dir, "init", "-q")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v — %s", err, out)
	}
	return dir
}

// adminTokensTeste memoiza, por servidor, o JWT do admin criado por
// tokenAdminTeste — o setup roda uma vez por servidor de teste.
var adminTokensTeste sync.Map // *Servidor → string

// Credenciais do admin de teste compartilhado (criado no primeiro fazerReq de
// cada servidor com banco). Exportadas dentro do pacote para os testes que
// precisam relogar ou comparar listas de usuários.
const (
	adminTesteEmail = "admin@praxis.test"
	adminTesteSenha = "senha-de-teste-123"
	adminTesteNome  = "Admin de Teste"
)

// tokenAdminTeste garante um admin no banco do servidor e devolve o JWT dele.
// Desde a Fase A o modo bootstrap não dá mais acesso à API (só às rotas de
// auth), então os testes autenticam como este admin — o comportamento que uma
// instalação real tem depois do /auth/setup. Servidor sem banco devolve ""
// (o seam de handler isolado continua com acesso pleno).
func tokenAdminTeste(t *testing.T, srv *Servidor) string {
	t.Helper()
	if srv.banco == nil {
		return ""
	}
	if tok, ok := adminTokensTeste.Load(srv); ok {
		return tok.(string)
	}
	corpo, _ := json.Marshal(map[string]string{
		"nome": adminTesteNome, "email": adminTesteEmail, "senha": adminTesteSenha,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/setup", bytes.NewReader(corpo))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code == http.StatusConflict {
		// Outro caminho do teste já criou o 1º usuário: loga com as credenciais
		// padrão (o teste que criou o SEU próprio admin usa o token dele).
		req = httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(corpo))
		rec = httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
	}
	var resp struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil || resp.Token == "" {
		t.Fatalf("criar admin de teste: status=%d corpo=%q", rec.Code, rec.Body.String())
	}
	adminTokensTeste.Store(srv, resp.Token)
	return resp.Token
}

// fazerReq executa uma requisição AUTENTICADA (admin de teste) no servidor e
// devolve o recorder. Para exercitar requisições anônimas ou com outra
// credencial, use fazerReqAnonima/fazerReqToken.
func fazerReq(t *testing.T, srv *Servidor, metodo, caminho string, corpo any) *httptest.ResponseRecorder {
	t.Helper()
	token := tokenAdminTeste(t, srv)
	req := httptest.NewRequest(metodo, caminho, corpoJSON(t, corpo))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

// fazerReqAnonima executa uma requisição SEM credencial — para rotas públicas
// (auth) e para asserções de 401.
func fazerReqAnonima(t *testing.T, srv *Servidor, metodo, caminho string, corpo any) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(metodo, caminho, corpoJSON(t, corpo))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

// corpoJSON serializa o corpo (nil → vazio) para as requisições de teste.
func corpoJSON(t *testing.T, corpo any) *bytes.Buffer {
	t.Helper()
	if corpo == nil {
		return bytes.NewBuffer(nil)
	}
	b, err := json.Marshal(corpo)
	if err != nil {
		t.Fatalf("marshal corpo: %v", err)
	}
	return bytes.NewBuffer(b)
}

func decodProjeto(t *testing.T, rec *httptest.ResponseRecorder) db.Projeto {
	t.Helper()
	var p db.Projeto
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatalf("decodificar projeto: %v (corpo=%q)", err, rec.Body.String())
	}
	return p
}

func decodErro(t *testing.T, rec *httptest.ResponseRecorder) ErroResp {
	t.Helper()
	var e ErroResp
	if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil {
		t.Fatalf("decodificar erro: %v (corpo=%q)", err, rec.Body.String())
	}
	return e
}

func TestCriarProjetoOK(t *testing.T) {
	srv := Novo(Opcoes{Banco: abrirBancoTemp(t)})
	repo := repoGitTemp(t)

	rec := fazerReq(t, srv, http.MethodPost, "/api/v1/projects", map[string]any{
		"nome":            "Meu Projeto",
		"pasta":           repo,
		"modo_integracao": "merge_local",
		"add_dirs":        []string{"../lib"},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, quero 201 (corpo=%q)", rec.Code, rec.Body.String())
	}
	p := decodProjeto(t, rec)
	if p.ID == 0 {
		t.Fatal("ID não preenchido")
	}
	if p.Slug != "meu-projeto" {
		t.Fatalf("slug = %q, quero meu-projeto (derivado do nome)", p.Slug)
	}
	if p.BranchPrincipal != "main" {
		t.Fatalf("branch_principal = %q, quero main (default)", p.BranchPrincipal)
	}
	if p.ModoIntegracao != "merge_local" {
		t.Fatalf("modo_integracao = %q, quero merge_local", p.ModoIntegracao)
	}
	if !p.Ativo {
		t.Fatal("Ativo deveria ser true por default")
	}
}

func TestCriarProjetoSlugExplicito(t *testing.T) {
	srv := Novo(Opcoes{Banco: abrirBancoTemp(t)})
	repo := repoGitTemp(t)

	rec := fazerReq(t, srv, http.MethodPost, "/api/v1/projects", map[string]any{
		"nome": "Qualquer", "slug": "custom-slug", "pasta": repo,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, quero 201", rec.Code)
	}
	if p := decodProjeto(t, rec); p.Slug != "custom-slug" {
		t.Fatalf("slug = %q, quero custom-slug", p.Slug)
	}
}

func TestCriarProjetoPastaInexistente(t *testing.T) {
	srv := Novo(Opcoes{Banco: abrirBancoTemp(t)})

	rec := fazerReq(t, srv, http.MethodPost, "/api/v1/projects", map[string]any{
		"nome": "X", "pasta": `C:\nao\existe\mesmo\praxis-xyz`,
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, quero 400", rec.Code)
	}
	if e := decodErro(t, rec); e.Erro.Codigo != "invalido" {
		t.Fatalf("codigo = %q, quero invalido", e.Erro.Codigo)
	}
}

func TestCriarProjetoPastaNaoRepoGit(t *testing.T) {
	srv := Novo(Opcoes{Banco: abrirBancoTemp(t)})
	// t.TempDir existe mas não é repo git.
	dir := t.TempDir()

	rec := fazerReq(t, srv, http.MethodPost, "/api/v1/projects", map[string]any{
		"nome": "X", "pasta": dir,
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, quero 400 (corpo=%q)", rec.Code, rec.Body.String())
	}
}

func TestCriarProjetoModoInvalido(t *testing.T) {
	srv := Novo(Opcoes{Banco: abrirBancoTemp(t)})
	repo := repoGitTemp(t)

	rec := fazerReq(t, srv, http.MethodPost, "/api/v1/projects", map[string]any{
		"nome": "X", "pasta": repo, "modo_integracao": "coisa",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, quero 400", rec.Code)
	}
}

func TestCriarProjetoNomeObrigatorio(t *testing.T) {
	srv := Novo(Opcoes{Banco: abrirBancoTemp(t)})
	repo := repoGitTemp(t)

	rec := fazerReq(t, srv, http.MethodPost, "/api/v1/projects", map[string]any{
		"nome": "   ", "pasta": repo,
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, quero 400", rec.Code)
	}
}

func TestCriarProjetoSlugDuplicado(t *testing.T) {
	srv := Novo(Opcoes{Banco: abrirBancoTemp(t)})
	repo := repoGitTemp(t)

	corpo := map[string]any{"nome": "Dup", "slug": "dup", "pasta": repo}
	if rec := fazerReq(t, srv, http.MethodPost, "/api/v1/projects", corpo); rec.Code != http.StatusCreated {
		t.Fatalf("1ª criação status = %d, quero 201", rec.Code)
	}
	rec := fazerReq(t, srv, http.MethodPost, "/api/v1/projects", corpo)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, quero 409", rec.Code)
	}
	if e := decodErro(t, rec); e.Erro.Codigo != "slug_duplicado" {
		t.Fatalf("codigo = %q, quero slug_duplicado", e.Erro.Codigo)
	}
}

func TestCriarProjetoCorpoInvalido(t *testing.T) {
	srv := Novo(Opcoes{Banco: abrirBancoTemp(t)})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/projects", bytes.NewBufferString("{nao json"))
	req.Header.Set("Authorization", "Bearer "+tokenAdminTeste(t, srv))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, quero 400", rec.Code)
	}
}

func TestCriarProjetoCampoDesconhecido(t *testing.T) {
	srv := Novo(Opcoes{Banco: abrirBancoTemp(t)})
	repo := repoGitTemp(t)

	rec := fazerReq(t, srv, http.MethodPost, "/api/v1/projects", map[string]any{
		"nome": "X", "pasta": repo, "campo_estranho": 1,
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, quero 400", rec.Code)
	}
}

func TestListarProjetos(t *testing.T) {
	srv := Novo(Opcoes{Banco: abrirBancoTemp(t)})
	repo := repoGitTemp(t)

	for _, nome := range []string{"Beta", "Alfa"} {
		rec := fazerReq(t, srv, http.MethodPost, "/api/v1/projects", map[string]any{
			"nome": nome, "pasta": repo,
		})
		if rec.Code != http.StatusCreated {
			t.Fatalf("criar %s: status %d", nome, rec.Code)
		}
	}
	rec := fazerReq(t, srv, http.MethodGet, "/api/v1/projects", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, quero 200", rec.Code)
	}
	var lista []db.Projeto
	if err := json.Unmarshal(rec.Body.Bytes(), &lista); err != nil {
		t.Fatalf("decodificar lista: %v", err)
	}
	if len(lista) != 2 || lista[0].Nome != "Alfa" {
		t.Fatalf("lista = %+v, quero [Alfa, Beta]", lista)
	}
}

func TestObterProjeto(t *testing.T) {
	srv := Novo(Opcoes{Banco: abrirBancoTemp(t)})
	repo := repoGitTemp(t)

	criado := decodProjeto(t, fazerReq(t, srv, http.MethodPost, "/api/v1/projects", map[string]any{
		"nome": "X", "pasta": repo,
	}))

	rec := fazerReq(t, srv, http.MethodGet, "/api/v1/projects/"+itoa(criado.ID), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, quero 200", rec.Code)
	}
	if p := decodProjeto(t, rec); p.ID != criado.ID {
		t.Fatalf("id = %d, quero %d", p.ID, criado.ID)
	}
}

func TestObterProjetoInexistente(t *testing.T) {
	srv := Novo(Opcoes{Banco: abrirBancoTemp(t)})
	rec := fazerReq(t, srv, http.MethodGet, "/api/v1/projects/999", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, quero 404", rec.Code)
	}
	if e := decodErro(t, rec); e.Erro.Codigo != "nao_encontrado" {
		t.Fatalf("codigo = %q, quero nao_encontrado", e.Erro.Codigo)
	}
}

func TestObterProjetoIDInvalido(t *testing.T) {
	srv := Novo(Opcoes{Banco: abrirBancoTemp(t)})
	rec := fazerReq(t, srv, http.MethodGet, "/api/v1/projects/abc", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, quero 400", rec.Code)
	}
}

func TestAtualizarProjeto(t *testing.T) {
	srv := Novo(Opcoes{Banco: abrirBancoTemp(t)})
	repo := repoGitTemp(t)

	criado := decodProjeto(t, fazerReq(t, srv, http.MethodPost, "/api/v1/projects", map[string]any{
		"nome": "Antigo", "pasta": repo,
	}))

	rec := fazerReq(t, srv, http.MethodPut, "/api/v1/projects/"+itoa(criado.ID), map[string]any{
		"nome": "Novo Nome", "pasta": repo, "modo_integracao": "merge_local", "ativo": false,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, quero 200 (corpo=%q)", rec.Code, rec.Body.String())
	}
	p := decodProjeto(t, rec)
	if p.Nome != "Novo Nome" || p.ModoIntegracao != "merge_local" || p.Ativo {
		t.Fatalf("atualização não refletiu: %+v", p)
	}
	if p.ID != criado.ID {
		t.Fatalf("id mudou: %d -> %d", criado.ID, p.ID)
	}
}

func TestAtualizarProjetoPreservaAtivoQuandoOmitido(t *testing.T) {
	srv := Novo(Opcoes{Banco: abrirBancoTemp(t)})
	repo := repoGitTemp(t)

	// Cria já inativo.
	criado := decodProjeto(t, fazerReq(t, srv, http.MethodPost, "/api/v1/projects", map[string]any{
		"nome": "X", "pasta": repo, "ativo": false,
	}))
	if criado.Ativo {
		t.Fatal("deveria ter sido criado inativo")
	}
	// Atualiza sem enviar "ativo": deve permanecer false.
	rec := fazerReq(t, srv, http.MethodPut, "/api/v1/projects/"+itoa(criado.ID), map[string]any{
		"nome": "X2", "pasta": repo,
	})
	if p := decodProjeto(t, rec); p.Ativo {
		t.Fatalf("Ativo = true, quero preservar false quando omitido")
	}
}

func TestAtualizarProjetoPreservaModoQuandoOmitido(t *testing.T) {
	srv := Novo(Opcoes{Banco: abrirBancoTemp(t)})
	repo := repoGitTemp(t)

	criado := decodProjeto(t, fazerReq(t, srv, http.MethodPost, "/api/v1/projects", map[string]any{
		"nome": "X", "pasta": repo, "modo_integracao": "merge_local", "branch_principal": "develop",
	}))
	// Atualiza sem enviar modo_integracao/branch_principal: devem ser preservados.
	rec := fazerReq(t, srv, http.MethodPut, "/api/v1/projects/"+itoa(criado.ID), map[string]any{
		"nome": "X2", "pasta": repo,
	})
	p := decodProjeto(t, rec)
	if p.ModoIntegracao != "merge_local" {
		t.Fatalf("modo_integracao = %q, quero preservar merge_local", p.ModoIntegracao)
	}
	if p.BranchPrincipal != "develop" {
		t.Fatalf("branch_principal = %q, quero preservar develop", p.BranchPrincipal)
	}
}

func TestAtualizarProjetoInexistente(t *testing.T) {
	srv := Novo(Opcoes{Banco: abrirBancoTemp(t)})
	repo := repoGitTemp(t)
	rec := fazerReq(t, srv, http.MethodPut, "/api/v1/projects/999", map[string]any{
		"nome": "X", "pasta": repo,
	})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, quero 404", rec.Code)
	}
}

// itoa evita importar strconv só para os testes.
func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
