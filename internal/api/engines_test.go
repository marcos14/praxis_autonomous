package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/marcos14/praxis-autonomous/internal/db"
)

func decodMotor(t *testing.T, rec *httptest.ResponseRecorder) db.Motor {
	t.Helper()
	var m db.Motor
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("decodificar motor: %v (corpo=%q)", err, rec.Body.String())
	}
	return m
}

func decodMotores(t *testing.T, rec *httptest.ResponseRecorder) []db.Motor {
	t.Helper()
	var ms []db.Motor
	if err := json.Unmarshal(rec.Body.Bytes(), &ms); err != nil {
		t.Fatalf("decodificar motores: %v (corpo=%q)", err, rec.Body.String())
	}
	return ms
}

func decodConta(t *testing.T, rec *httptest.ResponseRecorder) db.Conta {
	t.Helper()
	var c db.Conta
	if err := json.Unmarshal(rec.Body.Bytes(), &c); err != nil {
		t.Fatalf("decodificar conta: %v (corpo=%q)", err, rec.Body.String())
	}
	return c
}

// criarMotorAPI cria um motor via API e devolve o objeto criado.
func criarMotorAPI(t *testing.T, srv *Servidor, nome string) db.Motor {
	t.Helper()
	rec := fazerReq(t, srv, http.MethodPost, "/api/v1/engines", map[string]any{"nome": nome})
	if rec.Code != http.StatusCreated {
		t.Fatalf("criar motor %s: status %d (corpo=%s)", nome, rec.Code, rec.Body.String())
	}
	return decodMotor(t, rec)
}

func TestCriarMotorOK(t *testing.T) {
	srv := Novo(Opcoes{Banco: abrirBancoTemp(t)})
	rec := fazerReq(t, srv, http.MethodPost, "/api/v1/engines", map[string]any{
		"nome":            "claude",
		"modelo_exec":     "opus",
		"modelo_analise":  "sonnet",
		"budget_fase_usd": 6.0,
		"timeout_min":     45,
		"params":          map[string]any{"k": "v"},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d (corpo=%s)", rec.Code, rec.Body.String())
	}
	m := decodMotor(t, rec)
	if m.ID == 0 || m.Nome != "claude" {
		t.Fatalf("motor inválido: %+v", m)
	}
	if !m.Ativo {
		t.Fatal("ativo default deveria ser true")
	}
	if m.Prioridade != 0 {
		t.Fatalf("prioridade = %d, quero 0", m.Prioridade)
	}
	if m.Contas == nil {
		t.Fatal("contas deveria ser [] e não nil")
	}
}

func TestCriarMotorPrioridadeAutoIncrementa(t *testing.T) {
	srv := Novo(Opcoes{Banco: abrirBancoTemp(t)})
	m0 := criarMotorAPI(t, srv, "claude")
	m1 := criarMotorAPI(t, srv, "codex")
	if m0.Prioridade != 0 || m1.Prioridade != 1 {
		t.Fatalf("prioridades = %d,%d; quero 0,1", m0.Prioridade, m1.Prioridade)
	}
}

func TestDetectarMotoresEndpoint(t *testing.T) {
	srv := Novo(Opcoes{Banco: abrirBancoTemp(t)})
	criarMotorAPI(t, srv, "claude") // já cadastrado deve vir marcado
	rec := fazerReq(t, srv, http.MethodGet, "/api/v1/engines/deteccao", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (corpo=%s)", rec.Code, rec.Body.String())
	}
	var sug []struct {
		Nome         string `json:"nome"`
		JaCadastrado bool   `json:"ja_cadastrado"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &sug); err != nil {
		t.Fatalf("decodificar sugestões: %v (corpo=%s)", err, rec.Body.String())
	}
	if len(sug) == 0 {
		t.Fatal("esperava ao menos uma sugestão de motor")
	}
	var achouClaude bool
	for _, s := range sug {
		if s.Nome == "claude" {
			achouClaude = true
			if !s.JaCadastrado {
				t.Fatal("claude já foi cadastrado; deveria vir ja_cadastrado=true")
			}
		}
	}
	if !achouClaude {
		t.Fatal("sugestão do claude ausente")
	}
}

func TestAutocadastrarMotoresEndpoint(t *testing.T) {
	srv := Novo(Opcoes{Banco: abrirBancoTemp(t)})
	// Em ambiente de teste os CLIs não estão instalados, então nada é cadastrado.
	rec := fazerReq(t, srv, http.MethodPost, "/api/v1/engines/deteccao", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (corpo=%s)", rec.Code, rec.Body.String())
	}
	criados := decodMotores(t, rec)
	if criados == nil {
		t.Fatal("resposta deveria ser uma lista (mesmo que vazia), não nil")
	}
}

// TestMotorFallbackDefaultEUpdate: criar sem o campo liga o fallback (default);
// o PUT com fallback=false transforma o motor em uso manual e um PUT sem o
// campo preserva o valor atual.
func TestMotorFallbackDefaultEUpdate(t *testing.T) {
	srv := Novo(Opcoes{Banco: abrirBancoTemp(t)})
	m := criarMotorAPI(t, srv, "claude")
	if !m.Fallback {
		t.Fatal("motor criado sem o campo deveria participar do fallback (default)")
	}

	rec := fazerReq(t, srv, http.MethodPut, "/api/v1/engines/"+itoa(m.ID), map[string]any{"fallback": false})
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT fallback=false: status %d (corpo=%s)", rec.Code, rec.Body.String())
	}
	if atualizado := decodMotor(t, rec); atualizado.Fallback {
		t.Fatal("fallback deveria ficar false após o PUT")
	}

	// PUT sem o campo preserva (não volta para true).
	rec = fazerReq(t, srv, http.MethodPut, "/api/v1/engines/"+itoa(m.ID), map[string]any{"timeout_min": 15})
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT sem fallback: status %d (corpo=%s)", rec.Code, rec.Body.String())
	}
	if atualizado := decodMotor(t, rec); atualizado.Fallback {
		t.Fatal("PUT sem o campo não deveria religar o fallback")
	}
}

func TestCriarMotorNomeObrigatorio(t *testing.T) {
	srv := Novo(Opcoes{Banco: abrirBancoTemp(t)})
	rec := fazerReq(t, srv, http.MethodPost, "/api/v1/engines", map[string]any{"modelo_exec": "opus"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, quero 400", rec.Code)
	}
	if e := decodErro(t, rec); e.Erro.Codigo != "invalido" {
		t.Fatalf("codigo = %s, quero invalido", e.Erro.Codigo)
	}
}

func TestCriarMotorNomeDuplicado(t *testing.T) {
	srv := Novo(Opcoes{Banco: abrirBancoTemp(t)})
	criarMotorAPI(t, srv, "claude")
	rec := fazerReq(t, srv, http.MethodPost, "/api/v1/engines", map[string]any{"nome": "claude"})
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, quero 409", rec.Code)
	}
	if e := decodErro(t, rec); e.Erro.Codigo != "nome_duplicado" {
		t.Fatalf("codigo = %s, quero nome_duplicado", e.Erro.Codigo)
	}
}

func TestCriarMotorParamsInvalido(t *testing.T) {
	srv := Novo(Opcoes{Banco: abrirBancoTemp(t)})
	// params como array (não objeto) deve ser rejeitado.
	rec := fazerReq(t, srv, http.MethodPost, "/api/v1/engines", map[string]any{
		"nome":   "claude",
		"params": []int{1, 2, 3},
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, quero 400 (corpo=%s)", rec.Code, rec.Body.String())
	}
}

func TestCriarMotorBudgetNegativo(t *testing.T) {
	srv := Novo(Opcoes{Banco: abrirBancoTemp(t)})
	rec := fazerReq(t, srv, http.MethodPost, "/api/v1/engines", map[string]any{
		"nome":            "claude",
		"budget_fase_usd": -1.0,
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, quero 400", rec.Code)
	}
}

func TestListarMotoresOrdenado(t *testing.T) {
	srv := Novo(Opcoes{Banco: abrirBancoTemp(t)})
	criarMotorAPI(t, srv, "claude")
	criarMotorAPI(t, srv, "codex")
	rec := fazerReq(t, srv, http.MethodGet, "/api/v1/engines", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	ms := decodMotores(t, rec)
	if len(ms) != 2 || ms[0].Nome != "claude" || ms[1].Nome != "codex" {
		t.Fatalf("ordem inesperada: %+v", ms)
	}
}

func TestObterMotor404(t *testing.T) {
	srv := Novo(Opcoes{Banco: abrirBancoTemp(t)})
	rec := fazerReq(t, srv, http.MethodGet, "/api/v1/engines/999", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, quero 404", rec.Code)
	}
}

func TestObterMotorIDInvalido(t *testing.T) {
	srv := Novo(Opcoes{Banco: abrirBancoTemp(t)})
	rec := fazerReq(t, srv, http.MethodGet, "/api/v1/engines/abc", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, quero 400", rec.Code)
	}
}

func TestAtualizarMotorPreservaCampos(t *testing.T) {
	srv := Novo(Opcoes{Banco: abrirBancoTemp(t)})
	m := criarMotorAPI(t, srv, "codex")
	// Só muda o budget; nome/ativo/modelos devem ser preservados.
	criarMotorAPI(t, srv, "outro") // prioridade 1 para m? não — m criado antes, prioridade 0

	rec := fazerReq(t, srv, http.MethodPut, "/api/v1/engines/"+itoa(m.ID), map[string]any{
		"budget_fase_usd": 12.5,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (corpo=%s)", rec.Code, rec.Body.String())
	}
	got := decodMotor(t, rec)
	if got.Nome != "codex" {
		t.Fatalf("nome não preservado: %s", got.Nome)
	}
	if got.BudgetFaseUSD != 12.5 {
		t.Fatalf("budget = %v", got.BudgetFaseUSD)
	}
	if got.Prioridade != m.Prioridade {
		t.Fatalf("prioridade mudou: %d -> %d", m.Prioridade, got.Prioridade)
	}
}

func TestAtualizarMotorPrioridadeIgnorada(t *testing.T) {
	srv := Novo(Opcoes{Banco: abrirBancoTemp(t)})
	m := criarMotorAPI(t, srv, "claude")
	rec := fazerReq(t, srv, http.MethodPut, "/api/v1/engines/"+itoa(m.ID), map[string]any{
		"prioridade": 99,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if got := decodMotor(t, rec); got.Prioridade != 0 {
		t.Fatalf("prioridade = %d, o PUT não deveria alterá-la (quero 0)", got.Prioridade)
	}
}

func TestAtualizarMotor404(t *testing.T) {
	srv := Novo(Opcoes{Banco: abrirBancoTemp(t)})
	rec := fazerReq(t, srv, http.MethodPut, "/api/v1/engines/999", map[string]any{"nome": "x"})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, quero 404", rec.Code)
	}
}

func TestReordenarMotoresAPI(t *testing.T) {
	srv := Novo(Opcoes{Banco: abrirBancoTemp(t)})
	a := criarMotorAPI(t, srv, "claude")
	b := criarMotorAPI(t, srv, "codex")
	c := criarMotorAPI(t, srv, "opencode")

	rec := fazerReq(t, srv, http.MethodPut, "/api/v1/engines/ordem", map[string]any{
		"ids": []int64{c.ID, a.ID, b.ID},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (corpo=%s)", rec.Code, rec.Body.String())
	}
	ms := decodMotores(t, rec)
	quer := []string{"opencode", "claude", "codex"}
	for i, nome := range quer {
		if ms[i].Nome != nome {
			t.Fatalf("ms[%d] = %s, quero %s", i, ms[i].Nome, nome)
		}
	}
}

func TestReordenarMotoresAPIInvalido(t *testing.T) {
	srv := Novo(Opcoes{Banco: abrirBancoTemp(t)})
	a := criarMotorAPI(t, srv, "claude")
	criarMotorAPI(t, srv, "codex")

	// Lista incompleta → 400.
	rec := fazerReq(t, srv, http.MethodPut, "/api/v1/engines/ordem", map[string]any{
		"ids": []int64{a.ID},
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, quero 400", rec.Code)
	}

	// Lista vazia → 400.
	rec = fazerReq(t, srv, http.MethodPut, "/api/v1/engines/ordem", map[string]any{"ids": []int64{}})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status (vazio) = %d, quero 400", rec.Code)
	}
}

func TestContasAPICRUD(t *testing.T) {
	srv := Novo(Opcoes{Banco: abrirBancoTemp(t)})
	m := criarMotorAPI(t, srv, "claude")
	base := "/api/v1/engines/" + itoa(m.ID) + "/accounts"

	// Cria.
	rec := fazerReq(t, srv, http.MethodPost, base, map[string]any{
		"alias":      "principal",
		"config_dir": `C:\cfg\p`,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("criar conta: status %d (corpo=%s)", rec.Code, rec.Body.String())
	}
	c := decodConta(t, rec)
	if c.ID == 0 || c.Alias != "principal" || !c.Ativo || c.EngineID != m.ID {
		t.Fatalf("conta inválida: %+v", c)
	}

	// Aparece no motor.
	rec = fazerReq(t, srv, http.MethodGet, "/api/v1/engines/"+itoa(m.ID), nil)
	if got := decodMotor(t, rec); len(got.Contas) != 1 {
		t.Fatalf("contas = %d, quero 1", len(got.Contas))
	}

	// Atualiza.
	rec = fazerReq(t, srv, http.MethodPut, base+"/"+itoa(c.ID), map[string]any{
		"alias": "principal-2",
		"ativo": false,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("atualizar conta: status %d", rec.Code)
	}
	if got := decodConta(t, rec); got.Alias != "principal-2" || got.Ativo {
		t.Fatalf("conta não atualizada: %+v", got)
	}

	// Remove.
	rec = fazerReq(t, srv, http.MethodDelete, base+"/"+itoa(c.ID), nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("remover conta: status %d", rec.Code)
	}
	rec = fazerReq(t, srv, http.MethodGet, "/api/v1/engines/"+itoa(m.ID), nil)
	if got := decodMotor(t, rec); len(got.Contas) != 0 {
		t.Fatalf("contas pós-remoção = %d, quero 0", len(got.Contas))
	}
}

func TestPerfilCodexGerenciadoEPreservadoNoToggle(t *testing.T) {
	banco := abrirBancoTemp(t)
	srv := Novo(Opcoes{Banco: banco})
	m := criarMotorAPI(t, srv, "codex")
	base := "/api/v1/engines/" + itoa(m.ID) + "/accounts"

	rec := fazerReq(t, srv, http.MethodPost, base, map[string]any{"alias": "principal"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("criar perfil: status %d corpo=%s", rec.Code, rec.Body.String())
	}
	c := decodConta(t, rec)
	if !filepath.IsAbs(c.ConfigDir) || filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(c.ConfigDir)))) != filepath.Dir(banco.Caminho) {
		t.Fatalf("diretório gerenciado inesperado: %q", c.ConfigDir)
	}
	if st, err := os.Stat(c.ConfigDir); err != nil || !st.IsDir() {
		t.Fatalf("diretório do perfil não foi criado: stat=%v erro=%v", st, err)
	}

	rec = fazerReq(t, srv, http.MethodPut, base+"/"+itoa(c.ID), map[string]any{"ativo": false})
	if rec.Code != http.StatusOK {
		t.Fatalf("desativar perfil: status %d corpo=%s", rec.Code, rec.Body.String())
	}
	atual := decodConta(t, rec)
	if atual.ConfigDir != c.ConfigDir || atual.Ativo {
		t.Fatalf("toggle perdeu o diretório: antes=%+v depois=%+v", c, atual)
	}
}

func TestCriarContaMotorInexistente(t *testing.T) {
	srv := Novo(Opcoes{Banco: abrirBancoTemp(t)})
	rec := fazerReq(t, srv, http.MethodPost, "/api/v1/engines/999/accounts", map[string]any{"alias": "x"})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, quero 404", rec.Code)
	}
}

func TestCriarContaAliasObrigatorio(t *testing.T) {
	srv := Novo(Opcoes{Banco: abrirBancoTemp(t)})
	m := criarMotorAPI(t, srv, "claude")
	rec := fazerReq(t, srv, http.MethodPost, "/api/v1/engines/"+itoa(m.ID)+"/accounts",
		map[string]any{"config_dir": "x"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, quero 400", rec.Code)
	}
}

func TestCriarContaAliasDuplicado(t *testing.T) {
	srv := Novo(Opcoes{Banco: abrirBancoTemp(t)})
	m := criarMotorAPI(t, srv, "claude")
	base := "/api/v1/engines/" + itoa(m.ID) + "/accounts"
	fazerReq(t, srv, http.MethodPost, base, map[string]any{"alias": "principal"})
	rec := fazerReq(t, srv, http.MethodPost, base, map[string]any{"alias": "principal"})
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, quero 409", rec.Code)
	}
	if e := decodErro(t, rec); e.Erro.Codigo != "alias_duplicado" {
		t.Fatalf("codigo = %s, quero alias_duplicado", e.Erro.Codigo)
	}
}

func TestAtualizarContaDeOutroMotor404(t *testing.T) {
	srv := Novo(Opcoes{Banco: abrirBancoTemp(t)})
	m1 := criarMotorAPI(t, srv, "claude")
	m2 := criarMotorAPI(t, srv, "codex")
	rec := fazerReq(t, srv, http.MethodPost, "/api/v1/engines/"+itoa(m1.ID)+"/accounts",
		map[string]any{"alias": "principal"})
	c := decodConta(t, rec)
	// Tenta atualizar a conta de m1 sob a rota de m2.
	rec = fazerReq(t, srv, http.MethodPut, "/api/v1/engines/"+itoa(m2.ID)+"/accounts/"+itoa(c.ID),
		map[string]any{"alias": "x"})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, quero 404", rec.Code)
	}
}

func TestRemoverContaInexistente404(t *testing.T) {
	srv := Novo(Opcoes{Banco: abrirBancoTemp(t)})
	m := criarMotorAPI(t, srv, "claude")
	rec := fazerReq(t, srv, http.MethodDelete, "/api/v1/engines/"+itoa(m.ID)+"/accounts/999", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, quero 404", rec.Code)
	}
}
