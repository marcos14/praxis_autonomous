package main

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/marcos14/praxis-autonomous/internal/db"
	"github.com/marcos14/praxis-autonomous/internal/gitops"
	"github.com/marcos14/praxis-autonomous/internal/intake"
	"github.com/marcos14/praxis-autonomous/internal/pipeline"
	"github.com/marcos14/praxis-autonomous/internal/procs"
	"github.com/marcos14/praxis-autonomous/internal/scheduler"
)

// iniciarScheduler monta e sobe o scheduler que executa as demandas em background
// (fecha a lacuna 2g.n1: mecanismo → produto rodando de fato). Liga a fila do
// banco (FonteBanco) ao Runner do pipeline (worktree + executor→gates→corretor→
// revisor→commit por fase) via ExecutorDemanda. Devolve o *scheduler.Scheduler
// para ser passado à API como ControladorExecucao (pausar/cancelar interrompem o
// worker ao vivo). Roda em goroutine ligada ao ctx de vida do serviço.
//
// Falha ao resolver PRAXIS_HOME/registro de PIDs não impede subir o resto do
// serviço: devolve nil (a API opera sem controlador — as ações só transitam o
// status no banco, como antes do wiring).
func iniciarScheduler(ctx context.Context, banco *db.DB, git *gitops.Ops, registro *procs.Registro, logger *slog.Logger) *scheduler.Scheduler {
	home, err := db.PraxisHome()
	if err != nil {
		logger.Warn("scheduler: resolver PRAXIS_HOME", "erro", err)
		return nil
	}

	logf := func(msg string) { logger.Info(msg) }

	// Limites vindos da config global (com defaults do próprio scheduler/gates).
	maxGlobal, maxPorProjeto, gatesSimultaneos := limitesGlobais(ctx, banco, logger)

	runner := &pipeline.Runner{
		Store:    banco,
		Git:      git,
		Home:     home,
		Prompt:   intake.ProvedorPrompt(ctx, banco),
		Procs:    registro,
		SemGates: pipeline.NovoSemaforoGates(gatesSimultaneos),
	}
	executor := &scheduler.ExecutorDemanda{Store: banco, Runner: runner, Log: logf}

	sched := scheduler.Novo(scheduler.Opcoes{
		Fonte:         scheduler.NovaFonteBanco(banco),
		Executor:      executor,
		Store:         banco,
		MaxGlobal:     maxGlobal,
		MaxPorProjeto: maxPorProjeto,
		// Perfis são resolvidos dinamicamente do banco por motor e demanda no
		// ExecutorDemanda. A lista antiga usava aliases globais, colidia entre
		// vendors e exigia reiniciar o serviço após qualquer alteração.
		Contas: nil,
		Log:    logf,
	})
	go sched.Rodar(ctx)
	logger.Info("scheduler no ar", "max_global", maxGlobal, "max_por_projeto", maxPorProjeto, "gates_simultaneos", gatesSimultaneos)
	return sched
}

// limitesGlobais lê os limites de concorrência da config global (chaves
// execucoes_simultaneas / execucoes_por_projeto / gates_simultaneos). Ausente ou
// inválido → 0, deixando o scheduler aplicar seus próprios defaults.
func limitesGlobais(ctx context.Context, banco *db.DB, logger *slog.Logger) (maxGlobal, maxPorProjeto, gatesSimultaneos int) {
	entradas, err := banco.ObterConfigGlobal(ctx)
	if err != nil {
		logger.Warn("scheduler: ler config global", "erro", err)
		return 0, -1, 0
	}
	lerInt := func(chave string, def int) int {
		bruto, ok := entradas[chave]
		if !ok {
			return def
		}
		var f float64
		if err := json.Unmarshal(bruto, &f); err != nil {
			return def
		}
		return int(f)
	}
	// maxPorProjeto=-1 sinaliza "usar default do scheduler" quando a chave falta.
	mpp := -1
	if _, ok := entradas["execucoes_por_projeto"]; ok {
		mpp = lerInt("execucoes_por_projeto", -1)
	}
	return lerInt("execucoes_simultaneas", 0), mpp, lerInt("gates_simultaneos", 0)
}

// contasParaAfinidade coleta os aliases de contas ativas dos motores, para a
// afinidade conta↔demanda do scheduler (uma demanda fixa sua conta enquanto
// executa). Vazio = sem restrição de conta (execuções não competem por conta).
func contasParaAfinidade(ctx context.Context, banco *db.DB, logger *slog.Logger) []string {
	motores, err := banco.ListarMotores(ctx)
	if err != nil {
		logger.Warn("scheduler: listar motores para contas", "erro", err)
		return nil
	}
	vistos := map[string]bool{}
	contas := []string{}
	for _, m := range motores {
		if !m.Ativo {
			continue
		}
		for _, c := range m.Contas {
			if c.Ativo && c.Alias != "" && !vistos[c.Alias] {
				vistos[c.Alias] = true
				contas = append(contas, c.Alias)
			}
		}
	}
	return contas
}
