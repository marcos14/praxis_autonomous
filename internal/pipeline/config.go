package pipeline

import (
	"strings"

	"github.com/marcos14/praxis-autonomous/internal/motor"
)

// Config e a configuracao ja RESOLVIDA que o pipeline consome para uma demanda.
// No Praxis atual esses valores vinham de um autopilot.json lido do disco a cada
// passo (config.go); aqui eles chegam prontos no ContextoExec — quem resolve as
// camadas (global → projeto) e traduz motores/contas do banco e o scheduler
// (Fase 2d). O pipeline nao le config de arquivo nem toca no banco de config.
//
// Os nomes de motor usados aqui sao os nomes BASE conhecidos pelo pacote motor
// (claude/codex/opencode); a resolucao de aliases/contas (engine_accounts) para
// esses nomes base + diretório isolado do perfil ja foi feita pelo scheduler e chega em
// ConfigDirs.
type Config struct {
	MotorPadrao string            // motor usado quando a operacao nao especifica um
	Operacoes   map[string]string // operacao (executar/corrigir/revisar) → motor
	Modelos     map[string]string // motor → modelo; ausente cai em motor.ModeloPadrao
	Esforcos    map[string]string // motor → esforco; ausente cai em motor.EsforcoPadrao
	ConfigDirs  map[string]string // motor → CLAUDE_CONFIG_DIR ou CODEX_HOME do perfil preferido
	Contas      map[string]string // motor → alias do perfil preferido (registro no run)
	// Perfis lista TODOS os perfis ativos de cada motor na ordem de uso (o da
	// afinidade primeiro). O fallback esgota estes perfis um a um antes de trocar
	// de motor. Ausente/vazio → cai no par ConfigDirs/Contas (um perfil só).
	Perfis           map[string][]PerfilMotor
	AddDirs          []string // diretorios extras liberados ao harness
	BudgetFaseUSD    float64  // teto de custo por fase (0 = sem teto)
	TimeoutMin       int      // timeout por run do harness
	MaxCorrecoes     int      // ciclos de corretor por rodada de gates
	MaxCiclosRevisao int      // ciclos de correcao apos reprovacao do revisor
	Fallback         Fallback // troca de motor quando a franquia esgota
	// Gates/GatesExtra sao os gates deterministicos resolvidos por demanda (da
	// config efetiva do projeto). Quando presentes e o Runner nao tem um Gates
	// fixo (producao), o Runner monta um RunnerGates por fase com estes gates,
	// compartilhando o semaforo global. Vazio = sem gate deterministico.
	Gates      []Gate
	GatesExtra []GateExtra
	// GitSufixoPraxis acrescenta " - Praxis" ao nome do AUTOR dos commits de
	// fase (chave git_sufixo_praxis da config efetiva; o resolvedor do
	// scheduler liga por default e o admin pode desligar). Nao afeta o
	// committer, que e sempre o Praxis (gitops.Identidade).
	GitSufixoPraxis bool
}

// Fallback descreve a ordem de troca de motores quando o motor corrente sinaliza
// limite de sessao/uso. Portado de Config.Motores.Fallback do Praxis atual. A
// Ordem so contem motores que participam do fallback (engines.fallback = 1); um
// motor de uso manual (fallback = 0) pode INICIAR a operacao, mas nunca e alvo
// de troca automatica.
type Fallback struct {
	Ativo bool
	Ordem []string
}

// PerfilMotor identifica um perfil isolado de um motor: o alias (registro no
// run) e o diretorio raiz (CLAUDE_CONFIG_DIR/CODEX_HOME).
type PerfilMotor struct {
	Conta string
	Dir   string
}

// MotorParaOperacao devolve o motor configurado para a operacao, caindo no motor
// padrao e, por fim, em "claude". Espelha motorParaOperacao do Praxis atual.
func (c Config) MotorParaOperacao(operacao string) string {
	if m := normalizarMotor(c.Operacoes[operacao]); m != "" {
		return m
	}
	if m := normalizarMotor(c.MotorPadrao); m != "" {
		return m
	}
	return "claude"
}

// ModeloParaMotor devolve o modelo configurado para o motor; na ausencia, usa o
// modelo padrao do motor. Espelha modeloParaMotor (sem a camada de alias, que ja
// foi resolvida pelo scheduler).
func (c Config) ModeloParaMotor(nomeMotor string) string {
	nomeMotor = normalizarMotor(nomeMotor)
	if m := strings.TrimSpace(c.Modelos[nomeMotor]); m != "" {
		return m
	}
	return motor.ModeloPadrao(nomeMotor)
}

// EsforcoParaMotor devolve o esforco configurado para o motor; na ausencia, usa
// o esforco padrao do motor. Espelha esforcoParaMotor.
func (c Config) EsforcoParaMotor(nomeMotor string) string {
	nomeMotor = normalizarMotor(nomeMotor)
	if e := strings.TrimSpace(c.Esforcos[nomeMotor]); e != "" {
		return strings.ToLower(e)
	}
	return motor.EsforcoPadrao(nomeMotor)
}

// PerfisDoMotor devolve os perfis do motor na ordem de uso. Sem a lista Perfis,
// cai no par ConfigDirs/Contas (o perfil unico do desenho anterior); um motor
// sem perfil cadastrado devolve um perfil vazio (o CLI usa o perfil default do
// servico), mantendo o laco de fallback com exatamente uma tentativa por motor.
func (c Config) PerfisDoMotor(nomeMotor string) []PerfilMotor {
	nomeMotor = normalizarMotor(nomeMotor)
	if lista := c.Perfis[nomeMotor]; len(lista) > 0 {
		return lista
	}
	return []PerfilMotor{{
		Conta: strings.TrimSpace(c.Contas[nomeMotor]),
		Dir:   strings.TrimSpace(c.ConfigDirs[nomeMotor]),
	}}
}

// PerfilDirDoMotor devolve o diretório isolado do perfil preferido do motor.
func (c Config) PerfilDirDoMotor(nomeMotor string) string {
	return strings.TrimSpace(c.PerfisDoMotor(nomeMotor)[0].Dir)
}

// ContaDoMotor devolve o alias do perfil preferido do motor ("" sem perfil).
func (c Config) ContaDoMotor(nomeMotor string) string {
	return strings.TrimSpace(c.PerfisDoMotor(nomeMotor)[0].Conta)
}

// ConfigDirDoMotor preserva o nome antigo para consumidores externos.
func (c Config) ConfigDirDoMotor(nomeMotor string) string { return c.PerfilDirDoMotor(nomeMotor) }

// normalizarMotor deixa o nome do motor em minusculas e sem espacos (mesma
// normalizacao de normalizarNomeMotor do Praxis atual).
func normalizarMotor(nome string) string {
	return strings.ToLower(strings.TrimSpace(nome))
}
