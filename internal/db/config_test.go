package db

import (
	"context"
	"encoding/json"
	"testing"
)

// projetoParaConfig cria um projeto de apoio (necessário para overrides por
// projeto, que têm FK para projects) e devolve seu id.
func projetoParaConfig(t *testing.T, d *DB) int64 {
	t.Helper()
	p, err := d.CriarProjeto(context.Background(), projetoExemplo())
	if err != nil {
		t.Fatalf("criar projeto de apoio: %v", err)
	}
	return p.ID
}

func rm(s string) json.RawMessage { return json.RawMessage(s) }

func TestConfigGlobalVaziaNaoNil(t *testing.T) {
	d := abrirTemp(t)
	cfg, err := d.ObterConfigGlobal(context.Background())
	if err != nil {
		t.Fatalf("ObterConfigGlobal: %v", err)
	}
	if cfg == nil {
		t.Fatal("config global deveria ser mapa vazio, não nil")
	}
	if len(cfg) != 0 {
		t.Fatalf("config global deveria estar vazia, veio %v", cfg)
	}
}

func TestDefinirConfigGlobalRoundTrip(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()

	entradas := map[string]json.RawMessage{
		"execucoes_simultaneas": rm("2"),
		"gates":                 rm(`["go build","go vet"]`),
		"budget_usd":            rm("10.5"),
	}
	if err := d.DefinirConfigGlobal(ctx, entradas); err != nil {
		t.Fatalf("DefinirConfigGlobal: %v", err)
	}
	cfg, err := d.ObterConfigGlobal(ctx)
	if err != nil {
		t.Fatalf("ObterConfigGlobal: %v", err)
	}
	if len(cfg) != 3 {
		t.Fatalf("esperava 3 chaves, veio %d: %v", len(cfg), cfg)
	}
	if string(cfg["execucoes_simultaneas"]) != "2" {
		t.Fatalf("execucoes_simultaneas = %s", cfg["execucoes_simultaneas"])
	}
	if string(cfg["gates"]) != `["go build","go vet"]` {
		t.Fatalf("gates = %s", cfg["gates"])
	}
}

func TestDefinirConfigGlobalFullReplace(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()

	if err := d.DefinirConfigGlobal(ctx, map[string]json.RawMessage{"a": rm("1"), "b": rm("2")}); err != nil {
		t.Fatalf("primeira gravação: %v", err)
	}
	// Full replace: a nova gravação remove as chaves ausentes.
	if err := d.DefinirConfigGlobal(ctx, map[string]json.RawMessage{"b": rm("3")}); err != nil {
		t.Fatalf("segunda gravação: %v", err)
	}
	cfg, err := d.ObterConfigGlobal(ctx)
	if err != nil {
		t.Fatalf("ObterConfigGlobal: %v", err)
	}
	if len(cfg) != 1 || string(cfg["b"]) != "3" {
		t.Fatalf("esperava só b=3, veio %v", cfg)
	}
	if _, existe := cfg["a"]; existe {
		t.Fatal("chave 'a' deveria ter sido removida no full replace")
	}
}

func TestDefinirConfigGlobalCompactaJSON(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()

	if err := d.DefinirConfigGlobal(ctx, map[string]json.RawMessage{
		"obj": rm(`{ "a" :  1 ,  "b" : [ 2, 3 ] }`),
	}); err != nil {
		t.Fatalf("DefinirConfigGlobal: %v", err)
	}
	cfg, _ := d.ObterConfigGlobal(ctx)
	if string(cfg["obj"]) != `{"a":1,"b":[2,3]}` {
		t.Fatalf("JSON não compactado: %s", cfg["obj"])
	}
}

func TestConfigProjetoIsoladaDoGlobal(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()
	pid := projetoParaConfig(t, d)

	if err := d.DefinirConfigGlobal(ctx, map[string]json.RawMessage{"g": rm("1")}); err != nil {
		t.Fatalf("global: %v", err)
	}
	if err := d.DefinirConfigProjeto(ctx, pid, map[string]json.RawMessage{"p": rm("2")}); err != nil {
		t.Fatalf("projeto: %v", err)
	}
	// ObterConfigProjeto traz só o override, não o global.
	proj, _ := d.ObterConfigProjeto(ctx, pid)
	if len(proj) != 1 || string(proj["p"]) != "2" {
		t.Fatalf("override do projeto = %v, quero só p=2", proj)
	}
	// A gravação do projeto não mexeu no global.
	global, _ := d.ObterConfigGlobal(ctx)
	if len(global) != 1 || string(global["g"]) != "1" {
		t.Fatalf("global = %v, quero só g=1", global)
	}
}

func TestConfigEfetivaMergeEOrigem(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()
	pid := projetoParaConfig(t, d)

	// Global define três chaves; o projeto sobrepõe uma e adiciona outra.
	if err := d.DefinirConfigGlobal(ctx, map[string]json.RawMessage{
		"execucoes_simultaneas": rm("2"),
		"budget_usd":            rm("10"),
		"gates":                 rm(`["go build"]`),
	}); err != nil {
		t.Fatalf("global: %v", err)
	}
	if err := d.DefinirConfigProjeto(ctx, pid, map[string]json.RawMessage{
		"budget_usd": rm("99"), // override
		"max_ciclos": rm("3"),  // só do projeto
	}); err != nil {
		t.Fatalf("projeto: %v", err)
	}

	ef, err := d.ConfigEfetiva(ctx, pid)
	if err != nil {
		t.Fatalf("ConfigEfetiva: %v", err)
	}
	if len(ef) != 4 {
		t.Fatalf("esperava 4 chaves na efetiva, veio %d: %v", len(ef), ef)
	}
	// Override do projeto vence e reporta origem project.
	if v := ef["budget_usd"]; string(v.Valor) != "99" || v.Origem != OrigemProjeto {
		t.Fatalf("budget_usd = %+v, quero 99/project", v)
	}
	// Chave ausente no projeto cai no global.
	if v := ef["execucoes_simultaneas"]; string(v.Valor) != "2" || v.Origem != OrigemGlobal {
		t.Fatalf("execucoes_simultaneas = %+v, quero 2/global", v)
	}
	if v := ef["gates"]; v.Origem != OrigemGlobal {
		t.Fatalf("gates origem = %s, quero global", v.Origem)
	}
	// Chave só do projeto reporta origem project.
	if v := ef["max_ciclos"]; string(v.Valor) != "3" || v.Origem != OrigemProjeto {
		t.Fatalf("max_ciclos = %+v, quero 3/project", v)
	}
}

func TestConfigEfetivaSemProjetoOverrideEspelhaGlobal(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()
	pid := projetoParaConfig(t, d)

	if err := d.DefinirConfigGlobal(ctx, map[string]json.RawMessage{"a": rm("1")}); err != nil {
		t.Fatalf("global: %v", err)
	}
	ef, err := d.ConfigEfetiva(ctx, pid)
	if err != nil {
		t.Fatalf("ConfigEfetiva: %v", err)
	}
	if len(ef) != 1 || string(ef["a"].Valor) != "1" || ef["a"].Origem != OrigemGlobal {
		t.Fatalf("efetiva = %v, quero a=1/global", ef)
	}
}

func TestDefinirConfigProjetoRemoveOverrideVoltaAoGlobal(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()
	pid := projetoParaConfig(t, d)

	d.DefinirConfigGlobal(ctx, map[string]json.RawMessage{"x": rm("1")})
	d.DefinirConfigProjeto(ctx, pid, map[string]json.RawMessage{"x": rm("9")})
	if ef, _ := d.ConfigEfetiva(ctx, pid); ef["x"].Origem != OrigemProjeto {
		t.Fatalf("com override, origem deveria ser project, veio %s", ef["x"].Origem)
	}
	// Limpa o override (full replace com mapa vazio) → volta a herdar o global.
	if err := d.DefinirConfigProjeto(ctx, pid, map[string]json.RawMessage{}); err != nil {
		t.Fatalf("limpar override: %v", err)
	}
	ef, _ := d.ConfigEfetiva(ctx, pid)
	if string(ef["x"].Valor) != "1" || ef["x"].Origem != OrigemGlobal {
		t.Fatalf("após limpar override, x = %+v, quero 1/global", ef["x"])
	}
}

func TestConfigProjetoCascadeAoApagarProjeto(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()
	pid := projetoParaConfig(t, d)

	if err := d.DefinirConfigProjeto(ctx, pid, map[string]json.RawMessage{"k": rm("1")}); err != nil {
		t.Fatalf("projeto: %v", err)
	}
	// Apaga o projeto; o ON DELETE CASCADE deve remover as entradas de config.
	if _, err := d.Escritor.ExecContext(ctx, `DELETE FROM projects WHERE id = ?`, pid); err != nil {
		t.Fatalf("apagar projeto: %v", err)
	}
	var n int
	if err := d.Leitor.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM config_entries WHERE project_id = ?`, pid).Scan(&n); err != nil {
		t.Fatalf("contar config: %v", err)
	}
	if n != 0 {
		t.Fatalf("esperava 0 entradas após cascade, veio %d", n)
	}
}

func TestConfigValorNulo(t *testing.T) {
	d := abrirTemp(t)
	ctx := context.Background()

	// json.RawMessage vazio deve virar "null" na coluna (NOT NULL).
	if err := d.DefinirConfigGlobal(ctx, map[string]json.RawMessage{"vazio": nil}); err != nil {
		t.Fatalf("DefinirConfigGlobal: %v", err)
	}
	cfg, _ := d.ObterConfigGlobal(ctx)
	if string(cfg["vazio"]) != "null" {
		t.Fatalf("valor vazio = %q, quero null", cfg["vazio"])
	}
}
