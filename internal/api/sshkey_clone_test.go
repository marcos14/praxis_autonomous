package api

// Testes da Fase C: chave SSH por usuário (endpoints /me/ssh-key) e cadastro de
// projeto por clone (POST /projects com url_git + job em background).

import (
	"encoding/json"
	"net/http"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/marcos14/praxis-autonomous/internal/chavessh"
	"github.com/marcos14/praxis-autonomous/internal/db"
)

func temSSHKeygen(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("ssh-keygen não encontrado no PATH")
	}
}

func TestChaveSSHDoUsuario(t *testing.T) {
	temSSHKeygen(t)
	srv := Novo(Opcoes{Banco: abrirBancoTemp(t)})
	ana := criarUsuarioComPapel(t, srv, "ana-ssh@x.com", nil)

	// Sem chave ainda → 404.
	rec := fazerReqToken(t, srv, http.MethodGet, "/api/v1/me/ssh-key", ana, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET sem chave: status %d, quero 404 (corpo=%q)", rec.Code, rec.Body.String())
	}

	// Gera.
	rec = fazerReqToken(t, srv, http.MethodPost, "/api/v1/me/ssh-key", ana, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("gerar: status %d (corpo=%q)", rec.Code, rec.Body.String())
	}
	var chave chavessh.Chave
	_ = json.Unmarshal(rec.Body.Bytes(), &chave)
	if !strings.HasPrefix(chave.Publica, "ssh-ed25519 ") {
		t.Fatalf("pública inesperada: %q", chave.Publica)
	}
	// A privada NUNCA aparece na resposta.
	if strings.Contains(rec.Body.String(), "PRIVATE KEY") {
		t.Fatal("a chave privada vazou na resposta")
	}

	// Segunda geração → 409.
	rec = fazerReqToken(t, srv, http.MethodPost, "/api/v1/me/ssh-key", ana, nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("regenerar: status %d, quero 409", rec.Code)
	}

	// GET devolve a mesma pública.
	rec = fazerReqToken(t, srv, http.MethodGet, "/api/v1/me/ssh-key", ana, nil)
	var lida chavessh.Chave
	_ = json.Unmarshal(rec.Body.Bytes(), &lida)
	if lida.Publica != chave.Publica {
		t.Fatalf("GET devolveu outra pública")
	}
}

// TestCadastroPorClone exercita o fluxo completo com um repositório local
// (file://): job 202 → poll → projeto criado apontando para o clone gerenciado.
func TestCadastroPorClone(t *testing.T) {
	srv := Novo(Opcoes{Banco: abrirBancoTemp(t)})

	origem := repoGitTemp(t)
	// Um commit para o clone ter conteúdo (git clone de repo vazio avisa mas funciona).
	for _, args := range [][]string{
		{"-C", origem, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "--allow-empty", "-m", "init"},
	} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v — %s", args, err, out)
		}
	}

	rec := fazerReq(t, srv, http.MethodPost, "/api/v1/projects", map[string]any{
		"nome": "Clonado", "url_git": "file://" + strings.ReplaceAll(origem, "\\", "/"),
	})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("POST clone: status %d (corpo=%q)", rec.Code, rec.Body.String())
	}
	var resp struct {
		JobID string `json:"job_id"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.JobID == "" {
		t.Fatal("sem job_id na resposta")
	}

	// Poll até concluir (clone local é rápido).
	var job struct {
		Status  string      `json:"status"`
		Detalhe string      `json:"detalhe"`
		Projeto *db.Projeto `json:"projeto"`
	}
	prazo := time.Now().Add(30 * time.Second)
	for {
		rec = fazerReq(t, srv, http.MethodGet, "/api/v1/clones/"+resp.JobID, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET clone: status %d", rec.Code)
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &job)
		if job.Status != "clonando" {
			break
		}
		if time.Now().After(prazo) {
			t.Fatal("clone não terminou no prazo")
		}
		time.Sleep(50 * time.Millisecond)
	}
	if job.Status != "concluido" {
		t.Fatalf("job = %s (%s), quero concluido", job.Status, job.Detalhe)
	}
	if job.Projeto == nil || job.Projeto.ID == 0 {
		t.Fatal("job concluído sem projeto")
	}
	if !strings.Contains(job.Projeto.Pasta, "repos") {
		t.Fatalf("pasta do projeto fora dos clones gerenciados: %q", job.Projeto.Pasta)
	}

	// O projeto existe e é um repo git válido.
	rec = fazerReq(t, srv, http.MethodGet, "/api/v1/projects", nil)
	if !strings.Contains(rec.Body.String(), "Clonado") {
		t.Fatal("projeto clonado não aparece na lista")
	}
}

// TestCloneSSHExigeChave: url SSH sem chave gerada → 412 antes de qualquer clone.
func TestCloneSSHExigeChave(t *testing.T) {
	srv := Novo(Opcoes{Banco: abrirBancoTemp(t)})
	rec := fazerReq(t, srv, http.MethodPost, "/api/v1/projects", map[string]any{
		"nome": "Privado", "url_git": "git@github.com:org/repo.git",
	})
	if rec.Code != http.StatusPreconditionFailed {
		t.Fatalf("status %d, quero 412 (corpo=%q)", rec.Code, rec.Body.String())
	}
}

// TestClonePastaEUrlExclusivos: os dois modos juntos → 400.
func TestClonePastaEUrlExclusivos(t *testing.T) {
	srv := Novo(Opcoes{Banco: abrirBancoTemp(t)})
	rec := fazerReq(t, srv, http.MethodPost, "/api/v1/projects", map[string]any{
		"nome": "X", "pasta": repoGitTemp(t), "url_git": "https://example.com/r.git",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, quero 400", rec.Code)
	}
}

// TestSSHUserIDNoProjeto: definir a credencial exige chave existente; remover
// com 0 volta às credenciais do SO.
func TestSSHUserIDNoProjeto(t *testing.T) {
	temSSHKeygen(t)
	srv := Novo(Opcoes{Banco: abrirBancoTemp(t)})
	proj := criarProjetoTeste(t, srv)

	// admin de teste é o usuário 1, ainda sem chave → 400.
	rec := fazerReq(t, srv, http.MethodPut, "/api/v1/projects/"+itoa(proj), map[string]any{
		"nome": "P", "pasta": pastaDoProjeto(t, srv, proj), "ssh_user_id": 1,
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("credencial sem chave: status %d, quero 400 (corpo=%q)", rec.Code, rec.Body.String())
	}

	// Gera a chave do admin e tenta de novo.
	rec = fazerReq(t, srv, http.MethodPost, "/api/v1/me/ssh-key", nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("gerar chave: status %d", rec.Code)
	}
	rec = fazerReq(t, srv, http.MethodPut, "/api/v1/projects/"+itoa(proj), map[string]any{
		"nome": "P", "pasta": pastaDoProjeto(t, srv, proj), "ssh_user_id": 1,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("definir credencial: status %d (corpo=%q)", rec.Code, rec.Body.String())
	}
	p := decodProjeto(t, rec)
	if p.SSHUserID == nil || *p.SSHUserID != 1 {
		t.Fatalf("ssh_user_id = %v, quero 1", p.SSHUserID)
	}

	// 0 remove.
	rec = fazerReq(t, srv, http.MethodPut, "/api/v1/projects/"+itoa(proj), map[string]any{
		"nome": "P", "pasta": pastaDoProjeto(t, srv, proj), "ssh_user_id": 0,
	})
	p = decodProjeto(t, rec)
	if p.SSHUserID != nil {
		t.Fatalf("ssh_user_id após remover = %v, quero nil", p.SSHUserID)
	}
}

// pastaDoProjeto lê a pasta atual do projeto (o PUT exige pasta válida).
func pastaDoProjeto(t *testing.T, srv *Servidor, id int64) string {
	t.Helper()
	rec := fazerReq(t, srv, http.MethodGet, "/api/v1/projects/"+itoa(id), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("obter projeto: status %d", rec.Code)
	}
	return decodProjeto(t, rec).Pasta
}
