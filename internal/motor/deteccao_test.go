package motor

import "testing"

// fakeEnv devolve uma seam de lookup a partir de um mapa.
func fakeEnv(m map[string]string) func(string) (string, bool) {
	return func(nome string) (string, bool) {
		v, ok := m[nome]
		return v, ok
	}
}

// fakeCLI devolve uma seam de caminho: os nomes no conjunto estao "instalados".
func fakeCLI(instalados map[string]string) func(string) (string, bool) {
	return func(nome string) (string, bool) {
		p, ok := instalados[nome]
		return p, ok
	}
}

func acharSugestao(sug []SugestaoMotor, nome string) (SugestaoMotor, bool) {
	for _, s := range sug {
		if s.Nome == nome {
			return s, true
		}
	}
	return SugestaoMotor{}, false
}

func TestDetectarMotoresCobreCatalogo(t *testing.T) {
	sug := detectarMotores(fakeEnv(nil), fakeCLI(nil))
	if len(sug) != len(catalogoMotores) {
		t.Fatalf("esperava %d sugestoes, veio %d", len(catalogoMotores), len(sug))
	}
	for _, nome := range []string{"claude", "codex", "opencode"} {
		if _, ok := acharSugestao(sug, nome); !ok {
			t.Fatalf("sugestao ausente para %q", nome)
		}
	}
}

func TestDetectarMotoresInstalado(t *testing.T) {
	sug := detectarMotores(fakeEnv(nil), fakeCLI(map[string]string{"claude": "/usr/bin/claude"}))
	claude, _ := acharSugestao(sug, "claude")
	if !claude.Instalado || claude.CaminhoCLI != "/usr/bin/claude" {
		t.Fatalf("claude deveria estar instalado com caminho, veio %+v", claude)
	}
	codex, _ := acharSugestao(sug, "codex")
	if codex.Instalado {
		t.Fatalf("codex nao deveria estar instalado")
	}
}

func TestDetectarMotoresSugereModelosEDefaults(t *testing.T) {
	sug := detectarMotores(fakeEnv(nil), fakeCLI(nil))
	claude, _ := acharSugestao(sug, "claude")
	if claude.ModeloExec != "opus" || claude.ModeloAnalise != "sonnet" {
		t.Fatalf("modelos sugeridos do claude inesperados: %+v", claude)
	}
	if len(claude.Modelos) == 0 {
		t.Fatalf("claude deveria sugerir opcoes de modelo")
	}
	if claude.BudgetFaseUSD <= 0 || claude.TimeoutMin <= 0 {
		t.Fatalf("claude deveria ter budget/timeout sugeridos: %+v", claude)
	}
	if !claude.Capacidades.SchemaNativo || !claude.Capacidades.BudgetNativo {
		t.Fatalf("capacidades do claude deveriam vir preenchidas: %+v", claude.Capacidades)
	}
	codex, _ := acharSugestao(sug, "codex")
	if codex.ModeloExec != "gpt-5.5" {
		t.Fatalf("modelo exec do codex inesperado: %q", codex.ModeloExec)
	}
}

func TestDetectarMotoresContaDoAmbiente(t *testing.T) {
	env := map[string]string{"CLAUDE_CONFIG_DIR": "/home/user/.claude-alt"}
	sug := detectarMotores(fakeEnv(env), fakeCLI(nil))
	claude, _ := acharSugestao(sug, "claude")
	if len(claude.Contas) != 1 {
		t.Fatalf("esperava 1 conta derivada do ambiente, veio %d", len(claude.Contas))
	}
	c := claude.Contas[0]
	if c.ConfigDir != "/home/user/.claude-alt" || c.Origem != "CLAUDE_CONFIG_DIR" {
		t.Fatalf("conta derivada inesperada: %+v", c)
	}
}

func TestDetectarMotoresMascaraSegredo(t *testing.T) {
	env := map[string]string{"ANTHROPIC_API_KEY": "sk-secreta-123"}
	sug := detectarMotores(fakeEnv(env), fakeCLI(nil))
	claude, _ := acharSugestao(sug, "claude")
	var achou bool
	for _, v := range claude.Variaveis {
		if v.Nome == "ANTHROPIC_API_KEY" {
			achou = true
			if !v.Sensivel || !v.Definida {
				t.Fatalf("ANTHROPIC_API_KEY deveria vir sensivel e definida: %+v", v)
			}
			if v.Valor == "" || contemString([]string{v.Valor}, "sk-secreta-123") {
				t.Fatalf("valor sensivel nao pode expor a credencial: %q", v.Valor)
			}
		}
	}
	if !achou {
		t.Fatal("variavel ANTHROPIC_API_KEY nao foi reportada")
	}
}
