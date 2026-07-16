package pipeline

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/marcos14/praxis-autonomous/internal/motor"
)

// EsperaResetFranquia e quanto tempo, a partir de agora, o scheduler deve
// aguardar antes de reagendar uma demanda cuja franquia de tokens esgotou (e sem
// motor de fallback disponivel). No Praxis atual esse valor era um time.Sleep
// (esperaResetFranquia, 15min) que BLOQUEAVA a rodada; aqui ele apenas define o
// horario devolvido em ErroFranquia.RetomarEm — quem espera e o scheduler, que
// libera o worker enquanto isso. E var (nao const) para os testes encurtarem.
var EsperaResetFranquia = 15 * time.Minute

// ErroFranquia sinaliza que a franquia de tokens esgotou e NAO ha motor de
// fallback disponivel. Diferente do Praxis atual (que dormia ate o reset), o
// pipeline nao bloqueia: devolve este erro com o horario de retomada, e o
// scheduler reagenda a demanda para RetomarEm. Substitui esperarResetFranquia.
type ErroFranquia struct {
	Motor     string    // motor que esgotou
	Detalhe   string    // mensagem do harness sobre o limite/reset
	RetomarEm time.Time // quando o scheduler deve tentar de novo
}

func (e *ErroFranquia) Error() string {
	return fmt.Sprintf("franquia de tokens esgotada em %s — retomar em %s: %s",
		e.Motor, e.RetomarEm.Format(time.RFC3339), e.Detalhe)
}

// EstadoFallback lembra quais motores ja esgotaram a franquia dentro de uma
// rodada, para nao voltar a eles antes de um reset. Portado de fallback.go.
type EstadoFallback struct {
	Esgotados map[string]bool
}

// NovoEstadoFallback cria um estado de fallback vazio.
func NovoEstadoFallback() *EstadoFallback {
	return &EstadoFallback{Esgotados: map[string]bool{}}
}

func (e *EstadoFallback) marcarEsgotado(m string) {
	if e == nil {
		return
	}
	if e.Esgotados == nil {
		e.Esgotados = map[string]bool{}
	}
	e.Esgotados[normalizarMotor(m)] = true
}

func (e *EstadoFallback) esgotado(m string) bool {
	if e == nil || e.Esgotados == nil {
		return false
	}
	return e.Esgotados[normalizarMotor(m)]
}

// rodarComFallback executa uma operacao no motor primario e, se ele sinalizar
// limite de sessao/uso, troca para o proximo motor disponivel na ordem
// configurada. Quando nao ha fallback possivel, NAO bloqueia esperando o reset
// (como fazia o Praxis atual): devolve *ErroFranquia com o horario de retomada
// para o scheduler reagendar. Portado de rodarComFallback (fallback.go),
// adaptado ao ContextoExec (config resolvida, sem releitura de arquivo).
func (c *ContextoExec) rodarComFallback(operacao, motorPrimario string, op motor.OpcoesRun, estado *EstadoFallback) (*motor.ResultadoRun, string, error) {
	if estado == nil {
		estado = NovoEstadoFallback()
	}
	motorAtual := normalizarMotor(motorPrimario)
	if motorAtual == "" {
		motorAtual = "claude"
	}
	for {
		m, err := c.selecionar(motorAtual)
		if err != nil {
			return nil, motorAtual, err
		}
		if op.Modelo == "" {
			op.Modelo = c.Config.ModeloParaMotor(motorAtual)
		}
		if op.Esforco == "" {
			op.Esforco = c.Config.EsforcoParaMotor(motorAtual)
		}
		op.ClaudeConfigDir = c.Config.ConfigDirDoMotor(motorAtual)

		res, err := m.Rodar(op)
		if err != nil || res == nil || !res.LimiteSessao {
			return res, motorAtual, err
		}

		estado.marcarEsgotado(motorAtual)
		if c.Config.Fallback.Ativo {
			if prox := proximoMotorFallback(c.Config.Fallback.Ordem, motorAtual, estado); prox != "" {
				detalhe := strings.TrimSpace(res.DetalheLimite)
				if detalhe == "" {
					detalhe = "limite de sessao/uso atingido"
				}
				c.registrarEvento("troca_de_harness",
					fmt.Sprintf("Praxis: troca de harness (%s → %s)", motorAtual, prox),
					fmt.Sprintf("Operacao: %s\nMotivo: %s", operacao, detalhe))
				motorAtual = prox
				// zera modelo/esforco/config-dir para reresolver no proximo laco.
				op.Modelo = ""
				op.Esforco = ""
				op.ClaudeConfigDir = ""
				continue
			}
		}

		// sem fallback: NAO dorme. Devolve o horario de retomada para o scheduler.
		detalhe := strings.TrimSpace(res.DetalheLimite)
		if detalhe == "" {
			detalhe = "franquia de tokens esgotada (limite de sessao)"
		}
		if op.OnEspera != nil {
			op.OnEspera(detalhe)
		}
		return res, motorAtual, &ErroFranquia{
			Motor:     motorAtual,
			Detalhe:   detalhe,
			RetomarEm: c.agora().Add(EsperaResetFranquia),
		}
	}
}

// proximoMotorFallback devolve o proximo motor da ordem apos `atual` que ainda
// nao esgotou (segundo o estado). "" quando nao ha. Portado de fallback.go.
func proximoMotorFallback(ordem []string, atual string, estado *EstadoFallback) string {
	atual = normalizarMotor(atual)
	viuAtual := false
	for _, nome := range ordem {
		nome = normalizarMotor(nome)
		if nome == "" {
			continue
		}
		if nome == atual {
			viuAtual = true
			continue
		}
		if !viuAtual {
			continue
		}
		if estado == nil || !estado.esgotado(nome) {
			return nome
		}
	}
	return ""
}

// selecionar resolve o motor pelo nome, usando o seam injetavel (para testes com
// motor stub); na ausencia, cai em motor.Selecionar.
func (c *ContextoExec) selecionar(nome string) (motor.Motor, error) {
	if c.Selecionar != nil {
		return c.Selecionar(nome)
	}
	return motor.Selecionar(nome)
}

// agora devolve o horario atual, usando o seam injetavel (testes deterministicos)
// ou time.Now.
func (c *ContextoExec) agora() time.Time {
	if c.Agora != nil {
		return c.Agora()
	}
	return time.Now()
}

// registrarEvento grava um evento no banco associado a demanda/projeto do
// contexto. Falha de escrita e ignorada (evento e best-effort, como as
// notificacoes do Praxis atual). ctx do contexto e usado.
func (c *ContextoExec) registrarEvento(tipo, titulo, detalhe string) {
	if c.Store == nil {
		return
	}
	ctx := c.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	demandID := c.Demanda.ID
	projectID := c.Demanda.ProjectID
	_, _ = c.Store.RegistrarEvento(ctx, evento(projectID, demandID, tipo, titulo, detalhe))
}
