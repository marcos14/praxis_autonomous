package intake

import (
	"context"
	"strings"
	"testing"
)

func TestPromptEmbutidoAnalista(t *testing.T) {
	s, ok := PromptEmbutido(PromptAnalista)
	if !ok {
		t.Fatal("prompt analista embutido não encontrado")
	}
	if !strings.Contains(s, "{PRD}") {
		t.Fatal("prompt analista embutido deveria conter o marcador {PRD}")
	}
	if _, ok := PromptEmbutido("inexistente"); ok {
		t.Fatal("prompt inexistente não deveria existir")
	}
}

func TestResolverPromptSemStoreUsaEmbutido(t *testing.T) {
	s, err := ResolverPrompt(context.Background(), nil, PromptAnalista)
	if err != nil {
		t.Fatalf("ResolverPrompt: %v", err)
	}
	if !strings.Contains(s, "{PRD}") {
		t.Fatal("deveria cair no embutido")
	}
}

func TestResolverPromptOverrideDoBanco(t *testing.T) {
	d := abrirDB(t)
	ctx := context.Background()
	if _, err := d.SalvarPrompt(ctx, PromptAnalista, "prompt custom da empresa {PRD}"); err != nil {
		t.Fatalf("SalvarPrompt: %v", err)
	}
	s, err := ResolverPrompt(ctx, d, PromptAnalista)
	if err != nil {
		t.Fatalf("ResolverPrompt: %v", err)
	}
	if s != "prompt custom da empresa {PRD}" {
		t.Fatalf("override não aplicado: %q", s)
	}
}

func TestResolverPromptCaiNoEmbutidoSemOverride(t *testing.T) {
	d := abrirDB(t)
	s, err := ResolverPrompt(context.Background(), d, PromptAnalista)
	if err != nil {
		t.Fatalf("ResolverPrompt: %v", err)
	}
	if !strings.Contains(s, "somente leitura") {
		t.Fatal("deveria usar o embutido (sem override)")
	}
}

func TestResolverPromptDesconhecido(t *testing.T) {
	if _, err := ResolverPrompt(context.Background(), nil, "nao-existe"); err == nil {
		t.Fatal("prompt desconhecido deveria falhar")
	}
}
