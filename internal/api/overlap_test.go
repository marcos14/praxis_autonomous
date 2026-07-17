package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"testing"

	"github.com/marcos14/praxis-autonomous/internal/db"
)

// criarProjetoNomeado cria um projeto com um nome (e slug) distinto, para testes
// que precisam de mais de um projeto.
func criarProjetoNomeado(t *testing.T, srv *Servidor, nome string) int64 {
	t.Helper()
	rec := fazerReq(t, srv, http.MethodPost, "/api/v1/projects", map[string]any{
		"nome":  nome,
		"pasta": repoGitTemp(t),
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("criar projeto %q: status %d (corpo=%q)", nome, rec.Code, rec.Body.String())
	}
	return decodProjeto(t, rec).ID
}

// demandaComArquivos cria uma demanda (status pronta) e grava uma fala do
// analista com arquivos_provaveis, simulando o resultado da análise.
func demandaComArquivos(t *testing.T, banco *db.DB, projID int64, titulo string, arquivos []string) int64 {
	t.Helper()
	dem, err := banco.CriarDemanda(context.Background(), db.Demanda{
		ProjectID: projID, Titulo: titulo, Status: db.StatusDemandaPronta})
	if err != nil {
		t.Fatalf("criar demanda: %v", err)
	}
	meta, _ := json.Marshal(map[string]any{"arquivos_provaveis": arquivos})
	if _, err := banco.CriarMensagemChat(context.Background(), db.MensagemChat{
		DemandID: dem.ID, Papel: db.PapelAnalista, Conteudo: "análise", Meta: meta}); err != nil {
		t.Fatalf("chat analista: %v", err)
	}
	return dem.ID
}

func TestOverlapDetectaArquivoComum(t *testing.T) {
	banco := abrirBancoTemp(t)
	srv := Novo(Opcoes{Banco: banco})
	proj := criarProjetoTeste(t, srv)

	d1 := demandaComArquivos(t, banco, proj, "d1", []string{"internal/api/servidor.go", "web/js/app.js"})
	d2 := demandaComArquivos(t, banco, proj, "d2", []string{"internal/api/servidor.go", "outro.go"})

	// detalhe da d1 → deve listar a d2 com o arquivo comum.
	rec := fazerReq(t, srv, http.MethodGet, "/api/v1/demands/"+strconv.FormatInt(d1, 10)+"/overlap", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, quero 200 (corpo=%q)", rec.Code, rec.Body.String())
	}
	var sobre []Sobreposicao
	if err := json.Unmarshal(rec.Body.Bytes(), &sobre); err != nil {
		t.Fatalf("decodificar: %v", err)
	}
	if len(sobre) != 1 || sobre[0].DemandID != d2 {
		t.Fatalf("sobreposições = %+v, quero 1 apontando para d2 (%d)", sobre, d2)
	}
	if len(sobre[0].Arquivos) != 1 || sobre[0].Arquivos[0] != "internal/api/servidor.go" {
		t.Fatalf("arquivos comuns = %v, quero [internal/api/servidor.go]", sobre[0].Arquivos)
	}
}

func TestOverlapSemInterseccaoSemBadge(t *testing.T) {
	banco := abrirBancoTemp(t)
	srv := Novo(Opcoes{Banco: banco})
	proj := criarProjetoTeste(t, srv)

	d1 := demandaComArquivos(t, banco, proj, "d1", []string{"a.go"})
	demandaComArquivos(t, banco, proj, "d2", []string{"b.go"})

	rec := fazerReq(t, srv, http.MethodGet, "/api/v1/demands/"+strconv.FormatInt(d1, 10)+"/overlap", nil)
	var sobre []Sobreposicao
	_ = json.Unmarshal(rec.Body.Bytes(), &sobre)
	if len(sobre) != 0 {
		t.Fatalf("sem interseção deveria não ter sobreposição, got %+v", sobre)
	}
}

func TestOverlapsMapaGlobal(t *testing.T) {
	banco := abrirBancoTemp(t)
	srv := Novo(Opcoes{Banco: banco})
	proj := criarProjetoTeste(t, srv)

	d1 := demandaComArquivos(t, banco, proj, "d1", []string{"x.go"})
	d2 := demandaComArquivos(t, banco, proj, "d2", []string{"x.go"})

	rec := fazerReq(t, srv, http.MethodGet, "/api/v1/overlaps", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, quero 200", rec.Code)
	}
	var mapa map[string][]Sobreposicao
	if err := json.Unmarshal(rec.Body.Bytes(), &mapa); err != nil {
		t.Fatalf("decodificar: %v", err)
	}
	// ambas as demandas devem aparecer, apontando uma para a outra.
	if len(mapa[strconv.FormatInt(d1, 10)]) != 1 || len(mapa[strconv.FormatInt(d2, 10)]) != 1 {
		t.Fatalf("mapa = %+v, quero d1 e d2 se sobrepondo", mapa)
	}
}

// demandas de projetos diferentes NÃO se sobrepõem (branches independentes).
func TestOverlapProjetosDistintosNaoColidem(t *testing.T) {
	banco := abrirBancoTemp(t)
	srv := Novo(Opcoes{Banco: banco})
	p1 := criarProjetoNomeado(t, srv, "Projeto Alpha")
	p2 := criarProjetoNomeado(t, srv, "Projeto Beta")

	d1 := demandaComArquivos(t, banco, p1, "d1", []string{"comum.go"})
	demandaComArquivos(t, banco, p2, "d2", []string{"comum.go"})

	rec := fazerReq(t, srv, http.MethodGet, "/api/v1/demands/"+strconv.FormatInt(d1, 10)+"/overlap", nil)
	var sobre []Sobreposicao
	_ = json.Unmarshal(rec.Body.Bytes(), &sobre)
	if len(sobre) != 0 {
		t.Fatalf("projetos distintos não deveriam colidir, got %+v", sobre)
	}
}
