package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/marcos14/praxis-autonomous/internal/db"
)

// consultorFake captura os disparos do seam ConsultorSvc e resolve as pastas de
// trabalho numa raiz temporária (os handlers disparam na mesma goroutine do
// teste — sem corrida). Com raiz vazia, Pasta devolve "" (serviço sem pasta de
// trabalho: as rotas de anexo respondem 503).
type consultorFake struct {
	respostas []int64
	overviews []int64
	raiz      string
}

func (f *consultorFake) DispararResposta(id int64) { f.respostas = append(f.respostas, id) }
func (f *consultorFake) DispararOverview(id int64) { f.overviews = append(f.overviews, id) }
func (f *consultorFake) Pasta(id int64) string {
	if f.raiz == "" {
		return ""
	}
	return filepath.Join(f.raiz, fmt.Sprintf("c%d", id))
}

func decodGrupo(t *testing.T, rec *httptest.ResponseRecorder) db.Grupo {
	t.Helper()
	var g db.Grupo
	if err := json.Unmarshal(rec.Body.Bytes(), &g); err != nil {
		t.Fatalf("decodificar grupo: %v (corpo=%q)", err, rec.Body.String())
	}
	return g
}

func decodConsulta(t *testing.T, rec *httptest.ResponseRecorder) db.Consulta {
	t.Helper()
	var c db.Consulta
	if err := json.Unmarshal(rec.Body.Bytes(), &c); err != nil {
		t.Fatalf("decodificar consulta: %v (corpo=%q)", err, rec.Body.String())
	}
	return c
}

func TestGruposCRUDViaAPI(t *testing.T) {
	srv := Novo(Opcoes{Banco: abrirBancoTemp(t)})
	p1 := criarProjetoTeste(t, srv)
	rec := fazerReq(t, srv, http.MethodPost, "/api/v1/projects", map[string]any{
		"nome": "Proj Web", "pasta": repoGitTemp(t),
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("criar 2º projeto: status %d", rec.Code)
	}
	p2 := decodProjeto(t, rec).ID

	// criar (p2 primeiro = principal).
	rec = fazerReq(t, srv, http.MethodPost, "/api/v1/groups", map[string]any{
		"nome": "Vulcano Chat", "descricao": "Solução de chat", "project_ids": []int64{p2, p1},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("criar grupo: status %d (corpo=%q)", rec.Code, rec.Body.String())
	}
	g := decodGrupo(t, rec)
	if g.Slug != "vulcano-chat" || len(g.Membros) != 2 || g.Membros[0].ProjectID != p2 {
		t.Fatalf("grupo criado = %+v", g)
	}

	// sem membros → 400.
	rec = fazerReq(t, srv, http.MethodPost, "/api/v1/groups", map[string]any{
		"nome": "Vazio", "project_ids": []int64{},
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("grupo sem membros: status %d, quero 400", rec.Code)
	}

	// atualizar: renomeia e inverte a ordem.
	rec = fazerReq(t, srv, http.MethodPut, "/api/v1/groups/"+strconv.FormatInt(g.ID, 10), map[string]any{
		"nome": "Vulcano", "project_ids": []int64{p1, p2},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("atualizar grupo: status %d (corpo=%q)", rec.Code, rec.Body.String())
	}
	g2 := decodGrupo(t, rec)
	if g2.Nome != "Vulcano" || g2.Membros[0].ProjectID != p1 {
		t.Fatalf("grupo atualizado = %+v", g2)
	}

	// listar.
	rec = fazerReq(t, srv, http.MethodGet, "/api/v1/groups", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Vulcano") {
		t.Fatalf("listar grupos: status %d corpo=%q", rec.Code, rec.Body.String())
	}

	// excluir.
	rec = fazerReq(t, srv, http.MethodDelete, "/api/v1/groups/"+strconv.FormatInt(g.ID, 10), nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("excluir grupo: status %d", rec.Code)
	}
	rec = fazerReq(t, srv, http.MethodGet, "/api/v1/groups/"+strconv.FormatInt(g.ID, 10), nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("grupo excluído ainda responde: status %d", rec.Code)
	}
}

func TestConsultaCriarConversarE409EnquantoPensa(t *testing.T) {
	banco := abrirBancoTemp(t)
	fake := &consultorFake{}
	srv := Novo(Opcoes{Banco: banco, Consultas: fake})
	proj := criarProjetoTeste(t, srv)

	// criar: nasce pensando e dispara o consultor.
	rec := fazerReq(t, srv, http.MethodPost, "/api/v1/consultas", map[string]any{
		"project_id": proj, "mensagem": "Como funciona a baixa de títulos?",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("criar consulta: status %d (corpo=%q)", rec.Code, rec.Body.String())
	}
	cons := decodConsulta(t, rec)
	if cons.Status != db.StatusConsultaPensando {
		t.Fatalf("status = %q, quero pensando", cons.Status)
	}
	if cons.Titulo == "" {
		t.Fatal("título não derivado da mensagem")
	}
	if len(fake.respostas) != 1 || fake.respostas[0] != cons.ID {
		t.Fatalf("DispararResposta não chamado: %v", fake.respostas)
	}

	// projeto E grupo → 400; nenhum → 400.
	rec = fazerReq(t, srv, http.MethodPost, "/api/v1/consultas", map[string]any{
		"project_id": proj, "group_id": 1, "mensagem": "x",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("alvo duplo: status %d, quero 400", rec.Code)
	}
	rec = fazerReq(t, srv, http.MethodPost, "/api/v1/consultas", map[string]any{"mensagem": "x"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("sem alvo: status %d, quero 400", rec.Code)
	}

	// nova fala enquanto pensando → 409.
	rota := "/api/v1/consultas/" + strconv.FormatInt(cons.ID, 10)
	rec = fazerReq(t, srv, http.MethodPost, rota+"/chat", map[string]any{"conteudo": "complemento"})
	if rec.Code != http.StatusConflict {
		t.Fatalf("chat durante pensando: status %d, quero 409", rec.Code)
	}

	// consultor terminou (ociosa) → nova fala aceita e re-dispara.
	cons.Status = db.StatusConsultaOciosa
	if _, err := banco.AtualizarConsulta(context.Background(), cons); err != nil {
		t.Fatalf("marcar ociosa: %v", err)
	}
	rec = fazerReq(t, srv, http.MethodPost, rota+"/chat", map[string]any{"conteudo": "é para o cliente X"})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("chat após ociosa: status %d (corpo=%q)", rec.Code, rec.Body.String())
	}
	if len(fake.respostas) != 2 {
		t.Fatalf("segundo turno não disparado: %v", fake.respostas)
	}
	depois, _ := banco.ObterConsulta(context.Background(), cons.ID)
	if depois.Status != db.StatusConsultaPensando {
		t.Fatalf("status após fala = %q, quero pensando", depois.Status)
	}

	// chat lista as duas falas do usuário.
	rec = fazerReq(t, srv, http.MethodGet, rota+"/chat", nil)
	var msgs []db.MensagemConsulta
	if err := json.Unmarshal(rec.Body.Bytes(), &msgs); err != nil {
		t.Fatalf("decodificar chat: %v", err)
	}
	if len(msgs) != 2 || msgs[0].Papel != db.PapelConsultaUser {
		t.Fatalf("chat = %+v, quero 2 falas do user", msgs)
	}

	// excluir (bootstrap = admin local).
	rec = fazerReq(t, srv, http.MethodDelete, rota, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("excluir consulta: status %d (corpo=%q)", rec.Code, rec.Body.String())
	}
}

func TestConsultasRBAC(t *testing.T) {
	srv := Novo(Opcoes{Banco: abrirBancoTemp(t)})
	proj := criarProjetoTeste(t, srv)
	admin := setupAdmin(t, srv)

	// papel "suporte": só consultas.usar.
	rec := fazerReqToken(t, srv, http.MethodPost, "/api/v1/roles", admin, map[string]any{
		"nome": "suporte", "permissoes": []string{"consultas.usar"},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("criar papel suporte: status %d (corpo=%q)", rec.Code, rec.Body.String())
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
		t.Fatalf("criar usuário suporte: status %d (corpo=%q)", rec.Code, rec.Body.String())
	}
	suporte := loginToken(t, srv, "sup@x.com", "senha-forte-123")

	// PERMITIDO: criar consulta e conversar.
	rec = fazerReqToken(t, srv, http.MethodPost, "/api/v1/consultas", suporte, map[string]any{
		"project_id": proj, "mensagem": "como funciona a cobrança?",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("suporte criar consulta: status %d (corpo=%q)", rec.Code, rec.Body.String())
	}
	cons := decodConsulta(t, rec)
	if cons.CriadoPor == nil {
		t.Fatal("criado_por não registrado")
	}

	// NEGADO: criar projeto, grupo e demanda.
	rec = fazerReqToken(t, srv, http.MethodPost, "/api/v1/projects", suporte,
		map[string]any{"nome": "x", "pasta": repoGitTemp(t)})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("suporte criar projeto: status %d, quero 403", rec.Code)
	}
	rec = fazerReqToken(t, srv, http.MethodPost, "/api/v1/groups", suporte,
		map[string]any{"nome": "g", "project_ids": []int64{proj}})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("suporte criar grupo: status %d, quero 403", rec.Code)
	}
	rec = fazerReqToken(t, srv, http.MethodPost,
		"/api/v1/projects/"+strconv.FormatInt(proj, 10)+"/demands", suporte,
		map[string]any{"prd": "quero X"})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("suporte criar demanda: status %d, quero 403", rec.Code)
	}

	// NEGADO: excluir consulta de outro (o suporte criou; um segundo usuário sem
	// curinga não pode excluir).
	rec = fazerReqToken(t, srv, http.MethodPost, "/api/v1/users", admin, map[string]any{
		"nome": "Sup2", "email": "sup2@x.com", "senha": "senha-forte-123", "ativo": true,
		"papeis": []int64{papel.ID},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("criar 2º suporte: status %d", rec.Code)
	}
	suporte2 := loginToken(t, srv, "sup2@x.com", "senha-forte-123")
	rota := "/api/v1/consultas/" + strconv.FormatInt(cons.ID, 10)
	// Privada por default (M2): para o outro usuário a consulta nem existe.
	rec = fazerReqToken(t, srv, http.MethodDelete, rota, suporte2, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("excluir consulta privada alheia: status %d, quero 404", rec.Code)
	}
	// Tornada pública pelo criador, o outro a vê — mas continua sem poder excluir.
	rec = fazerReqToken(t, srv, http.MethodPut, rota+"/visibilidade", suporte, map[string]any{"visibilidade": "publica"})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("tornar pública: status %d (corpo=%q)", rec.Code, rec.Body.String())
	}
	rec = fazerReqToken(t, srv, http.MethodDelete, rota, suporte2, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("excluir consulta alheia: status %d, quero 403", rec.Code)
	}
	// o criador pode.
	rec = fazerReqToken(t, srv, http.MethodDelete, rota, suporte, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("criador excluir a própria consulta: status %d (corpo=%q)", rec.Code, rec.Body.String())
	}

	// papel sem consultas.usar NÃO cria consulta.
	rec = fazerReqToken(t, srv, http.MethodPost, "/api/v1/roles", admin, map[string]any{
		"nome": "criador", "permissoes": []string{"demandas.criar"},
	})
	_ = json.Unmarshal(rec.Body.Bytes(), &papel)
	rec = fazerReqToken(t, srv, http.MethodPost, "/api/v1/users", admin, map[string]any{
		"nome": "Dev", "email": "dev@x.com", "senha": "senha-forte-123", "ativo": true,
		"papeis": []int64{papel.ID},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("criar usuário dev: status %d", rec.Code)
	}
	dev := loginToken(t, srv, "dev@x.com", "senha-forte-123")
	rec = fazerReqToken(t, srv, http.MethodPost, "/api/v1/consultas", dev, map[string]any{
		"project_id": proj, "mensagem": "x",
	})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("dev criar consulta: status %d, quero 403", rec.Code)
	}
}

func TestOverviewSalvarEGerar(t *testing.T) {
	fake := &consultorFake{}
	srv := Novo(Opcoes{Banco: abrirBancoTemp(t), Consultas: fake})
	proj := criarProjetoTeste(t, srv)
	rota := "/api/v1/projects/" + strconv.FormatInt(proj, 10) + "/overview"

	rec := fazerReq(t, srv, http.MethodPut, rota, map[string]any{
		"overview_md": "## Objetivo\nCobrança de títulos.",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("salvar overview: status %d (corpo=%q)", rec.Code, rec.Body.String())
	}
	p := decodProjeto(t, rec)
	if !strings.Contains(p.OverviewMD, "Cobrança de títulos") || p.OverviewEm == "" {
		t.Fatalf("overview não persistido: %+v", p)
	}

	rec = fazerReq(t, srv, http.MethodPost, rota+"/gerar", nil)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("gerar overview: status %d (corpo=%q)", rec.Code, rec.Body.String())
	}
	if len(fake.overviews) != 1 || fake.overviews[0] != proj {
		t.Fatalf("DispararOverview não chamado: %v", fake.overviews)
	}

	// sem serviço de consultas → 503.
	srvSem := Novo(Opcoes{Banco: abrirBancoTemp(t)})
	projSem := criarProjetoTeste(t, srvSem)
	rec = fazerReq(t, srvSem, http.MethodPost,
		"/api/v1/projects/"+strconv.FormatInt(projSem, 10)+"/overview/gerar", nil)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("gerar sem serviço: status %d, quero 503", rec.Code)
	}
}

// TestProgressoSSESanitizado é o teste de segurança do canal ao vivo: o .jsonl
// contém código-fonte (input/output das tools e texto do assistente) e NADA
// disso pode chegar ao cliente — só os resumos allowlisted.
func TestProgressoSSESanitizado(t *testing.T) {
	banco := abrirBancoTemp(t)
	srv := Novo(Opcoes{Banco: banco})
	srv.intervaloPollLog = 5 * time.Millisecond
	proj := criarProjetoTeste(t, srv)

	ctx := context.Background()
	cons, _, err := banco.CriarConsultaComChat(ctx,
		db.Consulta{ProjectID: &proj, Status: db.StatusConsultaPensando},
		db.MensagemConsulta{Conteudo: "como funciona a baixa?"})
	if err != nil {
		t.Fatalf("criar consulta: %v", err)
	}

	logPath := filepath.Join(t.TempDir(), "consultor-c1.jsonl")
	linhas := []string{
		`{"type":"system","subtype":"init","cwd":"C:\\repos\\erp"}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Read","input":{"file_path":"C:\\repos\\erp\\internal\\financeiro\\baixa.go"}}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","content":"func Baixar(t Titulo) error { chave := \"SEGREDO-XYZ\" }"}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"vi que func Baixar usa chave SEGREDO-XYZ"}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"git log --oneline"}}]}}`,
		`{"type":"result","subtype":"success","is_error":false,"result":"{\"tipo\":\"resposta\"}"}`,
	}
	if err := os.WriteFile(logPath, []byte(strings.Join(linhas, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("escrever log: %v", err)
	}
	exec, err := banco.CriarExecucaoConsulta(ctx, db.ExecucaoConsulta{
		ConsultaID: &cons.ID, Operacao: db.OperacaoConsultor, Engine: "claude",
	})
	if err != nil {
		t.Fatalf("criar execução: %v", err)
	}
	exec.LogRef = logPath
	if _, err := banco.AtualizarExecucaoConsulta(ctx, exec); err != nil {
		t.Fatalf("gravar log_ref: %v", err)
	}

	reqCtx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/consultas/"+strconv.FormatInt(cons.ID, 10)+"/progresso", nil).WithContext(reqCtx)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	corpo := rec.Body.String()
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("Content-Type = %q", ct)
	}
	// O que DEVE aparecer: resumos seguros.
	if !strings.Contains(corpo, "lendo baixa.go") {
		t.Fatalf("progresso sem 'lendo baixa.go':\n%s", corpo)
	}
	if !strings.Contains(corpo, "executando verificação") {
		t.Fatalf("progresso sem resumo do Bash:\n%s", corpo)
	}
	if !strings.Contains(corpo, `"acao":"concluindo"`) {
		t.Fatalf("progresso sem concluindo:\n%s", corpo)
	}
	// O que NÃO PODE aparecer: conteúdo de arquivo, texto do assistente, caminho
	// completo, comando do Bash.
	for _, vazamento := range []string{"SEGREDO-XYZ", "func Baixar", "repos", "git log", "financeiro"} {
		if strings.Contains(corpo, vazamento) {
			t.Fatalf("VAZOU %q no progresso:\n%s", vazamento, corpo)
		}
	}
}

func TestResumirLinhaStreamFailClosed(t *testing.T) {
	casos := []string{
		"",
		"não é json",
		`{"type":"user","message":{"content":[{"type":"tool_result","content":"código aqui"}]}}`,
		`{"type":"system","subtype":"init"}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"texto do assistente com código"}]}}`,
	}
	for _, c := range casos {
		if resumo, ok := resumirLinhaStream(c); ok {
			t.Fatalf("linha %q não deveria produzir resumo (produziu %q)", c, resumo)
		}
	}
	// Read sem input reconhecível ainda produz resumo genérico (sem vazar nada).
	resumo, ok := resumirLinhaStream(`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Read","input":{}}]}}`)
	if !ok || !strings.Contains(resumo, "lendo arquivos do projeto") {
		t.Fatalf("Read sem alvo: %q ok=%v", resumo, ok)
	}
	// Tool desconhecida vira resumo genérico "analisando".
	resumo, ok = resumirLinhaStream(`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"WebFetch","input":{"url":"http://interno"}}]}}`)
	if !ok || strings.Contains(resumo, "interno") {
		t.Fatalf("tool desconhecida: %q ok=%v", resumo, ok)
	}
}

func TestArquivosAnexadosNaConsulta(t *testing.T) {
	banco := abrirBancoTemp(t)
	fake := &consultorFake{raiz: t.TempDir()}
	srv := Novo(Opcoes{Banco: banco, Consultas: fake})
	projID := criarProjetoTeste(t, srv)

	cons, _, err := banco.CriarConsultaComChat(context.Background(),
		db.Consulta{ProjectID: &projID}, db.MensagemConsulta{Conteudo: "x"})
	if err != nil {
		t.Fatal(err)
	}
	base := fmt.Sprintf("/api/v1/consultas/%d/referencias", cons.ID)

	// Upload válido (nome com acento e espaço, como arquivos reais).
	rec := fazerUpload(t, srv, base, "E-mail do cliente.md", []byte("## Caso\n\nO cliente relata X."))
	if rec.Code != 201 {
		t.Fatalf("upload = %d (%s), quero 201", rec.Code, rec.Body.String())
	}
	// Extensão proibida é recusada.
	if rec = fazerUpload(t, srv, base, "virus.exe", []byte("x")); rec.Code != 400 {
		t.Fatalf("extensão proibida = %d, quero 400", rec.Code)
	}
	// Nome com traversal é neutralizado (filepath.Base) — nada sai da pasta.
	rec = fazerUpload(t, srv, base, `..\..\evil.md`, []byte("x"))
	if rec.Code == 201 {
		if _, err := os.Stat(filepath.Join(fake.Pasta(cons.ID), "referencias", "evil.md")); err != nil {
			t.Fatalf("upload com traversal não foi neutralizado (%d)", rec.Code)
		}
	}

	// O anexo vira fala de sistema (o consultor fica sabendo no próximo turno).
	msgs, _ := banco.ListarMensagensConsulta(context.Background(), cons.ID)
	temFala := false
	for _, m := range msgs {
		if m.Papel == db.PapelConsultaSistema && strings.Contains(m.Conteudo, "E-mail do cliente.md") {
			temFala = true
		}
	}
	if !temFala {
		t.Fatalf("anexo não virou fala de sistema: %+v", msgs)
	}

	// Listagem devolve o arquivo com tamanho.
	rec = fazerReq(t, srv, "GET", base, nil)
	if rec.Code != 200 {
		t.Fatalf("listar = %d", rec.Code)
	}
	var refs []respReferencia
	_ = json.Unmarshal(rec.Body.Bytes(), &refs)
	achou := false
	for _, ref := range refs {
		if ref.Arquivo == "E-mail do cliente.md" && ref.Tamanho > 0 {
			achou = true
		}
	}
	if !achou {
		t.Fatalf("listagem = %+v, quero o e-mail com tamanho", refs)
	}

	// Download sempre como attachment (anexo nunca é exibido no domínio).
	url := base + "/E-mail%20do%20cliente.md"
	rec = fazerReq(t, srv, "GET", url, nil)
	if rec.Code != 200 {
		t.Fatalf("download = %d (%s)", rec.Code, rec.Body.String())
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.HasPrefix(cd, "attachment") {
		t.Fatalf("Content-Disposition = %q, quero attachment", cd)
	}
	if !strings.Contains(rec.Body.String(), "O cliente relata X.") {
		t.Fatalf("conteúdo divergente: %q", rec.Body.String())
	}

	// Exclusão remove o arquivo.
	if rec = fazerReq(t, srv, "DELETE", url, nil); rec.Code != 204 {
		t.Fatalf("excluir = %d, quero 204", rec.Code)
	}
	if rec = fazerReq(t, srv, "GET", url, nil); rec.Code != 404 {
		t.Fatalf("baixar excluído = %d, quero 404", rec.Code)
	}

	// Excluir a consulta leva a pasta de trabalho junto.
	pasta := fake.Pasta(cons.ID)
	if rec = fazerReq(t, srv, "DELETE", fmt.Sprintf("/api/v1/consultas/%d", cons.ID), nil); rec.Code != 204 {
		t.Fatalf("excluir consulta = %d, quero 204", rec.Code)
	}
	if _, err := os.Stat(pasta); !os.IsNotExist(err) {
		t.Fatalf("pasta da consulta sobreviveu à exclusão: %v", err)
	}
}

// Sem pasta de trabalho (serviço sem PRAXIS_HOME) as rotas de anexo respondem
// 503 em vez de gravar em lugar nenhum.
func TestAnexosDeConsultaSemPastaRespondem503(t *testing.T) {
	banco := abrirBancoTemp(t)
	srv := Novo(Opcoes{Banco: banco, Consultas: &consultorFake{}})
	projID := criarProjetoTeste(t, srv)
	cons, _, err := banco.CriarConsultaComChat(context.Background(),
		db.Consulta{ProjectID: &projID}, db.MensagemConsulta{Conteudo: "x"})
	if err != nil {
		t.Fatal(err)
	}
	base := fmt.Sprintf("/api/v1/consultas/%d/referencias", cons.ID)
	if rec := fazerReq(t, srv, "GET", base, nil); rec.Code != 503 {
		t.Fatalf("listar sem pasta = %d, quero 503", rec.Code)
	}
	if rec := fazerUpload(t, srv, base, "ata.md", []byte("x")); rec.Code != 503 {
		t.Fatalf("upload sem pasta = %d, quero 503", rec.Code)
	}
}

func TestCriacaoDeConsultaComAnexosPendentesSeguraOTurno(t *testing.T) {
	banco := abrirBancoTemp(t)
	fake := &consultorFake{raiz: t.TempDir()}
	srv := Novo(Opcoes{Banco: banco, Consultas: fake})
	projID := criarProjetoTeste(t, srv)

	// Com anexos pendentes: nasce ociosa e NÃO dispara o consultor.
	rec := fazerReq(t, srv, "POST", "/api/v1/consultas", map[string]any{
		"project_id": projID, "mensagem": "veja o e-mail anexado", "anexos_pendentes": true,
	})
	if rec.Code != 201 {
		t.Fatalf("criar = %d (%s)", rec.Code, rec.Body.String())
	}
	cons := decodConsulta(t, rec)
	if cons.Status != db.StatusConsultaOciosa {
		t.Fatalf("status = %q, quero ociosa (turno segurado)", cons.Status)
	}
	if len(fake.respostas) != 0 {
		t.Fatalf("respostas = %v, quero nenhuma antes dos anexos", fake.respostas)
	}

	// Sobe o arquivo e dispara o turno explicitamente.
	rec = fazerUpload(t, srv, fmt.Sprintf("/api/v1/consultas/%d/referencias", cons.ID),
		"email.md", []byte("## Caso"))
	if rec.Code != 201 {
		t.Fatalf("upload = %d (%s)", rec.Code, rec.Body.String())
	}
	rec = fazerReq(t, srv, "POST", fmt.Sprintf("/api/v1/consultas/%d/turno", cons.ID), map[string]any{})
	if rec.Code != 202 {
		t.Fatalf("disparar turno = %d (%s), quero 202", rec.Code, rec.Body.String())
	}
	if len(fake.respostas) != 1 || fake.respostas[0] != cons.ID {
		t.Fatalf("respostas = %v, quero o turno adiado", fake.respostas)
	}
	got, _ := banco.ObterConsulta(context.Background(), cons.ID)
	if got.Status != db.StatusConsultaPensando {
		t.Fatalf("status após o disparo = %q, quero pensando", got.Status)
	}
	// Turno em voo: novo disparo é recusado com 409.
	if rec = fazerReq(t, srv, "POST", fmt.Sprintf("/api/v1/consultas/%d/turno", cons.ID), map[string]any{}); rec.Code != 409 {
		t.Fatalf("disparo com turno em voo = %d, quero 409", rec.Code)
	}
}
