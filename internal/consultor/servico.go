package consultor

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
	"github.com/marcos14/praxis-autonomous/internal/referencias"
)

// Servico resolve a config do banco e dispara o consultor/gerador de overview em
// background. É o WIRING dos mecanismos deste pacote (espelho do intake.Servico):
// a API o chama ao criar uma consulta, ao receber uma fala do usuário e ao pedir
// a geração de overview; o `serve` o injeta.
type Servico struct {
	store        *db.DB
	dirLogs      string
	dirConsultas string // raiz das pastas de trabalho (PRAXIS_HOME/consultas)
	ctx          context.Context
	logf         func(string)

	wg sync.WaitGroup

	// Seams de teste (nil em produção):
	selecionar func(nome string) (motor.Motor, error)
	prompt     func(ctx context.Context, nome string) (string, error)
	agora      func() time.Time
	statPasta  func(string) error // valida a pasta de um repo (default: os.Stat)
	// atualizarRepo posiciona o repo na branch principal e faz o pull antes da
	// leitura (default: gitops.PosicionarBranchPrincipal). Devolve um aviso
	// legível quando o repo não pôde ficar 100% atualizado.
	atualizarRepo func(pasta, branch string) (string, error)
}

// OpcoesServico configura o Servico. Store e Ctx são obrigatórios.
type OpcoesServico struct {
	Store   *db.DB
	DirLogs string // pasta dos .jsonl (PRAXIS_HOME/logs)
	// DirConsultas é a raiz das pastas de trabalho das consultas
	// (PRAXIS_HOME/consultas), onde ficam os arquivos anexados pelo usuário.
	// Vazia = consultas sem anexos (as rotas de referência respondem 503).
	DirConsultas string
	Ctx          context.Context // ctx de vida do serviço (cancelado no shutdown)
	Log          func(string)
	// Git é o Ops compartilhado do serviço (mutex por projeto — passa o MESMO
	// do scheduler para as operações no repo nunca correrem em paralelo). Nil =
	// cria um próprio.
	Git *gitops.Ops

	// Seams de teste:
	Selecionar    func(nome string) (motor.Motor, error)
	Prompt        func(ctx context.Context, nome string) (string, error)
	Agora         func() time.Time
	StatPasta     func(string) error
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
	return &Servico{
		store:         o.Store,
		dirLogs:       o.DirLogs,
		dirConsultas:  o.DirConsultas,
		ctx:           ctx,
		logf:          logf,
		selecionar:    o.Selecionar,
		prompt:        o.Prompt,
		agora:         o.Agora,
		statPasta:     stat,
		atualizarRepo: atualizar,
	}
}

// DispararResposta inicia um turno do consultor em background (não bloqueia o
// handler HTTP). Falhas viram log/evento/fala de sistema.
func (s *Servico) DispararResposta(consultaID int64) {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		if err := s.Responder(s.ctx, consultaID); err != nil {
			s.logf(fmt.Sprintf("consultor: turno da consulta %d falhou: %v", consultaID, err))
		}
	}()
}

// DispararOverview inicia a geração do overview do projeto em background.
func (s *Servico) DispararOverview(projectID int64) {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		if err := s.GerarOverview(s.ctx, projectID); err != nil {
			s.logf(fmt.Sprintf("consultor: overview do projeto %d falhou: %v", projectID, err))
		}
	}()
}

// Aguardar bloqueia até os turnos/gerações em voo terminarem (shutdown gracioso).
func (s *Servico) Aguardar() { s.wg.Wait() }

// Pasta devolve a pasta de trabalho da consulta (onde vive a subpasta de
// referências anexadas). A API a usa para gravar/servir os anexos e para
// remover a pasta na exclusão da consulta. "" quando o serviço subiu sem
// DirConsultas — a API responde 503 nas rotas de referência.
func (s *Servico) Pasta(consultaID int64) string {
	if strings.TrimSpace(s.dirConsultas) == "" {
		return ""
	}
	return filepath.Join(s.dirConsultas, fmt.Sprintf("c%d", consultaID))
}

// Responder monta o Consultor a partir do banco e roda um turno (síncrono).
// Exposto para testes e para o `serve` rodar sem a goroutine.
func (s *Servico) Responder(ctx context.Context, consultaID int64) error {
	c, err := s.montarConsultor(ctx, consultaID)
	if err != nil {
		// Falha de montagem (projeto/grupo inválido): carimba a consulta para o
		// usuário ver o motivo em vez de um "pensando" eterno.
		if cons, obterErr := s.store.ObterConsulta(ctx, consultaID); obterErr == nil {
			cons.Status = db.StatusConsultaFalhou
			cons.Erro = err.Error()
			_, _ = s.store.AtualizarConsulta(ctx, cons)
			_, _ = s.store.CriarMensagemConsulta(ctx, db.MensagemConsulta{
				ConsultaID: cons.ID, Papel: db.PapelConsultaSistema,
				Conteudo: "Não consegui iniciar a análise: " + err.Error(),
			})
		}
		return err
	}
	return c.Responder(ctx, consultaID)
}

// GerarOverview monta o GeradorOverview a partir do banco e o executa (síncrono).
func (s *Servico) GerarOverview(ctx context.Context, projectID int64) error {
	proj, err := s.store.ObterProjeto(ctx, projectID)
	if err != nil {
		return fmt.Errorf("consultor: obter projeto %d: %w", projectID, err)
	}
	if err := s.statPasta(proj.Pasta); err != nil {
		return fmt.Errorf("consultor: pasta do projeto %q inacessível: %w", proj.Nome, err)
	}
	s.prepararRepo(ctx, nil, proj)
	motorNome, modelo, esforco, conta, configDir, budget, timeout := s.resolverMotorConsulta(ctx, nil, projectID)
	g := &GeradorOverview{
		Store:      s.store,
		Motor:      motorNome,
		Modelo:     modelo,
		Esforco:    esforco,
		Conta:      conta,
		ConfigDir:  configDir,
		Dir:        proj.Pasta,
		DirLogs:    s.dirLogs,
		AddDirs:    proj.AddDirs,
		BudgetUSD:  budget,
		TimeoutMin: timeout,
		Selecionar: s.selecionar,
		Prompt:     s.prompt,
		Agora:      s.agora,
	}
	return g.Gerar(ctx, projectID)
}

// montarConsultor resolve o alvo da consulta (projeto ou grupo) e o motor, e
// devolve um Consultor pronto: Dir/AddDirs apontando para o(s) repo(s) e o
// ContextoRepos com a descrição da solução e os overviews.
func (s *Servico) montarConsultor(ctx context.Context, consultaID int64) (*Consultor, error) {
	cons, err := s.store.ObterConsulta(ctx, consultaID)
	if err != nil {
		return nil, fmt.Errorf("consultor: obter consulta %d: %w", consultaID, err)
	}

	var (
		dir      string
		addDirs  []string
		contexto string
	)
	switch {
	case cons.ProjectID != nil:
		dir, addDirs, contexto, err = s.contextoProjeto(ctx, &cons, *cons.ProjectID)
	case cons.GroupID != nil:
		dir, addDirs, contexto, err = s.contextoGrupo(ctx, cons, *cons.GroupID)
	default:
		err = fmt.Errorf("consulta %d sem projeto nem grupo", consultaID)
	}
	if err != nil {
		return nil, err
	}

	motorNome, modelo, esforco, conta, configDir, budget, timeout := s.resolverMotorConsulta(ctx, cons.CriadoPor, consultaID)
	dirRefs := ""
	if pasta := s.Pasta(consultaID); pasta != "" {
		dirRefs = filepath.Join(pasta, referencias.Dir)
	}
	return &Consultor{
		Store:          s.store,
		Idioma:         s.store.IdiomaDoUsuario(ctx, cons.CriadoPor),
		Motor:          motorNome,
		Modelo:         modelo,
		Esforco:        esforco,
		Conta:          conta,
		ConfigDir:      configDir,
		Dir:            dir,
		DirLogs:        s.dirLogs,
		AddDirs:        addDirs,
		BudgetUSD:      budget,
		TimeoutMin:     timeout,
		ContextoRepos:  contexto,
		DirReferencias: dirRefs,
		Selecionar:     s.selecionar,
		Prompt:         s.prompt,
		Agora:          s.agora,
	}, nil
}

// contextoProjeto monta o alvo de uma consulta de projeto único.
func (s *Servico) contextoProjeto(ctx context.Context, cons *db.Consulta, projectID int64) (string, []string, string, error) {
	proj, err := s.store.ObterProjeto(ctx, projectID)
	if err != nil {
		return "", nil, "", fmt.Errorf("consultor: obter projeto %d: %w", projectID, err)
	}
	if err := s.statPasta(proj.Pasta); err != nil {
		return "", nil, "", fmt.Errorf("pasta do projeto %q inacessível: %v", proj.Nome, err)
	}
	s.prepararRepo(ctx, cons, proj)
	return proj.Pasta, proj.AddDirs, secaoRepo(proj), nil
}

// prepararRepo posiciona o repo do projeto na branch principal e o atualiza
// antes da leitura (o serviço roda num servidor — sem isso o consultor leria um
// clone defasado ou em outra branch). Nunca bloqueia a consulta: problemas
// viram aviso na conversa (fala de sistema) ou log, e a análise segue com o
// estado disponível.
func (s *Servico) prepararRepo(ctx context.Context, cons *db.Consulta, proj db.Projeto) {
	aviso, err := s.atualizarRepo(proj.Pasta, proj.BranchPrincipal)
	if err != nil {
		s.logf(fmt.Sprintf("consultor: atualizar repo do projeto %q: %v", proj.Nome, err))
		return
	}
	if aviso == "" {
		return
	}
	s.logf(fmt.Sprintf("consultor: repo do projeto %q: %s", proj.Nome, aviso))
	if cons != nil {
		s.avisar(ctx, *cons, "Aviso sobre o repositório "+proj.Nome+": "+aviso)
	}
}

// contextoGrupo monta o alvo de uma consulta de grupo: Dir = pasta do membro
// principal (ordem 0), AddDirs = pastas dos demais + add_dirs de todos, e o
// contexto com a descrição da solução e o overview de cada repo. Membro
// secundário com pasta inválida vira fala de sistema (aviso) e é ignorado; o
// principal inválido derruba a consulta com erro claro.
func (s *Servico) contextoGrupo(ctx context.Context, cons db.Consulta, groupID int64) (string, []string, string, error) {
	grupo, err := s.store.ObterGrupo(ctx, groupID)
	if err != nil {
		return "", nil, "", fmt.Errorf("consultor: obter grupo %d: %w", groupID, err)
	}
	if len(grupo.Membros) == 0 {
		return "", nil, "", fmt.Errorf("o grupo %q não tem repositórios", grupo.Nome)
	}

	var (
		dir     string
		addDirs []string
		secoes  []string
	)
	cabecalho := "# Solução: " + grupo.Nome
	if d := strings.TrimSpace(grupo.Descricao); d != "" {
		cabecalho += "\n\n" + d
	}
	secoes = append(secoes, cabecalho)

	for i, membro := range grupo.Membros {
		proj, err := s.store.ObterProjeto(ctx, membro.ProjectID)
		if err != nil {
			return "", nil, "", fmt.Errorf("consultor: obter projeto membro %d: %w", membro.ProjectID, err)
		}
		if err := s.statPasta(proj.Pasta); err != nil {
			if i == 0 {
				return "", nil, "", fmt.Errorf(
					"pasta do repositório principal %q inacessível: %v", proj.Nome, err)
			}
			s.avisar(ctx, cons, fmt.Sprintf(
				"O repositório %q do grupo está inacessível e ficou fora desta análise.", proj.Nome))
			continue
		}
		s.prepararRepo(ctx, &cons, proj)
		if i == 0 {
			dir = proj.Pasta
		} else {
			addDirs = append(addDirs, proj.Pasta)
		}
		addDirs = append(addDirs, proj.AddDirs...)
		secoes = append(secoes, secaoRepo(proj))
	}
	if dir == "" {
		return "", nil, "", fmt.Errorf("o grupo %q ficou sem repositório principal acessível", grupo.Nome)
	}
	return dir, addDirs, strings.Join(secoes, "\n\n"), nil
}

// secaoRepo formata a seção de contexto de um repositório (nome, pasta e
// overview — ou um aviso quando o overview ainda não foi gerado).
func secaoRepo(proj db.Projeto) string {
	sec := fmt.Sprintf("## Repositório: %s (pasta %s)", proj.Nome, proj.Pasta)
	if ov := strings.TrimSpace(proj.OverviewMD); ov != "" {
		return sec + "\n\n" + ov
	}
	return sec + "\n\n(este repositório ainda não tem overview cadastrado — investigue a estrutura com mais cautela antes de responder)"
}

// avisar registra uma fala de sistema na consulta (best-effort).
func (s *Servico) avisar(ctx context.Context, cons db.Consulta, texto string) {
	if _, err := s.store.CriarMensagemConsulta(ctx, db.MensagemConsulta{
		ConsultaID: cons.ID, Papel: db.PapelConsultaSistema, Conteudo: texto,
	}); err != nil {
		s.logf(fmt.Sprintf("consultor: registrar aviso na consulta %d: %v", cons.ID, err))
	}
}

// resolverMotorConsulta escolhe o motor/modelo de um turno de CONSULTA.
// Precedência:
//  1. o grupo de usuários de quem criou a consulta, quando define motor e/ou
//     modelo próprios (o rigor da consulta pode ser menor — o grupo permite dar
//     um modelo mais leve/barato a produto/suporte);
//  2. o primeiro motor ativo por prioridade, com modelo_consulta (novo campo
//     dedicado) e, na ausência dele, modelo_analise;
//  3. sem motor cadastrado: default "claude" (funciona out-of-the-box).
//
// criadoPor é o usuário dono da consulta (nil na geração de overview e em
// consultas criadas no modo bootstrap).
func (s *Servico) resolverMotorConsulta(ctx context.Context, criadoPor *int64, afinidade ...int64) (nome, modelo, esforco, conta, configDir string, budget float64, timeout int) {
	seed := int64(0)
	if len(afinidade) > 0 {
		seed = afinidade[0]
	}
	modeloGrupo := ""
	if criadoPor != nil {
		g, ok, err := s.store.GrupoDoUsuario(ctx, *criadoPor)
		if err != nil {
			s.logf(fmt.Sprintf("consultor: grupo do usuário %d: %v", *criadoPor, err))
		} else if ok {
			modeloGrupo = strings.TrimSpace(g.Modelo)
			if g.EngineID != nil {
				if m, err := s.store.ObterMotor(ctx, *g.EngineID); err == nil && m.Ativo {
					alias, dir := contaAtiva(m, seed)
					return m.Nome, escolherModelo(modeloGrupo, m.ModeloConsulta, m.ModeloAnalise),
						"", alias, dir, m.BudgetFaseUSD, m.TimeoutMin
				} else if err != nil {
					s.logf(fmt.Sprintf("consultor: motor do grupo %q: %v", g.Nome, err))
				}
				// motor do grupo removido/inativo: cai no padrão, preservando o
				// modelo do grupo (se houver).
			}
		}
	}

	motores, err := s.store.ListarMotores(ctx)
	if err != nil {
		s.logf(fmt.Sprintf("consultor: listar motores: %v", err))
		return "claude", modeloGrupo, "", "", "", 0, 0
	}
	for _, m := range motores {
		// Motores fora do fallback são de uso manual: valem quando o grupo de
		// usuários aponta para eles (acima), nunca na escolha automática.
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
