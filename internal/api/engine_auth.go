package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/marcos14/praxis-autonomous/internal/db"
	"github.com/marcos14/praxis-autonomous/internal/motor"
)

type reqCodigoLoginMotor struct {
	Codigo string `json:"codigo"`
}

func (s *Servidor) perfilMotorDaRota(w http.ResponseWriter, r *http.Request) (db.Motor, db.Conta, bool) {
	engineID, ok := lerID(w, r, "id")
	if !ok {
		return db.Motor{}, db.Conta{}, false
	}
	contaID, ok := lerID(w, r, "contaId")
	if !ok {
		return db.Motor{}, db.Conta{}, false
	}
	m, err := s.banco.ObterMotor(r.Context(), engineID)
	if err != nil {
		s.responderErroMotor(w, err)
		return db.Motor{}, db.Conta{}, false
	}
	if !motor.VendorComPerfilIsolado(m.Nome) {
		responderErro(w, http.StatusBadRequest, "motor_nao_suportado", "login assistido disponível apenas para Claude e Codex")
		return db.Motor{}, db.Conta{}, false
	}
	for _, c := range m.Contas {
		if c.ID == contaID {
			if strings.TrimSpace(c.ConfigDir) == "" {
				responderErro(w, http.StatusConflict, "perfil_sem_diretorio", "o perfil não possui diretório isolado")
				return db.Motor{}, db.Conta{}, false
			}
			return m, c, true
		}
	}
	responderErro(w, http.StatusNotFound, "nao_encontrado", "perfil não encontrado")
	return db.Motor{}, db.Conta{}, false
}

func (s *Servidor) handleEstadoAuthMotor(w http.ResponseWriter, r *http.Request) {
	m, c, ok := s.perfilMotorDaRota(w, r)
	if !ok {
		return
	}
	diag := motor.VerificarAutenticacao(r.Context(), m.Nome, c.ConfigDir)
	responderJSON(w, http.StatusOK, diag)
}

func (s *Servidor) handleIniciarLoginMotor(w http.ResponseWriter, r *http.Request) {
	m, c, ok := s.perfilMotorDaRota(w, r)
	if !ok {
		return
	}
	sessao, err := s.loginMotores.IniciarLogin(m.Nome, c.ConfigDir)
	if err != nil {
		switch {
		case errors.Is(err, motor.ErrLoginEmAndamento):
			responderErro(w, http.StatusConflict, "login_em_andamento", err.Error())
		default:
			s.log.Warn("iniciar login de motor", "motor", m.Nome, "conta", c.ID, "erro", err)
			responderErro(w, http.StatusBadRequest, "login_indisponivel", "não foi possível iniciar o login deste perfil")
		}
		return
	}
	responderJSON(w, http.StatusAccepted, sessao)
}

func (s *Servidor) handleObterLoginMotor(w http.ResponseWriter, r *http.Request) {
	sessao, err := s.loginMotores.ObterLogin(r.PathValue("sessionId"))
	if err != nil {
		responderErroLoginMotor(w, err)
		return
	}
	responderJSON(w, http.StatusOK, sessao)
}

func (s *Servidor) handleCancelarLoginMotor(w http.ResponseWriter, r *http.Request) {
	sessao, err := s.loginMotores.CancelarLogin(r.PathValue("sessionId"))
	if err != nil {
		responderErroLoginMotor(w, err)
		return
	}
	responderJSON(w, http.StatusOK, sessao)
}

func (s *Servidor) handleCodigoLoginMotor(w http.ResponseWriter, r *http.Request) {
	var req reqCodigoLoginMotor
	if !decodificarCorpo(w, r, &req) {
		return
	}
	sessao, err := s.loginMotores.EnviarCodigo(r.PathValue("sessionId"), req.Codigo)
	if err != nil {
		switch {
		case errors.Is(err, motor.ErrSessaoLoginNaoEncontrada):
			responderErro(w, http.StatusNotFound, "nao_encontrado", "sessão de login não encontrada")
		case errors.Is(err, motor.ErrCodigoLoginIndisponivel):
			responderErro(w, http.StatusConflict, "codigo_indisponivel", "a sessão não está aguardando um código do Claude")
		default:
			responderErro(w, http.StatusBadRequest, "codigo_invalido", err.Error())
		}
		return
	}
	responderJSON(w, http.StatusOK, sessao)
}

func responderErroLoginMotor(w http.ResponseWriter, err error) {
	if errors.Is(err, motor.ErrSessaoLoginNaoEncontrada) {
		responderErro(w, http.StatusNotFound, "nao_encontrado", "sessão de login não encontrada")
		return
	}
	responderErro(w, http.StatusInternalServerError, "erro_interno", "erro interno do servidor")
}
