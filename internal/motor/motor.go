package motor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/marcos14/praxis-autonomous/internal/procs"
)

// OpcoesRun descreve uma execucao headless de um motor de codigo, sempre em
// contexto limpo. Cada motor traduz estas intencoes para suas flags nativas.
//
// Portado de motor.go do Praxis atual, adaptado ao Praxis Autonomous:
//   - Raiz virou Dir (o worktree da demanda onde o harness roda);
//   - DirLogs e a pasta onde o .jsonl da execucao e gravado (antes derivada de
//     automacao/logs); no serviço aponta para PRAXIS_HOME/logs;
//   - PerfilDir vem do banco (engine_accounts.config_dir), nao de arquivo.
type OpcoesRun struct {
	Dir       string // worktree da demanda (cmd.Dir do processo filho)
	DirLogs   string // pasta onde gravar o .jsonl desta execucao
	Prompt    string
	Modelo    string
	Esforco   string
	PerfilDir string // raiz isolada do perfil: CLAUDE_CONFIG_DIR ou CODEX_HOME
	// ClaudeConfigDir é mantido temporariamente para compatibilidade com chamadas
	// externas/testes antigos. Código novo deve preencher PerfilDir.
	ClaudeConfigDir string
	AddDirs         []string
	BudgetUSD       float64
	TimeoutMin      int
	Schema          string // se != "", quer saida estruturada conforme este JSON Schema
	SomenteLeitura  bool   // revisor: nao edita arquivos nem commita
	ProibirCommit   bool   // executor/corretor: quem commita e o orquestrador
	RotuloLog       string // prefixo do arquivo de log em DirLogs
	Ctx             context.Context
	PausaCh         <-chan struct{}
	OnEspera        func(detalhe string)

	// OnLogPath, quando != nil, e chamado assim que o .jsonl do run e criado
	// (antes de o harness comecar a emitir). Permite ao chamador gravar o
	// log_ref da execucao NO INICIO do run — e o que faz o "log ao vivo" (SSE)
	// acompanhar a execucao em andamento, nao apenas a ja encerrada.
	OnLogPath func(caminho string)

	// RegistrarProcesso, quando != nil, e chamado logo apos o processo do harness
	// iniciar (com o PID) e devolve uma funcao de desregistro chamada quando o
	// processo termina. Alimenta o registro de PIDs da recuperacao pos-restart
	// (Fase 2i), que mata as arvores de processos orfaos no boot.
	RegistrarProcesso func(pid int) func()
}

// ResultadoRun e a saida normalizada de qualquer motor.
type ResultadoRun struct {
	IsError       bool
	Subtipo       string
	Resultado     string
	Estruturado   json.RawMessage
	CustoUSD      float64
	NumTurns      int
	TokensIn      int
	TokensOut     int
	LogPath       string
	LimiteSessao  bool
	DetalheLimite string
	// FalhaAutenticacao indica perfil deslogado/credencial invalida (ex.: "Not
	// logged in · Please run /login"). Diferente de LimiteSessao, nao se resolve
	// esperando: o fallback deve pular o perfil e um humano precisa relogar.
	FalhaAutenticacao bool
}

// Capacidades declara o que um motor faz nativamente.
type Capacidades struct {
	SchemaNativo   bool `json:"schema_nativo"`
	BudgetNativo   bool `json:"budget_nativo"`
	CustoUSDNativo bool `json:"custo_usd_nativo"`
}

// Motor e um backend de execucao de codigo (Claude Code, Codex, etc.).
type Motor interface {
	Nome() string
	Capacidades() Capacidades
	Rodar(op OpcoesRun) (*ResultadoRun, error)
}

var motoresRegistrados = map[string]func() Motor{
	"claude":   func() Motor { return motorClaude{} },
	"codex":    func() Motor { return motorCodex{} },
	"opencode": func() Motor { return motorOpencode{} },
}

// Selecionar devolve o motor pelo nome (vazio → "claude").
func Selecionar(nome string) (Motor, error) {
	nome = strings.ToLower(strings.TrimSpace(nome))
	if nome == "" {
		nome = "claude"
	}
	f, ok := motoresRegistrados[nome]
	if !ok {
		return nil, fmt.Errorf("motor desconhecido: %q", nome)
	}
	return f(), nil
}

// Instalado informa se o CLI do motor esta no PATH.
func Instalado(nome string) bool {
	nome = normalizarNomeMotor(nome)
	if nome == "" {
		return false
	}
	_, err := exec.LookPath(nome)
	return err == nil
}

// Instalados lista os motores conhecidos cujo CLI esta no PATH.
func Instalados() []string {
	var out []string
	for _, nome := range motoresConhecidos() {
		if Instalado(nome) {
			out = append(out, nome)
		}
	}
	return out
}

// Conhecidos lista, em ordem alfabetica, os motores registrados.
func Conhecidos() []string { return motoresConhecidos() }

// ModeloPadrao devolve o modelo default por motor. Para os que nao sao Claude
// devolvemos "" para deixar o proprio CLI usar o modelo configurado por ele.
func ModeloPadrao(nome string) string {
	switch strings.ToLower(strings.TrimSpace(nome)) {
	case "", "claude":
		return "opus"
	case "codex":
		return "gpt-5.5"
	case "opencode":
		// deixa o proprio opencode usar o modelo (provider/model) configurado.
		return ""
	default:
		return ""
	}
}

// EsforcoPadrao devolve o nivel de esforco default por motor.
func EsforcoPadrao(nome string) string {
	switch strings.ToLower(strings.TrimSpace(nome)) {
	case "claude", "codex":
		return "high"
	default:
		// opencode usa --variant especifico do provider; deixa o default dele.
		return ""
	}
}

// CoAuthorTrailer devolve o trailer Co-Authored-By do motor (para o commit que
// o orquestrador faz em nome do harness).
func CoAuthorTrailer(nome string) string {
	switch strings.ToLower(strings.TrimSpace(nome)) {
	case "", "claude":
		return "Co-Authored-By: Claude <noreply@anthropic.com>"
	case "codex":
		return "Co-Authored-By: Codex <noreply@openai.com>"
	case "opencode":
		return "Co-Authored-By: opencode <noreply@opencode.ai>"
	default:
		return ""
	}
}

// abrirLog cria o arquivo .jsonl da execucao em dirLogs.
func abrirLog(dirLogs, rotulo, ext string) (*os.File, string, error) {
	dirLogs = strings.TrimSpace(dirLogs)
	if dirLogs == "" {
		dirLogs = "."
	}
	if err := os.MkdirAll(dirLogs, 0o755); err != nil {
		return nil, "", err
	}
	p := filepath.Join(dirLogs, fmt.Sprintf("%s-%s.%s", rotulo, agoraTS(), ext))
	f, err := os.Create(p)
	if err != nil {
		return nil, "", err
	}
	return f, p, nil
}

// prepararProcessoFilho configura o cmd para que o cancelamento do ctx (pausa/
// cancelamento da demanda — Fase 2i) mate TODA a arvore do processo do harness,
// nao so o filho direto. Chamar ANTES de cmd.Start. Sem isso, exec.CommandContext
// so mata o filho direto, deixando netos (ferramentas invocadas pelo harness)
// orfaos — o que impede o `git worktree remove` no Windows.
func prepararProcessoFilho(cmd *exec.Cmd) {
	procs.ConfigurarGrupoProcesso(cmd)
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return procs.MatarArvore(cmd.Process.Pid)
	}
}

// registrarProcessoFilho registra o PID do processo ja iniciado no sink de op (se
// houver) e devolve a funcao de desregistro (para `defer`). No-op quando op nao
// traz RegistrarProcesso ou o processo ainda nao iniciou.
func registrarProcessoFilho(op OpcoesRun, cmd *exec.Cmd) func() {
	if op.RegistrarProcesso == nil || cmd.Process == nil {
		return func() {}
	}
	return op.RegistrarProcesso(cmd.Process.Pid)
}

func contextoTimeout(pai context.Context, min int) (context.Context, context.CancelFunc, time.Duration) {
	d := time.Duration(min) * time.Minute
	if d <= 0 {
		d = 2 * time.Hour
	}
	if pai == nil {
		pai = context.Background()
	}
	ctx, cancel := context.WithTimeout(pai, d)
	return ctx, cancel, d
}

// promptComSchema e o fallback para motores sem saida estruturada nativa.
func promptComSchema(prompt, schema string) string {
	if schema == "" {
		return prompt
	}
	return prompt + "\n\n---\nIMPORTANTE: ao final, responda APENAS com um unico objeto JSON valido " +
		"(sem cercas de markdown, sem texto em volta) que satisfaca EXATAMENTE este JSON Schema:\n" + schema
}

// DecodificarEstruturado le a saida estruturada de um run: prefere o campo
// nativo; na falta, extrai o JSON do texto final.
func DecodificarEstruturado(res *ResultadoRun, v any) error {
	if len(res.Estruturado) > 0 && string(res.Estruturado) != "null" {
		if err := json.Unmarshal(res.Estruturado, v); err == nil {
			return nil
		}
	}
	bruto := extrairJSON(res.Resultado)
	if bruto == "" {
		return fmt.Errorf("resposta sem JSON reconhecivel")
	}
	return json.Unmarshal([]byte(bruto), v)
}

// extrairJSON pega o primeiro objeto JSON de um texto (tolerante a cercas de
// markdown e a prosa em volta).
func extrairJSON(s string) string {
	ini := strings.Index(s, "{")
	fim := strings.LastIndex(s, "}")
	if ini < 0 || fim <= ini {
		return ""
	}
	return s[ini : fim+1]
}

// CustoEstimado aproxima o custo em USD a partir dos tokens, para motores que
// so reportam tokens. Modelo desconhecido retorna 0.
func CustoEstimado(modelo string, tokIn, tokOut int) float64 {
	tab := map[string][2]float64{
		"gpt-5.5":     {5, 30},
		"gpt-5":       {1.25, 10},
		"gpt-5-codex": {1.25, 10},
		"gpt-5-mini":  {0.25, 2},
		"o4-mini":     {1.10, 4.40},
	}
	p, ok := tab[strings.ToLower(strings.TrimSpace(modelo))]
	if !ok {
		return 0
	}
	return float64(tokIn)/1e6*p[0] + float64(tokOut)/1e6*p[1]
}
