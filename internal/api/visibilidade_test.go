package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"

	"github.com/marcos14/praxis-autonomous/internal/db"
)

// criarUsuarioComPapel cria (como admin) um papel com as permissões dadas e um
// usuário vinculado a ele, devolvendo o token do usuário logado. permissoes nil
// cria o usuário SEM papel (só a visualização implícita).
func criarUsuarioComPapel(t *testing.T, srv *Servidor, email string, permissoes []string) string {
	t.Helper()
	admin := tokenAdminTeste(t, srv)
	papeis := []int64{}
	if permissoes != nil {
		rec := fazerReqToken(t, srv, http.MethodPost, "/api/v1/roles", admin, map[string]any{
			"nome": "papel-" + email, "permissoes": permissoes,
		})
		if rec.Code != http.StatusCreated {
			t.Fatalf("criar papel: status %d (corpo=%q)", rec.Code, rec.Body.String())
		}
		var papel struct {
			ID int64 `json:"id"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &papel)
		papeis = append(papeis, papel.ID)
	}
	rec := fazerReqToken(t, srv, http.MethodPost, "/api/v1/users", admin, map[string]any{
		"nome": email, "email": email, "senha": "senha-forte-123", "ativo": true, "papeis": papeis,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("criar usuário %s: status %d (corpo=%q)", email, rec.Code, rec.Body.String())
	}
	return loginToken(t, srv, email, "senha-forte-123")
}

// TestVisibilidadeDeMotoresNaAPI cobre a Fase A nos motores: o cadastro com
// visibilidade grava a ACL; a listagem e o GET respeitam-na para quem não tem
// config.gerir; e o painel de uso segue o mesmo filtro.
func TestVisibilidadeDeMotoresNaAPI(t *testing.T) {
	srv := Novo(Opcoes{Banco: abrirBancoTemp(t)})
	admin := tokenAdminTeste(t, srv)

	rec := fazerReqToken(t, srv, http.MethodPost, "/api/v1/engines", admin,
		map[string]any{"nome": "publico"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("criar motor público: status %d (corpo=%q)", rec.Code, rec.Body.String())
	}
	rec = fazerReqToken(t, srv, http.MethodPost, "/api/v1/engines", admin,
		map[string]any{"nome": "privado", "visibilidade": "privada"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("criar motor privado: status %d (corpo=%q)", rec.Code, rec.Body.String())
	}
	var privado db.Motor
	_ = json.Unmarshal(rec.Body.Bytes(), &privado)
	if privado.Visibilidade != db.VisibilidadePrivada || privado.OwnerUserID == nil {
		t.Fatalf("motor privado sem decoração: %+v", privado)
	}

	// Usuário comum (sem config.gerir): só o motor público aparece.
	vis := criarUsuarioComPapel(t, srv, "vis@x.com", nil)
	rec = fazerReqToken(t, srv, http.MethodGet, "/api/v1/engines", vis, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("listar motores: status %d", rec.Code)
	}
	var lista []db.Motor
	_ = json.Unmarshal(rec.Body.Bytes(), &lista)
	if len(lista) != 1 || lista[0].Nome != "publico" {
		t.Fatalf("lista para usuário comum = %+v, quero só o motor público", lista)
	}

	// GET direto do motor escondido → 404 (não revela existência).
	rec = fazerReqToken(t, srv, http.MethodGet,
		"/api/v1/engines/"+strconv.FormatInt(privado.ID, 10), vis, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET motor escondido: status %d, quero 404", rec.Code)
	}

	// Painel de uso: mesmo filtro.
	rec = fazerReqToken(t, srv, http.MethodGet, "/api/v1/engines/uso", vis, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("uso: status %d", rec.Code)
	}
	var uso struct {
		Motores []struct {
			Nome string `json:"nome"`
		} `json:"motores"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &uso)
	for _, m := range uso.Motores {
		if m.Nome == "privado" {
			t.Fatal("painel de uso vazou motor escondido")
		}
	}

	// Admin (config.gerir) segue vendo tudo.
	rec = fazerReqToken(t, srv, http.MethodGet, "/api/v1/engines", admin, nil)
	_ = json.Unmarshal(rec.Body.Bytes(), &lista)
	if len(lista) != 2 {
		t.Fatalf("lista para admin = %d motores, quero 2", len(lista))
	}
}

// TestProjetoAutosservico cobre a Fase A nos projetos: quem tem projetos.criar
// cadastra o próprio projeto, escolhe a visibilidade e gerencia SÓ o que é seu.
func TestProjetoAutosservico(t *testing.T) {
	srv := Novo(Opcoes{Banco: abrirBancoTemp(t)})

	ana := criarUsuarioComPapel(t, srv, "ana@x.com", []string{"projetos.criar"})
	beto := criarUsuarioComPapel(t, srv, "beto@x.com", nil)

	// Ana cria um projeto PRIVADO.
	rec := fazerReqToken(t, srv, http.MethodPost, "/api/v1/projects", ana, map[string]any{
		"nome": "Da Ana", "pasta": repoGitTemp(t), "visibilidade": "privada",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("ana criar projeto: status %d (corpo=%q)", rec.Code, rec.Body.String())
	}
	proj := decodProjeto(t, rec)
	if proj.Visibilidade != db.VisibilidadePrivada || proj.OwnerUserID == nil {
		t.Fatalf("projeto sem dono/visibilidade: %+v", proj)
	}
	id := strconv.FormatInt(proj.ID, 10)

	// Beto não vê (nem na lista, nem por id).
	rec = fazerReqToken(t, srv, http.MethodGet, "/api/v1/projects", beto, nil)
	var lista []db.Projeto
	_ = json.Unmarshal(rec.Body.Bytes(), &lista)
	for _, p := range lista {
		if p.ID == proj.ID {
			t.Fatal("projeto privado da ana vazou na lista do beto")
		}
	}
	rec = fazerReqToken(t, srv, http.MethodGet, "/api/v1/projects/"+id, beto, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("beto GET projeto privado: status %d, quero 404", rec.Code)
	}

	// Ana edita o PRÓPRIO projeto (dona, sem projetos.gerir) — inclusive tornando-o público.
	rec = fazerReqToken(t, srv, http.MethodPut, "/api/v1/projects/"+id, ana, map[string]any{
		"nome": "Da Ana v2", "pasta": proj.Pasta, "visibilidade": "publica",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("ana editar o próprio projeto: status %d (corpo=%q)", rec.Code, rec.Body.String())
	}

	// Agora público: beto vê, mas NÃO edita (não é dono nem tem projetos.gerir).
	rec = fazerReqToken(t, srv, http.MethodGet, "/api/v1/projects/"+id, beto, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("beto GET projeto público: status %d, quero 200", rec.Code)
	}
	rec = fazerReqToken(t, srv, http.MethodPut, "/api/v1/projects/"+id, beto, map[string]any{
		"nome": "Do Beto", "pasta": proj.Pasta,
	})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("beto editar projeto alheio: status %d, quero 403", rec.Code)
	}

	// Beto também não cria projeto (sem projetos.criar).
	rec = fazerReqToken(t, srv, http.MethodPost, "/api/v1/projects", beto, map[string]any{
		"nome": "Do Beto", "pasta": repoGitTemp(t),
	})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("beto criar projeto: status %d, quero 403", rec.Code)
	}

	// Ana NÃO compartilha com grupo alheio (não pertence a nenhum grupo).
	rec = fazerReqToken(t, srv, http.MethodPost, "/api/v1/projects", ana, map[string]any{
		"nome": "Grupo Errado", "pasta": repoGitTemp(t), "visibilidade": "grupo", "grupo_id": 999,
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("grupo alheio: status %d, quero 400 (corpo=%q)", rec.Code, rec.Body.String())
	}
}

// TestBootstrapRestritoAoAuth confirma a postura da Fase A: com o banco SEM
// usuários, nada além das rotas públicas de auth responde.
func TestBootstrapRestritoAoAuth(t *testing.T) {
	srv := Novo(Opcoes{Banco: abrirBancoTemp(t)})

	// API bloqueada antes do primeiro admin…
	rec := fazerReqAnonima(t, srv, http.MethodGet, "/api/v1/projects", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("GET /projects no bootstrap: status %d, quero 401 (corpo=%q)", rec.Code, rec.Body.String())
	}
	rec = fazerReqAnonima(t, srv, http.MethodPost, "/api/v1/engines", map[string]any{"nome": "x"})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("POST /engines no bootstrap: status %d, quero 401", rec.Code)
	}
	// …mas o fluxo de setup segue aberto.
	rec = fazerReqAnonima(t, srv, http.MethodGet, "/api/v1/auth/status", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /auth/status: status %d, quero 200", rec.Code)
	}
}
