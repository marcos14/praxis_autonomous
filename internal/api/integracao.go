package api

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/marcos14/praxis-autonomous/internal/db"
	"github.com/marcos14/praxis-autonomous/internal/gitops"
)

// registrarRotasIntegracao registra as rotas de fechamento/integração da demanda
// (Fases 4c/4d): o preview de merge com a main.
func (s *Servidor) registrarRotasIntegracao(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/demands/{id}/merge-preview", s.handleMergePreview)
}

// respMergePreview descreve o estado de fechamento de uma demanda: a branch, os
// commits que ela acrescenta sobre a main, quantos ainda não foram publicados, o
// preview de conflito com a main e — no modo merge_request — o link para abrir o
// MR na plataforma.
type respMergePreview struct {
	Branch               string              `json:"branch"`
	Base                 string              `json:"base"`
	WorktreePath         string              `json:"worktree_path"` // caminho da worktree no servidor ("" após integrar)
	ModoIntegracao       string              `json:"modo_integracao"`
	URLMR                string              `json:"url_mr"`
	CommitsNaoPublicados int                 `json:"commits_nao_publicados"`
	Commits              []gitops.CommitInfo `json:"commits"`
	Limpo                bool                `json:"limpo"`
	Conflitos            []string            `json:"conflitos"`
	Aviso                string              `json:"aviso,omitempty"`
	Status               string              `json:"status"`       // status atual da demanda (pode ter mudado na reconciliação)
	JaIntegrada          bool                `json:"ja_integrada"` // true = branch já mesclada na main
}

// handleMergePreview devolve o preview de integração da demanda com a main:
// commits, contagem de não-publicados, conflitos e link do MR. É só leitura
// (não toca refs). Demanda sem branch (ainda não executou) → 409.
func (s *Servidor) handleMergePreview(w http.ResponseWriter, r *http.Request) {
	dem, ok := s.obterDemandaOu404(w, r)
	if !ok {
		return
	}
	proj, err := s.banco.ObterProjeto(r.Context(), dem.ProjectID)
	if err != nil {
		s.responderErroDemanda(w, err)
		return
	}
	if strings.TrimSpace(dem.Branch) == "" {
		responderErro(w, http.StatusConflict, "sem_branch",
			"a demanda ainda não tem branch (ainda não começou a executar)")
		return
	}

	// Reconciliação do modo merge_request (Fase 4e): se o MR já foi mesclado na
	// main, marca a demanda integrada e limpa worktree/branch antes de responder.
	if atual, integrou := s.reconciliarMR(r, proj, dem); integrou {
		dem = atual
	}

	base := baseDoProjeto(proj)
	repo := proj.Pasta

	resp := respMergePreview{
		Branch:         dem.Branch,
		Base:           base,
		WorktreePath:   strings.TrimSpace(dem.WorktreePath),
		ModoIntegracao: proj.ModoIntegracao,
		Status:         dem.Status,
		JaIntegrada:    dem.Status == db.StatusDemandaIntegrada,
		Conflitos:      []string{},
		Commits:        []gitops.CommitInfo{},
	}
	if proj.ModoIntegracao == db.ModoIntegracaoMergeRequest {
		resp.URLMR = montarURLMR(proj, dem.Branch, base)
	}

	// Preview de conflito (best-effort): se as refs não resolverem (base/branch
	// ausente localmente), não falha o endpoint — devolve um aviso.
	if previa, err := s.git.PreviaMerge(repo, base, dem.Branch); err != nil {
		resp.Aviso = "não foi possível simular o merge: " + err.Error()
	} else {
		resp.Limpo = previa.Limpo
		if previa.Conflitos != nil {
			resp.Conflitos = previa.Conflitos
		}
	}
	if commits, err := gitops.CommitsAFrente(repo, base, dem.Branch); err == nil {
		resp.Commits = commits
	}
	if n, err := gitops.CommitsNaoPublicados(repo, dem.Branch); err == nil {
		resp.CommitsNaoPublicados = n
	}

	responderJSON(w, http.StatusOK, resp)
}

// numTentativasPush é o número de tentativas do push manual (publicar_branch).
const numTentativasPush = 3

// acaoPublicarBranch empurra a branch da demanda para o origin (ação manual
// `publicar_branch`, útil quando o push automático pós-commit falhou — ver Fase
// 2f). É agnóstica ao modo de integração. Registra evento de sucesso/falha.
// Requer que a demanda já tenha branch. Falha de push → 502 (não altera status).
func (s *Servidor) acaoPublicarBranch(w http.ResponseWriter, r *http.Request, dem db.Demanda) {
	proj, err := s.banco.ObterProjeto(r.Context(), dem.ProjectID)
	if err != nil {
		s.responderErroDemanda(w, err)
		return
	}
	if strings.TrimSpace(dem.Branch) == "" {
		responderErro(w, http.StatusConflict, "sem_branch",
			"a demanda ainda não tem branch para publicar")
		return
	}
	if err := s.git.Push(proj.Pasta, dem.Branch, numTentativasPush); err != nil {
		s.registrarEventoDemanda(r, dem, "push_falhou", "Praxis: falha ao publicar a branch",
			"git push de "+dem.Branch+" falhou: "+err.Error())
		responderErro(w, http.StatusBadGateway, "push_falhou", "falha ao publicar a branch: "+err.Error())
		return
	}
	s.registrarEventoDemanda(r, dem, "branch_publicada", "Praxis: branch publicada",
		"branch "+dem.Branch+" publicada em origin.")
	s.responderDemandaComFases(w, r, dem)
}

// baseDoProjeto devolve a branch principal do projeto (default "main").
func baseDoProjeto(proj db.Projeto) string {
	if b := strings.TrimSpace(proj.BranchPrincipal); b != "" {
		return b
	}
	return "main"
}

// marcarConflito coloca a demanda em `conflito`, gravando os arquivos afetados
// no campo erro, e registra o evento. Devolve a demanda atualizada.
func (s *Servidor) marcarConflito(r *http.Request, dem db.Demanda, contexto string, arquivos []string) db.Demanda {
	dem.Status = db.StatusDemandaConflito
	dem.Erro = contexto + ": " + strings.Join(arquivos, ", ")
	atual, err := s.banco.AtualizarDemanda(r.Context(), dem)
	if err != nil {
		s.log.Error("marcar conflito", "erro", err, "demanda", dem.ID)
		atual = dem
	}
	s.registrarEventoDemanda(r, atual, "conflito", "Praxis: conflito com a main",
		contexto+" — arquivos em conflito: "+strings.Join(arquivos, ", "))
	return atual
}

// acaoIntegrar integra a branch da demanda na main via merge --no-ff local (modo
// merge_local, Fase 4d). Antes do merge, simula com PreviaMerge: em conflito,
// coloca a demanda em `conflito` (+ arquivos) e devolve 409, sem tocar a main;
// limpo, faz o merge, marca `integrada` e registra o evento. A limpeza do
// worktree/branch é da Fase 4e. Só faz sentido no modo merge_local.
func (s *Servidor) acaoIntegrar(w http.ResponseWriter, r *http.Request, dem db.Demanda) {
	proj, err := s.banco.ObterProjeto(r.Context(), dem.ProjectID)
	if err != nil {
		s.responderErroDemanda(w, err)
		return
	}
	if strings.TrimSpace(dem.Branch) == "" {
		responderErro(w, http.StatusConflict, "sem_branch", "a demanda ainda não tem branch para integrar")
		return
	}
	if proj.ModoIntegracao != db.ModoIntegracaoMergeLocal {
		responderErro(w, http.StatusConflict, "modo_invalido",
			"integrar local só no modo merge_local; no modo merge_request, abra o MR na plataforma")
		return
	}
	base := baseDoProjeto(proj)

	previa, err := s.git.PreviaMerge(proj.Pasta, base, dem.Branch)
	if err != nil {
		responderErro(w, http.StatusBadGateway, "preview_falhou", "não foi possível simular o merge: "+err.Error())
		return
	}
	if !previa.Limpo {
		atual := s.marcarConflito(r, dem, "conflito ao integrar na "+base, previa.Conflitos)
		responderJSONConflito(w, atual, previa.Conflitos)
		return
	}

	msg := "Merge da demanda #" + strconv.FormatInt(dem.ID, 10) + " (" + dem.Branch + ") na " + base
	if err := s.git.MergeNoFF(proj.Pasta, base, dem.Branch, msg); err != nil {
		responderErro(w, http.StatusBadGateway, "merge_falhou", "falha ao integrar: "+err.Error())
		return
	}
	dem.Status = db.StatusDemandaIntegrada
	dem.Erro = ""
	// limpeza pós-integração (Fase 4e): remove worktree + branch e zera o
	// worktree_path do registro (best-effort — não desfaz o merge já feito).
	s.limparPosIntegracao(r, proj, dem)
	dem.WorktreePath = ""
	atual, err := s.banco.AtualizarDemanda(r.Context(), dem)
	if err != nil {
		s.responderErroDemanda(w, err)
		return
	}
	s.registrarEventoDemanda(r, atual, "demanda_integrada", "Praxis: demanda integrada",
		"branch "+dem.Branch+" integrada na "+base+" (merge --no-ff).")
	s.responderDemandaComFases(w, r, atual)
}

// limparPosIntegracao remove o worktree e apaga a branch local da demanda após a
// integração (Fase 4e). Best-effort: falhas viram log/evento, sem reverter a
// integração. Remove o worktree ANTES da branch (uma branch em check-out num
// worktree não pode ser apagada).
func (s *Servidor) limparPosIntegracao(r *http.Request, proj db.Projeto, dem db.Demanda) {
	if wt := strings.TrimSpace(dem.WorktreePath); wt != "" {
		if err := s.git.WorktreeRemove(proj.Pasta, wt); err != nil {
			s.log.Warn("remover worktree pós-integração", "erro", err, "demanda", dem.ID)
		}
	}
	if br := strings.TrimSpace(dem.Branch); br != "" {
		if err := s.git.RemoverBranch(proj.Pasta, br); err != nil {
			s.log.Warn("remover branch pós-integração", "erro", err, "demanda", dem.ID)
		}
	}
	s.registrarEventoDemanda(r, dem, "limpeza_pos_integracao", "Praxis: worktree/branch removidos",
		"worktree e branch "+dem.Branch+" removidos após a integração.")
}

// reconciliarMR detecta, no modo merge_request, que a branch da demanda já foi
// mesclada na main (o dev abriu e mergeou o MR na plataforma): faz fetch da base,
// e se a branch estiver totalmente contida em origin/<base> (ou na base local),
// marca a demanda como integrada e limpa worktree/branch. Best-effort e
// idempotente. Devolve a demanda (possivelmente atualizada) e se integrou agora.
func (s *Servidor) reconciliarMR(r *http.Request, proj db.Projeto, dem db.Demanda) (db.Demanda, bool) {
	if proj.ModoIntegracao != db.ModoIntegracaoMergeRequest ||
		dem.Status == db.StatusDemandaIntegrada || strings.TrimSpace(dem.Branch) == "" {
		return dem, false
	}
	base := baseDoProjeto(proj)
	ref := base
	if gitops.TemRemote(proj.Pasta) {
		if err := s.git.Fetch(proj.Pasta, base); err == nil {
			ref = "origin/" + base
		}
	}
	integrada, err := gitops.BranchIntegrada(proj.Pasta, ref, dem.Branch)
	if err != nil || !integrada {
		return dem, false
	}
	dem.Status = db.StatusDemandaIntegrada
	dem.Erro = ""
	s.limparPosIntegracao(r, proj, dem)
	dem.WorktreePath = ""
	atual, err := s.banco.AtualizarDemanda(r.Context(), dem)
	if err != nil {
		s.log.Warn("reconciliar MR: atualizar demanda", "erro", err, "demanda", dem.ID)
		return dem, false
	}
	s.registrarEventoDemanda(r, atual, "demanda_integrada", "Praxis: demanda integrada",
		"merge da branch "+dem.Branch+" detectado na "+base+"; demanda integrada.")
	return atual, true
}

// acaoAtualizarBranch traz a main (atualizada) para dentro da branch da demanda
// (Fase 4d), resolvendo divergências antes do MR/merge. Faz fetch se houver
// remote (usa origin/<base>), simula com PreviaMerge e, se limpo, faz o merge no
// worktree; em conflito, coloca a demanda em `conflito` e devolve 409.
func (s *Servidor) acaoAtualizarBranch(w http.ResponseWriter, r *http.Request, dem db.Demanda) {
	proj, err := s.banco.ObterProjeto(r.Context(), dem.ProjectID)
	if err != nil {
		s.responderErroDemanda(w, err)
		return
	}
	if strings.TrimSpace(dem.Branch) == "" || strings.TrimSpace(dem.WorktreePath) == "" {
		responderErro(w, http.StatusConflict, "sem_branch",
			"a demanda ainda não tem branch/worktree para atualizar")
		return
	}
	base := baseDoProjeto(proj)

	// Traz a main mais recente quando há remote; passa a usar origin/<base>.
	ref := base
	if gitops.TemRemote(proj.Pasta) {
		if err := s.git.Fetch(proj.Pasta, base); err != nil {
			s.log.Warn("atualizar_branch: fetch falhou (segue com a main local)", "erro", err, "demanda", dem.ID)
		} else {
			ref = "origin/" + base
		}
	}

	previa, err := s.git.PreviaMerge(proj.Pasta, dem.Branch, ref)
	if err != nil {
		responderErro(w, http.StatusBadGateway, "preview_falhou", "não foi possível simular a atualização: "+err.Error())
		return
	}
	if !previa.Limpo {
		atual := s.marcarConflito(r, dem, "conflito ao trazer a "+base+" para a branch", previa.Conflitos)
		responderJSONConflito(w, atual, previa.Conflitos)
		return
	}

	msg := "Atualiza " + dem.Branch + " com " + ref
	if err := s.git.MergeNaBranch(dem.WorktreePath, ref, msg); err != nil {
		responderErro(w, http.StatusBadGateway, "merge_falhou", "falha ao atualizar a branch: "+err.Error())
		return
	}
	// Sai do estado de conflito se estava nele; volta a `concluida` se já tinha
	// terminado as fases, senão preserva o status atual não-conflito.
	if dem.Status == db.StatusDemandaConflito {
		dem.Status = db.StatusDemandaConcluida
	}
	dem.Erro = ""
	atual, err := s.banco.AtualizarDemanda(r.Context(), dem)
	if err != nil {
		s.responderErroDemanda(w, err)
		return
	}
	s.registrarEventoDemanda(r, atual, "branch_atualizada", "Praxis: branch atualizada",
		"branch "+dem.Branch+" atualizada com "+ref+".")
	s.responderDemandaComFases(w, r, atual)
}

// responderJSONConflito devolve 409 com a demanda (agora em conflito) e a lista
// de arquivos afetados, para o card exibir o conflito e oferecer a resolução.
func responderJSONConflito(w http.ResponseWriter, dem db.Demanda, arquivos []string) {
	responderJSON(w, http.StatusConflict, map[string]any{
		"erro": map[string]any{
			"codigo":   "conflito",
			"mensagem": "conflito com a main em " + strconv.Itoa(len(arquivos)) + " arquivo(s)",
			"arquivos": arquivos,
			"demanda":  dem,
		},
	})
}

// montarURLMR monta o link para abrir o merge/pull request na plataforma, a
// partir de url_plataforma do projeto e da branch da demanda. Detecta GitHub
// (compare) e assume padrão GitLab (novo MR) nos demais casos. Sem url_plataforma
// devolve "".
func montarURLMR(proj db.Projeto, branch, base string) string {
	raiz := strings.TrimRight(strings.TrimSpace(proj.URLPlataforma), "/")
	if raiz == "" {
		return ""
	}
	if strings.Contains(raiz, "github.") {
		return raiz + "/compare/" + url.PathEscape(base) + "..." + url.PathEscape(branch) + "?expand=1"
	}
	// GitLab (e compatíveis): abrir novo merge request com a branch de origem.
	return raiz + "/-/merge_requests/new?merge_request%5Bsource_branch%5D=" + url.QueryEscape(branch)
}
