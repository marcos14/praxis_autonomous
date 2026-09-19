// Package auth implementa a assinatura e validação de JWTs (HS256) usada para
// autenticar usuários na API. É deliberadamente minimalista e sem dependências
// externas: o token carrega apenas o sujeito (id do usuário) e os tempos de
// emissão/expiração — as permissões são resolvidas do banco a cada requisição,
// de modo que mudança de papel ou desativação do usuário tenha efeito imediato.
package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"
)

// ErrTokenInvalido é devolvido por Validar quando o token está malformado, tem
// assinatura inválida, algoritmo inesperado ou já expirou. É um erro único e
// genérico de propósito: o chamador não deve distinguir os motivos para o
// cliente (evita vazar detalhes para quem tenta forjar tokens).
var ErrTokenInvalido = errors.New("token inválido ou expirado")

// alg é o único algoritmo aceito. Fixá-lo (e recusar qualquer outro, inclusive
// "none") evita o clássico ataque de confusão de algoritmo em JWT.
const alg = "HS256"

// cabecalho é o header JWT fixo ({"alg":"HS256","typ":"JWT"}), pré-computado.
type cabecalho struct {
	Alg string `json:"alg"`
	Typ string `json:"typ"`
}

// claims é o payload do token. Sub é o id do usuário; Iat/Exp são timestamps
// Unix (segundos). Mantido pequeno de propósito — nada sensível trafega aqui.
type claims struct {
	Sub int64 `json:"sub"`
	Iat int64 `json:"iat"`
	Exp int64 `json:"exp"`
}

// Claims é o conteúdo público de um token emitido/validado: o usuário (Sub) e
// o instante em que ele vence (Exp, precisão de segundos — a do próprio token).
// A API carimba Exp no principal para os streams SSE encerrarem quando a
// credencial expira e para informar ao cliente quando renovar.
type Claims struct {
	Sub int64
	Exp time.Time
}

// Assinar gera um JWT HS256 para o usuário sub, válido por ttl a partir de agora,
// assinado com secret. Devolve o token compacto (header.payload.assinatura).
func Assinar(sub int64, ttl time.Duration, secret []byte) (string, error) {
	tok, _, err := AssinarClaims(sub, ttl, secret)
	return tok, err
}

// AssinarClaims é Assinar devolvendo também os claims gravados no token (o exp
// exato, já truncado a segundos como o JWT o carrega).
func AssinarClaims(sub int64, ttl time.Duration, secret []byte) (string, Claims, error) {
	agora := time.Now()
	exp := agora.Add(ttl)
	tok, err := assinarEm(sub, agora, exp, secret)
	if err != nil {
		return "", Claims{}, err
	}
	return tok, Claims{Sub: sub, Exp: time.Unix(exp.Unix(), 0)}, nil
}

// assinarEm é o núcleo de Assinar com os instantes explícitos (facilita testar
// tokens já expirados sem mexer no relógio).
func assinarEm(sub int64, iat, exp time.Time, secret []byte) (string, error) {
	hJSON, err := json.Marshal(cabecalho{Alg: alg, Typ: "JWT"})
	if err != nil {
		return "", err
	}
	cJSON, err := json.Marshal(claims{Sub: sub, Iat: iat.Unix(), Exp: exp.Unix()})
	if err != nil {
		return "", err
	}
	corpo := codificar(hJSON) + "." + codificar(cJSON)
	return corpo + "." + codificar(assinatura(corpo, secret)), nil
}

// Validar verifica a assinatura e a expiração do token e devolve o sub (id do
// usuário). Qualquer problema resulta em ErrTokenInvalido.
func Validar(token string, secret []byte) (int64, error) {
	c, err := ValidarClaims(token, secret)
	return c.Sub, err
}

// ValidarClaims é Validar devolvendo também o instante de expiração do token.
func ValidarClaims(token string, secret []byte) (Claims, error) {
	return validarEm(token, secret, time.Now())
}

// validarEm é o núcleo de Validar com o instante "agora" explícito (para testes
// determinísticos de expiração).
func validarEm(token string, secret []byte, agora time.Time) (Claims, error) {
	partes := strings.Split(token, ".")
	if len(partes) != 3 {
		return Claims{}, ErrTokenInvalido
	}
	corpo := partes[0] + "." + partes[1]
	sigRecebida, err := decodificar(partes[2])
	if err != nil {
		return Claims{}, ErrTokenInvalido
	}
	// Comparação em tempo constante — não vaza informação de timing sobre a
	// assinatura correta.
	if !hmac.Equal(sigRecebida, assinatura(corpo, secret)) {
		return Claims{}, ErrTokenInvalido
	}
	hJSON, err := decodificar(partes[0])
	if err != nil {
		return Claims{}, ErrTokenInvalido
	}
	var h cabecalho
	if err := json.Unmarshal(hJSON, &h); err != nil || h.Alg != alg {
		return Claims{}, ErrTokenInvalido
	}
	cJSON, err := decodificar(partes[1])
	if err != nil {
		return Claims{}, ErrTokenInvalido
	}
	var c claims
	if err := json.Unmarshal(cJSON, &c); err != nil {
		return Claims{}, ErrTokenInvalido
	}
	if c.Sub <= 0 || c.Exp <= 0 || agora.Unix() >= c.Exp {
		return Claims{}, ErrTokenInvalido
	}
	return Claims{Sub: c.Sub, Exp: time.Unix(c.Exp, 0)}, nil
}

// assinatura computa o HMAC-SHA256 de corpo com secret.
func assinatura(corpo string, secret []byte) []byte {
	m := hmac.New(sha256.New, secret)
	m.Write([]byte(corpo))
	return m.Sum(nil)
}

// codificar/decodificar usam base64 url-safe SEM padding (RawURLEncoding), como
// manda o formato JWT compacto.
func codificar(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func decodificar(s string) ([]byte, error) { return base64.RawURLEncoding.DecodeString(s) }

// FormatarSub/ParsearSub convertem o id do usuário para/de string decimal — úteis
// quando o sub precisa transitar como texto (ex.: logs). Mantidos aqui para
// centralizar a convenção do claim.
func FormatarSub(sub int64) string { return strconv.FormatInt(sub, 10) }
