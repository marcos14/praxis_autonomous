package notify

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// TestCatalogoEspelhaOJS garante que TiposConhecidos e TiposPadraoUsuario
// batem com o catálogo do frontend (tipo e flag padrao_usuario), na ordem.
func TestCatalogoEspelhaOJS(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "..", "web", "js", "notify-events.js"))
	if err != nil {
		t.Skipf("catálogo JS indisponível: %v", err)
	}
	re := regexp.MustCompile(`\{\s*tipo:\s*"([a-z_]+)"[^}]*\}`)
	var tiposJS, padraoJS []string
	for _, m := range re.FindAllSubmatch(src, -1) {
		tiposJS = append(tiposJS, string(m[1]))
		if regexp.MustCompile(`padrao_usuario:\s*true`).Match(m[0]) {
			padraoJS = append(padraoJS, string(m[1]))
		}
	}
	if len(tiposJS) != len(TiposConhecidos) {
		t.Fatalf("catálogo JS tem %d tipos, Go tem %d:\nJS %v\nGo %v", len(tiposJS), len(TiposConhecidos), tiposJS, TiposConhecidos)
	}
	for i := range tiposJS {
		if tiposJS[i] != TiposConhecidos[i] {
			t.Fatalf("posição %d: JS %q, Go %q", i, tiposJS[i], TiposConhecidos[i])
		}
	}
	// padrao_usuario: mesmo conjunto (ordem do JS é a do catálogo; a do Go é livre).
	set := map[string]bool{}
	for _, tipo := range TiposPadraoUsuario {
		if !TipoConhecido(tipo) {
			t.Fatalf("TiposPadraoUsuario tem tipo fora do catálogo: %q", tipo)
		}
		set[tipo] = true
	}
	if len(padraoJS) != len(set) {
		t.Fatalf("padrao_usuario: JS %v, Go %v", padraoJS, TiposPadraoUsuario)
	}
	for _, tipo := range padraoJS {
		if !set[tipo] {
			t.Fatalf("JS marca %q como padrao_usuario, Go não", tipo)
		}
	}
	if TipoConhecido("inexistente") {
		t.Fatal("tipo inexistente não deveria ser conhecido")
	}
}
