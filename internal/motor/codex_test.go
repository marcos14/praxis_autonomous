package motor

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMotorCodexCapacidades(t *testing.T) {
	c := motorCodex{}.Capacidades()
	if !c.SchemaNativo || c.BudgetNativo || c.CustoUSDNativo {
		t.Fatalf("codex: schema nativo, sem budget/custo nativos; veio %+v", c)
	}
	if (motorCodex{}).Nome() != "codex" {
		t.Fatalf("nome inesperado: %q", (motorCodex{}).Nome())
	}
}

func TestSchemaStrictOpenAI(t *testing.T) {
	schema := `{"type":"object","properties":{"b":{"type":"string"},"a":{"type":"object","properties":{"x":{"type":"string"}}}}}`
	strict, err := schemaStrictOpenAI(schema)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(strict), &m); err != nil {
		t.Fatal(err)
	}
	if m["additionalProperties"] != false {
		t.Fatalf("additionalProperties raiz nao foi desativado: %v", m["additionalProperties"])
	}
	req, _ := m["required"].([]any)
	if strings.Join([]string{req[0].(string), req[1].(string)}, ",") != "a,b" {
		t.Fatalf("required raiz inesperado: %#v", req)
	}
	props := m["properties"].(map[string]any)
	a := props["a"].(map[string]any)
	if a["additionalProperties"] != false {
		t.Fatalf("additionalProperties aninhado nao foi desativado: %v", a["additionalProperties"])
	}
}

func TestLimiteCodexAtingido(t *testing.T) {
	for _, texto := range []string{
		"429 too many requests",
		"rate limit exceeded",
		"usage limit reached",
		"insufficient_quota",
	} {
		if !limiteCodexAtingido(texto) {
			t.Fatalf("esperava limite para %q", texto)
		}
	}
	if limiteCodexAtingido("erro de sintaxe no comando") {
		t.Fatal("nao esperava limite")
	}
	if got := linhaLimiteCodex("prefixo\nrate limit exceeded\nrodape"); got != "rate limit exceeded" {
		t.Fatalf("linha limite: %q", got)
	}
}

func TestTextoErroCodex(t *testing.T) {
	if got := textoErroCodex(nil); got != "" {
		t.Fatalf("erro nulo deveria ser vazio: %q", got)
	}
	if got := textoErroCodex(json.RawMessage(`"limite atingido"`)); got != "limite atingido" {
		t.Fatalf("erro string mal extraido: %q", got)
	}
	got := textoErroCodex(json.RawMessage(`{"message":"quota","code":429}`))
	if !strings.Contains(got, "quota") || !strings.Contains(got, "429") {
		t.Fatalf("erro objeto mal extraido: %q", got)
	}
}
