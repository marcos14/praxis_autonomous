package api

import (
	"net/http"
	"net/url"
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
	ModoIntegracao       string              `json:"modo_integracao"`
	URLMR                string              `json:"url_mr"`
	CommitsNaoPublicados int                 `json:"commits_nao_publicados"`
	Commits              []gitops.CommitInfo `json:"commits"`
	Limpo                bool                `json:"limpo"`
	Conflitos            []string            `json:"conflitos"`
	Aviso                string              `json:"aviso,omitempty"`
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

	base := strings.TrimSpace(proj.BranchPrincipal)
	if base == "" {
		base = "main"
	}
	repo := proj.Pasta

	resp := respMergePreview{
		Branch:         dem.Branch,
		Base:           base,
		ModoIntegracao: proj.ModoIntegracao,
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
