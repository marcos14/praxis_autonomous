package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"

	"github.com/marcos14/praxis-autonomous/internal/db"
)

// cenarioVisaoAPI monta: admin (setup), um projeto, o papel "usa" (consultas,
// planejamentos, criar demandas) e três usuários: ana e bia no grupo G1, caio
// sem grupo. Devolve os tokens e ids necessários.
type cenarioVisaoAPI struct {
	srv                   *Servidor
	proj                  int64
	admin, ana, bia, caio string
	idAna, idBia, idCaio  int64
	g1                    int64
}

func montarCenarioVisaoAPI(t *testing.T) cenarioVisaoAPI {
	t.Helper()
	srv := Novo(Opcoes{Banco: abrirBancoTemp(t)})
	c := cenarioVisaoAPI{srv: srv}
	c.proj = criarProjetoNomeado(t, srv, "Projeto")
	c.admin = setupAdmin(t, srv)

	rec := fazerReqToken(t, srv, http.MethodPost, "/api/v1/roles", c.admin, map[string]any{
		"nome": "usa", "permissoes": []string{"consultas.usar", "planejamentos.usar", "demandas.criar"},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("criar papel: status %d (corpo=%q)", rec.Code, rec.Body.String())
	}
	var papel struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &papel)

	rec = fazerReqToken(t, srv, http.MethodPost, "/api/v1/user-groups", c.admin, map[string]any{"nome": "G1"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("criar grupo de usuários: status %d (corpo=%q)", rec.Code, rec.Body.String())
	}
	var grupo struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &grupo)
	c.g1 = grupo.ID

	criar := func(nome, email string, grupoID *int64) (string, int64) {
		corpo := map[string]any{
			"nome": nome, "email": email, "senha": "senha-forte-123", "ativo": true,
			"papeis": []int64{papel.ID},
		}
		if grupoID != nil {
			corpo["grupo_id"] = *grupoID
		}
		rec := fazerReqToken(t, srv, http.MethodPost, "/api/v1/users", c.admin, corpo)
		if rec.Code != http.StatusCreated {
			t.Fatalf("criar usuário %s: status %d (corpo=%q)", nome, rec.Code, rec.Body.String())
		}
		var u struct {
			ID int64 `json:"id"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &u)
		return loginToken(t, srv, email, "senha-forte-123"), u.ID
	}
	c.ana, c.idAna = criar("Ana", "ana@x.com", &c.g1)
	c.bia, c.idBia = criar("Bia", "bia@x.com", &c.g1)
	c.caio, c.idCaio = criar("Caio", "caio@x.com", nil)
	return c
}

// criarConsultaComo cria uma consulta no projeto com a visibilidade dada.
func (c cenarioVisaoAPI) criarConsultaComo(t *testing.T, token, visibilidade, mensagem string) db.Consulta {
	t.Helper()
	corpo := map[string]any{"project_id": c.proj, "mensagem": mensagem}
	if visibilidade != "" {
		corpo["visibilidade"] = visibilidade
	}
	rec := fazerReqToken(t, c.srv, http.MethodPost, "/api/v1/consultas", token, corpo)
	if rec.Code != http.StatusCreated {
		t.Fatalf("criar consulta %q: status %d (corpo=%q)", mensagem, rec.Code, rec.Body.String())
	}
	return decodConsulta(t, rec)
}

// listarConsultasComo devolve os títulos das consultas visíveis ao token.
func (c cenarioVisaoAPI) listarConsultasComo(t *testing.T, token, query string) []db.Consulta {
	t.Helper()
	rec := fazerReqToken(t, c.srv, http.MethodGet, "/api/v1/consultas"+query, token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("listar consultas: status %d (corpo=%q)", rec.Code, rec.Body.String())
	}
	var lista []db.Consulta
	if err := json.Unmarshal(rec.Body.Bytes(), &lista); err != nil {
		t.Fatalf("decodificar: %v", err)
	}
	return lista
}

func TestVisibilidadeDeConsultasNaAPI(t *testing.T) {
	c := montarCenarioVisaoAPI(t)
	privada := c.criarConsultaComo(t, c.ana, "", "privada por default")
	c.criarConsultaComo(t, c.ana, "grupo", "para o grupo")
	c.criarConsultaComo(t, c.ana, "publica", "para todos")
	if privada.Visibilidade != "privada" {
		t.Fatalf("visibilidade default = %q, quero privada", privada.Visibilidade)
	}

	// listagens seguem a regra de dono.
	if n := len(c.listarConsultasComo(t, c.ana, "")); n != 3 {
		t.Fatalf("ana vê %d, quero 3", n)
	}
	lista := c.listarConsultasComo(t, c.bia, "")
	if len(lista) != 2 {
		t.Fatalf("bia vê %d, quero 2 (grupo + pública)", len(lista))
	}
	if lista[0].CriadoPorNome != "Ana" {
		t.Fatalf("criado_por_nome = %q, quero Ana", lista[0].CriadoPorNome)
	}
	if n := len(c.listarConsultasComo(t, c.caio, "")); n != 1 {
		t.Fatalf("caio vê %d, quero 1 (pública)", n)
	}
	if n := len(c.listarConsultasComo(t, c.admin, "")); n != 3 {
		t.Fatalf("admin vê %d, quero 3", n)
	}
	// escopo.
	if n := len(c.listarConsultasComo(t, c.bia, "?escopo=meus")); n != 0 {
		t.Fatalf("bia escopo=meus vê %d, quero 0", n)
	}
	if n := len(c.listarConsultasComo(t, c.bia, "?escopo=grupo")); n != 2 {
		t.Fatalf("bia escopo=grupo vê %d, quero 2", n)
	}
	if rec := fazerReqToken(t, c.srv, http.MethodGet, "/api/v1/consultas?escopo=xyz", c.bia, nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("escopo inválido: status %d, quero 400", rec.Code)
	}

	// acesso por id: privada alheia é 404 (middleware); a própria, 200.
	rota := "/api/v1/consultas/" + strconv.FormatInt(privada.ID, 10)
	if rec := fazerReqToken(t, c.srv, http.MethodGet, rota, c.bia, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("bia abrindo privada de ana: status %d, quero 404", rec.Code)
	}
	if rec := fazerReqToken(t, c.srv, http.MethodGet, rota+"/chat", c.bia, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("bia lendo chat da privada: status %d, quero 404", rec.Code)
	}
	if rec := fazerReqToken(t, c.srv, http.MethodGet, rota, c.ana, nil); rec.Code != http.StatusOK {
		t.Fatalf("ana abrindo a própria: status %d, quero 200", rec.Code)
	}
	if rec := fazerReqToken(t, c.srv, http.MethodGet, rota, c.admin, nil); rec.Code != http.StatusOK {
		t.Fatalf("admin abrindo privada: status %d, quero 200", rec.Code)
	}

	// mudar a visibilidade: só dono ou admin; valor validado.
	if rec := fazerReqToken(t, c.srv, http.MethodPut, rota+"/visibilidade", c.bia, map[string]any{"visibilidade": "publica"}); rec.Code != http.StatusNotFound {
		t.Fatalf("bia alterando visibilidade de item que não vê: status %d, quero 404", rec.Code)
	}
	if rec := fazerReqToken(t, c.srv, http.MethodPut, rota+"/visibilidade", c.ana, map[string]any{"visibilidade": "secreta"}); rec.Code != http.StatusBadRequest {
		t.Fatalf("valor inválido: status %d, quero 400", rec.Code)
	}
	if rec := fazerReqToken(t, c.srv, http.MethodPut, rota+"/visibilidade", c.ana, map[string]any{"visibilidade": "publica"}); rec.Code != http.StatusNoContent {
		t.Fatalf("ana tornando pública: status %d, quero 204 (corpo=%q)", rec.Code, rec.Body.String())
	}
	if n := len(c.listarConsultasComo(t, c.bia, "")); n != 3 {
		t.Fatalf("bia após tornar pública vê %d, quero 3", n)
	}
	// agora bia vê a consulta, mas não é dona: 403 ao tentar alterar.
	if rec := fazerReqToken(t, c.srv, http.MethodPut, rota+"/visibilidade", c.bia, map[string]any{"visibilidade": "privada"}); rec.Code != http.StatusForbidden {
		t.Fatalf("bia alterando visibilidade alheia: status %d, quero 403", rec.Code)
	}
	if rec := fazerReqToken(t, c.srv, http.MethodPut, rota+"/visibilidade", c.admin, map[string]any{"visibilidade": "privada"}); rec.Code != http.StatusNoContent {
		t.Fatalf("admin alterando: status %d, quero 204", rec.Code)
	}
	// criação com valor inválido.
	rec := fazerReqToken(t, c.srv, http.MethodPost, "/api/v1/consultas", c.ana,
		map[string]any{"project_id": c.proj, "mensagem": "x", "visibilidade": "oculta"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("criar com visibilidade inválida: status %d, quero 400", rec.Code)
	}
}

func TestVisibilidadeDePlanejamentosNaAPI(t *testing.T) {
	c := montarCenarioVisaoAPI(t)
	criar := func(token, vis, msg string) db.Planejamento {
		t.Helper()
		corpo := map[string]any{"project_id": c.proj, "mensagem": msg}
		if vis != "" {
			corpo["visibilidade"] = vis
		}
		rec := fazerReqToken(t, c.srv, http.MethodPost, "/api/v1/planejamentos", token, corpo)
		if rec.Code != http.StatusCreated {
			t.Fatalf("criar planejamento: status %d (corpo=%q)", rec.Code, rec.Body.String())
		}
		return decodPlanejamento(t, rec)
	}
	privado := criar(c.ana, "", "privado")
	criar(c.ana, "publica", "público")

	listar := func(token, query string) []db.Planejamento {
		t.Helper()
		rec := fazerReqToken(t, c.srv, http.MethodGet, "/api/v1/planejamentos"+query, token, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("listar: status %d (corpo=%q)", rec.Code, rec.Body.String())
		}
		var lista []db.Planejamento
		_ = json.Unmarshal(rec.Body.Bytes(), &lista)
		return lista
	}
	if lista := listar(c.bia, ""); len(lista) != 1 || lista[0].CriadoPorNome != "Ana" || lista[0].Visibilidade != "publica" {
		t.Fatalf("bia vê %+v, quero só o público de Ana", lista)
	}
	if n := len(listar(c.ana, "?escopo=meus")); n != 2 {
		t.Fatalf("ana escopo=meus vê %d, quero 2", n)
	}
	rota := "/api/v1/planejamentos/" + strconv.FormatInt(privado.ID, 10)
	if rec := fazerReqToken(t, c.srv, http.MethodGet, rota+"/documentos", c.bia, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("bia lendo documentos do privado: status %d, quero 404", rec.Code)
	}
	if rec := fazerReqToken(t, c.srv, http.MethodPut, rota+"/visibilidade", c.admin, map[string]any{"visibilidade": "grupo"}); rec.Code != http.StatusNoContent {
		t.Fatalf("admin alterando: status %d, quero 204 (corpo=%q)", rec.Code, rec.Body.String())
	}
	if n := len(listar(c.bia, "")); n != 2 {
		t.Fatalf("bia após 'grupo' vê %d, quero 2 (mesmo grupo de ana)", n)
	}
	if n := len(listar(c.caio, "")); n != 1 {
		t.Fatalf("caio após 'grupo' vê %d, quero 1", n)
	}
}

func TestItensSemDonoSeguemAConfig(t *testing.T) {
	c := montarCenarioVisaoAPI(t)
	// Consulta criada por token de API (sem usuário → sem dono).
	tok, err := c.srv.banco.CriarToken(t.Context(), "integração", db.PapelAdmin)
	if err != nil {
		t.Fatalf("criar token: %v", err)
	}
	c.criarConsultaComo(t, tok.Token, "", "aberta pelo sistema de chamados")

	// padrão (admins): usuário comum não vê; admin e o token veem.
	if n := len(c.listarConsultasComo(t, c.bia, "")); n != 0 {
		t.Fatalf("bia com config padrão vê %d, quero 0", n)
	}
	if n := len(c.listarConsultasComo(t, c.admin, "")); n != 1 {
		t.Fatalf("admin vê %d, quero 1", n)
	}
	if n := len(c.listarConsultasComo(t, tok.Token, "")); n != 1 {
		t.Fatalf("token vê %d, quero 1", n)
	}

	// config inválida → 400.
	for _, corpo := range []map[string]any{
		{"sem_dono_visibilidade": "todos"},
		{"sem_dono_visibilidade": "grupo"},
		{"sem_dono_visibilidade": "grupo", "sem_dono_grupo_id": 9999},
	} {
		if rec := fazerReqToken(t, c.srv, http.MethodPut, "/api/v1/config", c.admin, corpo); rec.Code != http.StatusBadRequest {
			t.Fatalf("config %v: status %d, quero 400", corpo, rec.Code)
		}
	}

	// publica: todos veem, na hora.
	if rec := fazerReqToken(t, c.srv, http.MethodPut, "/api/v1/config", c.admin,
		map[string]any{"sem_dono_visibilidade": "publica"}); rec.Code != http.StatusOK {
		t.Fatalf("config publica: status %d (corpo=%q)", rec.Code, rec.Body.String())
	}
	if n := len(c.listarConsultasComo(t, c.caio, "")); n != 1 {
		t.Fatalf("caio com sem_dono=publica vê %d, quero 1", n)
	}

	// grupo G1: bia vê, caio não.
	if rec := fazerReqToken(t, c.srv, http.MethodPut, "/api/v1/config", c.admin,
		map[string]any{"sem_dono_visibilidade": "grupo", "sem_dono_grupo_id": c.g1}); rec.Code != http.StatusOK {
		t.Fatalf("config grupo: status %d (corpo=%q)", rec.Code, rec.Body.String())
	}
	if n := len(c.listarConsultasComo(t, c.bia, "")); n != 1 {
		t.Fatalf("bia com sem_dono=grupo G1 vê %d, quero 1", n)
	}
	if n := len(c.listarConsultasComo(t, c.caio, "")); n != 0 {
		t.Fatalf("caio com sem_dono=grupo G1 vê %d, quero 0", n)
	}
}
