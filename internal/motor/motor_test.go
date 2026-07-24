package motor

import (
	"testing"
)

func TestSelecionarRegistro(t *testing.T) {
	m, err := Selecionar("")
	if err != nil {
		t.Fatal(err)
	}
	if m.Nome() != "claude" {
		t.Fatalf("motor default inesperado: %s", m.Nome())
	}
	m, err = Selecionar("codex")
	if err != nil {
		t.Fatal(err)
	}
	if m.Nome() != "codex" {
		t.Fatalf("motor codex inesperado: %s", m.Nome())
	}
	if _, err := Selecionar("desconhecido"); err == nil {
		t.Fatal("esperava erro para motor desconhecido")
	}
}

func TestConhecidos(t *testing.T) {
	got := Conhecidos()
	if len(got) != 3 || got[0] != "claude" || got[1] != "codex" || got[2] != "opencode" {
		t.Fatalf("motores conhecidos inesperados (esperava ordem alfabetica): %+v", got)
	}
}

func TestCustoEstimado(t *testing.T) {
	got := CustoEstimado("gpt-5.5", 1_000_000, 1_000_000)
	if got != 35 {
		t.Fatalf("custo gpt-5.5 inesperado: %.2f", got)
	}
	got = CustoEstimado("gpt-5-codex", 1_000_000, 1_000_000)
	if got != 11.25 {
		t.Fatalf("custo inesperado: %.2f", got)
	}
	if got := CustoEstimado("modelo-inexistente", 1_000_000, 1_000_000); got != 0 {
		t.Fatalf("modelo desconhecido deveria custar 0, veio %.2f", got)
	}
}

func TestCoAuthorTrailer(t *testing.T) {
	if got := CoAuthorTrailer("claude"); got == "" || got == CoAuthorTrailer("codex") {
		t.Fatalf("trailers deveriam existir e ser diferentes, claude=%q codex=%q", got, CoAuthorTrailer("codex"))
	}
	if got := CoAuthorTrailer("desconhecido"); got != "" {
		t.Fatalf("motor desconhecido nao deveria ter trailer: %q", got)
	}
}

func TestModeloEEsforcoPadrao(t *testing.T) {
	if ModeloPadrao("") != "opus" || ModeloPadrao("claude") != "opus" {
		t.Fatalf("modelo padrao do claude deveria ser opus")
	}
	if ModeloPadrao("codex") != "gpt-5.5" {
		t.Fatalf("modelo padrao do codex inesperado: %q", ModeloPadrao("codex"))
	}
	if ModeloPadrao("opencode") != "" {
		t.Fatalf("opencode nao deveria ter modelo padrao fixo: %q", ModeloPadrao("opencode"))
	}
	if EsforcoPadrao("claude") != "high" || EsforcoPadrao("codex") != "high" {
		t.Fatalf("esforco padrao inesperado")
	}
	if EsforcoPadrao("opencode") != "" {
		t.Fatalf("opencode nao deveria ter esforco padrao fixo")
	}
}

func TestPrimeirasEUltimasLinhas(t *testing.T) {
	texto := "a\nb\nc\nd"
	if got := primeirasLinhas(texto, 2); got != "a\nb\n(...)" {
		t.Fatalf("primeirasLinhas: %q", got)
	}
	if got := ultimasLinhas(texto, 2); got != "c\nd" {
		t.Fatalf("ultimasLinhas: %q", got)
	}
	if got := primeirasLinhas("a\nb", 5); got != "a\nb" {
		t.Fatalf("primeirasLinhas sem corte: %q", got)
	}
}

// TestResumoErro: o resumo junta o subtipo do harness com a primeira linha do
// resultado — o subtipo sozinho ("success") esconde a causa real da falha.
func TestResumoErro(t *testing.T) {
	casos := []struct {
		nome string
		res  *ResultadoRun
		want string
	}{
		{"nil", nil, ""},
		{"so subtipo", &ResultadoRun{Subtipo: "error_during_execution"}, "error_during_execution"},
		{"subtipo enganoso + causa real",
			&ResultadoRun{Subtipo: "success", Resultado: "Not logged in · Please run /login"},
			"success: Not logged in · Please run /login"},
		{"so resultado", &ResultadoRun{Resultado: "deu ruim"}, "deu ruim"},
		{"multilinha corta na primeira",
			&ResultadoRun{Subtipo: "erro", Resultado: "linha 1\nlinha 2"},
			"erro: linha 1 (...)"},
	}
	for _, c := range casos {
		if got := ResumoErro(c.res); got != c.want {
			t.Fatalf("%s: ResumoErro = %q, quero %q", c.nome, got, c.want)
		}
	}
}
