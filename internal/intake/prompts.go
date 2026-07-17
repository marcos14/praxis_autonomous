package intake

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"strings"

	"github.com/marcos14/praxis-autonomous/internal/db"
)

// Nomes dos prompts de intake conhecidos (chave no banco e nome do arquivo
// embutido em prompts/<nome>.md). O planejador (Fase 3c) acrescenta o seu.
const (
	PromptAnalista = "analista"
)

// promptsEmbutidos carrega os defaults de prompt versionados junto do binário. É
// o "default embutido" de "prompts no banco com default embutido" (Fase 3b): a
// tabela prompts guarda só os overrides; na ausência de override, usa-se estes.
//
//go:embed prompts/*.md
var promptsEmbutidos embed.FS

// PromptEmbutido devolve o default embutido do prompt de nome (sem consultar o
// banco). Útil para semear a tela de Configurações e como fallback. Nome
// desconhecido → ok=false.
func PromptEmbutido(nome string) (string, bool) {
	nome = strings.ToLower(strings.TrimSpace(nome))
	b, err := promptsEmbutidos.ReadFile("prompts/" + nome + ".md")
	if err != nil {
		return "", false
	}
	return string(b), true
}

// ResolverPrompt devolve o conteúdo do prompt de nome: o override do banco quando
// existe, senão o default embutido. Falha só se não houver nem um nem outro
// (nome desconhecido) — ou em erro de infraestrutura do banco.
//
// store pode ser nil (testes puros): cai direto no default embutido.
func ResolverPrompt(ctx context.Context, store *db.DB, nome string) (string, error) {
	if store != nil {
		p, err := store.ObterPrompt(ctx, nome)
		if err == nil {
			return p.Conteudo, nil
		}
		if !errors.Is(err, db.ErrNaoEncontrado) {
			return "", err
		}
	}
	if s, ok := PromptEmbutido(nome); ok {
		return s, nil
	}
	return "", fmt.Errorf("prompt desconhecido: %q", nome)
}

// renderPrompt substitui os marcadores {VAR} do template pelos valores. Mesma
// semântica do renderPrompt do pipeline (Praxis atual).
func renderPrompt(tpl string, valores map[string]string) string {
	pares := make([]string, 0, len(valores)*2)
	for k, v := range valores {
		pares = append(pares, "{"+k+"}", v)
	}
	return strings.NewReplacer(pares...).Replace(tpl)
}
