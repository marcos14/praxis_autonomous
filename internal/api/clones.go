package api

// Cadastro de projeto POR CLONE (Fase C do PLANO_MULTIUSUARIO.md): o POST
// /projects aceita `url_git` como alternativa à `pasta` — o Praxis clona o
// repositório em PRAXIS_HOME/repos/<slug> com a chave SSH do usuário e cria o
// projeto apontando para lá. O clone pode demorar (repos grandes), então roda
// como JOB em background: o POST devolve 202 + id do job e a UI faz poll em
// GET /projects/clones/{id}. Jobs vivem em memória (como as sessões de login
// dos motores): reiniciar o serviço perde o STATUS, nunca um projeto criado.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/marcos14/praxis-autonomous/internal/db"
	"github.com/marcos14/praxis-autonomous/internal/gitops"
)

// timeoutClone limita o job (repos grandes em redes lentas ainda cabem).
const timeoutClone = 30 * time.Minute

// Estados de um job de clone.
const (
	cloneStatusClonando  = "clonando"
	cloneStatusConcluido = "concluido"
	cloneStatusErro      = "erro"
)

// jobClone é o estado de um cadastro por clone em andamento.
type jobClone struct {
	ID       string      `json:"id"`
	URL      string      `json:"url"`
	Status   string      `json:"status"`
	Detalhe  string      `json:"detalhe,omitempty"`
	Projeto  *db.Projeto `json:"projeto,omitempty"`
	CriadoEm time.Time   `json:"criado_em"`
}

// gerenteClones guarda os jobs em memória, expurgando os antigos.
type gerenteClones struct {
	mu   sync.Mutex
	jobs map[string]*jobClone
}

func novoGerenteClones() *gerenteClones {
	return &gerenteClones{jobs: map[string]*jobClone{}}
}

func (g *gerenteClones) criar(url string) (*jobClone, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	j := &jobClone{ID: hex.EncodeToString(b), URL: url,
		Status: cloneStatusClonando, CriadoEm: time.Now()}
	g.mu.Lock()
	defer g.mu.Unlock()
	// Expurgo: jobs terminados há mais de uma hora saem da memória.
	corte := time.Now().Add(-time.Hour)
	for id, v := range g.jobs {
		if v.Status != cloneStatusClonando && v.CriadoEm.Before(corte) {
			delete(g.jobs, id)
		}
	}
	g.jobs[j.ID] = j
	return j, nil
}

func (g *gerenteClones) obter(id string) (jobClone, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	j, ok := g.jobs[id]
	if !ok {
		return jobClone{}, false
	}
	return *j, true
}

func (g *gerenteClones) finalizar(id, status, detalhe string, projeto *db.Projeto) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if j, ok := g.jobs[id]; ok {
		j.Status, j.Detalhe, j.Projeto = status, detalhe, projeto
	}
}

// dirRepos é a pasta dos clones gerenciados: PRAXIS_HOME/repos (a pasta do
// banco É o PRAXIS_HOME — mesma raiz dos perfis de motor e das chaves SSH).
func (s *Servidor) dirRepos() string {
	if s.banco == nil || strings.TrimSpace(s.banco.Caminho) == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(s.banco.Caminho), "repos")
}

// handleCriarProjetoPorClone é o ramo do POST /projects com url_git: valida
// tudo que dá ANTES do clone (nome, slug livre, visibilidade, chave SSH) e
// dispara o job. A permissão (projetos.criar||gerir) já foi exigida pelo
// handleCriarProjeto.
func (s *Servidor) handleCriarProjetoPorClone(w http.ResponseWriter, r *http.Request, req reqProjeto) {
	if strings.TrimSpace(req.Pasta) != "" {
		responderErro(w, http.StatusBadRequest, "invalido", "informe pasta OU url_git, não os dois")
		return
	}
	nome := strings.TrimSpace(req.Nome)
	if nome == "" {
		responderErro(w, http.StatusBadRequest, "invalido", "nome é obrigatório")
		return
	}
	slug := gerarSlug(req.Slug)
	if slug == "" {
		slug = gerarSlug(nome)
	}
	if slug == "" {
		responderErro(w, http.StatusBadRequest, "invalido", "slug inválido: informe um slug ou um nome com caracteres alfanuméricos")
		return
	}
	dirRepos := s.dirRepos()
	if dirRepos == "" {
		erroT(w, r, http.StatusServiceUnavailable, "indisponivel", "erro.clone_indisponivel")
		return
	}
	destino := filepath.Join(dirRepos, slug)
	if _, err := os.Stat(destino); err == nil {
		responderErro(w, http.StatusConflict, "destino_existente",
			"já existe um clone em "+destino+" (slug em uso?)")
		return
	}

	url := strings.TrimSpace(req.URLGit)
	uid := usuarioDaRequisicao(r)

	// URLs SSH exigem a chave do usuário (é ela que o clone vai usar); https
	// público passa sem chave — e sem credencial registrada no projeto.
	var ambiente []string
	var sshUser *int64
	if urlGitUsaSSH(url) {
		if s.ssh == nil || uid == nil || !s.ssh.Existe(*uid) {
			erroT(w, r, http.StatusPreconditionFailed, "sem_chave", "erro.clone_exige_chave")
			return
		}
		ambiente = s.ssh.AmbienteGit(*uid)
		sshUser = uid
	}

	// Visibilidade validada ANTES do clone (falhar depois de minutos de
	// download seria hostil).
	var aclUsuarios, aclGrupos []int64
	if req.Visibilidade != "" {
		var msg string
		aclUsuarios, aclGrupos, msg = s.resolverVisibilidadeACL(r, req.Visibilidade, req.GrupoID,
			uid, temPermissao(r, db.PermProjetosGerir))
		if msg != "" {
			responderErro(w, http.StatusBadRequest, "invalido", msg)
			return
		}
	}

	job, err := s.clones.criar(url)
	if err != nil {
		s.log.Error("criar job de clone", "erro", err)
		erroT(w, r, http.StatusInternalServerError, "erro_interno", "erro.interno")
		return
	}

	// O job continua além da requisição (context.Background de propósito) — a
	// UI acompanha pelo GET do job.
	go s.executarClone(job.ID, url, destino, ambiente, req, nome, slug, uid, sshUser,
		req.Visibilidade, aclUsuarios, aclGrupos)

	responderJSON(w, http.StatusAccepted, map[string]string{
		"job_id": job.ID,
		"status": cloneStatusClonando,
	})
}

// executarClone roda o clone e, no sucesso, cria o projeto (dono, visibilidade
// e credencial SSH incluídos). Qualquer falha vira o status/detalhe do job — e
// o diretório é limpo para o retry partir do zero.
func (s *Servidor) executarClone(jobID, url, destino string, ambiente []string, req reqProjeto,
	nome, slug string, owner, sshUser *int64, visibilidade string, aclUsuarios, aclGrupos []int64) {

	ctx, cancelar := context.WithTimeout(context.Background(), timeoutClone)
	defer cancelar()

	if err := gitops.Clonar(ctx, url, destino, ambiente); err != nil {
		s.clones.finalizar(jobID, cloneStatusErro, err.Error(), nil)
		return
	}
	if msg := validarPastaRepoGit(destino); msg != "" {
		_ = os.RemoveAll(destino)
		s.clones.finalizar(jobID, cloneStatusErro, "clone concluído mas inválido: "+msg, nil)
		return
	}

	p := db.Projeto{
		Nome:            nome,
		Slug:            slug,
		Pasta:           destino,
		BranchPrincipal: strings.TrimSpace(req.BranchPrincipal),
		ModoIntegracao:  strings.TrimSpace(req.ModoIntegracao),
		URLPlataforma:   strings.TrimSpace(req.URLPlataforma),
		AddDirs:         req.AddDirs,
		Ativo:           true,
		OwnerUserID:     owner,
		SSHUserID:       sshUser,
	}
	if p.BranchPrincipal == "" {
		p.BranchPrincipal = branchPrincipalPadrao
	}
	if p.ModoIntegracao == "" {
		p.ModoIntegracao = modoIntegracaoPadrao
	}
	if req.Ativo != nil {
		p.Ativo = *req.Ativo
	}

	criado, err := s.banco.CriarProjeto(ctx, p)
	if err != nil {
		_ = os.RemoveAll(destino)
		s.clones.finalizar(jobID, cloneStatusErro, "clone ok, mas o cadastro falhou: "+err.Error(), nil)
		return
	}
	if visibilidade != "" && visibilidade != db.VisibilidadePublica {
		if err := s.banco.DefinirAcessoProjeto(ctx, criado.ID, aclUsuarios, aclGrupos); err != nil {
			s.log.Error("definir acesso do projeto clonado", "projeto", criado.ID, "erro", err)
		}
	}
	if completo, err := s.banco.ObterProjeto(ctx, criado.ID); err == nil {
		criado = completo
	}
	s.clones.finalizar(jobID, cloneStatusConcluido, "", &criado)
}

// handleObterClone devolve o estado de um job de clone.
func (s *Servidor) handleObterClone(w http.ResponseWriter, r *http.Request) {
	j, ok := s.clones.obter(r.PathValue("cloneId"))
	if !ok {
		erroT(w, r, http.StatusNotFound, "nao_encontrado", "erro.clone_nao_encontrado")
		return
	}
	responderJSON(w, http.StatusOK, j)
}

// urlGitUsaSSH detecta URLs que autenticam por SSH: scp-like
// (git@host:org/repo.git) e ssh://; http(s) não usa chave.
func urlGitUsaSSH(url string) bool {
	u := strings.ToLower(strings.TrimSpace(url))
	if strings.HasPrefix(u, "ssh://") {
		return true
	}
	if strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://") ||
		strings.HasPrefix(u, "file://") || strings.HasPrefix(u, "git://") {
		return false
	}
	// forma scp-like: usuario@host:caminho
	return strings.Contains(u, "@") && strings.Contains(u, ":")
}
