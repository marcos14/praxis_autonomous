package pipeline

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/marcos14/praxis-autonomous/internal/db"
	"github.com/marcos14/praxis-autonomous/internal/gitops"
	"github.com/marcos14/praxis-autonomous/internal/motor"
	"github.com/marcos14/praxis-autonomous/internal/procs"
)

// maxSlugDemanda limita o tamanho do slug derivado do titulo da demanda, para que
// o nome da branch/worktree nao estoure limites de caminho (agravado pelos
// worktrees profundos do Windows, mesmo com core.longpaths).
const maxSlugDemanda = 40

// Runner conduz uma demanda no seu worktree/branch DEDICADO: prepara o ambiente
// (branch praxis/d<id>-<slug> a partir da main atualizada + `git worktree add`)
// e roda o ciclo completo de uma fase (executor→gates→corretor→revisor→commit,
// portado na Fase 2b) ali dentro — um commit por fase, feito sempre pelo
// orquestrador. Empacota o que o executar.go do Praxis atual fazia no unico
// working tree do projeto, agora isolado por demanda.
//
// O Runner NAO decide fila nem concorrencia (isso e do scheduler, Fase 2d) e NAO
// publica a branch (push automatico e a Fase 2f): apenas prepara o ambiente e
// executa uma fase por chamada. Quem resolve a Config (global→projeto, motores e
// contas do banco) e a passa em RodarFase e o scheduler (Fases 2d/2g).
type Runner struct {
	Store *db.DB      // fila/estado no banco (demandas, fases, runs, eventos)
	Git   *gitops.Ops // operacoes git serializadas por projeto
	Home  string      // PRAXIS_HOME (base de worktrees/ e logs/); "" resolve via db.PraxisHome
	Gates Gates       // runner de gates (Fase 2c); nil = etapa de gates aprovada
	// Prompt carrega o template de um prompt por nome (executor.md/corretor.md/
	// revisor.md). Repassado ao ContextoExec.
	Prompt func(nome string) (string, error)

	// Procs registra os PIDs dos processos de harness vivos, para a recuperacao
	// pos-restart (Fase 2i) matar os orfaos no boot. nil = sem registro (testes).
	Procs *procs.Registro

	// Seams de teste (nil em producao):
	Selecionar func(nome string) (motor.Motor, error) // default: motor.Selecionar
	Agora      func() time.Time                       // default: time.Now
}

// Preparar garante que a demanda tenha branch e worktree dedicados, criando-os
// se ainda nao existirem, e persiste branch/worktree_path na demanda. Devolve a
// demanda atualizada (Branch e WorktreePath preenchidos).
//
// A branch praxis/d<id>-<slug> parte SEMPRE da main atualizada: com remote
// configurado, faz `git fetch origin <branch_principal>` e deriva de
// origin/<branch_principal>; sem remote (ou se o fetch falhar), parte da main
// local. Falha de fetch NAO bloqueia — registra evento e cai na main local
// (mesmo espirito de resiliencia do push automatico da Fase 2f).
//
// Preparar e IDEMPOTENTE: se o worktree ja existe e e valido (ex.: chamada para
// a 2a fase da mesma demanda, ou retomada), reaproveita-o sem recriar a branch.
func (r *Runner) Preparar(ctx context.Context, dem db.Demanda) (db.Demanda, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if r.Store == nil {
		return dem, fmt.Errorf("runner sem Store")
	}
	if r.Git == nil {
		return dem, fmt.Errorf("runner sem Git")
	}

	proj, err := r.Store.ObterProjeto(ctx, dem.ProjectID)
	if err != nil {
		return dem, fmt.Errorf("obter projeto %d da demanda %d: %w", dem.ProjectID, dem.ID, err)
	}
	repo := strings.TrimSpace(proj.Pasta)
	if repo == "" {
		return dem, fmt.Errorf("projeto %d sem pasta configurada", proj.ID)
	}

	home, err := r.resolverHome()
	if err != nil {
		return dem, err
	}

	branch := strings.TrimSpace(dem.Branch)
	if branch == "" {
		branch = NomeBranch(dem.ID, dem.Titulo)
	}
	worktree := strings.TrimSpace(dem.WorktreePath)
	if worktree == "" {
		worktree = CaminhoWorktree(home, proj.Slug, dem.ID, dem.Titulo)
	}

	// idempotencia: worktree ja existe e e um repo git valido → reaproveita.
	if gitops.EhRepoGit(worktree) {
		return r.persistirAmbiente(ctx, dem, branch, worktree)
	}

	base := strings.TrimSpace(proj.BranchPrincipal)
	if base == "" {
		base = "main"
	}
	if gitops.TemRemote(repo) {
		if err := r.Git.Fetch(repo, base); err != nil {
			// resiliencia: sem a main remota atualizada, parte da main local.
			r.registrarEvento(ctx, dem, "aviso",
				"Praxis: fetch da main falhou — usando main local",
				fmt.Sprintf("Projeto %s (%s): %v", proj.Nome, base, err))
		} else {
			base = "origin/" + base
		}
	}

	if err := r.Git.WorktreeAdd(repo, worktree, branch, base); err != nil {
		return dem, fmt.Errorf("criar worktree da demanda %d (branch %s, base %s): %w", dem.ID, branch, base, err)
	}
	r.registrarEvento(ctx, dem, "worktree_criado",
		fmt.Sprintf("Praxis: branch %s criada", branch),
		fmt.Sprintf("Worktree: %s\nBase: %s", worktree, base))

	return r.persistirAmbiente(ctx, dem, branch, worktree)
}

// persistirAmbiente grava branch/worktree_path na demanda se mudaram e devolve a
// linha atualizada (ou a propria demanda, se nada mudou / sem Store).
func (r *Runner) persistirAmbiente(ctx context.Context, dem db.Demanda, branch, worktree string) (db.Demanda, error) {
	if dem.Branch == branch && dem.WorktreePath == worktree {
		return dem, nil
	}
	dem.Branch = branch
	dem.WorktreePath = worktree
	atual, err := r.Store.AtualizarDemanda(ctx, dem)
	if err != nil {
		return dem, fmt.Errorf("persistir branch/worktree da demanda %d: %w", dem.ID, err)
	}
	return atual, nil
}

// RodarFase executa o ciclo completo de UMA fase (executor→gates→corretor→
// revisor→commit, Fase 2b) no worktree ja preparado da demanda, com a Config
// resolvida. Monta o ContextoExec e delega a ExecutarFase. O commit e feito pelo
// orquestrador dentro de ExecutarFase; o push automatico da branch e a Fase 2f.
//
// A demanda precisa ter passado por Preparar (WorktreePath preenchido). O erro
// de retorno e reservado a falhas de infraestrutura; os desfechos logicos da
// fase ficam em ResultadoFase.Situacao.
func (r *Runner) RodarFase(ctx context.Context, dem db.Demanda, fase db.Fase, cfg Config, pausaCh <-chan struct{}) (ResultadoFase, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	worktree := strings.TrimSpace(dem.WorktreePath)
	if worktree == "" {
		return ResultadoFase{Situacao: SituacaoFalhou, Erro: "worktree nao preparado"},
			fmt.Errorf("demanda %d sem worktree preparado — chame Preparar antes", dem.ID)
	}

	dirLogs, err := r.dirLogs(dem)
	if err != nil {
		return ResultadoFase{Situacao: SituacaoFalhou, Erro: err.Error()}, err
	}

	c := &ContextoExec{
		Demanda:    dem,
		Fase:       fase,
		Worktree:   worktree,
		DirLogs:    dirLogs,
		Config:     cfg,
		Store:      r.Store,
		Git:        r.Git,
		Gates:      r.Gates,
		Prompt:     r.Prompt,
		Ctx:        ctx,
		PausaCh:    pausaCh,
		Selecionar: r.Selecionar,
		Agora:      r.Agora,
	}
	if r.Procs != nil {
		c.RegistrarProcesso = r.Procs.Registrar
	}
	res, err := c.ExecutarFase()
	if err != nil || !res.CommitFeito {
		return res, err
	}

	// Fase 2f: push automatico da branch APOS o commit da fase, tolerante a
	// falha. So no modo merge_request (merge_local integra localmente, sem push).
	// A falha de push NAO altera o desfecho da fase — apenas registra o alerta e
	// deixa o retry para o proximo commit ou para a acao manual publicar_branch.
	if r.autoPushHabilitado(ctx, dem) {
		rp := r.empurrarBranch(ctx, dem)
		res.Publicado = rp.Publicado
		res.CommitsNaoPublicados = rp.CommitsNaoPublicados
	}
	return res, nil
}

// TentativasPush e o numero de tentativas do push automatico da branch apos um
// commit de fase (retry com espera crescente dentro do proprio gitops.Push).
const TentativasPush = 3

// ResultadoPush resume uma tentativa de publicar a branch da demanda. Nunca
// representa erro de infraestrutura — o push e sempre tolerante a falha; a falha
// de rede/credenciais vira alerta, nao erro.
type ResultadoPush struct {
	Publicado            bool   // o push concluiu com sucesso nesta tentativa
	Pulado               bool   // push nao se aplica (sem remote / demanda sem branch)
	Motivo               string // quando Pulado: por que; quando falhou: o erro do push
	CommitsNaoPublicados int    // commits locais ainda aguardando push (0 = tudo publicado)
}

// PublicarBranch e a acao manual "publicar_branch" do plano: publica a branch da
// demanda no origin sob demanda do usuario — um retry manual do push automatico.
// E tolerante a falha como o push pos-commit: uma falha de rede/credenciais NAO
// vira erro (vem em ResultadoPush.Motivo com Publicado=false); o erro de retorno
// e reservado a pre-condicoes (Runner sem Git, demanda sem branch/worktree).
//
// Autenticacao usa as credenciais git da maquina (credential manager/SSH); o
// Praxis nunca armazena senha de git. Push so de branches praxis/* (guarda no
// gitops.Push). Diferente do push automatico, NAO depende do modo de integracao:
// se o usuario pediu para publicar, publica (desde que haja remote).
func (r *Runner) PublicarBranch(ctx context.Context, dem db.Demanda) (ResultadoPush, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if r.Git == nil {
		return ResultadoPush{}, fmt.Errorf("runner sem Git")
	}
	if strings.TrimSpace(dem.Branch) == "" || strings.TrimSpace(dem.WorktreePath) == "" {
		return ResultadoPush{}, fmt.Errorf("demanda %d sem branch/worktree preparados — chame Preparar antes", dem.ID)
	}
	return r.empurrarBranch(ctx, dem), nil
}

// autoPushHabilitado informa se o push automatico pos-commit se aplica a esta
// demanda: verdadeiro apenas quando o projeto esta no modo merge_request (o
// merge_local integra localmente, sem push). A presenca de remote e checada
// depois, em empurrarBranch.
func (r *Runner) autoPushHabilitado(ctx context.Context, dem db.Demanda) bool {
	if r.Store == nil {
		return false
	}
	proj, err := r.Store.ObterProjeto(ctx, dem.ProjectID)
	if err != nil {
		return false
	}
	return proj.ModoIntegracao == db.ModoIntegracaoMergeRequest
}

// empurrarBranch tenta publicar a branch da demanda no origin de forma TOLERANTE
// A FALHA. Sem remote configurado e no-op (Pulado). Em falha de push
// (rede/credenciais/branch protegida), NAO propaga erro: registra o evento de
// alerta "commits nao publicados (N)" e devolve a contagem, deixando o retry para
// o proximo commit (o proximo push empurra tambem os commits acumulados) ou para
// a acao manual publicar_branch. Em sucesso, registra o evento de branch
// publicada. Reaproveitado pelo push automatico (RodarFase) e pelo manual
// (PublicarBranch).
func (r *Runner) empurrarBranch(ctx context.Context, dem db.Demanda) ResultadoPush {
	if ctx == nil {
		ctx = context.Background()
	}
	worktree := strings.TrimSpace(dem.WorktreePath)
	branch := strings.TrimSpace(dem.Branch)
	if worktree == "" || branch == "" {
		return ResultadoPush{Pulado: true, Motivo: "demanda sem branch/worktree preparados"}
	}
	if !gitops.TemRemote(worktree) {
		// sem remote nao ha o que publicar (dev local); nao e falha.
		return ResultadoPush{Pulado: true, Motivo: "projeto sem remote configurado"}
	}

	pushErr := r.Git.Push(worktree, branch, TentativasPush)
	// contagem best-effort do alerta; nao mascara o desfecho do push.
	naoPub, cerr := gitops.CommitsNaoPublicados(worktree, branch)
	if cerr != nil {
		naoPub = 0
	}
	if pushErr != nil {
		r.registrarEvento(ctx, dem, "push_falhou",
			fmt.Sprintf("Praxis: commits nao publicados (%d)", naoPub),
			fmt.Sprintf("Falha ao publicar a branch %s: %v\nA fase concluiu normalmente; o push sera retentado no proximo commit ou pela acao manual publicar_branch.", branch, pushErr))
		return ResultadoPush{Motivo: pushErr.Error(), CommitsNaoPublicados: naoPub}
	}
	r.registrarEvento(ctx, dem, "branch_publicada",
		fmt.Sprintf("Praxis: branch %s publicada", branch),
		fmt.Sprintf("Push da branch %s concluido no origin.", branch))
	return ResultadoPush{Publicado: true, CommitsNaoPublicados: naoPub}
}

// dirLogs devolve (criando) a pasta dos .jsonl das execucoes desta demanda:
// PRAXIS_HOME/logs/d<id>. Isola os logs por demanda para os rotulos de run
// (fase-<codigo>-<etapa>) nao colidirem entre demandas concorrentes.
func (r *Runner) dirLogs(dem db.Demanda) (string, error) {
	home, err := r.resolverHome()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, "logs", fmt.Sprintf("d%d", dem.ID))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("criar dir de logs %q: %w", dir, err)
	}
	return dir, nil
}

// resolverHome devolve o PRAXIS_HOME do Runner (campo Home) ou o default do
// sistema (db.PraxisHome).
func (r *Runner) resolverHome() (string, error) {
	if h := strings.TrimSpace(r.Home); h != "" {
		return h, nil
	}
	return db.PraxisHome()
}

// registrarEvento grava um evento associado a demanda/projeto (best-effort — a
// falha de escrita e ignorada, como as notificacoes do Praxis atual).
func (r *Runner) registrarEvento(ctx context.Context, dem db.Demanda, tipo, titulo, detalhe string) {
	if r.Store == nil {
		return
	}
	_, _ = r.Store.RegistrarEvento(ctx, evento(dem.ProjectID, dem.ID, tipo, titulo, detalhe))
}

// NomeBranch monta o nome da branch dedicada de uma demanda: praxis/d<id>-<slug>,
// com o slug derivado do titulo. O d<id> garante unicidade entre demandas mesmo
// com titulos iguais; o slug e so legibilidade.
func NomeBranch(demandaID int64, titulo string) string {
	return gitops.PrefixoBranch + nomeCurtoDemanda(demandaID, titulo)
}

// CaminhoWorktree monta o caminho do worktree dedicado de uma demanda:
// <home>/worktrees/<projeto>/d<id>-<slug>. Espelha o tail da branch para facilitar
// a correlacao visual entre pasta e branch.
func CaminhoWorktree(home, projetoSlug string, demandaID int64, titulo string) string {
	proj := strings.TrimSpace(projetoSlug)
	if proj == "" {
		proj = "sem-projeto"
	}
	return filepath.Join(home, "worktrees", proj, nomeCurtoDemanda(demandaID, titulo))
}

// nomeCurtoDemanda devolve o identificador legivel e unico da demanda usado tanto
// no tail da branch quanto no nome do diretorio do worktree: d<id>-<slug>.
func nomeCurtoDemanda(demandaID int64, titulo string) string {
	slug := slugDemanda(titulo)
	if slug == "" {
		return fmt.Sprintf("d%d", demandaID)
	}
	return fmt.Sprintf("d%d-%s", demandaID, slug)
}

// slugDemanda converte o titulo num slug ASCII kebab-case (minusculo, so
// [a-z0-9-], sem hifens nas pontas), truncado em maxSlugDemanda. Mesma politica
// do gerarSlug usado nos projetos.
func slugDemanda(titulo string) string {
	titulo = strings.ToLower(strings.TrimSpace(titulo))
	var b strings.Builder
	ultimoHifen := false
	for _, r := range titulo {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			ultimoHifen = false
		default:
			if !ultimoHifen && b.Len() > 0 {
				b.WriteByte('-')
				ultimoHifen = true
			}
		}
		if b.Len() >= maxSlugDemanda {
			break
		}
	}
	return strings.Trim(b.String(), "-")
}
