package api

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/marcos14/praxis-autonomous/internal/auth"
	"github.com/marcos14/praxis-autonomous/internal/db"
)

// cabecalhoToken é o header alternativo (além de Authorization: Bearer) onde um
// token de API pode ser enviado, usado por integrações. Portado do X-Praxis-Token.
const cabecalhoToken = "X-Praxis-Token"

// chaveCtxPrincipal é a chave, no contexto da requisição, do principal (usuário
// ou token) resolvido pelo middleware de autorização.
type chaveCtx string

const chaveCtxPrincipal chaveCtx = "principal"

// errNaoAutorizado marca uma falha de autenticação (credencial ausente, inválida
// ou de usuário inativo/removido). O middleware mapeia para HTTP 401. É único de
// propósito: o cliente nunca sabe qual dos motivos ocorreu.
var errNaoAutorizado = errors.New("não autorizado")

// principal é o resultado da autenticação de uma requisição: o usuário (ou token
// de API) e o conjunto de permissões efetivas. Um principal com PermCuringa (`*`)
// é administrador pleno. viaToken distingue integrações (token de API) de usuários.
type principal struct {
	userID     int64
	nome       string
	email      string
	permissoes map[string]bool
	viaToken   bool
}

// tem informa se o principal possui a permissão perm — diretamente ou via curinga
// (`*`). Um principal nil nunca tem permissão.
func (p *principal) tem(perm string) bool {
	if p == nil || p.permissoes == nil {
		return false
	}
	return p.permissoes[perm] || p.permissoes[db.PermCuringa]
}

// principalBootstrap é o principal usado no modo de inicialização (nenhum usuário
// cadastrado ainda, ou servidor sem banco em testes de handler isolados): acesso
// local pleno, para permitir criar o primeiro admin e manter a UI/os testes
// operando. Assim que existir ≥1 usuário, este modo deixa de valer.
func principalBootstrap() *principal {
	return &principal{permissoes: map[string]bool{db.PermCuringa: true}}
}

// comAuth é o middleware de autorização por permissões (RBAC). Fluxo:
//   - rota pública (assets, health, /auth/status|login|setup) → passa;
//   - resolve o principal a partir da credencial (JWT de usuário via Bearer, ou
//     token de API via X-Praxis-Token / Bearer sem formato de JWT); sem credencial
//     e sem usuários cadastrados → modo bootstrap (admin local);
//   - sem principal → 401; com principal mas sem a permissão exigida pela rota → 403.
//
// Rotas de mutação em /demands/{id}/actions exigem apenas autenticação aqui; a
// permissão fina (operar vs. integrar) é checada no handler, pois varia com a ação.
func (s *Servidor) comAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		publica, permReq := requisitoRota(r.Method, r.URL.Path)
		if publica {
			next.ServeHTTP(w, r)
			return
		}
		pr, err := s.resolverPrincipal(r)
		if err != nil {
			if errors.Is(err, errNaoAutorizado) {
				responderErro(w, http.StatusUnauthorized, "nao_autenticado",
					"autenticação necessária: faça login")
				return
			}
			// Cliente desistiu da requisição (navegou/fechou): a resolução do
			// principal foi cancelada. Não é erro do servidor — não loga nem
			// responde (a conexão já foi embora).
			if errors.Is(err, context.Canceled) {
				return
			}
			s.log.Error("resolver principal", "erro", err)
			responderErro(w, http.StatusInternalServerError, "erro_interno", "erro interno do servidor")
			return
		}
		if permReq != "" && !pr.tem(permReq) {
			responderErro(w, http.StatusForbidden, "sem_permissao",
				"você não tem permissão para esta operação (requer '"+permReq+"')")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), chaveCtxPrincipal, pr)))
	})
}

// resolverPrincipal identifica quem faz a requisição. Devolve errNaoAutorizado
// para 401 (credencial inválida ou ausente com usuários já cadastrados) e um erro
// comum para falhas internas (banco).
func (s *Servidor) resolverPrincipal(r *http.Request) (*principal, error) {
	// Sem banco: testes de handler isolados — mantém o acesso local pleno.
	if s.banco == nil {
		return principalBootstrap(), nil
	}
	ctx := r.Context()
	if cred := credencialToken(r); cred != "" {
		if pareceJWT(cred) {
			return s.principalDeJWT(ctx, cred)
		}
		return s.principalDeToken(ctx, cred)
	}
	// Sem credencial: modo bootstrap só enquanto não houver usuários.
	n, err := s.banco.ContarUsuarios(ctx)
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return principalBootstrap(), nil
	}
	return nil, errNaoAutorizado
}

// principalDeJWT valida um JWT de usuário e monta o principal com as permissões
// resolvidas do banco AGORA (mudança de papel/desativação tem efeito imediato).
func (s *Servidor) principalDeJWT(ctx context.Context, token string) (*principal, error) {
	secret, err := s.segredoJWT(ctx)
	if err != nil {
		return nil, err
	}
	sub, err := auth.Validar(token, secret)
	if err != nil {
		return nil, errNaoAutorizado
	}
	u, err := s.banco.ObterUsuario(ctx, sub)
	if err != nil {
		if errors.Is(err, db.ErrNaoEncontrado) {
			return nil, errNaoAutorizado // usuário do token não existe mais
		}
		return nil, err
	}
	if !u.Ativo {
		return nil, errNaoAutorizado
	}
	perms, err := s.banco.PermissoesDoUsuario(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	return &principal{userID: u.ID, nome: u.Nome, email: u.Email, permissoes: perms}, nil
}

// principalDeToken resolve um token de API para seu principal, mapeando o papel
// (leitor/operador/admin) para o conjunto de permissões correspondente.
func (s *Servidor) principalDeToken(ctx context.Context, token string) (*principal, error) {
	t, err := s.banco.AutenticarToken(ctx, token)
	if err != nil {
		if errors.Is(err, db.ErrNaoEncontrado) {
			return nil, errNaoAutorizado
		}
		return nil, err
	}
	return &principal{permissoes: permissoesDoPapelToken(t.Papel), viaToken: true}, nil
}

// permissoesDoPapelToken mapeia o papel de um token de API para permissões RBAC,
// preservando a semântica dos tokens existentes (Fase 5a):
//   - leitor   → só visualização (nenhuma permissão de escrita);
//   - operador → operar demandas e integrar (ciclo do sistema de chamados);
//   - admin    → tudo (curinga).
func permissoesDoPapelToken(papel string) map[string]bool {
	switch papel {
	case db.PapelAdmin:
		return map[string]bool{db.PermCuringa: true}
	case db.PapelOperador:
		return map[string]bool{
			db.PermDemandasCriar:     true,
			db.PermDemandasResponder: true,
			db.PermDemandasOperar:    true,
			db.PermIntegracaoGerir:   true,
		}
	default: // leitor e desconhecidos: só leitura
		return map[string]bool{}
	}
}

// credencialToken extrai a credencial da requisição, na ordem de precedência:
//  1. Authorization: Bearer <t> (JWT de usuário ou token de API);
//  2. X-Praxis-Token: <t> (token de API de integrações);
//  3. query ?token=<t> — necessária para SSE via EventSource, que NÃO permite
//     definir headers. Só as rotas de stream (/events, /logs) usam essa forma; o
//     token não vaza nos logs porque o comLog registra apenas o Path (sem query).
func credencialToken(r *http.Request) string {
	if b := bearer(r); b != "" {
		return b
	}
	if xt := strings.TrimSpace(r.Header.Get(cabecalhoToken)); xt != "" {
		return xt
	}
	return strings.TrimSpace(r.URL.Query().Get("token"))
}

// bearer extrai o valor de "Authorization: Bearer <v>" (string vazia se ausente).
func bearer(r *http.Request) string {
	a := r.Header.Get("Authorization")
	if strings.HasPrefix(a, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(a, "Bearer "))
	}
	return ""
}

// pareceJWT informa se v tem a forma compacta de um JWT (header.payload.assinatura
// = exatamente dois pontos). Tokens de API (base64url sem pontos) não batem, então
// o Bearer é roteado corretamente entre usuário (JWT) e integração (token de API).
func pareceJWT(v string) bool { return strings.Count(v, ".") == 2 }

// requisitoRota devolve (pública, permissão) para um método+caminho:
//   - pública=true → sem autenticação;
//   - permissão=="" → basta estar autenticado (visualização, o "básico");
//   - permissão!="" → exige aquela permissão.
func requisitoRota(metodo, caminho string) (publica bool, permissao string) {
	if !strings.HasPrefix(caminho, "/api/v1/") {
		return true, "" // assets da web, /healthz e afins
	}
	switch caminho {
	case "/api/v1/auth/status", "/api/v1/auth/login", "/api/v1/auth/setup":
		return true, "" // autenticação: públicas (checar status, logar, criar 1º admin)
	}

	resto := strings.TrimPrefix(caminho, "/api/v1/")
	seg := strings.Split(resto, "/")

	// Gestão de acessos (usuários, papéis, tokens, catálogo de permissões):
	// leitura e escrita exigem usuarios.gerir.
	switch seg[0] {
	case "users", "roles", "tokens", "permissions":
		return false, db.PermUsuariosGerir
	}

	// Leituras (GET/HEAD) das demais rotas: basta autenticação (visualização).
	if metodo == http.MethodGet || metodo == http.MethodHead {
		return false, ""
	}

	// Mutações: permissão específica por recurso.
	return false, permissaoMutacao(seg, resto)
}

// permissaoMutacao resolve a permissão exigida por uma mutação (não-GET) fora das
// rotas de gestão de acessos. Rotas desconhecidas exigem admin (curinga) por
// segurança — uma rota nova fica trancada até ser mapeada aqui explicitamente.
func permissaoMutacao(seg []string, resto string) string {
	switch seg[0] {
	case "projects":
		// projects, projects/{id}, projects/{id}/config → gerir projetos;
		// projects/{id}/demands → criar demanda.
		if len(seg) >= 3 && seg[2] == "demands" {
			return db.PermDemandasCriar
		}
		return db.PermProjetosGerir
	case "demands":
		if len(seg) == 2 && seg[1] == "ordem" {
			return db.PermDemandasOperar
		}
		if len(seg) >= 3 {
			switch seg[2] {
			case "chat", "answers", "phases", "approve-plan":
				return db.PermDemandasResponder
			case "actions":
				// A permissão varia com a ação (operar vs. integrar): o handler
				// decide. Aqui basta estar autenticado.
				return ""
			}
		}
		return db.PermCuringa // mutação inesperada em demands: só admin
	case "config":
		return db.PermConfigGerir
	case "engines":
		return db.PermConfigGerir
	}
	return db.PermCuringa
}

// principalDaRequisicao devolve o principal resolvido pelo middleware. Nunca é
// nil em handlers protegidos; devolve um principal bootstrap por segurança caso
// o handler seja chamado fora do middleware (não deveria acontecer).
func principalDaRequisicao(r *http.Request) *principal {
	if p, ok := r.Context().Value(chaveCtxPrincipal).(*principal); ok && p != nil {
		return p
	}
	return principalBootstrap()
}

// temPermissao é o atalho de checagem de permissão dentro de um handler.
func temPermissao(r *http.Request, perm string) bool {
	return principalDaRequisicao(r).tem(perm)
}

// exigirPermissao responde 403 e devolve false quando o principal não tem perm;
// devolve true (sem escrever nada) quando tem. Usado por handlers cuja permissão
// varia com o corpo (ex.: /actions).
func exigirPermissao(w http.ResponseWriter, r *http.Request, perm string) bool {
	if temPermissao(r, perm) {
		return true
	}
	responderErro(w, http.StatusForbidden, "sem_permissao",
		"você não tem permissão para esta operação (requer '"+perm+"')")
	return false
}
