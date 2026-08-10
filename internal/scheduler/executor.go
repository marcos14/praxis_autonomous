package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/marcos14/praxis-autonomous/internal/db"
	"github.com/marcos14/praxis-autonomous/internal/i18n"
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
		// agendável, então o scheduler não fica em laço. Aguardar humano NÃO é
		// erro — o campo erro fica limpo (o evento abaixo e o banner da aba de
		// fases explicam a pausa); escrever aqui acendia o badge de erro da UI.
		e.marcarDemanda(ctx, dem, db.StatusDemandaPausada, "")
		e.registrarEvento(ctx, dem, "aguardando_humano",
			i18n.TI("evento.aguardando_humano.titulo"),
			i18n.TI("evento.aguardando_humano.detalhe"))
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
		e.limparErro(ctx, dem)
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
		switch {
		case f.Status == db.StatusFaseConcluida:
			concluidas[f.Codigo] = true
		case f.Status == db.StatusFaseFalhou:
			return db.Fase{}, filaFalhou
		case faseRetomavel(f.Status):
			pendentes++
		}
	}
	if pendentes == 0 {
		return db.Fase{}, filaConcluida
	}
	for _, f := range fases {
		if !faseRetomavel(f.Status) {
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

// faseRetomavel informa se a fase pode ser (re)executada agora: pendente,
// pausada (pausa/franquia) ou presa em `executando`. O scheduler roda no máximo
// um worker por demanda, então quando proximaFase avalia a fila NÃO há run vivo
// desta demanda — uma fase `executando` aqui é órfã de uma queda do serviço em
// que o pipeline não chegou a persistir o desfecho. A recuperação pós-restart
// normalmente já a resetou para `pausada`; tratá-la como retomável é a rede de
// segurança que impede a demanda de travar (invisível, ela bloqueava as fases
// dependentes e a fila era dada como "aguardando humano").
func faseRetomavel(status string) bool {
	switch status {
	case db.StatusFasePendente, db.StatusFasePausada, db.StatusFaseExecutando:
		return true
	}
	return false
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
// O erro é atribuído SEMPRE: não-vazio registra a falha corrente; vazio LIMPA
// uma falha antiga (a demanda concluiu/voltou a progredir) — sem isso o badge
// de erro da UI ficava aceso para sempre depois de uma recuperação.
func (e *ExecutorDemanda) marcarDemanda(ctx context.Context, dem db.Demanda, status, erro string) {
	atual, err := e.Store.ObterDemanda(ctx, dem.ID)
	if err != nil {
		e.logf("executor: obter demanda para marcar status: " + err.Error())
		return
	}
	atual.Status = status
	atual.Erro = erro
	if _, err := e.Store.AtualizarDemanda(ctx, atual); err != nil {
		e.logf("executor: atualizar status da demanda: " + err.Error())
	}
}

// limparErro apaga o texto de falha da demanda depois de uma fase concluída com
// sucesso: a demanda voltou a progredir, e o erro antigo (que acende o badge da
// UI) não descreve mais o estado atual. Best-effort; no-op sem erro gravado.
func (e *ExecutorDemanda) limparErro(ctx context.Context, dem db.Demanda) {
	atual, err := e.Store.ObterDemanda(ctx, dem.ID)
	if err != nil || atual.Erro == "" {
		return
	}
	atual.Erro = ""
	if _, err := e.Store.AtualizarDemanda(ctx, atual); err != nil {
		e.logf("executor: limpar erro da demanda: " + err.Error())
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
			i18n.TI("evento.fase_nova.titulo"),
			i18n.TI("evento.fase_nova.detalhe",
				"codigo", codigo, "titulo", strings.TrimSpace(nv.Titulo)))
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
//
// Visibilidade (Fase A): só entram na config os motores VISÍVEIS ao criador da
// demanda (ACL engine_access) — demanda sem criador (token de API/bootstrap) usa
// apenas motores públicos. A ACL vale para executar, não só para listar: o motor
// restrito de um usuário nunca gasta franquia com trabalho de outro.
func resolverConfigBanco(ctx context.Context, store *db.DB, dem db.Demanda, conta string) (pipeline.Config, error) {
	cfg := pipeline.Config{
		Operacoes:        map[string]string{},
		Modelos:          map[string]string{},
		Esforcos:         map[string]string{},
		ConfigDirs:       map[string]string{},
		Contas:           map[string]string{},
		Perfis:           map[string][]pipeline.PerfilMotor{},
		MaxCorrecoes:     maxCorrecoesDefault,
		MaxCiclosRevisao: maxCiclosRevisaoDefault,
		GitSufixoPraxis:  true, // sufixo " - Praxis" no autor: ligado por default
	}

	motores, err := store.ListarMotores(ctx)
	if err != nil {
		return pipeline.Config{}, fmt.Errorf("listar motores: %w", err)
	}
	visiveis, err := store.IDsMotoresVisiveis(ctx, dem.CriadoPor)
	if err != nil {
		return pipeline.Config{}, fmt.Errorf("motores visíveis: %w", err)
	}
	// Nomes de TODOS os motores registrados, para distinguir (abaixo) um
	// motor_preferido que aponta para um motor escondido pela ACL de um que
	// aponta para um CLI não cadastrado (permitido desde sempre).
	registrados := map[string]bool{}
	for _, m := range motores {
		registrados[m.Nome] = true
	}
	var ordem []string
	nomesVisiveis := map[string]bool{}
	for _, m := range motores {
		if !m.Ativo || !visiveis[m.ID] {
			continue
		}
		nomesVisiveis[m.Nome] = true
		// A cadeia de fallback só contém motores que participam dele; um motor
		// de uso manual (fallback = false) ainda tem modelo/perfis resolvidos
		// abaixo, para quando o motor preferido do projeto apontar para ele.
		if m.Fallback {
			ordem = append(ordem, m.Nome)
		}
		if strings.TrimSpace(m.ModeloExec) != "" {
			cfg.Modelos[m.Nome] = m.ModeloExec
		}
		// Resolve TODOS os perfis ativos de CADA motor, com o da afinidade na
		// frente: o fallback esgota os perfis do motor um a um antes de trocar
		// de motor, e cada um usa seu CODEX_HOME/CLAUDE_CONFIG_DIR isolado.
		if perfis := perfisDoMotor(m, conta, dem.ID); len(perfis) > 0 {
			cfg.Perfis[m.Nome] = perfis
			if strings.TrimSpace(perfis[0].Dir) != "" {
				cfg.ConfigDirs[m.Nome] = perfis[0].Dir
			}
			cfg.Contas[m.Nome] = perfis[0].Conta
		}
		if cfg.MotorPadrao == "" && m.Fallback {
			// o motor de maior prioridade que participa do fallback é o padrão;
			// seu budget/timeout valem para a fase. Motores de uso manual nunca
			// são escolhidos automaticamente.
			cfg.MotorPadrao = m.Nome
			cfg.BudgetFaseUSD = m.BudgetFaseUSD
			cfg.TimeoutMin = m.TimeoutMin
		}
	}
	if len(ordem) > 0 {
		// Ativo mesmo com um único motor na cadeia: um motor preferido de uso
		// manual (fora da cadeia) ainda precisa cair nela quando esgotar.
		cfg.Fallback = pipeline.Fallback{Ativo: true, Ordem: ordem}
	}

	efetiva, err := store.ConfigEfetiva(ctx, dem.ProjectID)
	if err != nil {
		return pipeline.Config{}, fmt.Errorf("config efetiva do projeto %d: %w", dem.ProjectID, err)
	}
	if s := configString(efetiva, "motor_preferido"); s != "" {
		// Um preferido REGISTRADO mas escondido pela ACL do criador é ignorado
		// (fica o padrão da cadeia); um nome não cadastrado segue valendo — é o
		// caminho histórico de apontar um CLI que não está no banco.
		if nomesVisiveis[s] || !registrados[s] {
			cfg.MotorPadrao = s
		}
	}
	if n, ok := configInt(efetiva, "max_correcoes"); ok {
		cfg.MaxCorrecoes = n
	}
	if n, ok := configInt(efetiva, "max_ciclos_revisao"); ok {
		cfg.MaxCiclosRevisao = n
	}
	if b, ok := db.ConfigBool(efetiva, "git_sufixo_praxis"); ok {
		cfg.GitSufixoPraxis = b
	}
	if cmds := configListaStrings(efetiva, "gates"); len(cmds) > 0 {
		// a config "gates" é uma lista de comandos de shell; vira um bloco de gate
		// fixo rodado em toda fase (build/lint/test do projeto-alvo).
		cfg.Gates = []pipeline.Gate{{Nome: "gates", Comandos: cmds}}
	}

	// diretórios extras liberados ao harness vêm do cadastro do projeto.
	if proj, err := store.ObterProjeto(ctx, dem.ProjectID); err == nil {
		cfg.AddDirs = proj.AddDirs
	}
	return cfg, nil
}

// perfisDoMotor devolve TODOS os perfis ativos do motor na ordem de uso do
// fallback: primeiro o preferido (o que casa com o alias `conta` da afinidade
// do scheduler ou, na ausência, o escolhido pela afinidade determinística) e
// depois os demais, rotacionados. Vazio quando o motor não tem conta ativa.
func perfisDoMotor(m db.Motor, conta string, afinidade ...int64) []pipeline.PerfilMotor {
	ativas := make([]db.Conta, 0, len(m.Contas))
	for _, c := range m.Contas {
		if c.Ativo {
			ativas = append(ativas, c)
		}
	}
	if len(ativas) == 0 {
		return nil
	}

	inicio := -1
	if conta = strings.TrimSpace(conta); conta != "" {
		for i, c := range ativas {
			if c.Alias == conta {
				inicio = i
				break
			}
		}
	}
	if inicio < 0 {
		seed := int64(0)
		if len(afinidade) > 0 {
			seed = afinidade[0]
		}
		if seed <= 0 {
			inicio = 0
		} else {
			inicio = int((seed - 1) % int64(len(ativas)))
		}
	}

	perfis := make([]pipeline.PerfilMotor, 0, len(ativas))
	for i := range ativas {
		c := ativas[(inicio+i)%len(ativas)]
		perfis = append(perfis, pipeline.PerfilMotor{Conta: c.Alias, Dir: c.ConfigDir})
	}
	return perfis
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

// configListaStrings lê uma chave de config efetiva que é uma lista de strings
// (ex.: "gates"). Devolve os itens não-vazios; nil quando ausente/incompatível.
func configListaStrings(efetiva map[string]db.ValorEfetivo, chave string) []string {
	v, ok := efetiva[chave]
	if !ok {
		return nil
	}
	var itens []string
	if err := json.Unmarshal(v.Valor, &itens); err != nil {
		return nil
	}
	out := make([]string, 0, len(itens))
	for _, it := range itens {
		if it = strings.TrimSpace(it); it != "" {
			out = append(out, it)
		}
	}
	return out
}
