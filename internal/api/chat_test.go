package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/marcos14/praxis-autonomous/internal/db"
)

// decodMensagem decodifica uma MensagemChat do corpo da resposta.
func decodMensagem(t *testing.T, rec *httptest.ResponseRecorder) db.MensagemChat {
	t.Helper()
	var m db.MensagemChat
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("decodificar mensagem: %v (corpo=%q)", err, rec.Body.String())
	}
	return m
}

// criarDemandaChatTeste cria uma demanda via chat (PRD) e devolve a resposta.
func criarDemandaChatTeste(t *testing.T, srv *Servidor, projID int64, titulo, prd string) respDemanda {
	t.Helper()
	rec := fazerReq(t, srv, http.MethodPost, "/api/v1/projects/"+strconv.FormatInt(projID, 10)+"/demands",
		map[string]any{"titulo": titulo, "prd": prd})
	if rec.Code != http.StatusCreated {
		t.Fatalf("criar demanda chat: status %d (corpo=%q)", rec.Code, rec.Body.String())
	}
	return decodDemanda(t, rec)
}

func TestCriarDemandaViaChatNasceRecebida(t *testing.T) {
	srv := Novo(Opcoes{Banco: abrirBancoTemp(t)})
	proj := criarProjetoTeste(t, srv)

	d := criarDemandaChatTeste(t, srv, proj, "Boleto híbrido", "Incluir QR Code PIX no boleto")
	if d.ID == 0 {
		t.Fatal("demanda sem id")
	}
	if d.Status != db.StatusDemandaRecebida {
		t.Fatalf("status = %q, quero recebida", d.Status)
	}
	if d.Origem != db.OrigemUI {
		t.Fatalf("origem = %q, quero ui (default do chat)", d.Origem)
	}
	if len(d.Fases) != 0 {
		t.Fatalf("fases = %d, quero 0 (nasce sem fases)", len(d.Fases))
	}

	// o PRD virou a primeira mensagem do chat (papel user).
	rec := fazerReq(t, srv, http.MethodGet, "/api/v1/demands/"+strconv.FormatInt(d.ID, 10)+"/chat", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET chat: status %d (corpo=%q)", rec.Code, rec.Body.String())
	}
	var msgs []db.MensagemChat
	if err := json.Unmarshal(rec.Body.Bytes(), &msgs); err != nil {
		t.Fatalf("decodificar chat: %v (corpo=%q)", err, rec.Body.String())
	}
	if len(msgs) != 1 || msgs[0].Papel != db.PapelUser || msgs[0].Conteudo != "Incluir QR Code PIX no boleto" {
		t.Fatalf("chat = %+v, quero o PRD como mensagem do user", msgs)
	}
}

func TestCriarDemandaViaChatDerivaTitulo(t *testing.T) {
	srv := Novo(Opcoes{Banco: abrirBancoTemp(t)})
	proj := criarProjetoTeste(t, srv)

	// sem título → deriva da primeira linha do PRD (sem a marcação markdown).
	rec := fazerReq(t, srv, http.MethodPost, "/api/v1/projects/"+strconv.FormatInt(proj, 10)+"/demands",
		map[string]any{"prd": "# Split de recebimento\n\nDetalhes do chamado…"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, quero 201 (corpo=%q)", rec.Code, rec.Body.String())
	}
	d := decodDemanda(t, rec)
	if d.Titulo != "Split de recebimento" {
		t.Fatalf("titulo = %q, quero derivado do PRD", d.Titulo)
	}
}

func TestCriarDemandaSemFasesSemPRD400(t *testing.T) {
	srv := Novo(Opcoes{Banco: abrirBancoTemp(t)})
	proj := criarProjetoTeste(t, srv)
	rec := fazerReq(t, srv, http.MethodPost, "/api/v1/projects/"+strconv.FormatInt(proj, 10)+"/demands",
		map[string]any{"titulo": "vazia"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, quero 400 (corpo=%q)", rec.Code, rec.Body.String())
	}
}

func TestPostChatAcrescentaMensagemDoUsuario(t *testing.T) {
	srv := Novo(Opcoes{Banco: abrirBancoTemp(t)})
	proj := criarProjetoTeste(t, srv)
	d := criarDemandaChatTeste(t, srv, proj, "Demanda", "PRD inicial")

	rec := fazerReq(t, srv, http.MethodPost, "/api/v1/demands/"+strconv.FormatInt(d.ID, 10)+"/chat",
		map[string]any{"conteudo": "mais um detalhe"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST chat: status %d (corpo=%q)", rec.Code, rec.Body.String())
	}
	m := decodMensagem(t, rec)
	if m.ID == 0 || m.Papel != db.PapelUser || m.Conteudo != "mais um detalhe" {
		t.Fatalf("mensagem = %+v, quero fala do user persistida", m)
	}

	// agora o chat tem 2 mensagens (PRD + complemento), em ordem.
	rec = fazerReq(t, srv, http.MethodGet, "/api/v1/demands/"+strconv.FormatInt(d.ID, 10)+"/chat", nil)
	var msgs []db.MensagemChat
	if err := json.Unmarshal(rec.Body.Bytes(), &msgs); err != nil {
		t.Fatalf("decodificar chat: %v", err)
	}
	if len(msgs) != 2 || msgs[1].Conteudo != "mais um detalhe" {
		t.Fatalf("chat = %+v, quero 2 mensagens em ordem", msgs)
	}
}

func TestPostChatConteudoObrigatorio(t *testing.T) {
	srv := Novo(Opcoes{Banco: abrirBancoTemp(t)})
	proj := criarProjetoTeste(t, srv)
	d := criarDemandaChatTeste(t, srv, proj, "Demanda", "PRD")

	rec := fazerReq(t, srv, http.MethodPost, "/api/v1/demands/"+strconv.FormatInt(d.ID, 10)+"/chat",
		map[string]any{"conteudo": "   "})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, quero 400 (corpo=%q)", rec.Code, rec.Body.String())
	}
}

func TestPostChatDemandaInexistente(t *testing.T) {
	srv := Novo(Opcoes{Banco: abrirBancoTemp(t)})
	rec := fazerReq(t, srv, http.MethodPost, "/api/v1/demands/999/chat",
		map[string]any{"conteudo": "x"})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, quero 404 (corpo=%q)", rec.Code, rec.Body.String())
	}
}
