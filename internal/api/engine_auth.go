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
		s.responderErroMotor(w, r, err)
		return db.Motor{}, db.Conta{}, false
	}
	if !motor.VendorComPerfilIsolado(m.Nome) {
		erroT(w, r, http.StatusBadRequest, "motor_nao_suportado", "erro.motor_login_nao_suportado")
		return db.Motor{}, db.Conta{}, false
	}
	for _, c := range m.Contas {
		if c.ID == contaID {
			if strings.TrimSpace(c.ConfigDir) == "" {
				erroT(w, r, http.StatusConflict, "perfil_sem_diretorio", "erro.motor_perfil_sem_diretorio")
				return db.Motor{}, db.Conta{}, false
			}
			return m, c, true
		}
	}
	erroT(w, r, http.StatusNotFound, "nao_encontrado", "erro.motor_perfil_nao_encontrado")
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
			erroT(w, r, http.StatusBadRequest, "login_indisponivel", "erro.motor_login_indisponivel")
		}
		return
	}
	responderJSON(w, http.StatusAccepted, sessao)
}

func (s *Servidor) handleObterLoginMotor(w http.ResponseWriter, r *http.Request) {
	sessao, err := s.loginMotores.ObterLogin(r.PathValue("sessionId"))
	if err != nil {
		responderErroLoginMotor(w, r, err)
		return
	}
	responderJSON(w, http.StatusOK, sessao)
}

func (s *Servidor) handleCancelarLoginMotor(w http.ResponseWriter, r *http.Request) {
	sessao, err := s.loginMotores.CancelarLogin(r.PathValue("sessionId"))
	if err != nil {
		responderErroLoginMotor(w, r, err)
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
			erroT(w, r, http.StatusNotFound, "nao_encontrado", "erro.motor_sessao_login_nao_encontrada")
		case errors.Is(err, motor.ErrCodigoLoginIndisponivel):
			erroT(w, r, http.StatusConflict, "codigo_indisponivel", "erro.motor_codigo_indisponivel")
		default:
			responderErro(w, http.StatusBadRequest, "codigo_invalido", err.Error())
		}
		return
	}
	responderJSON(w, http.StatusOK, sessao)
}

func responderErroLoginMotor(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, motor.ErrSessaoLoginNaoEncontrada) {
		erroT(w, r, http.StatusNotFound, "nao_encontrado", "erro.motor_sessao_login_nao_encontrada")
		return
	}
	erroT(w, r, http.StatusInternalServerError, "erro_interno", "erro.interno")
}
