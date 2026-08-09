package estrategista

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/marcos14/praxis-autonomous/internal/db"
	"github.com/marcos14/praxis-autonomous/internal/gitops"
	"github.com/marcos14/praxis-autonomous/internal/motor"
)

// Servico resolve a config do banco e dispara o estrategista em background. É o
// WIRING dos mecanismos deste pacote (espelho do consultor.Servico): a API o
// chama ao criar um planejamento e ao receber uma fala do usuário; o `serve` o
// injeta.
type Servico struct {
	store            *db.DB
	dirLogs          string
	dirPlanejamentos string // raiz das pastas de trabalho (PRAXIS_HOME/planejamentos)
	ctx              context.Context
	logf             func(string)

	wg sync.WaitGroup

	// Seams de teste (nil em produção):
	selecionar func(nome string) (motor.Motor, error)
	prompt     func(ctx context.Context, nome string) (string, error)
	agora      func() time.Time
	statPasta  func(string) error // valida a pasta de um repo (default: os.Stat)
	statusRepo func(string) (string, error)
	// atualizarRepo posiciona o repo na branch principal e faz o pull antes da
	// leitura (default: gitops.PosicionarBranchPrincipal).
	atualizarRepo func(pasta, branch string) (string, error)
}

// OpcoesServico configura o Servico. Store, DirPlanejamentos e Ctx são
// obrigatórios em produção.
type OpcoesServico struct {
	Store            *db.DB
	DirLogs          string          // pasta dos .jsonl (PRAXIS_HOME/logs)
	DirPlanejamentos string          // raiz das pastas de trabalho (PRAXIS_HOME/planejamentos)
	Ctx              context.Context // ctx de vida do serviço (cancelado no shutdown)
	Log              func(string)
	// Git é o Ops compartilhado do serviço (mutex por projeto — passa o MESMO
	// do scheduler para as operações no repo nunca correrem em paralelo). Nil =
	// cria um próprio.
	Git *gitops.Ops

	// Seams de teste:
	Selecionar    func(nome string) (motor.Motor, error)
	Prompt        func(ctx context.Context, nome string) (string, error)
	Agora         func() time.Time
	StatPasta     func(string) error
	StatusRepo    func(string) (string, error)
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
	stat := o.StatPasta
	if stat == nil {
		stat = func(p string) error {
			info, err := os.Stat(p)
			if err != nil {
				return err
			}
			if !info.IsDir() {
				return fmt.Errorf("não é um diretório: %s", p)
			}
			return nil
		}
	}
	atualizar := o.AtualizarRepo
	if atualizar == nil {
		g := o.Git
		if g == nil {
			g = gitops.Novo()
		}
		atualizar = g.PosicionarBranchPrincipal
	}
	status := o.StatusRepo
	if status == nil {
		status = statusPorcelainPadrao
	}
	return &Servico{
		store:            o.Store,
		dirLogs:          o.DirLogs,
		dirPlanejamentos: o.DirPlanejamentos,
		ctx:              ctx,
		logf:             logf,
		selecionar:       o.Selecionar,
		prompt:           o.Prompt,
		agora:            o.Agora,
		statPasta:        stat,
		statusRepo:       status,
		atualizarRepo:    atualizar,
	}
}

// statusPorcelainPadrao é o StatusRepo de produção.
func statusPorcelainPadrao(dir string) (string, error) { return gitops.StatusPorcelain(dir) }

// DispararResposta inicia um turno do estrategista em background (não bloqueia
// o handler HTTP). Falhas viram log/evento/fala de sistema.
func (s *Servico) DispararResposta(planejamentoID int64) {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		if err := s.Responder(s.ctx, planejamentoID); err != nil {
			s.logf(fmt.Sprintf("estrategista: turno do planejamento %d falhou: %v", planejamentoID, err))
		}
	}()
}

// Aguardar bloqueia até os turnos em voo terminarem (shutdown gracioso).
func (s *Servico) Aguardar() { s.wg.Wait() }

// Pasta devolve a pasta de trabalho do planejamento (onde vivem documentos e
// artefatos). A API a usa para servir artefatos e para remover a pasta na
// exclusão do planejamento.
func (s *Servico) Pasta(planejamentoID int64) string {
	return filepath.Join(s.dirPlanejamentos, fmt.Sprintf("p%d", planejamentoID))
}

// Responder monta o Estrategista a partir do banco e roda um turno (síncrono).
// Exposto para testes e para o `serve` rodar sem a goroutine.
func (s *Servico) Responder(ctx context.Context, planejamentoID int64) error {
	e, err := s.montarEstrategista(ctx, planejamentoID)
	if err != nil {
		// Falha de montagem (projeto/grupo inválido): carimba o planejamento
		// para o usuário ver o motivo em vez de um "pensando" eterno.
		if plan, obterErr := s.store.ObterPlanejamento(ctx, planejamentoID); obterErr == nil {
			plan.Status = db.StatusPlanejamentoFalhou
			plan.Erro = err.Error()
			_, _ = s.store.AtualizarPlanejamento(ctx, plan)
			_, _ = s.store.CriarMensagemPlanejamento(ctx, db.MensagemPlanejamento{
				PlanejamentoID: plan.ID, Papel: db.PapelPlanejamentoSistema,
				Conteudo: "Não consegui iniciar o turno: " + err.Error(),
			})
		}
		return err
	}
	return e.Responder(ctx, planejamentoID)
}

// montarEstrategista resolve o alvo do planejamento (projeto ou grupo) e o
// motor, e devolve um Estrategista pronto: pasta de trabalho própria, repos em
// leitura e o ContextoRepos com a descrição da solução e os overviews.
func (s *Servico) montarEstrategista(ctx context.Context, planejamentoID int64) (*Estrategista, error) {
	plan, err := s.store.ObterPlanejamento(ctx, planejamentoID)
	if err != nil {
		return nil, fmt.Errorf("estrategista: obter planejamento %d: %w", planejamentoID, err)
	}

	var (
		repos    []string
		extras   []string
		contexto string
	)
	switch {
	case plan.ProjectID != nil:
		repos, extras, contexto, err = s.contextoProjeto(ctx, plan, *plan.ProjectID)
	case plan.GroupID != nil:
		repos, extras, contexto, err = s.contextoGrupo(ctx, plan, *plan.GroupID)
	default:
		err = fmt.Errorf("planejamento %d sem projeto nem grupo", planejamentoID)
	}
	if err != nil {
		return nil, err
	}

	motorNome, modelo, esforco, conta, configDir, budget, timeout := s.resolverMotorPlanejamento(ctx, plan.CriadoPor, planejamentoID)
	return &Estrategista{
		Store:         s.store,
		Idioma:        s.store.IdiomaDoUsuario(ctx, plan.CriadoPor),
		Motor:         motorNome,
		Modelo:        modelo,
		Esforco:       esforco,
		Conta:         conta,
		ConfigDir:     configDir,
		DirTrabalho:   s.Pasta(plan.ID),
		DirLogs:       s.dirLogs,
		Repos:         repos,
		DirsExtras:    extras,
		BudgetUSD:     budget,
		TimeoutMin:    timeout,
		ContextoRepos: contexto,
		Foco:          plan.Foco,
		NivelVisual:   plan.NivelVisual,
		Selecionar:    s.selecionar,
		Prompt:        s.prompt,
		Agora:         s.agora,
		StatusRepo:    s.statusRepo,
	}, nil
}

// contextoProjeto monta o alvo de um planejamento de projeto único.
func (s *Servico) contextoProjeto(ctx context.Context, plan db.Planejamento, projectID int64) ([]string, []string, string, error) {
	proj, err := s.store.ObterProjeto(ctx, projectID)
	if err != nil {
		return nil, nil, "", fmt.Errorf("estrategista: obter projeto %d: %w", projectID, err)
	}
	if err := s.statPasta(proj.Pasta); err != nil {
		return nil, nil, "", fmt.Errorf("pasta do projeto %q inacessível: %v", proj.Nome, err)
	}
	s.prepararRepo(ctx, &plan, proj)
	return []string{proj.Pasta}, resolverExtras(proj), secaoRepo(proj), nil
}

// contextoGrupo monta o alvo de um planejamento de grupo: repos = pastas dos
// membros acessíveis (o principal, ordem 0, é obrigatório), extras = add_dirs
// de todos, e o contexto com a descrição da solução e o overview de cada repo.
// Membro secundário com pasta inválida vira fala de sistema (aviso) e é
// ignorado; o principal inválido derruba o turno com erro claro.
func (s *Servico) contextoGrupo(ctx context.Context, plan db.Planejamento, groupID int64) ([]string, []string, string, error) {
	grupo, err := s.store.ObterGrupo(ctx, groupID)
	if err != nil {
		return nil, nil, "", fmt.Errorf("estrategista: obter grupo %d: %w", groupID, err)
	}
	if len(grupo.Membros) == 0 {
		return nil, nil, "", fmt.Errorf("o grupo %q não tem repositórios", grupo.Nome)
	}

	var (
		repos  []string
		extras []string
		secoes []string
	)
	cabecalho := "# Solução: " + grupo.Nome
	if d := strings.TrimSpace(grupo.Descricao); d != "" {
		cabecalho += "\n\n" + d
	}
	secoes = append(secoes, cabecalho)

	for i, membro := range grupo.Membros {
		proj, err := s.store.ObterProjeto(ctx, membro.ProjectID)
		if err != nil {
			return nil, nil, "", fmt.Errorf("estrategista: obter projeto membro %d: %w", membro.ProjectID, err)
		}
		if err := s.statPasta(proj.Pasta); err != nil {
			if i == 0 {
				return nil, nil, "", fmt.Errorf(
					"pasta do repositório principal %q inacessível: %v", proj.Nome, err)
			}
			s.avisar(ctx, plan, fmt.Sprintf(
				"O repositório %q do grupo está inacessível e ficou fora deste turno.", proj.Nome))
			continue
		}
		s.prepararRepo(ctx, &plan, proj)
		repos = append(repos, proj.Pasta)
		extras = append(extras, resolverExtras(proj)...)
		secoes = append(secoes, secaoRepo(proj))
	}
	if len(repos) == 0 {
		return nil, nil, "", fmt.Errorf("o grupo %q ficou sem repositório acessível", grupo.Nome)
	}
	return repos, extras, strings.Join(secoes, "\n\n"), nil
}

// resolverExtras resolve os add_dirs do projeto para caminhos absolutos
// (relativos são ancorados na pasta do projeto — no consultor essa resolução é
// do motor via cmd.Dir; aqui o cwd é a pasta do planejamento, então a âncora
// precisa ser explícita).
func resolverExtras(proj db.Projeto) []string {
	extras := make([]string, 0, len(proj.AddDirs))
	for _, d := range proj.AddDirs {
		d = strings.TrimSpace(d)
		if d == "" || d == "." {
			continue
		}
		if !filepath.IsAbs(d) {
			d = filepath.Join(proj.Pasta, d)
		}
		extras = append(extras, d)
	}
	return extras
}

// prepararRepo posiciona o repo do projeto na branch principal e o atualiza
// antes da leitura. Nunca bloqueia o turno: problemas viram aviso na conversa
// (fala de sistema) ou log, e a análise segue com o estado disponível.
func (s *Servico) prepararRepo(ctx context.Context, plan *db.Planejamento, proj db.Projeto) {
	aviso, err := s.atualizarRepo(proj.Pasta, proj.BranchPrincipal)
	if err != nil {
		s.logf(fmt.Sprintf("estrategista: atualizar repo do projeto %q: %v", proj.Nome, err))
		return
	}
	if aviso == "" {
		return
	}
	s.logf(fmt.Sprintf("estrategista: repo do projeto %q: %s", proj.Nome, aviso))
	if plan != nil {
		s.avisar(ctx, *plan, "Aviso sobre o repositório "+proj.Nome+": "+aviso)
	}
}

// secaoRepo formata a seção de contexto de um repositório (nome, pasta e
// overview — ou um aviso quando o overview ainda não foi gerado).
func secaoRepo(proj db.Projeto) string {
	sec := fmt.Sprintf("## Repositório: %s (pasta %s)", proj.Nome, proj.Pasta)
	if ov := strings.TrimSpace(proj.OverviewMD); ov != "" {
		return sec + "\n\n" + ov
	}
	return sec + "\n\n(este repositório ainda não tem overview cadastrado — investigue a estrutura com mais cautela antes de planejar)"
}

// avisar registra uma fala de sistema no planejamento (best-effort).
func (s *Servico) avisar(ctx context.Context, plan db.Planejamento, texto string) {
	if _, err := s.store.CriarMensagemPlanejamento(ctx, db.MensagemPlanejamento{
		PlanejamentoID: plan.ID, Papel: db.PapelPlanejamentoSistema, Conteudo: texto,
	}); err != nil {
		s.logf(fmt.Sprintf("estrategista: registrar aviso no planejamento %d: %v", plan.ID, err))
	}
}

// resolverMotorPlanejamento escolhe o motor/modelo de um turno de PLANEJAMENTO.
// Mesma precedência das consultas (o estrategista é um trabalho de análise, não
// de execução): grupo de usuários do criador → primeiro motor ativo no fallback
// (modelo_consulta e, na ausência, modelo_analise) → default "claude".
func (s *Servico) resolverMotorPlanejamento(ctx context.Context, criadoPor *int64, afinidade ...int64) (nome, modelo, esforco, conta, configDir string, budget float64, timeout int) {
	seed := int64(0)
	if len(afinidade) > 0 {
		seed = afinidade[0]
	}
	modeloGrupo := ""
	if criadoPor != nil {
		g, ok, err := s.store.GrupoDoUsuario(ctx, *criadoPor)
		if err != nil {
			s.logf(fmt.Sprintf("estrategista: grupo do usuário %d: %v", *criadoPor, err))
		} else if ok {
			modeloGrupo = strings.TrimSpace(g.Modelo)
			if g.EngineID != nil {
				if m, err := s.store.ObterMotor(ctx, *g.EngineID); err == nil && m.Ativo {
					alias, dir := contaAtiva(m, seed)
					return m.Nome, escolherModelo(modeloGrupo, m.ModeloConsulta, m.ModeloAnalise),
						"", alias, dir, m.BudgetFaseUSD, m.TimeoutMin
				} else if err != nil {
					s.logf(fmt.Sprintf("estrategista: motor do grupo %q: %v", g.Nome, err))
				}
				// motor do grupo removido/inativo: cai no padrão, preservando o
				// modelo do grupo (se houver).
			}
		}
	}

	motores, err := s.store.ListarMotores(ctx)
	if err != nil {
		s.logf(fmt.Sprintf("estrategista: listar motores: %v", err))
		return "claude", modeloGrupo, "", "", "", 0, 0
	}
	for _, m := range motores {
		if !m.Ativo || !m.Fallback {
			continue
		}
		alias, dir := contaAtiva(m, seed)
		return m.Nome, escolherModelo(modeloGrupo, m.ModeloConsulta, m.ModeloAnalise),
			"", alias, dir, m.BudgetFaseUSD, m.TimeoutMin
	}
	return "claude", modeloGrupo, "", "", "", 0, 0
}

// escolherModelo devolve o primeiro modelo não-vazio da lista de precedência.
func escolherModelo(candidatos ...string) string {
	for _, c := range candidatos {
		if s := strings.TrimSpace(c); s != "" {
			return s
		}
	}
	return ""
}

// contaAtiva devolve o alias e o config_dir da conta ativa do motor escolhida
// pela afinidade ("" se não houver — afinidade simples, como no intake).
func contaAtiva(m db.Motor, afinidade ...int64) (alias, configDir string) {
	seed := int64(0)
	if len(afinidade) > 0 {
		seed = afinidade[0]
	}
	if c, ok := db.ContaAtivaPara(m, seed); ok {
		return c.Alias, c.ConfigDir
	}
	return "", ""
}
