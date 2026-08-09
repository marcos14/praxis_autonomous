package api

import (
	"strings"
	"testing"

	"github.com/marcos14/praxis-autonomous/internal/i18n"
)

// TestManualMesmosSlugsEmTodosIdiomas garante que o manual expõe o MESMO
// conjunto de seções, na mesma ordem, em qualquer idioma — é o fallback por
// página em ação: um idioma sem tradução ainda entrega o manual inteiro (em
// pt-BR), nunca uma navegação com buracos ou 404.
func TestManualMesmosSlugsEmTodosIdiomas(t *testing.T) {
	base := carregarManual(i18n.Padrao)
	if len(base) == 0 {
		t.Fatal("manual pt-BR vazio: o embed não carregou")
	}
	for _, lang := range i18n.Suportados {
		secoes := carregarManual(lang)
		if len(secoes) != len(base) {
			t.Errorf("manual %s tem %d seções; pt-BR tem %d", lang, len(secoes), len(base))
			continue
		}
		for i := range base {
			if secoes[i].Slug != base[i].Slug {
				t.Errorf("manual %s: seção %d é %q; esperado %q (ordem/slug devem casar com pt-BR)",
					lang, i, secoes[i].Slug, base[i].Slug)
			}
			if strings.TrimSpace(secoes[i].Titulo) == "" {
				t.Errorf("manual %s: seção %q sem título (a 1ª linha precisa ser \"# Título\")", lang, secoes[i].Slug)
			}
			if strings.TrimSpace(secoes[i].Conteudo) == "" {
				t.Errorf("manual %s: seção %q sem conteúdo", lang, secoes[i].Slug)
			}
		}
	}
}

// TestManualIdiomaTraduzidoDifereDaFonte é o termômetro da tradução: reporta
// (sem falhar) quantas seções de cada idioma ainda caem no fallback pt-BR.
// Não falha porque um idioma parcialmente traduzido é um estado VÁLIDO do
// projeto — mas o número precisa ficar visível para não passar despercebido.
func TestManualIdiomaTraduzidoDifereDaFonte(t *testing.T) {
	base := carregarManual(i18n.Padrao)
	porSlug := make(map[string]string, len(base))
	for _, s := range base {
		porSlug[s.Slug] = s.Conteudo
	}
	for _, lang := range i18n.Suportados {
		if lang == i18n.Padrao {
			continue
		}
		pendentes := []string{}
		for _, s := range carregarManual(lang) {
			if s.Conteudo == porSlug[s.Slug] {
				pendentes = append(pendentes, s.Slug)
			}
		}
		if len(pendentes) > 0 {
			t.Logf("manual %s: %d de %d seções ainda em fallback pt-BR: %s",
				lang, len(pendentes), len(base), strings.Join(pendentes, ", "))
		} else {
			t.Logf("manual %s: todas as %d seções traduzidas", lang, len(base))
		}
	}
}
