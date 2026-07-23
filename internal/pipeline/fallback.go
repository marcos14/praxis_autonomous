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

// EstadoFallback lembra quais motores e perfis ja esgotaram a franquia dentro
// de uma rodada, para nao voltar a eles antes de um reset. Portado de
// fallback.go; os perfis entraram com o fallback intra-motor (esgotar todos os
// perfis do motor prioritario antes de trocar de motor).
type EstadoFallback struct {
	Esgotados       map[string]bool // motor → todos os perfis esgotados
	PerfisEsgotados map[string]bool // "motor\x00conta" → perfil esgotado
}

// NovoEstadoFallback cria um estado de fallback vazio.
func NovoEstadoFallback() *EstadoFallback {
	return &EstadoFallback{Esgotados: map[string]bool{}, PerfisEsgotados: map[string]bool{}}
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

// chavePerfil compoe a chave de um perfil no estado. O alias pode se repetir
// entre motores, entao a chave carrega os dois.
func chavePerfil(m, conta string) string { return normalizarMotor(m) + "\x00" + conta }

func (e *EstadoFallback) marcarPerfilEsgotado(m, conta string) {
	if e == nil {
		return
	}
	if e.PerfisEsgotados == nil {
		e.PerfisEsgotados = map[string]bool{}
	}
	e.PerfisEsgotados[chavePerfil(m, conta)] = true
}

func (e *EstadoFallback) perfilEsgotado(m, conta string) bool {
	if e == nil || e.PerfisEsgotados == nil {
		return false
	}
	return e.PerfisEsgotados[chavePerfil(m, conta)]
}

// rodarComFallback executa uma operacao no motor primario e, se ele sinalizar
// limite de sessao/uso, esgota primeiro os DEMAIS PERFIS do mesmo motor (na
// ordem de Config.Perfis) e so entao troca para o proximo motor disponivel na
// ordem configurada. Quando nao ha fallback possivel, NAO bloqueia esperando o
// reset: devolve *ErroFranquia com o horario de retomada para o scheduler
// reagendar. Alem do resultado e do motor usado, devolve o alias do perfil que
// executou (registro no run).
func (c *ContextoExec) rodarComFallback(operacao, motorPrimario string, op motor.OpcoesRun, estado *EstadoFallback) (*motor.ResultadoRun, string, string, error) {
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
			return nil, motorAtual, "", err
		}
		if op.Modelo == "" {
			op.Modelo = c.Config.ModeloParaMotor(motorAtual)
		}
		if op.Esforco == "" {
			op.Esforco = c.Config.EsforcoParaMotor(motorAtual)
		}
		perfil, temPerfil := c.perfilLivre(motorAtual, estado)
		if !temPerfil {
			// todos os perfis do motor ja esgotaram nesta rodada (so acontece
			// quando o fallback volta a um motor ja usado); trata como esgotado.
			estado.marcarEsgotado(motorAtual)
			if prox := c.proximoMotorLivre(motorAtual, estado); prox != "" {
				motorAtual = prox
				op.Modelo, op.Esforco = "", ""
				continue
			}
			return nil, motorAtual, "", &ErroFranquia{
				Motor:     motorAtual,
				Detalhe:   "todos os perfis disponiveis esgotaram a franquia",
				RetomarEm: c.agora().Add(EsperaResetFranquia),
			}
		}
		op.PerfilDir = perfil.Dir

		res, err := m.Rodar(op)
		if err != nil || res == nil || !res.LimiteSessao {
			return res, motorAtual, perfil.Conta, err
		}

		// franquia DESTE PERFIL esgotou.
		estado.marcarPerfilEsgotado(motorAtual, perfil.Conta)
		detalhe := strings.TrimSpace(res.DetalheLimite)
		if detalhe == "" {
			detalhe = "limite de sessao/uso atingido"
		}

		// primeiro tenta outro perfil do MESMO motor (prioridade do motor vale
		// mais que a ordem de fallback).
		if prox, ok := c.perfilLivre(motorAtual, estado); ok {
			c.registrarEvento("troca_de_perfil",
				fmt.Sprintf("Praxis: troca de perfil (%s:%s → %s:%s)", motorAtual, rotuloConta(perfil.Conta), motorAtual, rotuloConta(prox.Conta)),
				fmt.Sprintf("Operacao: %s\nMotivo: %s", operacao, detalhe))
			continue
		}

		// perfis do motor esgotados: agora sim troca de motor.
		estado.marcarEsgotado(motorAtual)
		if prox := c.proximoMotorLivre(motorAtual, estado); prox != "" {
			c.registrarEvento("troca_de_harness",
				fmt.Sprintf("Praxis: troca de harness (%s → %s)", motorAtual, prox),
				fmt.Sprintf("Operacao: %s\nMotivo: %s", operacao, detalhe))
			motorAtual = prox
			// zera modelo/esforco para reresolver no proximo laco.
			op.Modelo = ""
			op.Esforco = ""
			continue
		}

		// sem fallback: NAO dorme. Devolve o horario de retomada para o scheduler.
		if op.OnEspera != nil {
			op.OnEspera(detalhe)
		}
		return res, motorAtual, perfil.Conta, &ErroFranquia{
			Motor:     motorAtual,
			Detalhe:   detalhe,
			RetomarEm: c.agora().Add(EsperaResetFranquia),
		}
	}
}

// perfilLivre devolve o primeiro perfil do motor ainda nao esgotado nesta
// rodada, na ordem de uso (afinidade primeiro). ok=false quando todos esgotaram.
func (c *ContextoExec) perfilLivre(nomeMotor string, estado *EstadoFallback) (PerfilMotor, bool) {
	for _, p := range c.Config.PerfisDoMotor(nomeMotor) {
		if !estado.perfilEsgotado(nomeMotor, p.Conta) {
			return p, true
		}
	}
	return PerfilMotor{}, false
}

// proximoMotorLivre resolve o proximo motor da cadeia de fallback ("" quando o
// fallback esta inativo ou nao ha motor livre).
func (c *ContextoExec) proximoMotorLivre(atual string, estado *EstadoFallback) string {
	if !c.Config.Fallback.Ativo {
		return ""
	}
	return proximoMotorFallback(c.Config.Fallback.Ordem, atual, estado)
}

// rotuloConta da um nome legivel ao perfil vazio (motor sem conta cadastrada
// usa o perfil default do CLI) nos eventos de troca.
func rotuloConta(conta string) string {
	if strings.TrimSpace(conta) == "" {
		return "(perfil padrao)"
	}
	return conta
}

// proximoMotorFallback devolve o proximo motor da ordem apos `atual` que ainda
// nao esgotou (segundo o estado). "" quando nao ha. Um `atual` que NAO esta na
// ordem (motor de uso manual, fora da cadeia) cai no primeiro motor livre da
// cadeia — o uso manual tem fallback, so nao e alvo dele.
func proximoMotorFallback(ordem []string, atual string, estado *EstadoFallback) string {
	atual = normalizarMotor(atual)
	naCadeia := false
	for _, nome := range ordem {
		if normalizarMotor(nome) == atual {
			naCadeia = true
			break
		}
	}
	viuAtual := !naCadeia
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
