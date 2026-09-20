package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/marcos14/praxis-autonomous/internal/db"
)

// modosIntegracao são os valores aceitos para projects.modo_integracao (o banco
// também tem um CHECK; validamos antes para devolver um erro amigável).
var modosIntegracao = map[string]bool{"merge_request": true, "merge_local": true}

// modoIntegracaoPadrao é o modo assumido quando o campo vem em branco.
const modoIntegracaoPadrao = "merge_request"

// branchPrincipalPadrao é a branch principal assumida quando não informada.
const branchPrincipalPadrao = "main"

// reqProjeto é o corpo aceito em POST/PUT de projetos. Ativo é ponteiro para
// distinguir "não informado" (assume ativo) de "false" explícito no update.
type reqProjeto struct {
	Nome            string   `json:"nome"`
	Slug            string   `json:"slug"`
	Pasta           string   `json:"pasta"`
	BranchPrincipal string   `json:"branch_principal"`
	ModoIntegracao  string   `json:"modo_integracao"`
	URLPlataforma   string   `json:"url_plataforma"`
	AddDirs         []string `json:"add_dirs"`
	Ativo           *bool    `json:"ativo"`
}

// registrarRotasProjetos registra as rotas de CRUD de projetos no mux, mais as
// rotas do overview do repositório (feature de consultas — mutações caem em
// projetos.gerir pelo case "projects" do permissaoMutacao).
func (s *Servidor) registrarRotasProjetos(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/projects", s.handleCriarProjeto)
	mux.HandleFunc("GET /api/v1/projects", s.handleListarProjetos)
	mux.HandleFunc("GET /api/v1/projects/{id}", s.handleObterProjeto)
	mux.HandleFunc("PUT /api/v1/projects/{id}", s.handleAtualizarProjeto)
	mux.HandleFunc("PUT /api/v1/projects/{id}/overview", s.handleSalvarOverview)
	mux.HandleFunc("POST /api/v1/projects/{id}/overview/gerar", s.handleGerarOverview)
	// ACL de visibilidade do projeto (quem enxerga o projeto — usuários/grupos).
	// Leitura e escrita exigem projetos.gerir (ver requisitoRota).
	mux.HandleFunc("GET /api/v1/projects/{id}/access", s.handleObterAcesso)
	mux.HandleFunc("PUT /api/v1/projects/{id}/access", s.handleDefinirAcesso)
}

// reqAcesso é o corpo de PUT /projects/{id}/access: as listas COMPLETAS de ids
// liberados (substituição, não incremento). Ambas vazias = projeto aberto a
// todos os usuários autenticados.
type reqAcesso struct {
	Usuarios []int64 `json:"usuarios"`
	Grupos   []int64 `json:"grupos"`
}

// respAcesso devolve a ACL atual mais as opções disponíveis (usuários ativos e
// grupos de usuários) para a UI montar os seletores — sem exigir usuarios.gerir
// de quem só gerencia projetos.
type respAcesso struct {
	db.AcessoProjeto
	Disponiveis struct {
		Usuarios []db.RefAcesso `json:"usuarios"`
		Grupos   []db.RefAcesso `json:"grupos"`
	} `json:"disponiveis"`
}

// handleObterAcesso devolve a ACL do projeto e as opções para os seletores.
func (s *Servidor) handleObterAcesso(w http.ResponseWriter, r *http.Request) {
	id, ok := lerIDProjeto(w, r)
	if !ok {
		return
	}
	resp, err := s.montarRespAcesso(r, id)
	if err != nil {
		s.responderErroProjeto(w, r, err)
		return
	}
	responderJSON(w, http.StatusOK, resp)
}

// handleDefinirAcesso substitui a ACL do projeto pelas listas do corpo e devolve
// a ACL resultante (mesmo formato do GET).
func (s *Servidor) handleDefinirAcesso(w http.ResponseWriter, r *http.Request) {
	id, ok := lerIDProjeto(w, r)
	if !ok {
		return
	}
	var req reqAcesso
	if !decodificarCorpo(w, r, &req) {
		return
	}
	if err := s.banco.DefinirAcessoProjeto(r.Context(), id, req.Usuarios, req.Grupos); err != nil {
		if errors.Is(err, db.ErrNaoEncontrado) {
			erroT(w, r, http.StatusNotFound, "nao_encontrado", "erro.projeto_usuario_ou_grupo_nao_encontrado")
			return
		}
		s.responderErroProjeto(w, r, err)
		return
	}
	resp, err := s.montarRespAcesso(r, id)
	if err != nil {
		s.responderErroProjeto(w, r, err)
		return
	}
	responderJSON(w, http.StatusOK, resp)
}

// montarRespAcesso monta a resposta dos endpoints de ACL: a ACL persistida do
// projeto mais as opções de usuários/grupos para os seletores da UI.
func (s *Servidor) montarRespAcesso(r *http.Request, projectID int64) (respAcesso, error) {
	var resp respAcesso
	acesso, err := s.banco.ObterAcessoProjeto(r.Context(), projectID)
	if err != nil {
		return resp, err
	}
	resp.AcessoProjeto = acesso
	if resp.Disponiveis.Usuarios, err = s.banco.ListarRefsUsuarios(r.Context()); err != nil {
		return resp, err
	}
	if resp.Disponiveis.Grupos, err = s.banco.ListarRefsGruposUsuarios(r.Context()); err != nil {
		return resp, err
	}
	return resp, nil
}

// reqOverview é o corpo de PUT /projects/{id}/overview (edição manual).
type reqOverview struct {
	OverviewMD string `json:"overview_md"`
}

// handleSalvarOverview grava o overview editado manualmente.
func (s *Servidor) handleSalvarOverview(w http.ResponseWriter, r *http.Request) {
	id, ok := lerIDProjeto(w, r)
	if !ok {
		return
	}
	var req reqOverview
	if !decodificarCorpo(w, r, &req) {
		return
	}
	if err := s.banco.AtualizarOverview(r.Context(), id, strings.TrimSpace(req.OverviewMD)); err != nil {
		s.responderErroProjeto(w, r, err)
		return
	}
	p, err := s.banco.ObterProjeto(r.Context(), id)
	if err != nil {
		s.responderErroProjeto(w, r, err)
		return
	}
	responderJSON(w, http.StatusOK, p)
}

// handleGerarOverview dispara a geração do overview pelo harness (read-only) em
// background e devolve 202. O evento overview_gerado no SSE global avisa a UI.
func (s *Servidor) handleGerarOverview(w http.ResponseWriter, r *http.Request) {
	id, ok := lerIDProjeto(w, r)
	if !ok {
		return
	}
	if _, err := s.banco.ObterProjeto(r.Context(), id); err != nil {
		s.responderErroProjeto(w, r, err)
		return
	}
	if s.consultor == nil {
		erroT(w, r, http.StatusServiceUnavailable, "indisponivel", "erro.consulta_servico_inativo")
		return
	}
	s.consultor.DispararOverview(id)
	responderJSON(w, http.StatusAccepted, map[string]string{
		"status":  "gerando",
		"detalhe": "o overview está sendo gerado em background; acompanhe pelo evento overview_gerado",
	})
}

// decodificarCorpo lê e valida o JSON do corpo, recusando campos desconhecidos e
// corpo vazio. Em erro, já escreve a resposta 400 e devolve false.
func decodificarCorpo(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		if errors.Is(err, io.EOF) {
			erroT(w, r, http.StatusBadRequest, "invalido", "erro.corpo_obrigatorio")
			return false
		}
		erroT(w, r, http.StatusBadRequest, "invalido", "erro.corpo_invalido", "detalhe", err.Error())
		return false
	}
	return true
}

// handleCriarProjeto cria um projeto a partir do corpo, aplicando defaults e
// validações, e devolve 201 com o projeto persistido.
func (s *Servidor) handleCriarProjeto(w http.ResponseWriter, r *http.Request) {
	var req reqProjeto
	if !decodificarCorpo(w, r, &req) {
		return
	}
	p, msg := montarProjeto(req, db.Projeto{}, true)
	if msg != "" {
		responderErro(w, http.StatusBadRequest, "invalido", msg)
		return
	}
	criado, err := s.banco.CriarProjeto(r.Context(), p)
	if err != nil {
		s.responderErroProjeto(w, r, err)
		return
	}
	responderJSON(w, http.StatusCreated, criado)
}

// handleListarProjetos devolve os projetos ordenados por nome — todos para quem
// enxerga tudo (projetos.gerir/admin, tokens), só os visíveis pela ACL para os
// demais usuários.
func (s *Servidor) handleListarProjetos(w http.ResponseWriter, r *http.Request) {
	var (
		projetos []db.Projeto
		err      error
	)
	if uid := visibilidadeDaRequisicao(r); uid != nil {
		projetos, err = s.banco.ListarProjetosVisiveis(r.Context(), *uid)
	} else {
		projetos, err = s.banco.ListarProjetos(r.Context())
	}
	if err != nil {
		s.responderErroProjeto(w, r, err)
		return
	}
	responderJSON(w, http.StatusOK, projetos)
}

// handleObterProjeto devolve um projeto por id.
func (s *Servidor) handleObterProjeto(w http.ResponseWriter, r *http.Request) {
	id, ok := lerIDProjeto(w, r)
	if !ok {
		return
	}
	p, err := s.banco.ObterProjeto(r.Context(), id)
	if err != nil {
		s.responderErroProjeto(w, r, err)
		return
	}
	responderJSON(w, http.StatusOK, p)
}

// handleAtualizarProjeto atualiza os campos de um projeto existente. Parte do
// projeto atual para preservar campos não fornecidos (ativo).
func (s *Servidor) handleAtualizarProjeto(w http.ResponseWriter, r *http.Request) {
	id, ok := lerIDProjeto(w, r)
	if !ok {
		return
	}
	atual, err := s.banco.ObterProjeto(r.Context(), id)
	if err != nil {
		s.responderErroProjeto(w, r, err)
		return
	}
	var req reqProjeto
	if !decodificarCorpo(w, r, &req) {
		return
	}
	p, msg := montarProjeto(req, atual, false)
	if msg != "" {
		responderErro(w, http.StatusBadRequest, "invalido", msg)
		return
	}
	p.ID = id
	atualizado, err := s.banco.AtualizarProjeto(r.Context(), p)
	if err != nil {
		s.responderErroProjeto(w, r, err)
		return
	}
	responderJSON(w, http.StatusOK, atualizado)
}

// montarProjeto aplica defaults e validações sobre req, usando base como valores
// atuais (relevante no update: campos ausentes herdam de base). Devolve o Projeto
// pronto para persistir e, em falha de validação, uma mensagem não-vazia.
func montarProjeto(req reqProjeto, base db.Projeto, criando bool) (db.Projeto, string) {
	p := base

	p.Nome = strings.TrimSpace(req.Nome)
	if p.Nome == "" {
		return db.Projeto{}, "nome é obrigatório"
	}

	p.Pasta = strings.TrimSpace(req.Pasta)
	if p.Pasta == "" {
		return db.Projeto{}, "pasta é obrigatória"
	}
	if msg := validarPastaRepoGit(p.Pasta); msg != "" {
		return db.Projeto{}, msg
	}

	// Slug: usa o informado; se em branco, deriva do nome (na criação) ou mantém
	// o atual (no update).
	slug := gerarSlug(req.Slug)
	if slug == "" {
		if criando || base.Slug == "" {
			slug = gerarSlug(p.Nome)
		} else {
			slug = base.Slug
		}
	}
	if slug == "" {
		return db.Projeto{}, "slug inválido: informe um slug ou um nome com caracteres alfanuméricos"
	}
	p.Slug = slug

	// Campos opcionais com default: quando vêm em branco, preservam o valor atual
	// (relevante no update) e só caem no default quando não há valor prévio.
	p.BranchPrincipal = strings.TrimSpace(req.BranchPrincipal)
	if p.BranchPrincipal == "" {
		if base.BranchPrincipal != "" {
			p.BranchPrincipal = base.BranchPrincipal
		} else {
			p.BranchPrincipal = branchPrincipalPadrao
		}
	}

	modo := strings.TrimSpace(req.ModoIntegracao)
	if modo == "" {
		if base.ModoIntegracao != "" {
			modo = base.ModoIntegracao
		} else {
			modo = modoIntegracaoPadrao
		}
	}
	if !modosIntegracao[modo] {
		return db.Projeto{}, "modo_integracao deve ser 'merge_request' ou 'merge_local'"
	}
	p.ModoIntegracao = modo

	p.URLPlataforma = strings.TrimSpace(req.URLPlataforma)

	if req.AddDirs != nil {
		p.AddDirs = req.AddDirs
	}

	if req.Ativo != nil {
		p.Ativo = *req.Ativo
	} else if criando {
		p.Ativo = true
	}

	return p, ""
}

// gerarSlug normaliza um texto para um slug: minúsculo, ASCII alfanumérico com
// hífens, sem hífens nas pontas. Devolve "" se sobrar vazio.
func gerarSlug(texto string) string {
	texto = strings.ToLower(strings.TrimSpace(texto))
	var b strings.Builder
	ultimoHifen := false
	for _, r := range texto {
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
	}
	return strings.Trim(b.String(), "-")
}

// validarPastaRepoGit confirma que a pasta existe, é um diretório e é um
// repositório git. Devolve "" se válida ou uma mensagem de erro.
func validarPastaRepoGit(pasta string) string {
	info, err := os.Stat(pasta)
	if err != nil {
		if os.IsNotExist(err) {
			return "pasta não existe: " + pasta
		}
		return "não foi possível acessar a pasta: " + err.Error()
	}
	if !info.IsDir() {
		return "pasta não é um diretório: " + pasta
	}
	// git -C <pasta> rev-parse --is-inside-work-tree devolve "true" e código 0
	// dentro de um repositório; fora, sai com erro. Mesma detecção do Praxis atual.
	// O stderr do git vai junto na mensagem: "não é um repositório" também é o
	// que se vê quando o git recusa a pasta por pertencer a outro usuário
	// ("dubious ownership" — típico do serviço rodando como LocalSystem) ou
	// quando o git nem está no PATH do serviço; sem o motivo real, o operador
	// procura o problema no lugar errado.
	cmd := exec.Command("git", "-C", pasta, "rev-parse", "--is-inside-work-tree")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err == nil && strings.TrimSpace(string(out)) == "true" {
		return ""
	}
	msg := "pasta não é um repositório git: " + pasta
	if detalhe := strings.TrimSpace(stderr.String()); detalhe != "" {
		msg += " (git: " + detalhe + ")"
	} else if err != nil {
		msg += " (git: " + err.Error() + ")"
	}
	return msg
}

// lerIDProjeto extrai e valida o path param {id}. Em erro, escreve 400 e devolve
// false.
func lerIDProjeto(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		erroT(w, r, http.StatusBadRequest, "invalido", "erro.id_invalido")
		return 0, false
	}
	return id, true
}

// responderErroProjeto traduz os erros do store para respostas HTTP.
func (s *Servidor) responderErroProjeto(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, db.ErrNaoEncontrado):
		erroT(w, r, http.StatusNotFound, "nao_encontrado", "erro.projeto_nao_encontrado")
	case errors.Is(err, db.ErrSlugDuplicado):
		erroT(w, r, http.StatusConflict, "slug_duplicado", "erro.projeto_slug_duplicado")
	default:
		s.log.Error("erro no store de projetos", "erro", err)
		erroT(w, r, http.StatusInternalServerError, "erro_interno", "erro.interno")
	}
}
