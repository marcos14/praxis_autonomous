package api

import (
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

// registrarRotasProjetos registra as rotas de CRUD de projetos no mux.
func (s *Servidor) registrarRotasProjetos(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/projects", s.handleCriarProjeto)
	mux.HandleFunc("GET /api/v1/projects", s.handleListarProjetos)
	mux.HandleFunc("GET /api/v1/projects/{id}", s.handleObterProjeto)
	mux.HandleFunc("PUT /api/v1/projects/{id}", s.handleAtualizarProjeto)
}

// decodificarCorpo lê e valida o JSON do corpo, recusando campos desconhecidos e
// corpo vazio. Em erro, já escreve a resposta 400 e devolve false.
func decodificarCorpo(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		if errors.Is(err, io.EOF) {
			responderErro(w, http.StatusBadRequest, "invalido", "corpo JSON obrigatório")
			return false
		}
		responderErro(w, http.StatusBadRequest, "invalido", "corpo JSON inválido: "+err.Error())
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
		s.responderErroProjeto(w, err)
		return
	}
	responderJSON(w, http.StatusCreated, criado)
}

// handleListarProjetos devolve todos os projetos ordenados por nome.
func (s *Servidor) handleListarProjetos(w http.ResponseWriter, r *http.Request) {
	projetos, err := s.banco.ListarProjetos(r.Context())
	if err != nil {
		s.responderErroProjeto(w, err)
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
		s.responderErroProjeto(w, err)
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
		s.responderErroProjeto(w, err)
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
		s.responderErroProjeto(w, err)
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
	cmd := exec.Command("git", "-C", pasta, "rev-parse", "--is-inside-work-tree")
	if out, err := cmd.Output(); err != nil || strings.TrimSpace(string(out)) != "true" {
		return "pasta não é um repositório git: " + pasta
	}
	return ""
}

// lerIDProjeto extrai e valida o path param {id}. Em erro, escreve 400 e devolve
// false.
func lerIDProjeto(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		responderErro(w, http.StatusBadRequest, "invalido", "id inválido")
		return 0, false
	}
	return id, true
}

// responderErroProjeto traduz os erros do store para respostas HTTP.
func (s *Servidor) responderErroProjeto(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, db.ErrNaoEncontrado):
		responderErro(w, http.StatusNotFound, "nao_encontrado", "projeto não encontrado")
	case errors.Is(err, db.ErrSlugDuplicado):
		responderErro(w, http.StatusConflict, "slug_duplicado", "já existe um projeto com esse slug")
	default:
		s.log.Error("erro no store de projetos", "erro", err)
		responderErro(w, http.StatusInternalServerError, "erro_interno", "erro interno do servidor")
	}
}
