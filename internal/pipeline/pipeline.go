package pipeline

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/marcos14/praxis-autonomous/internal/db"
	"github.com/marcos14/praxis-autonomous/internal/gitops"
	"github.com/marcos14/praxis-autonomous/internal/motor"
)

// Situacao e o desfecho da execucao de uma fase.
type Situacao string

const (
	SituacaoConcluida          Situacao = "concluida"
	SituacaoFalhou             Situacao = "falhou"
	SituacaoPausada            Situacao = "pausada"
	SituacaoAguardandoFranquia Situacao = "aguardando_franquia"
)

// ResultadoFase resume o desfecho de ExecutarFase para o scheduler. A situacao
// e sempre preenchida; o erro de retorno de ExecutarFase e reservado a falhas de
// INFRAESTRUTURA (banco/git/motor indisponivel), nao aos desfechos logicos da
// fase (que ficam aqui).
type ResultadoFase struct {
	Situacao    Situacao
	Erro        string     // motivo, quando Situacao == SituacaoFalhou
	CustoUSD    float64    // custo somado dos runs desta fase
	MotorExec   string     // motor efetivamente usado no executor
	CommitFeito bool       // houve commit local nesta fase
	RetomarEm   time.Time  // quando reagendar, se Situacao == SituacaoAguardandoFranquia
	FasesNovas  []FaseNova // fases aprovadas pelo revisor; enfileiramento e do scheduler (2d/2g)

	// Push automatico da branch (Fase 2f), preenchido pelo Runner APOS o commit
	// (ExecutarFase nao publica). Push tolerante a falha: uma falha aqui nao muda
	// a Situacao (a fase conclui mesmo assim).
	Publicado            bool // o push da branch concluiu nesta fase
	CommitsNaoPublicados int  // commits locais ainda aguardando push (0 = tudo publicado)
}

// ResultadoGates e o desfecho da bateria de gates. Espelha o ResultadoGates do
// gates.go do Praxis atual; a implementacao (semaforo global + execucao dos
// comandos) e portada na Fase 2c e injetada via ContextoExec.Gates.
type ResultadoGates struct {
	Ok       bool
	Ambiente bool   // falha de ambiente/config (comando ausente), nao do codigo
	Gate     string // "nome: comando" que falhou
	Erro     string // ultimas linhas da saida do comando que falhou
	LogPath  string
}

// Gates roda os gates deterministicos de uma fase num worktree. Fase 2c fornece
// a implementacao concreta; ate la, ContextoExec.Gates == nil e a etapa de gates
// e tratada como aprovada (a pipeline ja executa executor→revisor→commit).
type Gates interface {
	Rodar(ctx context.Context, dir string, fase db.Fase) (ResultadoGates, error)
}

// ContextoExec reune tudo que o ciclo de uma fase precisa, de forma EXPLICITA —
// substitui os globais/arquivos que o executar.go do Praxis atual lia (raiz do
// projeto, autopilot.json, fases.csv, prompts em disco, notificador). Quem monta
// e popula o ContextoExec e o scheduler (Fase 2d), a partir do banco.
type ContextoExec struct {
	Demanda  db.Demanda // demanda dona da fase (ID/ProjectID/PlanoMD)
	Fase     db.Fase    // fase a executar
	Worktree string     // worktree dedicado da demanda (cmd.Dir dos harnesses)
	DirLogs  string     // pasta dos .jsonl das execucoes
	Config   Config     // config ja resolvida (global→projeto) pelo scheduler
	Store    *db.DB     // fila/estado no banco (fases, runs, eventos)
	Git      *gitops.Ops
	Gates    Gates // nil ate a Fase 2c (etapa de gates tratada como aprovada)

	// Prompt carrega o template de um prompt por nome (executor.md/corretor.md/
	// revisor.md). No Praxis atual vinha de arquivo (carregarPrompt); aqui e
	// injetado (os prompts com default embutido no banco chegam nas Fases 3b/3c).
	Prompt func(nome string) (string, error)

	Ctx     context.Context
	PausaCh <-chan struct{}

	// RegistrarProcesso, quando != nil, registra o PID de cada processo de harness
	// desta fase (para a recuperacao pos-restart matar orfaos — Fase 2i). Repassado
	// a OpcoesRun; o Runner a preenche a partir de um *procs.Registro.
	RegistrarProcesso func(pid int) func()

	// Seams de teste (nil em producao):
	Selecionar func(nome string) (motor.Motor, error) // default: motor.Selecionar
	Agora      func() time.Time                       // default: time.Now
}

// ExecutarFase conduz o ciclo completo de uma fase no worktree da demanda:
// pre-checagem → executor → gates (+corretor) → revisor (+corretor) → commit
// local. Portado de pipelineFase (executar.go), adaptado ao ContextoExec.
//
// Adaptacoes ao Praxis Autonomous:
//   - estado da fase/execucoes/eventos vao para o BANCO (nao CSV/notificador);
//   - a "guarda do plano" (exigir que o arquivo do plano fosse editado) NAO se
//     aplica: o plano vive em demands.plano_md, nao num arquivo do repo alvo;
//   - as fases novas sugeridas pelo revisor sao DEVOLVIDAS (ResultadoFase.
//     FasesNovas) para o scheduler enfileirar — a pipeline nao mexe na fila;
//   - o estado da demanda (status/custo agregado) e mexido pelo scheduler, nao
//     aqui, para nao correr com a maquina de estados da demanda.
//
// O retorno de erro e reservado a falhas de infraestrutura (banco/git). Os
// desfechos logicos (concluida/falhou/pausada/aguardando_franquia) ficam em
// ResultadoFase.Situacao.
func (c *ContextoExec) ExecutarFase() (ResultadoFase, error) {
	ctx := c.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	f := c.Fase // copia local; persistimos as transicoes via Store

	// retomada: uma fase `pausada` foi interrompida no meio e pode ter trabalho
	// nao commitado que pertence a ela mesma — nao exigimos arvore limpa. Nos
	// demais casos, a arvore do worktree deve estar limpa antes de comecar.
	retomando := f.Status == db.StatusFasePausada
	if !retomando {
		limpo, err := gitops.Limpo(c.Worktree)
		if err != nil {
			return ResultadoFase{Situacao: SituacaoFalhou, Erro: err.Error()}, err
		}
		if !limpo {
			motivo := fmt.Sprintf("worktree %s tem mudancas nao commitadas antes da fase", c.Worktree)
			res, perr := c.finalizarFalha(&f, 0, "", motivo, nil)
			return res, perr
		}
	}

	// marca executando + tentativa e persiste
	f.Status = db.StatusFaseExecutando
	f.Tentativas++
	if err := c.persistirFase(ctx, &f); err != nil {
		return ResultadoFase{Situacao: SituacaoFalhou, Erro: err.Error()}, err
	}
	c.registrarEvento("fase_iniciada", fmt.Sprintf("Praxis: Fase %s iniciada", f.Codigo), f.Titulo)

	custo := 0.0
	motorExecutor := "claude"
	estadoFallback := NovoEstadoFallback()

	// rodar executa uma operacao com fallback, registrando a execucao no banco.
	// Devolve (resultado, motorUsado, erro). O erro pode ser *ErroFranquia (franquia
	// esgotada, sem fallback) ou o ctx cancelado (pausa), tratados pelo chamador.
	rodar := func(operacao, nomePrompt, rotulo string, extras map[string]string, schema string, somenteLeitura bool) (*motor.ResultadoRun, string, error) {
		if c.Prompt == nil {
			return nil, "", fmt.Errorf("pipeline sem provedor de prompts (Prompt == nil)")
		}
		tpl, err := c.Prompt(nomePrompt)
		if err != nil {
			return nil, "", err
		}
		valores := map[string]string{"FASE": f.Codigo, "TITULO": f.Titulo, "PLANO": c.Demanda.PlanoMD}
		for k, v := range extras {
			valores[k] = v
		}
		motorPrimario := c.Config.MotorParaOperacao(operacao)
		modelo := strings.TrimSpace(f.Modelo)
		if modelo == "" {
			modelo = c.Config.ModeloParaMotor(motorPrimario)
		}
		faseID := f.ID
		exec := db.Execucao{
			DemandID: c.Demanda.ID, PhaseID: &faseID,
			Operacao: operacaoDB(operacao), Engine: motorPrimario, Modelo: modelo,
		}
		exec, err = c.Store.CriarExecucao(ctx, exec)
		if err != nil {
			return nil, motorPrimario, fmt.Errorf("registrar execucao: %w", err)
		}
		op := motor.OpcoesRun{
			Dir: c.Worktree, DirLogs: c.DirLogs, Prompt: renderPrompt(tpl, valores),
			Modelo: modelo, Esforco: c.Config.EsforcoParaMotor(motorPrimario),
			AddDirs: c.Config.AddDirs, BudgetUSD: c.Config.BudgetFaseUSD, TimeoutMin: c.Config.TimeoutMin,
			Schema: schema, ProibirCommit: true, SomenteLeitura: somenteLeitura,
			RotuloLog: fmt.Sprintf("fase-%s-%s", f.Codigo, rotulo),
			Ctx:       ctx, PausaCh: c.PausaCh,
			RegistrarProcesso: c.RegistrarProcesso,
		}
		res, motorUsado, runErr := c.rodarComFallback(operacao, motorPrimario, op, estadoFallback)

		// fecha o registro da execucao (best-effort — nao mascara runErr).
		exec.Engine = motorUsado
		exec.TerminadoEm = c.agoraISO()
		if res != nil {
			exec.CustoUSD = res.CustoUSD
			exec.TokensIn = int64(res.TokensIn)
			exec.TokensOut = int64(res.TokensOut)
			exec.IsError = res.IsError
			exec.LogRef = res.LogPath
			custo += res.CustoUSD
		} else {
			exec.IsError = true
		}
		if _, err := c.Store.AtualizarExecucao(ctx, exec); err != nil {
			// falha ao atualizar o registro nao invalida o run em si; loga como evento.
			c.registrarEvento("aviso", "Praxis: falha ao registrar execucao", err.Error())
		}
		if runErr != nil {
			return res, motorUsado, runErr
		}
		// budget soft: motores sem budget nativo sao barrados pelo custo acumulado.
		if c.Config.BudgetFaseUSD > 0 && res != nil {
			if m, selErr := c.selecionar(motorUsado); selErr == nil && !m.Capacidades().BudgetNativo && custo > c.Config.BudgetFaseUSD {
				return res, motorUsado, fmt.Errorf("budget soft excedido para %s: custo acumulado ~US$ %.2f > limite US$ %.2f", motorUsado, custo, c.Config.BudgetFaseUSD)
			}
		}
		return res, motorUsado, nil
	}

	// tratarErro mapeia um erro de qualquer etapa para o desfecho apropriado
	// (franquia/pausa/falha) ja persistido.
	tratarErro := func(prefixo string, err error) (ResultadoFase, error) {
		var ef *ErroFranquia
		if errors.As(err, &ef) {
			return c.finalizarFranquia(&f, custo, ef)
		}
		if ctx.Err() != nil {
			return c.finalizarPausa(&f, custo)
		}
		return c.finalizarFalha(&f, custo, motorExecutor, fmt.Sprintf("%s: %v", prefixo, err), nil)
	}

	// corrigir roda o corretor (contexto limpo) a partir de um motivo.
	corrigir := func(rotulo, motivo string) error {
		c.registrarEvento("correcao_iniciada", fmt.Sprintf("Praxis: correcao iniciada na Fase %s", f.Codigo), motivo)
		res, _, err := rodar("corrigir", "corretor.md", rotulo, map[string]string{"MOTIVO": motivo}, "", false)
		if err != nil {
			return err
		}
		if res.IsError {
			return fmt.Errorf("corretor terminou com erro (%s) — log: %s", res.Subtipo, res.LogPath)
		}
		return nil
	}

	// gatesVerdes roda os gates e, a cada vermelho, um ciclo de corretor, ate o
	// limite. Sem runner de gates (ate a Fase 2c) e no-op aprovado.
	gatesVerdes := func() error {
		if c.Gates == nil {
			return nil
		}
		for tent := 0; ; tent++ {
			rg, err := c.Gates.Rodar(ctx, c.Worktree, f)
			if err != nil {
				return err
			}
			if rg.Ok {
				return nil
			}
			if rg.Ambiente {
				return fmt.Errorf("gate [%s] nao pode ser executado — parece problema de ambiente/config (binario ausente, PATH ou sintaxe incompativel com o shell), nao do codigo: %s\nlog: %s", rg.Gate, rg.Erro, rg.LogPath)
			}
			if tent >= c.Config.MaxCorrecoes {
				return fmt.Errorf("gates continuam vermelhos apos %d correcao(oes) — gate [%s], log: %s", tent, rg.Gate, rg.LogPath)
			}
			c.registrarEvento("gates_falharam", fmt.Sprintf("Praxis: gates falharam na Fase %s", f.Codigo),
				fmt.Sprintf("Gate: %s\nLog: %s", rg.Gate, rg.LogPath))
			motivo := fmt.Sprintf("Os comandos de verificacao (gates) falharam.\nGate: %s\nFinal da saida:\n```\n%s\n```", rg.Gate, rg.Erro)
			if err := corrigir(fmt.Sprintf("corretor%d", tent+1), motivo); err != nil {
				return err
			}
		}
	}

	// 1) executor — a fase em si, contexto limpo
	resExec, motorExec, err := rodar("executar", "executor.md", "executor", nil, "", false)
	if err != nil {
		return tratarErro("executor", err)
	}
	motorExecutor = motorExec
	if resExec.IsError {
		return c.finalizarFalha(&f, custo, motorExecutor,
			fmt.Sprintf("executor terminou com erro (%s) — log: %s", resExec.Subtipo, resExec.LogPath), nil)
	}

	// 2) gates + correcoes
	if err := gatesVerdes(); err != nil {
		return tratarErro("gates", err)
	}

	// 3) revisor (contexto limpo, so leitura) + no maximo MaxCiclosRevisao correcoes
	var fasesNovas []FaseNova
	for ciclo := 0; ; ciclo++ {
		resRev, _, err := rodar("revisar", "revisor.md", fmt.Sprintf("revisor%d", ciclo+1), nil, SchemaVeredito, true)
		if err != nil {
			return tratarErro("revisor", err)
		}
		var ver Veredito
		if resRev.IsError || motor.DecodificarEstruturado(resRev, &ver) != nil {
			return c.finalizarFalha(&f, custo, motorExecutor,
				fmt.Sprintf("revisor nao devolveu veredito valido — log: %s", resRev.LogPath), nil)
		}
		if ver.Aprovado() {
			fasesNovas = ver.FasesNovas
			break
		}
		c.registrarEvento("revisor_reprovou", fmt.Sprintf("Praxis: revisor reprovou a Fase %s", f.Codigo), strings.Join(ver.Problemas, "\n"))
		if ciclo >= c.Config.MaxCiclosRevisao {
			return c.finalizarFalha(&f, custo, motorExecutor,
				fmt.Sprintf("revisor reprovou apos %d ciclo(s) de correcao: %s", ciclo, strings.Join(ver.Problemas, " | ")), nil)
		}
		motivo := "O revisor de codigo REPROVOU a entrega com os problemas:\n- " + strings.Join(ver.Problemas, "\n- ")
		if err := corrigir(fmt.Sprintf("corretor-rev%d", ciclo+1), motivo); err != nil {
			return tratarErro("corretor", err)
		}
		if err := gatesVerdes(); err != nil {
			return tratarErro("gates", err)
		}
	}

	// 4) commit local do worktree (sem push — push automatico e a Fase 2f)
	commitFeito := false
	limpo, err := gitops.Limpo(c.Worktree)
	if err != nil {
		return ResultadoFase{Situacao: SituacaoFalhou, Erro: err.Error(), CustoUSD: custo, MotorExec: motorExecutor}, err
	}
	if !limpo {
		trailer := motor.CoAuthorTrailer(motorExecutor)
		if trailer != "" {
			trailer = "\n\n" + trailer
		}
		msg := fmt.Sprintf("Fase %s: %s [praxis]\n\n%s%s\n",
			f.Codigo, f.Titulo, primeirasLinhas(strings.TrimSpace(resExec.Resultado), 15), trailer)
		if err := c.Git.Commit(c.Worktree, msg); err != nil {
			return ResultadoFase{Situacao: SituacaoFalhou, Erro: err.Error(), CustoUSD: custo, MotorExec: motorExecutor}, err
		}
		commitFeito = true
	}

	// 5) fecha a fase
	return c.finalizarConcluida(&f, custo, motorExecutor, commitFeito, fasesNovas)
}

// finalizarConcluida marca a fase como concluida e devolve o resultado.
func (c *ContextoExec) finalizarConcluida(f *db.Fase, custo float64, motorExec string, commitFeito bool, fasesNovas []FaseNova) (ResultadoFase, error) {
	f.Status = db.StatusFaseConcluida
	f.CustoUSD += custo
	f.ConcluidoEm = c.agoraISO()
	f.Observacao = fmt.Sprintf("ok — custo US$ %.2f", custo)
	if err := c.persistirFase(context.Background(), f); err != nil {
		return ResultadoFase{Situacao: SituacaoConcluida, CustoUSD: custo, MotorExec: motorExec, CommitFeito: commitFeito, FasesNovas: fasesNovas}, err
	}
	c.registrarEvento("fase_concluida", fmt.Sprintf("Praxis: Fase %s concluida", f.Codigo), f.Observacao)
	return ResultadoFase{
		Situacao: SituacaoConcluida, CustoUSD: custo, MotorExec: motorExec,
		CommitFeito: commitFeito, FasesNovas: fasesNovas,
	}, nil
}

// finalizarFalha marca a fase como falhou e devolve o resultado.
func (c *ContextoExec) finalizarFalha(f *db.Fase, custo float64, motorExec, motivo string, fasesNovas []FaseNova) (ResultadoFase, error) {
	f.Status = db.StatusFaseFalhou
	f.CustoUSD += custo
	f.Observacao = primeirasLinhas(motivo, 1)
	var perr error
	if err := c.persistirFase(context.Background(), f); err != nil {
		perr = err
	}
	c.registrarEvento("fase_falhou", fmt.Sprintf("Praxis: Fase %s falhou", f.Codigo), motivo)
	return ResultadoFase{Situacao: SituacaoFalhou, Erro: motivo, CustoUSD: custo, MotorExec: motorExec, FasesNovas: fasesNovas}, perr
}

// finalizarPausa marca a fase como pausada (retomavel) apos cancelamento do ctx.
func (c *ContextoExec) finalizarPausa(f *db.Fase, custo float64) (ResultadoFase, error) {
	f.Status = db.StatusFasePausada
	f.CustoUSD += custo
	f.Observacao = "pausada — retomavel"
	var perr error
	if err := c.persistirFase(context.Background(), f); err != nil {
		perr = err
	}
	c.registrarEvento("fase_pausada", fmt.Sprintf("Praxis: Fase %s pausada", f.Codigo), f.Observacao)
	return ResultadoFase{Situacao: SituacaoPausada, CustoUSD: custo}, perr
}

// finalizarFranquia marca a fase como pausada (retomavel) e devolve o horario de
// retomada para o scheduler reagendar. NAO bloqueia (contraste com o Praxis
// atual, que dormia ate o reset).
func (c *ContextoExec) finalizarFranquia(f *db.Fase, custo float64, ef *ErroFranquia) (ResultadoFase, error) {
	f.Status = db.StatusFasePausada
	f.CustoUSD += custo
	f.Observacao = fmt.Sprintf("aguardando franquia (%s) — retomar em %s", ef.Motor, ef.RetomarEm.Format(time.RFC3339))
	var perr error
	if err := c.persistirFase(context.Background(), f); err != nil {
		perr = err
	}
	c.registrarEvento("franquia_esgotada", "Praxis: franquia de tokens esgotada",
		fmt.Sprintf("Fase %s — %s\n%s\nRetomo automatico apos o reset.", f.Codigo, f.Titulo, ef.Detalhe))
	return ResultadoFase{Situacao: SituacaoAguardandoFranquia, CustoUSD: custo, RetomarEm: ef.RetomarEm}, perr
}

// persistirFase grava a fase no banco (Store) mantendo c.Fase espelhado.
func (c *ContextoExec) persistirFase(ctx context.Context, f *db.Fase) error {
	if c.Store == nil {
		c.Fase = *f
		return nil
	}
	atual, err := c.Store.AtualizarFase(ctx, *f)
	if err != nil {
		return fmt.Errorf("persistir fase %s: %w", f.Codigo, err)
	}
	*f = atual
	c.Fase = atual
	return nil
}

// operacaoDB traduz o nome interno da operacao (executar/corrigir/revisar) para a
// constante da coluna runs.operacao.
func operacaoDB(operacao string) string {
	switch operacao {
	case "executar":
		return db.OperacaoExecutor
	case "corrigir":
		return db.OperacaoCorretor
	case "revisar":
		return db.OperacaoRevisor
	default:
		return operacao
	}
}

// evento monta um db.Evento com project_id/demand_id opcionais (0 → NULL).
func evento(projectID, demandID int64, tipo, titulo, detalhe string) db.Evento {
	e := db.Evento{Tipo: tipo, Titulo: titulo, Detalhe: detalhe}
	if projectID > 0 {
		pid := projectID
		e.ProjectID = &pid
	}
	if demandID > 0 {
		did := demandID
		e.DemandID = &did
	}
	return e
}

// agoraISO devolve o horario atual em ISO-8601 UTC, no mesmo formato dos
// timestamps gravados pelo banco (strftime '%Y-%m-%dT%H:%M:%fZ').
func (c *ContextoExec) agoraISO() string {
	return c.agora().UTC().Format("2006-01-02T15:04:05.000Z")
}

// renderPrompt substitui os marcadores {VAR} do template pelos valores. Portado
// de renderPrompt (util.go) do Praxis atual.
func renderPrompt(tpl string, valores map[string]string) string {
	pares := make([]string, 0, len(valores)*2)
	for k, v := range valores {
		pares = append(pares, "{"+k+"}", v)
	}
	return strings.NewReplacer(pares...).Replace(tpl)
}

// primeirasLinhas devolve as primeiras n linhas de s (portado de util.go).
func primeirasLinhas(s string, n int) string {
	linhas := strings.Split(s, "\n")
	if len(linhas) <= n {
		return s
	}
	return strings.Join(linhas[:n], "\n")
}
