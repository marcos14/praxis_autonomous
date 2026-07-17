package intake

import (
	"context"
	"strings"
	"testing"
)

// TestProvedorPromptResolvePipeline garante que os prompts do ciclo de execução
// (executor/corretor/revisor) estão embutidos e são resolvidos pelo provedor do
// pipeline, aceitando o nome com o sufixo ".md" que o pipeline usa.
func TestProvedorPromptResolvePipeline(t *testing.T) {
	prov := ProvedorPrompt(context.Background(), nil) // sem store → default embutido
	for _, nome := range []string{"executor.md", "corretor.md", "revisor.md"} {
		tpl, err := prov(nome)
		if err != nil {
			t.Fatalf("resolver %q: %v", nome, err)
		}
		if strings.TrimSpace(tpl) == "" {
			t.Fatalf("prompt %q vazio", nome)
		}
		// os prompts devem conter os marcadores que o pipeline preenche.
		if !strings.Contains(tpl, "{FASE}") || !strings.Contains(tpl, "{TITULO}") || !strings.Contains(tpl, "{PLANO}") {
			t.Fatalf("prompt %q sem marcadores {FASE}/{TITULO}/{PLANO}", nome)
		}
	}
	// o corretor precisa do marcador {MOTIVO}.
	corr, _ := prov("corretor.md")
	if !strings.Contains(corr, "{MOTIVO}") {
		t.Fatal("corretor sem {MOTIVO}")
	}
}
