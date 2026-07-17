package importador

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/marcos14/praxis-autonomous/internal/db"
)

func abrirBanco(t *testing.T) *db.DB {
	t.Helper()
	d, err := db.Abrir(filepath.Join(t.TempDir(), "praxis.db"))
	if err != nil {
		t.Fatalf("abrir banco: %v", err)
	}
	t.Cleanup(func() { _ = d.Fechar() })
	return d
}

// pastaClassica monta a estrutura de um projeto do Praxis clássico com
// automacao/autopilot.json e fases.csv, e devolve o caminho da pasta.
func pastaClassica(t *testing.T) string {
	t.Helper()
	pasta := t.TempDir()
	autom := filepath.Join(pasta, "automacao")
	if err := os.MkdirAll(autom, 0o755); err != nil {
		t.Fatal(err)
	}
	autopilot := `{
		"projeto": "ERP Junsoft",
		"plano": "PLANO.md",
		"motor": "claude",
		"add_dirs": ["docs", "shared"],
		"max_budget_usd": 12.5,
		"max_correcoes": 3,
		"max_ciclos_revisao": 2,
		"max_fases_novas": 5,
		"gates": [{"nome":"build","comandos":["go build ./...","go vet ./..."]}],
		"motores": {"ordem": ["claude","codex"]}
	}`
	if err := os.WriteFile(filepath.Join(autom, "autopilot.json"), []byte(autopilot), 0o644); err != nil {
		t.Fatal(err)
	}
	csv := "fase;titulo;status;depende_de;requer_humano;notificar;gate_extra;modelo;tentativas;custo_usd;concluido_em;observacao\n" +
		"0;Fundação;concluida;;nao;nao;;;1;2.00;2026-07-15;ok\n" +
		"1a;Camada de banco;pendente;0;nao;nao;;;0;0;;\n" +
		"1b;Servidor HTTP;pendente;0;sim;nao;;;0;0;;\n"
	if err := os.WriteFile(filepath.Join(autom, "fases.csv"), []byte(csv), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pasta, "PLANO.md"), []byte("# Plano do ERP"), 0o644); err != nil {
		t.Fatal(err)
	}
	return pasta
}

func TestImportarCriaProjetoConfigEFases(t *testing.T) {
	d := abrirBanco(t)
	ctx := context.Background()
	pasta := pastaClassica(t)

	res, err := Importar(ctx, d, pasta)
	if err != nil {
		t.Fatalf("Importar: %v", err)
	}
	if !res.Criado {
		t.Fatal("deveria ter criado o projeto")
	}
	if res.Nome != "ERP Junsoft" {
		t.Fatalf("nome = %q, quero ERP Junsoft", res.Nome)
	}
	// 2 fases pendentes (a concluída é ignorada).
	if res.Fases != 2 {
		t.Fatalf("fases importadas = %d, quero 2", res.Fases)
	}

	// projeto persistido com add_dirs.
	proj, err := d.ObterProjeto(ctx, res.ProjectID)
	if err != nil {
		t.Fatalf("obter projeto: %v", err)
	}
	if len(proj.AddDirs) != 2 {
		t.Fatalf("add_dirs = %v, quero 2", proj.AddDirs)
	}

	// config do projeto mapeada.
	cfg, err := d.ObterConfigProjeto(ctx, res.ProjectID)
	if err != nil {
		t.Fatalf("config projeto: %v", err)
	}
	if _, ok := cfg["budget_demanda_usd"]; !ok {
		t.Fatalf("config sem budget_demanda_usd: %v", cfg)
	}
	if _, ok := cfg["gates"]; !ok {
		t.Fatalf("config sem gates: %v", cfg)
	}
	if _, ok := cfg["motor_preferido"]; !ok {
		t.Fatalf("config sem motor_preferido: %v", cfg)
	}

	// a demanda importada nasce pausada, com o plano.
	demandas, _ := d.ListarDemandas(ctx, db.FiltroDemandas{})
	if len(demandas) != 1 {
		t.Fatalf("demandas = %d, quero 1", len(demandas))
	}
	if demandas[0].Status != db.StatusDemandaPausada {
		t.Fatalf("status = %q, quero pausada", demandas[0].Status)
	}
	if demandas[0].PlanoMD == "" {
		t.Fatal("plano_md não importado")
	}
}

func TestImportarIdempotente(t *testing.T) {
	d := abrirBanco(t)
	ctx := context.Background()
	pasta := pastaClassica(t)

	if _, err := Importar(ctx, d, pasta); err != nil {
		t.Fatalf("1ª importação: %v", err)
	}
	res, err := Importar(ctx, d, pasta)
	if err != nil {
		t.Fatalf("2ª importação: %v", err)
	}
	if res.Criado {
		t.Fatal("reimportar não deveria criar de novo")
	}
	// nada duplicado.
	projs, _ := d.ListarProjetos(ctx)
	if len(projs) != 1 {
		t.Fatalf("projetos = %d, quero 1 (sem duplicar)", len(projs))
	}
	demandas, _ := d.ListarDemandas(ctx, db.FiltroDemandas{})
	if len(demandas) != 1 {
		t.Fatalf("demandas = %d, quero 1 (sem duplicar)", len(demandas))
	}
}

func TestImportarSemAutopilotFalha(t *testing.T) {
	d := abrirBanco(t)
	if _, err := Importar(context.Background(), d, t.TempDir()); err == nil {
		t.Fatal("pasta sem autopilot.json deveria falhar")
	}
}
