package consultor

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/marcos14/praxis-autonomous/internal/db"
	"github.com/marcos14/praxis-autonomous/internal/intake"
	"github.com/marcos14/praxis-autonomous/internal/motor"
)

// SchemaOverview é o JSON Schema da saída do gerador de overview.
const SchemaOverview = `{"type":"object","required":["overview_md"],"properties":{
"overview_md":{"type":"string"}}}`

// SaidaOverview é a saída estruturada do gerador de overview.
type SaidaOverview struct {
	OverviewMD string `json:"overview_md"`
}

// GeradorOverview roda o harness em modo somente leitura sobre o repo do
// projeto para produzir o overview de negócio (markdown sem código) que a
// feature de consultas injeta como contexto. Mesma anatomia do Consultor.
type GeradorOverview struct {
	Store *db.DB

	Motor      string
	Modelo     string
	Esforco    string
	Conta      string // alias do perfil usado (registro no run); "" = sem conta
	ConfigDir  string
	Dir        string
	DirLogs    string
	AddDirs    []string
	BudgetUSD  float64
	TimeoutMin int

	// Seams de teste (nil em produção):
	Selecionar func(nome string) (motor.Motor, error)
	Prompt     func(ctx context.Context, nome string) (string, error)
	Agora      func() time.Time
}

// Gerar produz e persiste o overview do projeto. A saída também passa pelo
// pós-filtro anti-código (o overview será exibido e injetado em prompts — não
// pode conter código). Registra a execução em consulta_runs (operacao=overview)
// e emite o evento overview_gerado (o SSE global avisa a UI).
func (g *GeradorOverview) Gerar(ctx context.Context, projectID int64) error {
	proj, err := g.Store.ObterProjeto(ctx, projectID)
	if err != nil {
		return fmt.Errorf("overview: obter projeto %d: %w", projectID, err)
	}

	tpl, err := g.prompt(ctx, intake.PromptOverview)
	if err != nil {
		return g.falhar(proj, fmt.Errorf("overview: resolver prompt: %w", err))
	}

	exec, err := g.Store.CriarExecucaoConsulta(ctx, db.ExecucaoConsulta{
		ProjectID: &proj.ID, Operacao: db.OperacaoOverview, Engine: g.Motor, Conta: g.Conta, Modelo: g.Modelo,
	})
	if err != nil {
		return g.falhar(proj, fmt.Errorf("overview: registrar execução: %w", err))
	}

	m, err := g.selecionar(g.Motor)
	if err != nil {
		g.fecharExecComErro(ctx, exec)
		return g.falhar(proj, err)
	}
	res, runErr := m.Rodar(motor.OpcoesRun{
		Dir: g.Dir, DirLogs: g.DirLogs,
		Prompt: renderPrompt(tpl, map[string]string{"PROJETO": proj.Nome}),
		Modelo: g.Modelo, Esforco: g.Esforco, PerfilDir: g.ConfigDir,
		AddDirs: g.AddDirs, BudgetUSD: g.BudgetUSD, TimeoutMin: g.TimeoutMin,
		Schema: SchemaOverview, SomenteLeitura: true, ProibirCommit: true,
		RotuloLog: fmt.Sprintf("overview-p%d", proj.ID), Ctx: ctx,
	})

	exec.TerminadoEm = g.agoraISO()
	exec.Engine = m.Nome()
	if res != nil {
		exec.CustoUSD = res.CustoUSD
		exec.TokensIn = int64(res.TokensIn)
		exec.TokensOut = int64(res.TokensOut)
		exec.IsError = res.IsError
		exec.LogRef = res.LogPath
	} else {
		exec.IsError = true
	}
	if _, err := g.Store.AtualizarExecucaoConsulta(ctx, exec); err != nil {
		g.registrarEvento(proj, "aviso", "Praxis: falha ao registrar execução do overview", err.Error())
	}
	if runErr != nil {
		return g.falhar(proj, runErr)
	}
	if res.IsError {
		return g.falhar(proj, fmt.Errorf("geração terminou com erro (%s)", motor.ResumoErro(res)))
	}

	var saida SaidaOverview
	if err := motor.DecodificarEstruturado(res, &saida); err != nil {
		return g.falhar(proj, fmt.Errorf("geração não devolveu JSON válido: %w", err))
	}
	limpo, redigidos, recusar := Sanitizar(strings.TrimSpace(saida.OverviewMD))
	if recusar || strings.TrimSpace(limpo) == "" {
		return g.falhar(proj, fmt.Errorf("overview gerado era técnico demais (%d trecho(s) redigido(s)) — tente de novo", redigidos))
	}
	if err := g.Store.AtualizarOverview(ctx, proj.ID, limpo); err != nil {
		return g.falhar(proj, fmt.Errorf("persistir overview: %w", err))
	}
	g.registrarEvento(proj, "overview_gerado", "Praxis: overview do repositório gerado",
		fmt.Sprintf("%s · custo US$ %.2f", proj.Nome, exec.CustoUSD))
	return nil
}

// falhar registra o evento de falha e devolve o erro (a geração de overview não
// tem estado próprio a carimbar — o overview anterior fica como estava).
func (g *GeradorOverview) falhar(proj db.Projeto, err error) error {
	g.registrarEvento(proj, "overview_falhou", "Praxis: geração do overview falhou", err.Error())
	return fmt.Errorf("overview do projeto %d: %w", proj.ID, err)
}

// registrarEvento grava um evento do projeto (best-effort).
func (g *GeradorOverview) registrarEvento(proj db.Projeto, tipo, titulo, detalhe string) {
	pid := proj.ID
	_, _ = g.Store.RegistrarEvento(context.Background(), db.Evento{
		ProjectID: &pid, Tipo: tipo, Titulo: titulo, Detalhe: detalhe,
	})
}

// fecharExecComErro marca a execução como erro (best-effort) quando o run nem
// chegou a rodar.
func (g *GeradorOverview) fecharExecComErro(ctx context.Context, exec db.ExecucaoConsulta) {
	exec.IsError = true
	exec.TerminadoEm = g.agoraISO()
	_, _ = g.Store.AtualizarExecucaoConsulta(ctx, exec)
}

func (g *GeradorOverview) selecionar(nome string) (motor.Motor, error) {
	if g.Selecionar != nil {
		return g.Selecionar(nome)
	}
	return motor.Selecionar(nome)
}

func (g *GeradorOverview) prompt(ctx context.Context, nome string) (string, error) {
	if g.Prompt != nil {
		return g.Prompt(ctx, nome)
	}
	return intake.ResolverPrompt(ctx, g.Store, nome)
}

func (g *GeradorOverview) agora() time.Time {
	if g.Agora != nil {
		return g.Agora()
	}
	return time.Now()
}

func (g *GeradorOverview) agoraISO() string {
	return g.agora().UTC().Format("2006-01-02T15:04:05.000Z")
}
