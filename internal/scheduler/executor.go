package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/marcos14/praxis-autonomous/internal/db"
	"github.com/marcos14/praxis-autonomous/internal/pipeline"
)

// ExecutorDemanda é o Executor real do scheduler (Fase 2g): liga a fila do
// scheduler ao Runner do pipeline (Fases 2e/2f). A cada chamada de Executar ele
// prepara o worktree/branch da demanda (idempotente), escolhe a PRÓXIMA fase
// elegível (respeitando depende_de e requer_humano) e roda o ciclo completo
// dessa fase; o scheduler cuida da concorrência e do redespacho.
//
// Uma fase por chamada: quando ainda há trabalho, devolve Desfecho{Concluido:
// false} e o scheduler redespacha a demanda assim que houver folga — assim uma
// demanda longa não prende o worker entre fases e várias demandas progridem em
// paralelo (o critério da Fase 2g: 2 demandas → 2 branches independentes).
type ExecutorDemanda struct {
	Store  *db.DB           // fila/estado no banco
	Runner *pipeline.Runner // prepara worktree + roda o ciclo de fase

	// Config resolve a configuração já pronta (global→projeto, motores/contas)
	// para uma demanda. Seam: nil usa resolverConfigBanco (lê do banco). A conta
	// é o alias resolvido pela afinidade conta↔demanda do scheduler ("" quando não
	// há contas configuradas).
	Config func(ctx context.Context, dem db.Demanda, conta string) (pipeline.Config, error)

	Log func(string)
}

func (e *ExecutorDemanda) logf(msg string) {
	if e.Log != nil {
		e.Log(msg)
	}
}

// Executar conduz um passo da demanda: prepara o ambiente, escolhe e roda a
// próxima fase elegível, e traduz o desfecho da fase para o Desfecho do
// scheduler. Erros de INFRAESTRUTURA (banco/git indisponível) são devolvidos como
// erro (o scheduler reagenda com backoff); os desfechos lógicos da fase viram
// Desfecho.
func (e *ExecutorDemanda) Executar(ctx context.Context, item Item, conta string) (Desfecho, error) {
	if e.Store == nil {
		return Desfecho{}, fmt.Errorf("executor sem Store")
	}
	if e.Runner == nil {
		return Desfecho{}, fmt.Errorf("executor sem Runner")
	}

	dem, err := e.Store.ObterDemanda(ctx, item.DemandaID)
	if err != nil {
		return Desfecho{}, fmt.Errorf("obter demanda %d: %w", item.DemandaID, err)
	}
	// Corrida com a acao pausar/cancelar (Fase 2i): o status pode ter mudado entre
	// a Fonte listar a demanda e este Executar rodar. Estados TERMINAIS
	// (concluida/cancelada/falhou/integrada) saem da fila de vez (Concluido:true).
	// Estados PAUSAVEIS/humanos (pausada/aguardando_*/conflito) NAO sao concluidos
	// — apenas liberam o worker: nao sao reagendaveis pela Fonte agora, mas voltam
	// a ser candidatos assim que a acao `retomar` os devolver a `pronta` (nao os
	// marcamos como concluidos, senao a retomada nunca redespacharia).
	if terminal(dem.Status) {
		return Desfecho{Concluido: true}, nil
	}
	if pausadoOuHumano(dem.Status) {
		return Desfecho{Concluido: false}, nil
	}

	// prepara branch/worktree dedicados (idempotente entre fases da demanda).
	dem, err = e.Runner.Preparar(ctx, dem)
	if err != nil {
		return Desfecho{}, fmt.Errorf("preparar demanda %d: %w", dem.ID, err)
	}

	fases, err := e.Store.ListarFases(ctx, dem.ID)
	if err != nil {
		return Desfecho{}, fmt.Errorf("listar fases da demanda %d: %w", dem.ID, err)
	}

	prox, sit := proximaFase(fases)
	switch sit {
	case filaVazia, filaConcluida:
		e.marcarDemanda(ctx, dem, db.StatusDemandaConcluida, "")
		return Desfecho{Concluido: true}, nil
	case filaFalhou:
		e.marcarDemanda(ctx, dem, db.StatusDemandaFalhou, "uma fase falhou; a demanda não pode prosseguir")
		return Desfecho{Concluido: true}, nil
	case filaBloqueada:
		// só restam fases que exigem humano (ou presas por dependência de uma
		// dessas): pausa a demanda aguardando intervenção. `pausada` não é
		// agendável, então o scheduler não fica em laço.
		e.marcarDemanda(ctx, dem, db.StatusDemandaPausada, "aguardando intervenção humana em uma fase (requer_humano)")
		e.registrarEvento(ctx, dem, "aguardando_humano",
			"Praxis: demanda aguardando intervenção humana",
			"A próxima fase exige um humano (requer_humano) e não pode ser executada automaticamente.")
		return Desfecho{Concluido: true}, nil
	case filaProntaParaRodar:
		// segue abaixo.
	}

	cfg, err := e.resolverConfig(ctx, dem, conta)
	if err != nil {
		return Desfecho{}, fmt.Errorf("resolver config da demanda %d: %w", dem.ID, err)
	}

	res, err := e.Runner.RodarFase(ctx, dem, prox, cfg, nil)
	if err != nil {
		// falha de infraestrutura no ciclo da fase → backoff pelo scheduler.
		return Desfecho{}, fmt.Errorf("rodar fase %s da demanda %d: %w", prox.Codigo, dem.ID, err)
	}

	switch res.Situacao {
	case pipeline.SituacaoAguardandoFranquia:
		// não concluída: o scheduler segura o redespacho até RetomarEm (franquia).
		return Desfecho{RetomarEm: res.RetomarEm}, nil
	case pipeline.SituacaoPausada:
		// ctx cancelado (shutdown): há mais trabalho, mas o serviço está parando.
		// A retomada pós-restart é a Fase 2i; aqui só liberamos o worker.
		return Desfecho{Concluido: false}, nil
	case pipeline.SituacaoFalhou:
		e.marcarDemanda(ctx, dem, db.StatusDemandaFalhou, res.Erro)
		return Desfecho{Concluido: true}, nil
	default: // SituacaoConcluida
		e.acumularCusto(ctx, dem, res.CustoUSD)
		e.enfileirarFasesNovas(ctx, dem, fases, res.FasesNovas)
		// há mais trabalho? o próximo despacho reavalia a fila (pode concluir a
		// demanda ou rodar a próxima fase).
		return Desfecho{Concluido: false}, nil
	}
}

// situacaoFila é o desfecho agregado da fila de fases de uma demanda.
type situacaoFila int

const (
	filaVazia           situacaoFila = iota // demanda sem fases
	filaConcluida                           // todas as fases concluídas
	filaFalhou                              // alguma fase falhou (bloqueia a demanda)
	filaBloqueada                           // só restam fases requer_humano (ou presas por elas)
	filaProntaParaRodar                     // há uma fase elegível para rodar agora
)

// proximaFase escolhe a próxima fase elegível respeitando depende_de (todas as
// dependências concluídas) e requer_humano (nunca executada automaticamente). As
// fases já vêm ordenadas por (ordem, id) de ListarFases. Devolve a fase e a
// situação agregada da fila.
func proximaFase(fases []db.Fase) (db.Fase, situacaoFila) {
	if len(fases) == 0 {
		return db.Fase{}, filaVazia
	}
	concluidas := map[string]bool{}
	pendentes := 0
	for _, f := range fases {
		switch f.Status {
		case db.StatusFaseConcluida:
			concluidas[f.Codigo] = true
		case db.StatusFaseFalhou:
			return db.Fase{}, filaFalhou
		case db.StatusFasePendente, db.StatusFasePausada:
			pendentes++
		}
	}
	if pendentes == 0 {
		return db.Fase{}, filaConcluida
	}
	for _, f := range fases {
		if f.Status != db.StatusFasePendente && f.Status != db.StatusFasePausada {
			continue
		}
		if f.RequerHumano {
			continue
		}
		if depsSatisfeitas(f.DependeDe, concluidas) {
			return f, filaProntaParaRodar
		}
	}
	// há pendentes, mas nenhuma elegível agora: bloqueada por humano/dependência.
	return db.Fase{}, filaBloqueada
}

// depsSatisfeitas informa se todos os códigos em deps estão entre as concluídas.
func depsSatisfeitas(deps []string, concluidas map[string]bool) bool {
	for _, d := range deps {
		d = strings.TrimSpace(d)
		if d == "" {
			continue
		}
		if !concluidas[d] {
			return false
		}
	}
	return true
}

// terminal informa se o status da demanda é definitivo: nunca mais volta à fila.
func terminal(status string) bool {
	switch status {
	case db.StatusDemandaConcluida, db.StatusDemandaIntegrada,
		db.StatusDemandaCancelada, db.StatusDemandaFalhou:
		return true
	}
	return false
}

// pausadoOuHumano informa se a demanda não é conduzível automaticamente agora,
// mas PODE voltar à fila depois (pausada, aguardando humano/aprovação, conflito).
// Diferente de terminal: não deve ser marcada como concluída, para a ação
// `retomar` conseguir redespachá-la.
func pausadoOuHumano(status string) bool {
	switch status {
	case db.StatusDemandaPausada, db.StatusDemandaConflito,
		db.StatusDemandaAguardandoRespostas, db.StatusDemandaAguardandoAprovacao:
		return true
	}
	return false
}

// marcarDemanda atualiza status/erro da demanda (best-effort — erro vira log).
func (e *ExecutorDemanda) marcarDemanda(ctx context.Context, dem db.Demanda, status, erro string) {
	atual, err := e.Store.ObterDemanda(ctx, dem.ID)
	if err != nil {
		e.logf("executor: obter demanda para marcar status: " + err.Error())
		return
	}
	atual.Status = status
	if erro != "" {
		atual.Erro = erro
	}
	if _, err := e.Store.AtualizarDemanda(ctx, atual); err != nil {
		e.logf("executor: atualizar status da demanda: " + err.Error())
	}
}

// acumularCusto soma o custo da fase ao custo agregado da demanda (best-effort).
func (e *ExecutorDemanda) acumularCusto(ctx context.Context, dem db.Demanda, custo float64) {
	if custo <= 0 {
		return
	}
	atual, err := e.Store.ObterDemanda(ctx, dem.ID)
	if err != nil {
		return
	}
	atual.CustoUSD += custo
	_, _ = e.Store.AtualizarDemanda(ctx, atual)
}

// enfileirarFasesNovas persiste as fases que o revisor propôs (Fase 2b devolve em
// ResultadoFase.FasesNovas). Cada uma vira uma fase pendente com código único na
// demanda e ordem após as existentes, para o próprio ciclo automático conduzi-la.
func (e *ExecutorDemanda) enfileirarFasesNovas(ctx context.Context, dem db.Demanda, existentes []db.Fase, novas []pipeline.FaseNova) {
	if len(novas) == 0 {
		return
	}
	codigos := map[string]bool{}
	maxOrdem := 0
	for _, f := range existentes {
		codigos[f.Codigo] = true
		if f.Ordem > maxOrdem {
			maxOrdem = f.Ordem
		}
	}
	for i, nv := range novas {
		maxOrdem++
		codigo := codigoLivre(codigos, i+1)
		codigos[codigo] = true
		obs := strings.TrimSpace(nv.Descricao)
		if nv.Observacao != "" {
			obs = strings.TrimSpace(obs + "\n" + nv.Observacao)
		}
		_, err := e.Store.CriarFase(ctx, db.Fase{
			DemandID:   dem.ID,
			Codigo:     codigo,
			Titulo:     strings.TrimSpace(nv.Titulo),
			Status:     db.StatusFasePendente,
			DependeDe:  nv.DependeDe,
			GateExtra:  strings.TrimSpace(nv.GateExtra),
			Observacao: obs,
			Ordem:      maxOrdem,
		})
		if err != nil {
			e.logf("executor: enfileirar fase nova: " + err.Error())
			continue
		}
		e.registrarEvento(ctx, dem, "fase_nova",
			"Praxis: fase nova sugerida pelo revisor",
			codigo+": "+strings.TrimSpace(nv.Titulo))
	}
}

// codigoLivre gera um código de fase nova (nX) ainda não usado na demanda.
func codigoLivre(usados map[string]bool, seq int) string {
	for {
		c := fmt.Sprintf("n%d", seq)
		if !usados[c] {
			return c
		}
		seq++
	}
}

// registrarEvento grava um evento da demanda (best-effort).
func (e *ExecutorDemanda) registrarEvento(ctx context.Context, dem db.Demanda, tipo, titulo, detalhe string) {
	ev := db.Evento{Tipo: tipo, Titulo: titulo, Detalhe: detalhe}
	if dem.ProjectID > 0 {
		pid := dem.ProjectID
		ev.ProjectID = &pid
	}
	if dem.ID > 0 {
		did := dem.ID
		ev.DemandID = &did
	}
	_, _ = e.Store.RegistrarEvento(ctx, ev)
}

// resolverConfig usa o seam Config, ou o resolvedor padrão que lê do banco.
func (e *ExecutorDemanda) resolverConfig(ctx context.Context, dem db.Demanda, conta string) (pipeline.Config, error) {
	if e.Config != nil {
		return e.Config(ctx, dem, conta)
	}
	return resolverConfigBanco(ctx, e.Store, dem, conta)
}

// Defaults da config de pipeline quando o banco não define os valores.
const (
	maxCorrecoesDefault     = 2
	maxCiclosRevisaoDefault = 2
)

// resolverConfigBanco monta a pipeline.Config de uma demanda a partir do banco:
// motores ativos (ordem de fallback por prioridade, modelo de execução, budget e
// timeout) e a config efetiva do projeto (motor preferido, máx. correções, máx.
// ciclos de revisão). Robusto a banco esparso: sem motores cadastrados, cai no
// motor "claude" com os modelos padrão do pacote motor.
func resolverConfigBanco(ctx context.Context, store *db.DB, dem db.Demanda, conta string) (pipeline.Config, error) {
	cfg := pipeline.Config{
		Operacoes:        map[string]string{},
		Modelos:          map[string]string{},
		Esforcos:         map[string]string{},
		ConfigDirs:       map[string]string{},
		MaxCorrecoes:     maxCorrecoesDefault,
		MaxCiclosRevisao: maxCiclosRevisaoDefault,
	}

	motores, err := store.ListarMotores(ctx)
	if err != nil {
		return pipeline.Config{}, fmt.Errorf("listar motores: %w", err)
	}
	var ordem []string
	for _, m := range motores {
		if !m.Ativo {
			continue
		}
		ordem = append(ordem, m.Nome)
		if strings.TrimSpace(m.ModeloExec) != "" {
			cfg.Modelos[m.Nome] = m.ModeloExec
		}
		if cfg.MotorPadrao == "" {
			// o motor de maior prioridade (primeiro ativo) é o padrão; seu
			// budget/timeout valem para a fase, e sua conta dá o CLAUDE_CONFIG_DIR.
			cfg.MotorPadrao = m.Nome
			cfg.BudgetFaseUSD = m.BudgetFaseUSD
			cfg.TimeoutMin = m.TimeoutMin
			if dir := configDirDaConta(m, conta); dir != "" {
				cfg.ConfigDirs[m.Nome] = dir
			}
		}
	}
	if len(ordem) > 1 {
		cfg.Fallback = pipeline.Fallback{Ativo: true, Ordem: ordem}
	}

	efetiva, err := store.ConfigEfetiva(ctx, dem.ProjectID)
	if err != nil {
		return pipeline.Config{}, fmt.Errorf("config efetiva do projeto %d: %w", dem.ProjectID, err)
	}
	if s := configString(efetiva, "motor_preferido"); s != "" {
		cfg.MotorPadrao = s
	}
	if n, ok := configInt(efetiva, "max_correcoes"); ok {
		cfg.MaxCorrecoes = n
	}
	if n, ok := configInt(efetiva, "max_ciclos_revisao"); ok {
		cfg.MaxCiclosRevisao = n
	}

	// diretórios extras liberados ao harness vêm do cadastro do projeto.
	if proj, err := store.ObterProjeto(ctx, dem.ProjectID); err == nil {
		cfg.AddDirs = proj.AddDirs
	}
	return cfg, nil
}

// configDirDaConta devolve o CLAUDE_CONFIG_DIR da conta do motor: a que casa com
// o alias `conta` (afinidade do scheduler) ou, na ausência, a primeira conta
// ativa.
func configDirDaConta(m db.Motor, conta string) string {
	conta = strings.TrimSpace(conta)
	var primeiraAtiva string
	for _, c := range m.Contas {
		if !c.Ativo {
			continue
		}
		if primeiraAtiva == "" {
			primeiraAtiva = c.ConfigDir
		}
		if conta != "" && c.Alias == conta {
			return c.ConfigDir
		}
	}
	return primeiraAtiva
}

// configString lê uma chave string da config efetiva ("" se ausente/incompatível).
func configString(efetiva map[string]db.ValorEfetivo, chave string) string {
	v, ok := efetiva[chave]
	if !ok {
		return ""
	}
	var s string
	if err := json.Unmarshal(v.Valor, &s); err != nil {
		return ""
	}
	return strings.TrimSpace(s)
}

// configInt lê uma chave numérica da config efetiva. Devolve (valor, true) quando
// presente e numérica.
func configInt(efetiva map[string]db.ValorEfetivo, chave string) (int, bool) {
	v, ok := efetiva[chave]
	if !ok {
		return 0, false
	}
	var f float64
	if err := json.Unmarshal(v.Valor, &f); err != nil {
		return 0, false
	}
	return int(f), true
}
