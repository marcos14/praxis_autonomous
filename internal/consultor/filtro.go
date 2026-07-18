// Package consultor implementa o chat de consulta/análise de código para times
// de produto e suporte: o harness roda em modo somente leitura sobre o(s)
// repositório(s) e responde em linguagem de negócio, sem nunca expor código.
//
// A proteção contra vazamento é em camadas: prompt rígido, harness read-only,
// o pós-filtro deste arquivo (a rede de segurança que não depende do prompt —
// que é editável por quem tem config.gerir) e o SSE de progresso sanitizado.
package consultor

import (
	"regexp"
	"strings"
)

// marcadorRedacao substitui trechos redigidos pelo pós-filtro.
const marcadorRedacao = "_[trecho técnico removido pela política de segurança]_"

// MensagemRecusaFiltro é a resposta padrão quando o pós-filtro descarta a saída
// inteira (fail-closed): a resposta era técnica demais para ser saneada.
const MensagemRecusaFiltro = "Não consegui responder sem expor detalhes técnicos internos. " +
	"Reformule a pergunta em termos de comportamento de negócio (o que o sistema deve fazer), " +
	"que eu explico sem entrar no código."

// padroesLinhaCodigo casam construções fortes de código no início/corpo de uma
// linha de prosa (fora de cercas). Cobrem as linguagens prováveis dos repos
// (Go, JS, Python, C#, Java, SQL) sem depender de uma só sintaxe.
var padroesLinhaCodigo = []*regexp.Regexp{
	regexp.MustCompile(`:=|=>|->\s|\};|\);\s*$|\)\s*\{`),
	regexp.MustCompile(`(?i)^\s*(func|def|class|import|package|public|private|var|let|const)\s`),
	regexp.MustCompile(`(?i)\bselect\s+.+\s+from\s|\binsert\s+into\s|\bupdate\s+.+\s+set\s|\bdelete\s+from\s`),
	// Linhas de continuação de um bloco de código (corpo entre chaves): palavras-
	// chave de controle em inglês no início da linha e linhas só de pontuação.
	regexp.MustCompile(`^\s*(return|if|for|while|foreach|elif|else|try|catch|except|switch|case)\b`),
	regexp.MustCompile(`^[{}()\[\];,]+$`),
}

// simbolosCodigo são os caracteres cuja densidade alta numa linha sugere código
// colado sem cerca.
const simbolosCodigo = "{}();=<>[]&"

// Sanitizar aplica o pós-filtro anti-código a um markdown produzido pelo
// consultor, na ordem: (1) blocos cercados (```/~~~) são sempre redigidos;
// (2) inline code longo (>80 chars) é redigido, spans curtos (nomes de
// rotinas/telas) são preservados; (3) sequências de 3+ linhas com cara de código
// fora de cercas são redigidas (linhas de tabela e listas markdown são ignoradas
// pela heurística). Devolve o texto limpo, quantos trechos foram redigidos e
// recusar=true quando a resposta era majoritariamente técnica — o conteúdo
// redigido supera em 3× a prosa restante, ou não sobrou prosa nenhuma. Nesse
// caso o chamador descarta tudo e responde MensagemRecusaFiltro (fail-closed).
func Sanitizar(md string) (limpo string, redigidos int, recusar bool) {
	texto, nCercas, linhasCerca := redigirCercas(md)
	texto, nInline := redigirInlineLongo(texto)
	texto, nBlocos, linhasSoltas := redigirLinhasSuspeitas(texto)

	redigidos = nCercas + nInline + nBlocos
	perdidas := linhasCerca + nInline + linhasSoltas
	prosa := linhasDeProsa(texto)
	if redigidos > 0 && (prosa == 0 || perdidas > 3*prosa) {
		return "", redigidos, true
	}
	return texto, redigidos, false
}

// linhasDeProsa conta as linhas não-vazias do texto sanitizado que ainda têm
// conteúdo próprio (não são apenas o marcador de redação).
func linhasDeProsa(texto string) int {
	n := 0
	for ln := range strings.SplitSeq(texto, "\n") {
		aparada := strings.TrimSpace(ln)
		if aparada != "" && aparada != marcadorRedacao {
			n++
		}
	}
	return n
}

// redigirCercas substitui cada bloco cercado (``` ou ~~~, com ou sem linguagem)
// pelo marcador, devolvendo também quantas linhas de conteúdo foram removidas.
// Cerca aberta sem fechamento é redigida até o fim do texto (não deixamos
// escapar nada por cerca malformada).
func redigirCercas(md string) (string, int, int) {
	linhas := strings.Split(md, "\n")
	var (
		out      []string
		dentro   bool
		blocos   int
		conteudo int
		abertura string
	)
	for _, ln := range linhas {
		aparada := strings.TrimSpace(ln)
		if !dentro && (strings.HasPrefix(aparada, "```") || strings.HasPrefix(aparada, "~~~")) {
			dentro = true
			abertura = aparada[:3]
			blocos++
			out = append(out, marcadorRedacao)
			continue
		}
		if dentro {
			if strings.HasPrefix(aparada, abertura) {
				dentro = false
			} else if aparada != "" {
				conteudo++
			}
			continue // conteúdo (e fechamento) da cerca: descartado
		}
		out = append(out, ln)
	}
	return strings.Join(out, "\n"), blocos, conteudo
}

// redigirInlineLongo substitui spans de `código inline` longos (>80 chars). Um
// span curto — nome de rotina, tela, tabela — é permitido pela política.
var reInline = regexp.MustCompile("`[^`]+`")

func redigirInlineLongo(md string) (string, int) {
	n := 0
	out := reInline.ReplaceAllStringFunc(md, func(s string) string {
		if len(s) > 82 { // 80 de conteúdo + os dois acentos graves
			n++
			return marcadorRedacao
		}
		return s
	})
	return out, n
}

// redigirLinhasSuspeitas detecta código colado SEM cerca: 3+ linhas consecutivas
// com cara de código viram um único marcador. Linhas de tabela markdown (|) e
// itens de lista (-, *) são tratados como prosa — falso-positivo conhecido.
// Devolve também quantas linhas foram redigidas (para o fail-closed).
func redigirLinhasSuspeitas(md string) (string, int, int) {
	linhas := strings.Split(md, "\n")
	suspeita := make([]bool, len(linhas))
	for i, ln := range linhas {
		suspeita[i] = linhaSuspeita(ln)
	}

	var (
		out       []string
		blocos    int
		redigidas int
	)
	for i := 0; i < len(linhas); {
		if !suspeita[i] {
			out = append(out, linhas[i])
			i++
			continue
		}
		j := i
		for j < len(linhas) && suspeita[j] {
			j++
		}
		if j-i >= 3 {
			out = append(out, marcadorRedacao)
			blocos++
			redigidas += j - i
		} else {
			out = append(out, linhas[i:j]...)
		}
		i = j
	}
	return strings.Join(out, "\n"), blocos, redigidas
}

// linhaSuspeita decide se uma linha isolada tem cara de código. Linhas vazias,
// de tabela markdown e de lista são prosa por definição.
func linhaSuspeita(ln string) bool {
	aparada := strings.TrimSpace(ln)
	if aparada == "" {
		return false
	}
	if strings.HasPrefix(aparada, "|") || strings.HasPrefix(aparada, "-") ||
		strings.HasPrefix(aparada, "*") || strings.HasPrefix(aparada, "#") ||
		strings.HasPrefix(aparada, ">") {
		return false
	}
	for _, re := range padroesLinhaCodigo {
		if re.MatchString(aparada) {
			return true
		}
	}
	// Densidade de símbolos: >=25% dos caracteres não-espaço são de código.
	semEspaco := strings.Map(func(r rune) rune {
		if r == ' ' || r == '\t' {
			return -1
		}
		return r
	}, aparada)
	if len(semEspaco) == 0 {
		return false
	}
	n := 0
	for _, r := range semEspaco {
		if strings.ContainsRune(simbolosCodigo, r) {
			n++
		}
	}
	return float64(n) >= 0.25*float64(len(semEspaco))
}
