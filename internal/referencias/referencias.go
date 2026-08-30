// Pacote referencias concentra as regras dos DOCUMENTOS DE APOIO que o usuário
// anexa a uma sessão de IA (planejamento ou consulta): quais nomes/extensões
// são aceitos, onde os arquivos moram dentro da pasta de trabalho da sessão e
// como a listagem é injetada no prompt.
//
// Existe para que planejamentos e consultas apliquem exatamente a mesma regra —
// a validação de nome é uma fronteira de segurança (path traversal, extensões
// executáveis) e não pode divergir entre as duas features.
package referencias

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Dir é a subpasta da pasta de trabalho da sessão onde ficam os arquivos
// anexados pelo usuário. O harness a lê como insumo; a ingestão de artefatos do
// planejamento a ignora (só varre a raiz).
const Dir = "referencias"

// extensoes são os tipos aceitos como referência — documentos que os harnesses
// sabem ler (texto, pdf e imagem).
var extensoes = map[string]bool{
	".md": true, ".txt": true, ".csv": true, ".json": true, ".pdf": true,
	".html": true, ".png": true, ".jpg": true, ".jpeg": true, ".webp": true,
}

// NomeValido informa se nome é um arquivo de referência aceitável: nome simples
// (sem caminho, sem ponto inicial, sem caracteres de controle) com extensão da
// lista de tipos legíveis. Aceita acentos e espaços — nomes reais de arquivos de
// usuário ("Transcrição da reunião.md").
func NomeValido(nome string) bool {
	if nome == "" || len(nome) > 120 || strings.HasPrefix(nome, ".") {
		return false
	}
	if nome != filepath.Base(nome) || strings.ContainsAny(nome, `/\:*?"<>|`) || strings.Contains(nome, "..") {
		return false
	}
	for _, r := range nome {
		if r < 0x20 {
			return false
		}
	}
	return extensoes[strings.ToLower(filepath.Ext(nome))]
}

// Arquivo é uma referência encontrada em disco (a pasta é a fonte da verdade —
// não há índice no banco).
type Arquivo struct {
	Nome    string
	Tamanho int64
	ModTime string // ISO-8601 UTC; "" quando o stat falhar
}

// Listar devolve as referências válidas da pasta dir, em ordem de nome. Pasta
// inexistente (nada foi anexado) devolve lista vazia, sem erro.
func Listar(dir string) []Arquivo {
	entradas, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var arquivos []Arquivo
	for _, ent := range entradas {
		if ent.IsDir() || !NomeValido(ent.Name()) {
			continue
		}
		a := Arquivo{Nome: ent.Name()}
		if info, err := ent.Info(); err == nil {
			a.Tamanho = info.Size()
			a.ModTime = info.ModTime().UTC().Format("2006-01-02T15:04:05.000Z")
		}
		arquivos = append(arquivos, a)
	}
	sort.Slice(arquivos, func(i, j int) bool { return arquivos[i].Nome < arquivos[j].Nome })
	return arquivos
}

// BlocoPrompt monta o texto do marcador {REFERENCIAS} do prompt. dir é a pasta
// física; prefixo é como o caminho é APRESENTADO ao harness — "referencias"
// quando o cwd dele já é a pasta de trabalho (estrategista) ou o caminho
// absoluto da subpasta quando o cwd é outro (consultor, que roda dentro do
// repo). O harness só lê o que a listagem apontar como existente.
func BlocoPrompt(dir, prefixo string) string {
	arquivos := Listar(dir)
	if len(arquivos) == 0 {
		return "(nenhuma referência anexada)"
	}
	linhas := make([]string, 0, len(arquivos))
	for _, a := range arquivos {
		linhas = append(linhas, fmt.Sprintf("- %s (%d bytes)",
			filepath.ToSlash(filepath.Join(prefixo, a.Nome)), a.Tamanho))
	}
	return strings.Join(linhas, "\n")
}
