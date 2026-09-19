package api

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/marcos14/praxis-autonomous/internal/db"
)

// Chaves da config global que regem a validade das credenciais (M1 do
// PLANO_INTERNET). São relidas a cada emissão/renovação de token — ajustar na
// tela de Configurações vale sem reiniciar o serviço.
const (
	// ChaveSessaoJWTMin é a validade do JWT de acesso, em minutos. Curta de
	// propósito: o JWT vive só na memória do navegador e é renovado pela sessão.
	ChaveSessaoJWTMin = "sessao_jwt_min"
	// ChaveSessaoInatividadeDias é em quantos dias sem uso a sessão cai. O prazo
	// desliza a cada renovação — quem usa o Praxis todo dia nunca é deslogado.
	ChaveSessaoInatividadeDias = "sessao_inatividade_dias"
	// ChaveSessaoMaximaDias é o teto absoluto da sessão desde o login, em dias;
	// vencido, exige novo login mesmo em uso contínuo.
	ChaveSessaoMaximaDias = "sessao_maxima_dias"
)

// Defaults das chaves acima: JWT de uma hora, um mês de inatividade, teto de
// três meses.
const (
	padraoJWTMin          = 60
	padraoInatividadeDias = 30
	padraoMaximaDias      = 90
)

// prazosAuth são as validades efetivas: do JWT de acesso e da sessão persistida.
type prazosAuth struct {
	JWT    time.Duration
	Sessao db.PrazosSessao
}

// prazosPadrao devolve os prazos default (sem config).
func prazosPadrao() prazosAuth {
	return prazosAuth{
		JWT: padraoJWTMin * time.Minute,
		Sessao: db.PrazosSessao{
			Inatividade: padraoInatividadeDias * 24 * time.Hour,
			Maxima:      padraoMaximaDias * 24 * time.Hour,
		},
	}
}

// prazosAuth lê a config global e resolve os prazos. Valor ausente, não
// numérico ou menor que 1 cai no default daquela chave; erro ao ler a config
// cai nos defaults inteiros (e é logado) — a autenticação nunca fica bloqueada
// por config. Um teto menor que a inatividade é respeitado pelo store (a
// expiração nunca passa do teto).
func (s *Servidor) prazosAuth(ctx context.Context) prazosAuth {
	p := prazosPadrao()
	if s.banco == nil {
		return p
	}
	entradas, err := s.banco.ObterConfigGlobal(ctx)
	if err != nil {
		s.log.Warn("ler prazos de sessão da config global", "erro", err)
		return p
	}
	p.JWT = time.Duration(configInteiro(entradas, ChaveSessaoJWTMin, padraoJWTMin)) * time.Minute
	p.Sessao.Inatividade = time.Duration(configInteiro(entradas, ChaveSessaoInatividadeDias, padraoInatividadeDias)) * 24 * time.Hour
	p.Sessao.Maxima = time.Duration(configInteiro(entradas, ChaveSessaoMaximaDias, padraoMaximaDias)) * 24 * time.Hour
	return p
}

// configInteiro lê um inteiro ≥ 1 de uma entrada da config global. Aceita
// número JSON (como a tela de Configurações grava) e, por tolerância, string
// numérica. Ausente, ilegível ou abaixo de 1 devolve o default; fração é
// truncada.
func configInteiro(entradas map[string]json.RawMessage, chave string, padrao int) int {
	bruto, ok := entradas[chave]
	if !ok {
		return padrao
	}
	var v float64
	if err := json.Unmarshal(bruto, &v); err != nil {
		var txt string
		if err := json.Unmarshal(bruto, &txt); err != nil {
			return padrao
		}
		f, err := strconv.ParseFloat(strings.TrimSpace(txt), 64)
		if err != nil {
			return padrao
		}
		v = f
	}
	if v < 1 {
		return padrao
	}
	return int(v)
}
