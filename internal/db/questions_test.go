package db

import (
	"context"
	"errors"
	"testing"
)

// criarDemandaTesteQ cria uma demanda simples para os testes de perguntas.
func criarDemandaTesteQ(t *testing.T, d *DB, sufixo string) int64 {
	t.Helper()
	proj := criarProjetoTeste(t, d, sufixo)
	dem, err := d.CriarDemanda(context.Background(), Demanda{ProjectID: proj, Titulo: "Demanda " + sufixo})
	if err != nil {
		t.Fatalf("criar demanda de teste: %v", err)
	}
	return dem.ID
}

func TestSubstituirEListarPerguntas(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()
	dem := criarDemandaTesteQ(t, d, "q1")

	criadas, err := d.SubstituirPerguntas(ctx, dem, []Pergunta{
		{Pergunta: "Rateio por percentual ou item?", Contexto: "titulo único por nota",
			Tipo: "escolha", Opcoes: []string{"percentual", "item"}, Sugestao: "percentual", Impacto: ImpactoAlto},
		{Pergunta: "Migrar notas antigas?", Tipo: "texto"},
	})
	if err != nil {
		t.Fatalf("SubstituirPerguntas: %v", err)
	}
	if len(criadas) != 2 {
		t.Fatalf("len criadas = %d, quero 2", len(criadas))
	}
	if criadas[0].Ordem != 1 || criadas[1].Ordem != 2 {
		t.Fatalf("ordem reatribuída errada: %d, %d", criadas[0].Ordem, criadas[1].Ordem)
	}
	if criadas[0].ID == 0 {
		t.Fatal("id não preenchido")
	}

	got, err := d.ListarPerguntas(ctx, dem)
	if err != nil {
		t.Fatalf("ListarPerguntas: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len listadas = %d, quero 2", len(got))
	}
	if len(got[0].Opcoes) != 2 || got[0].Opcoes[0] != "percentual" {
		t.Fatalf("opcoes mal (des)serializadas: %+v", got[0].Opcoes)
	}
	if got[1].Opcoes == nil {
		t.Fatal("opcoes deveria ser [] e não nil")
	}

	// Substituir de novo troca TODO o conjunto (não acumula).
	novas, err := d.SubstituirPerguntas(ctx, dem, []Pergunta{{Pergunta: "só uma agora"}})
	if err != nil {
		t.Fatalf("SubstituirPerguntas 2: %v", err)
	}
	if len(novas) != 1 {
		t.Fatalf("len após substituir = %d, quero 1", len(novas))
	}
	todas, _ := d.ListarPerguntas(ctx, dem)
	if len(todas) != 1 {
		t.Fatalf("perguntas acumularam: len = %d, quero 1", len(todas))
	}
}

func TestSubstituirPerguntasDemandaInexistente(t *testing.T) {
	d := abrirTemp(t)
	_, err := d.SubstituirPerguntas(context.Background(), 9999, []Pergunta{{Pergunta: "x"}})
	if !errors.Is(err, ErrNaoEncontrado) {
		t.Fatalf("demanda inexistente: err = %v, quero ErrNaoEncontrado", err)
	}
}

func TestResponderPerguntas(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()
	dem := criarDemandaTesteQ(t, d, "q2")
	criadas, err := d.SubstituirPerguntas(ctx, dem, []Pergunta{
		{Pergunta: "P1"}, {Pergunta: "P2"},
	})
	if err != nil {
		t.Fatalf("SubstituirPerguntas: %v", err)
	}

	n, err := d.ResponderPerguntas(ctx, dem, []RespostaPergunta{
		{ID: criadas[0].ID, Resposta: "resposta 1"},
		{ID: criadas[1].ID, Resposta: "  resposta 2  "},
	})
	if err != nil {
		t.Fatalf("ResponderPerguntas: %v", err)
	}
	if n != 2 {
		t.Fatalf("respondidas = %d, quero 2", n)
	}

	got, _ := d.ListarPerguntas(ctx, dem)
	if got[0].Resposta != "resposta 1" || got[0].RespondidaEm == "" {
		t.Fatalf("P1 não respondida: %+v", got[0])
	}
	if got[1].Resposta != "resposta 2" { // aparado
		t.Fatalf("P2 resposta = %q, quero aparada", got[1].Resposta)
	}
}

func TestResponderPerguntasIgnoraOutraDemanda(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()
	demA := criarDemandaTesteQ(t, d, "qa")
	demB := criarDemandaTesteQ(t, d, "qb")
	pergsB, _ := d.SubstituirPerguntas(ctx, demB, []Pergunta{{Pergunta: "de B"}})

	// tentar responder uma pergunta de B usando o id da demanda A: não afeta nada.
	n, err := d.ResponderPerguntas(ctx, demA, []RespostaPergunta{{ID: pergsB[0].ID, Resposta: "invasão"}})
	if err != nil {
		t.Fatalf("ResponderPerguntas: %v", err)
	}
	if n != 0 {
		t.Fatalf("respondidas = %d, quero 0 (guarda por demanda)", n)
	}
	got, _ := d.ListarPerguntas(ctx, demB)
	if got[0].Resposta != "" {
		t.Fatalf("pergunta de B foi respondida indevidamente: %q", got[0].Resposta)
	}
}
