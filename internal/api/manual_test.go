package api

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestListarManual(t *testing.T) {
	srv := Novo(Opcoes{Banco: abrirBancoTemp(t)})
	rec := fazerReq(t, srv, http.MethodGet, "/api/v1/manual", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, quero 200", rec.Code)
	}
	var secoes []SecaoManual
	if err := json.Unmarshal(rec.Body.Bytes(), &secoes); err != nil {
		t.Fatalf("decodificar: %v", err)
	}
	if len(secoes) < 5 {
		t.Fatalf("seções = %d, quero >= 5", len(secoes))
	}
	// a listagem não traz o conteúdo (só slug/titulo).
	for _, s := range secoes {
		if s.Slug == "" || s.Titulo == "" {
			t.Fatalf("seção sem slug/titulo: %+v", s)
		}
		if s.Conteudo != "" {
			t.Fatalf("listagem não deveria trazer conteúdo: %+v", s)
		}
	}
}

func TestSecaoManualConteudo(t *testing.T) {
	srv := Novo(Opcoes{Banco: abrirBancoTemp(t)})
	// pega a primeira seção da lista e busca seu conteúdo.
	rec := fazerReq(t, srv, http.MethodGet, "/api/v1/manual", nil)
	var secoes []SecaoManual
	_ = json.Unmarshal(rec.Body.Bytes(), &secoes)
	if len(secoes) == 0 {
		t.Fatal("sem seções")
	}
	slug := secoes[0].Slug

	rec = fazerReq(t, srv, http.MethodGet, "/api/v1/manual/"+slug, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, quero 200", rec.Code)
	}
	var sec SecaoManual
	if err := json.Unmarshal(rec.Body.Bytes(), &sec); err != nil {
		t.Fatalf("decodificar: %v", err)
	}
	if sec.Conteudo == "" {
		t.Fatal("conteúdo da seção vazio")
	}
	// o título não deve conter a marcação markdown "# ".
	if len(sec.Titulo) == 0 || sec.Titulo[0] == '#' {
		t.Fatalf("título não foi extraído: %q", sec.Titulo)
	}
}

func TestSecaoManualInexistente(t *testing.T) {
	srv := Novo(Opcoes{Banco: abrirBancoTemp(t)})
	rec := fazerReq(t, srv, http.MethodGet, "/api/v1/manual/nao-existe", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, quero 404", rec.Code)
	}
}
