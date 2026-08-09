// Package importador traz projetos do Praxis clássico (autopilot.json + fases.csv
// em automacao/) para o banco do Praxis Autonomous (Fase 5e). É opcional e
// idempotente: reimportar a mesma pasta não duplica projeto nem demanda.
package importador

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/marcos14/praxis-autonomous/internal/db"
)

const (
	dirAutomacao = "automacao"
	nomeConfig   = "autopilot.json"
	nomeCSV      = "fases.csv"
)

// configClassica é o subconjunto de autopilot.json que o importador lê.
type configClassica struct {
	Plano            string   `json:"plano"`
	Projeto          string   `json:"projeto"`
	Motor            string   `json:"motor"`
	AddDirs          []string `json:"add_dirs"`
	MaxBudgetUSD     float64  `json:"max_budget_usd"`
	TimeoutMin       int      `json:"timeout_min"`
	MaxCorrecoes     int      `json:"max_correcoes"`
	MaxCiclosRevisao int      `json:"max_ciclos_revisao"`
	MaxFasesNovas    int      `json:"max_fases_novas"`
	Gates            []struct {
		Comandos []string `json:"comandos"`
	} `json:"gates"`
	Motores struct {
		Ordem []string `json:"ordem"`
	} `json:"motores"`
}

// Resultado descreve o desfecho de uma importação.
type Resultado struct {
	Nome      string `json:"nome"`
	ProjectID int64  `json:"project_id"`
	Criado    bool   `json:"criado"`    // false = já existia (idempotência)
	Fases     int    `json:"fases"`     // fases pendentes importadas para a demanda
	DemandID  int64  `json:"demand_id"` // 0 se nenhuma demanda foi criada
}

// Store é o mínimo do banco que o importador usa.
type Store interface {
	ListarProjetos(ctx context.Context) ([]db.Projeto, error)
	CriarProjeto(ctx context.Context, p db.Projeto) (db.Projeto, error)
	DefinirConfigProjeto(ctx context.Context, projectID int64, entradas map[string]json.RawMessage) error
	CriarDemandaComFases(ctx context.Context, dem db.Demanda, fases []db.Fase) (db.Demanda, []db.Fase, error)
}

// Importar lê automacao/autopilot.json (e fases.csv, se houver) da pasta do
// projeto e cria o projeto + config correspondentes. Idempotente: se já existe um
// projeto apontando para a mesma pasta, não faz nada (Criado=false). As fases
// PENDENTES da fila clássica viram uma demanda `pausada` (o usuário decide quando
// retomar); fases já concluídas são ignoradas.
func Importar(ctx context.Context, store Store, pastaProjeto string) (Resultado, error) {
	pasta, err := filepath.Abs(pastaProjeto)
	if err != nil {
		return Resultado{}, fmt.Errorf("resolver pasta: %w", err)
	}

	// idempotência: já importado?
	existentes, err := store.ListarProjetos(ctx)
	if err != nil {
		return Resultado{}, err
	}
	for _, p := range existentes {
		if mesmaPasta(p.Pasta, pasta) {
			return Resultado{Nome: p.Nome, ProjectID: p.ID, Criado: false}, nil
		}
	}

	cfg, err := lerConfig(filepath.Join(pasta, dirAutomacao, nomeConfig))
	if err != nil {
		return Resultado{}, err
	}

	nome := strings.TrimSpace(cfg.Projeto)
	if nome == "" {
		nome = filepath.Base(pasta)
	}
	proj, err := store.CriarProjeto(ctx, db.Projeto{
		Nome:            nome,
		Pasta:           pasta,
		BranchPrincipal: "main",
		ModoIntegracao:  db.ModoIntegracaoMergeRequest,
		AddDirs:         cfg.AddDirs,
		Ativo:           true,
	})
	if err != nil {
		return Resultado{}, fmt.Errorf("criar projeto importado: %w", err)
	}

	if entradas := configEntries(cfg); len(entradas) > 0 {
		if err := store.DefinirConfigProjeto(ctx, proj.ID, entradas); err != nil {
			return Resultado{}, fmt.Errorf("gravar config importada: %w", err)
		}
	}

	res := Resultado{Nome: proj.Nome, ProjectID: proj.ID, Criado: true}

	// fases pendentes da fila clássica → demanda pausada (best-effort).
	fases := lerFasesPendentes(filepath.Join(pasta, dirAutomacao, nomeCSV))
	if len(fases) > 0 {
		plano := lerPlano(pasta, cfg.Plano)
		dem, criadas, err := store.CriarDemandaComFases(ctx, db.Demanda{
			ProjectID: proj.ID,
			Titulo:    nome + " — importado do Praxis clássico",
			Origem:    db.OrigemUI,
			Status:    db.StatusDemandaPausada,
			PlanoMD:   plano,
			BudgetUSD: cfg.MaxBudgetUSD,
		}, fases)
		if err != nil {
			return res, fmt.Errorf("importar fases: %w", err)
		}
		res.DemandID = dem.ID
		res.Fases = len(criadas)
	}
	return res, nil
}

// lerConfig carrega e decodifica o autopilot.json.
func lerConfig(caminho string) (configClassica, error) {
	b, err := os.ReadFile(caminho)
	if err != nil {
		return configClassica{}, fmt.Errorf("ler %s: %w", caminho, err)
	}
	var cfg configClassica
	if err := json.Unmarshal(b, &cfg); err != nil {
		return configClassica{}, fmt.Errorf("autopilot.json inválido em %s: %w", caminho, err)
	}
	return cfg, nil
}

// configEntries mapeia a config clássica para as chaves de config do novo modelo.
func configEntries(cfg configClassica) map[string]json.RawMessage {
	entradas := map[string]json.RawMessage{}
	set := func(chave string, valor any) {
		if b, err := json.Marshal(valor); err == nil {
			entradas[chave] = b
		}
	}
	if cfg.MaxBudgetUSD > 0 {
		set("budget_demanda_usd", cfg.MaxBudgetUSD)
	}
	if cfg.MaxCorrecoes > 0 {
		set("max_correcoes", cfg.MaxCorrecoes)
	}
	if cfg.MaxCiclosRevisao > 0 {
		set("max_ciclos_revisao", cfg.MaxCiclosRevisao)
	}
	if cfg.MaxFasesNovas != 0 {
		set("max_fases_novas", cfg.MaxFasesNovas)
	}
	if motor := motorPreferido(cfg); motor != "" {
		set("motor_preferido", motor)
	}
	if gates := comandosDeGates(cfg); len(gates) > 0 {
		set("gates", gates)
	}
	return entradas
}

// motorPreferido deriva o motor preferido: o campo legado `motor` ou o primeiro
// da ordem de fallback.
func motorPreferido(cfg configClassica) string {
	if m := strings.TrimSpace(cfg.Motor); m != "" {
		return m
	}
	if len(cfg.Motores.Ordem) > 0 {
		return strings.TrimSpace(cfg.Motores.Ordem[0])
	}
	return ""
}

// comandosDeGates achata os comandos de todos os gates clássicos numa lista de
// strings (o formato do campo de config "gates" do novo modelo).
func comandosDeGates(cfg configClassica) []string {
	cmds := []string{}
	for _, g := range cfg.Gates {
		for _, c := range g.Comandos {
			if c = strings.TrimSpace(c); c != "" {
				cmds = append(cmds, c)
			}
		}
	}
	return cmds
}

// lerFasesPendentes lê o fases.csv (separador ';') e devolve as fases ainda não
// concluídas como []db.Fase. Ausência do arquivo → nil (sem erro). Formato:
// fase;titulo;status;depende_de;requer_humano;notificar;gate_extra;modelo;...
func lerFasesPendentes(caminho string) []db.Fase {
	arq, err := os.Open(caminho)
	if err != nil {
		return nil
	}
	defer arq.Close()

	r := csv.NewReader(arq)
	r.Comma = ';'
	r.FieldsPerRecord = -1
	linhas, err := r.ReadAll()
	if err != nil || len(linhas) < 2 {
		return nil
	}
	cab := map[string]int{}
	for i, c := range linhas[0] {
		cab[strings.TrimSpace(strings.ToLower(c))] = i
	}
	campo := func(l []string, nome string) string {
		if i, ok := cab[nome]; ok && i < len(l) {
			return strings.TrimSpace(l[i])
		}
		return ""
	}

	fases := []db.Fase{}
	ordem := 0
	for _, l := range linhas[1:] {
		status := strings.ToLower(campo(l, "status"))
		if status == "concluida" || status == "concluída" {
			continue // já feita no Praxis clássico
		}
		codigo := campo(l, "fase")
		titulo := campo(l, "titulo")
		if codigo == "" || titulo == "" {
			continue
		}
		ordem++
		fases = append(fases, db.Fase{
			Codigo:       codigo,
			Titulo:       titulo,
			Status:       db.StatusFasePendente,
			DependeDe:    separarDeps(campo(l, "depende_de")),
			RequerHumano: ehSim(campo(l, "requer_humano")),
			GateExtra:    campo(l, "gate_extra"),
			Modelo:       campo(l, "modelo"),
			Observacao:   campo(l, "observacao"),
			Ordem:        ordem,
		})
	}
	return fases
}

// separarDeps divide "1a+1b" em ["1a","1b"] (formato de dependências do CSV).
func separarDeps(s string) []string {
	deps := []string{}
	for _, d := range strings.Split(s, "+") {
		if d = strings.TrimSpace(d); d != "" {
			deps = append(deps, d)
		}
	}
	return deps
}

// ehSim reconhece os valores afirmativos usados no CSV clássico.
func ehSim(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "sim", "s", "true", "1", "yes":
		return true
	}
	return false
}

// lerPlano carrega o markdown do plano referenciado por cfg.Plano (relativo à
// pasta do projeto). Ausente → "".
func lerPlano(pasta, plano string) string {
	plano = strings.TrimSpace(plano)
	if plano == "" {
		return ""
	}
	caminho := plano
	if !filepath.IsAbs(caminho) {
		caminho = filepath.Join(pasta, plano)
	}
	if b, err := os.ReadFile(caminho); err == nil {
		return string(b)
	}
	return ""
}

// mesmaPasta compara dois caminhos de projeto de forma tolerante (limpa e
// normaliza os separadores).
func mesmaPasta(a, b string) bool {
	ca, err1 := filepath.Abs(a)
	cb, err2 := filepath.Abs(b)
	if err1 != nil {
		ca = filepath.Clean(a)
	}
	if err2 != nil {
		cb = filepath.Clean(b)
	}
	return strings.EqualFold(filepath.Clean(ca), filepath.Clean(cb))
}
