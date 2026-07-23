package api

import (
	"net/http"
	"time"

	"github.com/marcos14/praxis-autonomous/internal/db"
	"github.com/marcos14/praxis-autonomous/internal/motor"
	"github.com/marcos14/praxis-autonomous/internal/uso"
)

// respUsoJanelas agrupa o consumo do Praxis nas três janelas apresentadas.
type respUsoJanelas struct {
	Hoje         db.UsoJanela `json:"hoje"`
	Ultimos7Dias db.UsoJanela `json:"ultimos_7_dias"`
	Total        db.UsoJanela `json:"total"`
}

// respUsoPerfil é a linha de um perfil ativo no painel de uso.
type respUsoPerfil struct {
	ID        int64              `json:"id"`
	Alias     string             `json:"alias"`
	UsoPraxis respUsoJanelas     `json:"uso_praxis"`
	Franquia  *motor.UsoFranquia `json:"franquia,omitempty"`
}

// respUsoMotor é um motor ativo com seus perfis ativos no painel de uso.
type respUsoMotor struct {
	ID       int64           `json:"id"`
	Nome     string          `json:"nome"`
	Fallback bool            `json:"fallback"`
	Perfis   []respUsoPerfil `json:"perfis"`
	// SemPerfil agrega execuções do motor registradas sem perfil (contas
	// removidas ou runs anteriores à migração 11). Presente só quando há uso.
	SemPerfil *respUsoJanelas `json:"uso_sem_perfil,omitempty"`
}

type respUsoMotores struct {
	IntervaloMin int            `json:"intervalo_min"`
	Motores      []respUsoMotor `json:"motores"`
}

// handleUsoMotores devolve, para cada motor ATIVO e seus perfis ATIVOS, o
// consumo registrado pelo Praxis (hoje/7 dias/total) e o último snapshot de
// franquia do vendor lido pelo monitor (quando disponível). Leitura autenticada
// comum: percentuais e custos agregados, nunca tokens/credenciais.
func (s *Servidor) handleUsoMotores(w http.ResponseWriter, r *http.Request) {
	motores, err := s.banco.ListarMotores(r.Context())
	if err != nil {
		s.responderErroMotor(w, err)
		return
	}
	usos, err := s.banco.UsoPraxisPorConta(r.Context(), time.Now().UTC())
	if err != nil {
		s.log.Error("agregar uso do praxis", "erro", err)
		responderErro(w, http.StatusInternalServerError, "erro_interno", "erro interno do servidor")
		return
	}
	porChave := map[[2]string]db.UsoPraxis{}
	for _, u := range usos {
		porChave[[2]string{u.Engine, u.Conta}] = u
	}
	janelasDe := func(engine, conta string) (respUsoJanelas, bool) {
		u, ok := porChave[[2]string{engine, conta}]
		if !ok {
			return respUsoJanelas{}, false
		}
		return respUsoJanelas{Hoje: u.Hoje, Ultimos7Dias: u.Ultimos7Dias, Total: u.Total}, true
	}

	resp := respUsoMotores{IntervaloMin: uso.IntervaloPadraoMin, Motores: []respUsoMotor{}}
	if s.uso != nil {
		resp.IntervaloMin = s.uso.IntervaloMin(r.Context())
	}
	for _, m := range motores {
		if !m.Ativo {
			continue
		}
		rm := respUsoMotor{ID: m.ID, Nome: m.Nome, Fallback: m.Fallback, Perfis: []respUsoPerfil{}}
		for _, c := range m.Contas {
			if !c.Ativo {
				continue
			}
			p := respUsoPerfil{ID: c.ID, Alias: c.Alias}
			p.UsoPraxis, _ = janelasDe(m.Nome, c.Alias)
			if s.uso != nil {
				if fr, ok := s.uso.Snapshot(c.ID); ok {
					p.Franquia = &fr
				}
			}
			rm.Perfis = append(rm.Perfis, p)
		}
		if semPerfil, ok := janelasDe(m.Nome, ""); ok && semPerfil.Total.Execucoes > 0 {
			rm.SemPerfil = &semPerfil
		}
		resp.Motores = append(resp.Motores, rm)
	}
	responderJSON(w, http.StatusOK, resp)
}
