package intake

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/marcos14/praxis-autonomous/internal/db"
	"github.com/marcos14/praxis-autonomous/internal/gitops"
	"github.com/marcos14/praxis-autonomous/internal/motor"
)

// Servico resolve a config do banco e dispara o analista em background quando uma
// demanda precisa ser analisada (Fase 3b). É o WIRING do mecanismo Analista: a
// API o chama (Disparar) ao criar uma demanda por chat; o `serve` o injeta.
//
// "Background por padrão" (princípio do redesenho): Disparar não bloqueia o
// handler HTTP — spawna uma goroutine com o ctx de vida do serviço. O scheduler
// completo (2g.n1) ainda não conduz o intake; até lá este Servico é o caminho que
// faz a demanda "andar sozinha" da criação até `aguardando_respostas`.
type Servico struct {
	store   *db.DB
	dirLogs string
	ctx     context.Context
	logf    func(string)

	wg sync.WaitGroup

	// Seams de teste (nil em produção):
	selecionar func(nome string) (motor.Motor, error)
	agora      func() time.Time
	// atualizarRepo posiciona o repo do projeto na branch principal e faz o
	// pull antes da análise/planejamento (default:
	// gitops.PosicionarBranchPrincipal). O serviço roda num servidor — sem isso
	// o analista leria um clone defasado ou em outra branch.
	atualizarRepo func(pasta, branch string) (string, error)
}

// OpcoesServico configura o Servico. Store e Ctx são obrigatórios.
type OpcoesServico struct {
	Store   *db.DB
	DirLogs string          // pasta dos .jsonl (PRAXIS_HOME/logs)
	Ctx     context.Context // ctx de vida do serviço (cancelado no shutdown)
	Log     func(string)
	// Git é o Ops compartilhado (mutex por projeto — passe o MESMO do
	// scheduler). Nil = cria um próprio.
	Git *gitops.Ops

	// Seams de teste:
	Selecionar    func(nome string) (motor.Motor, error)
	Agora         func() time.Time
	AtualizarRepo func(pasta, branch string) (string, error)
}

// NovoServico monta o Servico aplicando defaults nos seams.
func NovoServico(o OpcoesServico) *Servico {
	ctx := o.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	logf := o.Log
	if logf == nil {
		logf = func(string) {}
	}
	atualizar := o.AtualizarRepo
	if atualizar == nil {
		g := o.Git
		if g == nil {
			g = gitops.Novo()
		}
		atualizar = g.PosicionarBranchPrincipal
	}
	return &Servico{
		store:         o.Store,
		dirLogs:       o.DirLogs,
		ctx:           ctx,
		logf:          logf,
		selecionar:    o.Selecionar,
		agora:         o.Agora,
		atualizarRepo: atualizar,
	}
}

// Disparar inicia a análise da demanda em background (não bloqueia o chamador).
// Falhas viram log/evento — o handler HTTP que chama não deve depender do desfecho.
func (s *Servico) Disparar(demandaID int64) {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		if err := s.Analisar(s.ctx, demandaID); err != nil {
			s.logf(fmt.Sprintf("intake: análise da demanda %d falhou: %v", demandaID, err))
		}
	}()
}

// DispararPlanejamento inicia o planejamento da demanda em background (Fase 3c):
// não bloqueia o chamador (o handler HTTP que transitou a demanda para
// `planejando`/rejeitou o plano). Falhas viram log/evento.
func (s *Servico) DispararPlanejamento(demandaID int64) {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		if err := s.Planejar(s.ctx, demandaID); err != nil {
			s.logf(fmt.Sprintf("intake: planejamento da demanda %d falhou: %v", demandaID, err))
		}
	}()
}

// Aguardar bloqueia até as análises/planejamentos em voo terminarem (usado no
// shutdown gracioso).
func (s *Servico) Aguardar() { s.wg.Wait() }

// Planejar monta o Planejador a partir do banco e o executa (síncrono). Exposto
// para o `serve`/testes rodarem o planejamento sem a goroutine.
func (s *Servico) Planejar(ctx context.Context, demandaID int64) error {
	p, err := s.montarPlanejador(ctx, demandaID)
	if err != nil {
		return err
	}
	return p.Planejar(ctx, demandaID)
}

// montarPlanejador resolve o projeto e o motor da demanda e devolve um Planejador
// pronto. Reusa a resolução de motor do analista (planejar é, como analisar, uma
// tarefa readonly de raciocínio sobre o código, com o modelo_analise).
func (s *Servico) montarPlanejador(ctx context.Context, demandaID int64) (*Planejador, error) {
	dem, err := s.store.ObterDemanda(ctx, demandaID)
	if err != nil {
		return nil, fmt.Errorf("intake: obter demanda %d: %w", demandaID, err)
	}
	proj, err := s.store.ObterProjeto(ctx, dem.ProjectID)
	if err != nil {
		return nil, fmt.Errorf("intake: obter projeto %d: %w", dem.ProjectID, err)
	}
	s.prepararRepo(ctx, dem, proj)
	motorNome, modelo, esforco, configDir, budget, timeout := s.resolverMotor(ctx)

	return &Planejador{
		Store:      s.store,
		Motor:      motorNome,
		Modelo:     modelo,
		Esforco:    esforco,
		ConfigDir:  configDir,
		Dir:        proj.Pasta,
		DirLogs:    s.dirLogs,
		AddDirs:    proj.AddDirs,
		BudgetUSD:  budget,
		TimeoutMin: timeout,
		Selecionar: s.selecionar,
		Agora:      s.agora,
	}, nil
}

// Analisar monta o Analista a partir do banco e o executa (síncrono). Exposto
// para o `serve`/testes rodarem a análise sem a goroutine.
func (s *Servico) Analisar(ctx context.Context, demandaID int64) error {
	a, err := s.montarAnalista(ctx, demandaID)
	if err != nil {
		return err
	}
	return a.Analisar(ctx, demandaID)
}

// montarAnalista resolve o projeto e o motor da demanda e devolve um Analista pronto.
func (s *Servico) montarAnalista(ctx context.Context, demandaID int64) (*Analista, error) {
	dem, err := s.store.ObterDemanda(ctx, demandaID)
	if err != nil {
		return nil, fmt.Errorf("intake: obter demanda %d: %w", demandaID, err)
	}
	proj, err := s.store.ObterProjeto(ctx, dem.ProjectID)
	if err != nil {
		return nil, fmt.Errorf("intake: obter projeto %d: %w", dem.ProjectID, err)
	}
	s.prepararRepo(ctx, dem, proj)
	motorNome, modelo, esforco, configDir, budget, timeout := s.resolverMotor(ctx)

	return &Analista{
		Store:      s.store,
		Motor:      motorNome,
		Modelo:     modelo,
		Esforco:    esforco,
		ConfigDir:  configDir,
		Dir:        proj.Pasta,
		DirLogs:    s.dirLogs,
		AddDirs:    proj.AddDirs,
		BudgetUSD:  budget,
		TimeoutMin: timeout,
		Selecionar: s.selecionar,
		Agora:      s.agora,
	}, nil
}

// prepararRepo posiciona o repo do projeto na branch principal e o atualiza
// antes de o analista/planejador lerem o código (o serviço roda num servidor —
// o clone pode estar defasado ou em outra branch). Nunca bloqueia a demanda:
// problemas viram evento (a UI mostra na aba Eventos) e log, e a análise segue
// com o estado disponível.
func (s *Servico) prepararRepo(ctx context.Context, dem db.Demanda, proj db.Projeto) {
	aviso, err := s.atualizarRepo(proj.Pasta, proj.BranchPrincipal)
	if err != nil {
		s.logf(fmt.Sprintf("intake: atualizar repo do projeto %q: %v", proj.Nome, err))
		return
	}
	if aviso == "" {
		return
	}
	s.logf(fmt.Sprintf("intake: repo do projeto %q: %s", proj.Nome, aviso))
	pid, did := proj.ID, dem.ID
	_, _ = s.store.RegistrarEvento(ctx, db.Evento{
		ProjectID: &pid, DemandID: &did, Tipo: "aviso",
		Titulo:  "Praxis: repositório não pôde ser totalmente atualizado",
		Detalhe: aviso,
	})
}

// resolverMotor escolhe o motor de ANÁLISE: o primeiro motor ativo por
// prioridade (a ordem de fallback), com seu modelo_analise, budget e timeout, e o
// config_dir da primeira conta ativa (afinidade simples). Sem motor cadastrado,
// cai no default "claude" com modelo default — o analista funciona out-of-the-box.
//
// É a resolução mínima que o analista precisa; a resolução completa (motor por
// operação, fallback, contas com afinidade) é do wiring do scheduler (2g.n1).
func (s *Servico) resolverMotor(ctx context.Context) (nome, modelo, esforco, configDir string, budget float64, timeout int) {
	motores, err := s.store.ListarMotores(ctx)
	if err != nil {
		s.logf(fmt.Sprintf("intake: listar motores: %v", err))
		return "claude", "", "", "", 0, 0
	}
	for _, m := range motores {
		if !m.Ativo {
			continue
		}
		cfg := ""
		for _, c := range m.Contas {
			if c.Ativo {
				cfg = c.ConfigDir
				break
			}
		}
		return m.Nome, m.ModeloAnalise, "", cfg, m.BudgetFaseUSD, m.TimeoutMin
	}
	return "claude", "", "", "", 0, 0
}
