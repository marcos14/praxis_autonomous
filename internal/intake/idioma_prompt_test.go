package intake

import (
	"strings"
	"testing"

	"github.com/marcos14/praxis-autonomous/internal/i18n"
)

// TestPromptsTemMarcadorIdioma garante que todo prompt que produz conteúdo LIDO
// PELO USUÁRIO carrega o marcador {IDIOMA} — sem ele o harness responde no
// idioma em que o prompt foi escrito (pt-BR) e o i18n para na borda da UI.
// Os prompts do ciclo de código (executor/corretor/revisor) ficam de fora de
// propósito: o que eles escrevem (código, comentários, mensagens de commit)
// segue a língua do repositório, não a preferência de quem abriu a demanda.
func TestPromptsTemMarcadorIdioma(t *testing.T) {
	for _, nome := range []string{
		PromptAnalista, PromptPlanejador, PromptConsultor, PromptEstrategista, PromptOverview,
	} {
		conteudo, ok := PromptEmbutido(nome)
		if !ok {
			t.Errorf("prompt embutido %q não existe", nome)
			continue
		}
		if !strings.Contains(conteudo, "{IDIOMA}") {
			t.Errorf("prompt %q não tem o marcador {IDIOMA}", nome)
		}
	}
}

// TestRenderPromptSubstituiIdioma cobre a substituição de ponta a ponta: o nome
// do idioma entra no prompt e nenhum {IDIOMA} sobra cru (um marcador não
// substituído vazaria para o modelo como texto literal).
func TestRenderPromptSubstituiIdioma(t *testing.T) {
	casos := map[string]string{
		"pt-BR": "português",
		"en":    "English",
		"es":    "español",
		"zh-CN": "简体中文",
		"":      "português", // sem preferência → idioma da instância (padrão pt-BR)
	}
	for tag, esperado := range casos {
		tpl, _ := PromptEmbutido(PromptAnalista)
		saida := renderPrompt(tpl, map[string]string{
			"PRD":    "x",
			"IDIOMA": i18n.NomeIdiomaOuInstancia(tag),
		})
		if !strings.Contains(saida, esperado) {
			t.Errorf("idioma %q: prompt não menciona %q", tag, esperado)
		}
		if strings.Contains(saida, "{IDIOMA}") {
			t.Errorf("idioma %q: sobrou marcador {IDIOMA} sem substituir", tag)
		}
	}
}
