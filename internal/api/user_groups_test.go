package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/marcos14/praxis-autonomous/internal/db"
)

func TestGruposUsuariosCRUDViaAPI(t *testing.T) {
	srv := Novo(Opcoes{Banco: abrirBancoTemp(t)})
	// Criar um usuário encerra o modo bootstrap; o teste inteiro roda como admin.
	admin := setupAdmin(t, srv)
	fazerReq := func(t *testing.T, srv *Servidor, metodo, caminho string, corpo any) *httptest.ResponseRecorder {
		return fazerReqToken(t, srv, metodo, caminho, admin, corpo)
	}

	// motor para vincular ao grupo (com o novo modelo_consulta).
	rec := fazerReq(t, srv, http.MethodPost, "/api/v1/engines", map[string]any{
		"nome": "claude", "modelo_exec": "opus", "modelo_analise": "sonnet", "modelo_consulta": "haiku",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("criar motor: status %d (corpo=%q)", rec.Code, rec.Body.String())
	}
	var motor db.Motor
	_ = json.Unmarshal(rec.Body.Bytes(), &motor)
	if motor.ModeloConsulta != "haiku" {
		t.Fatalf("modelo_consulta não aceito pela API: %+v", motor)
	}

	// criar grupo.
	rec = fazerReq(t, srv, http.MethodPost, "/api/v1/user-groups", map[string]any{
		"nome": "Suporte", "descricao": "time de suporte", "engine_id": motor.ID, "modelo": "haiku",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("criar grupo: status %d (corpo=%q)", rec.Code, rec.Body.String())
	}
	var g db.GrupoUsuarios
	_ = json.Unmarshal(rec.Body.Bytes(), &g)
	if g.ID == 0 || g.EngineID == nil {
		t.Fatalf("grupo criado = %+v", g)
	}

	// nome duplicado → 409.
	rec = fazerReq(t, srv, http.MethodPost, "/api/v1/user-groups", map[string]any{"nome": "Suporte"})
	if rec.Code != http.StatusConflict {
		t.Fatalf("grupo duplicado: status %d, quero 409", rec.Code)
	}

	// atualizar.
	rec = fazerReq(t, srv, http.MethodPut, "/api/v1/user-groups/"+strconv.FormatInt(g.ID, 10),
		map[string]any{"nome": "Suporte N1", "modelo": "haiku-lite"})
	if rec.Code != http.StatusOK {
		t.Fatalf("atualizar grupo: status %d (corpo=%q)", rec.Code, rec.Body.String())
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &g)
	if g.Nome != "Suporte N1" || g.Modelo != "haiku-lite" || g.EngineID != nil {
		t.Fatalf("grupo atualizado = %+v (engine_id deve zerar quando não reenviado)", g)
	}

	// usuário criado já vinculado ao grupo.
	rec = fazerReq(t, srv, http.MethodPost, "/api/v1/users", map[string]any{
		"nome": "Ana", "email": "ana@x.com", "senha": "senha-forte-123", "ativo": true,
		"papeis": []int64{}, "grupo_id": g.ID,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("criar usuário com grupo: status %d (corpo=%q)", rec.Code, rec.Body.String())
	}
	var u db.Usuario
	_ = json.Unmarshal(rec.Body.Bytes(), &u)
	if u.GrupoID == nil || *u.GrupoID != g.ID || u.GrupoNome != "Suporte N1" {
		t.Fatalf("usuário sem grupo vinculado: %+v", u)
	}

	// listagem do grupo traz o membro.
	rec = fazerReq(t, srv, http.MethodGet, "/api/v1/user-groups", nil)
	var grupos []db.GrupoUsuarios
	_ = json.Unmarshal(rec.Body.Bytes(), &grupos)
	if len(grupos) != 1 || len(grupos[0].Usuarios) != 1 || grupos[0].Usuarios[0] != "Ana" {
		t.Fatalf("listagem = %+v, quero Ana como membro", grupos)
	}

	// update do usuário sem grupo_id remove o vínculo.
	rec = fazerReq(t, srv, http.MethodPut, "/api/v1/users/"+strconv.FormatInt(u.ID, 10), map[string]any{
		"nome": "Ana", "email": "ana@x.com", "ativo": true, "papeis": []int64{},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("atualizar usuário: status %d (corpo=%q)", rec.Code, rec.Body.String())
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &u)
	if u.GrupoID != nil {
		t.Fatalf("grupo deveria ter sido removido no update sem grupo_id: %+v", u)
	}

	// excluir grupo.
	rec = fazerReq(t, srv, http.MethodDelete, "/api/v1/user-groups/"+strconv.FormatInt(g.ID, 10), nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("excluir grupo: status %d", rec.Code)
	}
}

func TestGruposUsuariosExigemUsuariosGerir(t *testing.T) {
	srv := Novo(Opcoes{Banco: abrirBancoTemp(t)})
	admin := setupAdmin(t, srv)

	// papel só com consultas.usar (não gere usuários).
	rec := fazerReqToken(t, srv, http.MethodPost, "/api/v1/roles", admin, map[string]any{
		"nome": "suporte", "permissoes": []string{"consultas.usar"},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("criar papel: status %d", rec.Code)
	}
	var papel struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &papel)
	rec = fazerReqToken(t, srv, http.MethodPost, "/api/v1/users", admin, map[string]any{
		"nome": "Sup", "email": "sup@x.com", "senha": "senha-forte-123", "ativo": true,
		"papeis": []int64{papel.ID},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("criar usuário: status %d", rec.Code)
	}
	suporte := loginToken(t, srv, "sup@x.com", "senha-forte-123")

	// leitura E escrita barradas sem usuarios.gerir.
	rec = fazerReqToken(t, srv, http.MethodGet, "/api/v1/user-groups", suporte, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("listar grupos sem permissão: status %d, quero 403", rec.Code)
	}
	rec = fazerReqToken(t, srv, http.MethodPost, "/api/v1/user-groups", suporte, map[string]any{"nome": "x"})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("criar grupo sem permissão: status %d, quero 403", rec.Code)
	}
}
