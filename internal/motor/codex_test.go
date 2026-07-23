package motor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
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

func TestMotorCodexUsaCodexHomeDoPerfil(t *testing.T) {
	dirBin := t.TempDir()
	registro := filepath.Join(t.TempDir(), "codex_env.txt")
	nome := "codex"
	conteudo := "#!/bin/sh\nprintf '%s' \"$CODEX_HOME\" > \"" + registro + "\"\necho '{\"type\":\"item.completed\",\"item\":{\"type\":\"agent_message\",\"text\":\"ok\"}}'\necho '{\"type\":\"turn.completed\",\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}'\nexit 0\n"
	if runtime.GOOS == "windows" {
		nome = "codex.bat"
		conteudo = "@echo off\r\n>\"" + registro + "\" echo %CODEX_HOME%\r\necho {\"type\":\"item.completed\",\"item\":{\"type\":\"agent_message\",\"text\":\"ok\"}}\r\necho {\"type\":\"turn.completed\",\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}\r\nexit /b 0\r\n"
	}
	if err := os.WriteFile(filepath.Join(dirBin, nome), []byte(conteudo), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dirBin+string(os.PathListSeparator)+os.Getenv("PATH"))

	perfil := filepath.Join(t.TempDir(), "perfil-codex")
	res, err := motorCodex{}.Rodar(OpcoesRun{
		Dir: t.TempDir(), DirLogs: t.TempDir(), Prompt: "teste", RotuloLog: "env",
		TimeoutMin: 1, PerfilDir: perfil,
	})
	if err != nil {
		t.Fatalf("rodar codex fake: %v", err)
	}
	if res == nil || strings.TrimSpace(res.Resultado) != "ok" {
		t.Fatalf("resultado inesperado: %+v", res)
	}
	b, err := os.ReadFile(registro)
	if err != nil {
		t.Fatal(err)
	}
	abs, _ := filepath.Abs(perfil)
	if got := strings.TrimSpace(string(b)); got != abs {
		t.Fatalf("CODEX_HOME = %q, quero %q", got, abs)
	}
}
