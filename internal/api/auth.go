package api

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/marcos14/praxis-autonomous/internal/db"
)

// cabecalhoToken é o header alternativo (além de Authorization: Bearer) onde o
// token de API pode ser enviado. Portado do X-Praxis-Token do Praxis atual.
const cabecalhoToken = "X-Praxis-Token"

// chaveCtxPapel é a chave do papel resolvido no contexto da requisição.
type chaveCtx string

const chaveCtxPapel chaveCtx = "papel"

// rankPapel dá a ordem de privilégio de um papel (maior = mais poder). Papel
// desconhecido = 0 (sem acesso).
func rankPapel(p string) int {
	switch p {
	case db.PapelLeitor:
		return 1
	case db.PapelOperador:
		return 2
	case db.PapelAdmin:
		return 3
	}
	return 0
}

// tokenDaRequisicao extrai o token de "Authorization: Bearer <t>" ou do header
// X-Praxis-Token. Portado do auth.go do Praxis atual.
func tokenDaRequisicao(r *http.Request) string {
	if a := r.Header.Get("Authorization"); strings.HasPrefix(a, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(a, "Bearer "))
	}
	return strings.TrimSpace(r.Header.Get(cabecalhoToken))
}

// papelRequeridoPara devolve o papel mínimo exigido por uma rota. Retorna "" para
// rotas sem exigência (assets web, health). Regras:
//   - fora de /api/v1/ → sem auth;
//   - /api/v1/tokens*  → admin (gestão de tokens);
//   - GET/HEAD         → leitor (leitura);
//   - demais métodos   → operador (escrita/ação).
func papelRequeridoPara(metodo, caminho string) string {
	if !strings.HasPrefix(caminho, "/api/v1/") {
		return ""
	}
	if strings.HasPrefix(caminho, "/api/v1/tokens") {
		return db.PapelAdmin
	}
	if metodo == http.MethodGet || metodo == http.MethodHead {
		return db.PapelLeitor
	}
	return db.PapelOperador
}

// comAuth é o middleware de autorização por token (Fase 5a). Modelo de confiança:
//   - SEM token → tratado como acesso LOCAL confiável (papel admin). O bind
//     padrão é loopback; o acesso externo se dá por túnel/reverse-proxy. Os
//     tokens são ADITIVOS: servem a chamadores programáticos (sistema de
//     chamados) que se autenticam com um papel específico.
//   - COM token válido → o papel do token. Um token `leitor` fica BARRADO de
//     escritas (403), mesmo vindo do loopback — é como um integrador read-only se
//     restringe explicitamente.
//   - COM token inválido/revogado → 401.
func (s *Servidor) comAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requerido := papelRequeridoPara(r.Method, r.URL.Path)
		if requerido == "" {
			next.ServeHTTP(w, r)
			return
		}
		papel := db.PapelAdmin // sem token = acesso local confiável
		if tok := tokenDaRequisicao(r); tok != "" {
			t, err := s.banco.AutenticarToken(r.Context(), tok)
			if err != nil {
				if errors.Is(err, db.ErrNaoEncontrado) {
					responderErro(w, http.StatusUnauthorized, "token_invalido",
						"token de API inválido ou revogado")
					return
				}
				s.log.Error("autenticar token", "erro", err)
				responderErro(w, http.StatusInternalServerError, "erro_interno", "erro interno do servidor")
				return
			}
			papel = t.Papel
		}
		if rankPapel(papel) < rankPapel(requerido) {
			responderErro(w, http.StatusForbidden, "sem_permissao",
				"o papel '"+papel+"' não tem permissão; esta operação requer '"+requerido+"'")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), chaveCtxPapel, papel)))
	})
}

// papelDaRequisicao devolve o papel resolvido pelo middleware (ou admin quando
// ausente, coerente com o acesso local).
func papelDaRequisicao(r *http.Request) string {
	if p, ok := r.Context().Value(chaveCtxPapel).(string); ok && p != "" {
		return p
	}
	return db.PapelAdmin
}
