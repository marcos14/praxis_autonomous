// Package i18n traduz as mensagens do servidor e resolve o idioma de cada
// requisição. Sem dependências externas: catálogos JSON planos embutidos no
// binário, chaves pontuadas ("erro.nao_encontrado") e interpolação {nome}.
//
// Os identificadores do sistema (códigos de erro, slugs de status, permissões,
// chaves de config) NÃO passam por aqui — são contrato estável da API; apenas a
// mensagem legível é traduzida.
package i18n

import (
	"embed"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

//go:embed locales/*.json
var arquivos embed.FS

// Padrao é o idioma de fallback final. pt-BR preserva o comportamento das
// instalações existentes.
const Padrao = "pt-BR"

// Suportados lista os idiomas com catálogo embutido, na ordem de exibição.
var Suportados = []string{"pt-BR", "en", "es", "zh-CN"}

var (
	umaVez    sync.Once
	catalogos map[string]map[string]string
)

// carregar decodifica os catálogos embutidos uma única vez. Catálogo ausente ou
// inválido é erro de build/packaging — panic imediato é preferível a servir
// chaves cruas silenciosamente.
func carregar() {
	catalogos = make(map[string]map[string]string, len(Suportados))
	for _, lang := range Suportados {
		b, err := arquivos.ReadFile("locales/" + lang + ".json")
		if err != nil {
			panic(fmt.Sprintf("i18n: catálogo %s ausente: %v", lang, err))
		}
		m := map[string]string{}
		if err := json.Unmarshal(b, &m); err != nil {
			panic(fmt.Sprintf("i18n: catálogo %s inválido: %v", lang, err))
		}
		catalogos[lang] = m
	}
}

// Catalogo devolve o catálogo completo de um idioma (uso em testes/diagnóstico).
func Catalogo(lang string) map[string]string {
	umaVez.Do(carregar)
	return catalogos[Normalizar(lang)]
}

// Normalizar mapeia uma tag de idioma (BCP-47 ou variação frouxa: "pt", "pt_br",
// "en-US", "zh-Hans") para um idioma suportado. Devolve "" quando não há
// correspondência.
func Normalizar(tag string) string {
	t := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(tag), "_", "-"))
	switch {
	case strings.HasPrefix(t, "pt"):
		return "pt-BR"
	case strings.HasPrefix(t, "en"):
		return "en"
	case strings.HasPrefix(t, "es"):
		return "es"
	case strings.HasPrefix(t, "zh"):
		return "zh-CN"
	}
	return ""
}

// DoAcceptLanguage escolhe o idioma suportado de maior preferência num header
// Accept-Language ("pt-BR,pt;q=0.9,en;q=0.8"). Devolve "" se nenhum casar.
func DoAcceptLanguage(header string) string {
	melhor, melhorQ := "", -1.0
	for _, parte := range strings.Split(header, ",") {
		seg := strings.Split(strings.TrimSpace(parte), ";")
		lang := Normalizar(seg[0])
		if lang == "" {
			continue
		}
		q := 1.0
		for _, p := range seg[1:] {
			if p = strings.TrimSpace(p); strings.HasPrefix(p, "q=") {
				if v, err := strconv.ParseFloat(p[2:], 64); err == nil {
					q = v
				}
			}
		}
		if q > melhorQ {
			melhor, melhorQ = lang, q
		}
	}
	return melhor
}

// idiomaInstancia guarda o idioma da INSTÂNCIA (config global `idioma`): o
// idioma dos eventos persistidos, das notificações e do conteúdo gerado sem um
// usuário no contexto. É um global de processo (atomic) para os geradores de
// evento — pipeline, scheduler, intake — não precisarem carregar a config a
// cada gravação; o boot do serve e o PUT da config global o atualizam.
var idiomaInstancia atomic.Value

// DefinirIdiomaInstancia atualiza o idioma da instância (normalizado; vazio ou
// desconhecido → padrão). Chamado no boot do serviço e ao salvar a config.
func DefinirIdiomaInstancia(tag string) {
	l := Normalizar(tag)
	if l == "" {
		l = Padrao
	}
	idiomaInstancia.Store(l)
}

// IdiomaInstancia devolve o idioma da instância (padrão antes do boot definir).
func IdiomaInstancia() string {
	if v, ok := idiomaInstancia.Load().(string); ok {
		return v
	}
	return Padrao
}

// TI traduz no idioma da instância — atalho para os geradores de eventos e
// notificações: i18n.TI("evento.x", "nome", valor).
func TI(chave string, args ...string) string {
	return T(IdiomaInstancia(), chave, args...)
}

// nomesIdioma é como cada idioma é NOMEADO para o modelo, no próprio idioma —
// a forma mais confiável de fixar a língua de saída de um harness de IA.
var nomesIdioma = map[string]string{
	"pt-BR": "português do Brasil (pt-BR)",
	"en":    "English (en)",
	"es":    "español (es)",
	"zh-CN": "简体中文 (zh-CN)",
}

// NomeIdioma devolve o nome do idioma para injetar nos prompts ({IDIOMA}).
// Tag vazia/desconhecida cai no padrão da instalação.
func NomeIdioma(tag string) string {
	if n, ok := nomesIdioma[Normalizar(tag)]; ok {
		return n
	}
	return nomesIdioma[Padrao]
}

// NomeIdiomaOuInstancia resolve o nome do idioma de saída da IA: a preferência
// do usuário quando houver, senão o idioma da instância. É a regra dos prompts —
// a conversa sai no idioma de quem a conduz; o que não tem dono (overview do
// repositório) sai no idioma da instalação.
func NomeIdiomaOuInstancia(preferenciaUsuario string) string {
	if l := Normalizar(preferenciaUsuario); l != "" {
		return NomeIdioma(l)
	}
	return NomeIdioma(IdiomaInstancia())
}

// T traduz a chave no idioma dado, interpolando pares nome/valor alternados
// (T(lang, "erro.x", "arquivo", nome) substitui {arquivo}). Chave ausente cai no
// idioma padrão e, em último caso, devolve a própria chave — visível, nunca
// quebra (o teste de paridade impede o caso no build).
func T(lang, chave string, args ...string) string {
	umaVez.Do(carregar)
	texto, ok := catalogos[Normalizar(lang)][chave]
	if !ok {
		if texto, ok = catalogos[Padrao][chave]; !ok {
			return chave
		}
	}
	for i := 0; i+1 < len(args); i += 2 {
		texto = strings.ReplaceAll(texto, "{"+args[i]+"}", args[i+1])
	}
	return texto
}

// TN traduz com o plural mínimo do projeto: chave+".one" quando n == 1 e
// chave+".other" nos demais casos (cobre en/pt/es; zh repete o texto nas duas).
// O próprio n fica disponível como {n}.
func TN(lang, chave string, n int, args ...string) string {
	sufixo := ".other"
	if n == 1 {
		sufixo = ".one"
	}
	return T(lang, chave+sufixo, append(append([]string{}, args...), "n", strconv.Itoa(n))...)
}
