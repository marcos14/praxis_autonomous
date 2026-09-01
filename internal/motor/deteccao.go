package motor

import (
	"os"
	"os/exec"
	"strings"
)

// A deteccao automatica resolve o problema de cadastrar motores num servidor
// remoto sem saber, de antemao, quais harnesses estao instalados e como estao
// configurados. Ela inspeciona o PATH (CLI instalado) e as variaveis de
// ambiente relevantes de cada harness conhecido, devolvendo uma sugestao pronta
// (modelos, budget, timeout, esforco e contas derivadas do ambiente) que a UI
// pode cadastrar com um clique. Nao toca no banco: e uma leitura pura do
// ambiente, o que a mantem testavel e sem efeitos colaterais.

// catalogoMotor descreve os defaults sugeridos para um harness conhecido, alem
// das variaveis de ambiente que interessam a deteccao.
type catalogoMotor struct {
	nome           string
	modeloExec     string
	modeloAnalise  string
	modeloConsulta string
	modelos        []string // opcoes de modelo sugeridas para o harness
	budgetFaseUSD  float64
	timeoutMin     int
	esforco        string
	observacao     string
	varsConta      []string // env vars que apontam o config dir de uma conta
	varsSecreta    []string // env vars sensiveis (mascaradas) que indicam credencial
	varsExtra      []string // outras env vars relevantes (override de modelo, base url)
}

// catalogoMotores lista os harnesses conhecidos com seus defaults de sugestao.
// A ordem espelha a prioridade tipica de fallback (claude → codex → opencode).
var catalogoMotores = []catalogoMotor{
	{
		nome:           "claude",
		modeloExec:     "opus",
		modeloAnalise:  "sonnet",
		modeloConsulta: "haiku",
		modelos:        []string{"opus", "sonnet", "haiku"},
		budgetFaseUSD:  100.0,
		timeoutMin:     45,
		esforco:        "high",
		observacao:     "Cada conta secundaria e um CLAUDE_CONFIG_DIR proprio; suporta schema, budget e custo em USD nativos.",
		varsConta:      []string{"CLAUDE_CONFIG_DIR"},
		varsSecreta:    []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN"},
		varsExtra:      []string{"ANTHROPIC_MODEL", "ANTHROPIC_BASE_URL"},
	},
	{
		nome:           "codex",
		modeloExec:     "gpt-5.5",
		modeloAnalise:  "gpt-5",
		modeloConsulta: "gpt-5-mini",
		modelos:        []string{"gpt-5.5", "gpt-5", "gpt-5-codex", "gpt-5-mini", "o4-mini"},
		budgetFaseUSD:  100.0,
		timeoutMin:     45,
		esforco:        "high",
		observacao:     "Budget e custo em USD nao sao nativos; o custo e estimado por tokens.",
		varsConta:      []string{"CODEX_HOME"},
		varsSecreta:    []string{"OPENAI_API_KEY"},
		varsExtra:      []string{"OPENAI_BASE_URL"},
	},
	{
		nome:           "opencode",
		modeloExec:     "",
		modeloAnalise:  "",
		modeloConsulta: "",
		modelos:        nil,
		budgetFaseUSD:  0,
		timeoutMin:     60,
		esforco:        "",
		observacao:     "Usa o provider/model configurado no proprio opencode; sem budget nativo (teto por tempo).",
		varsConta:      []string{"OPENCODE_CONFIG"},
		varsSecreta:    []string{"OPENCODE_API_KEY"},
		varsExtra:      nil,
	},
}

// VarAmbiente descreve uma variavel de ambiente relevante para um harness. Para
// variaveis sensiveis, Valor vem mascarado (nunca expoe a credencial).
type VarAmbiente struct {
	Nome     string `json:"nome"`
	Definida bool   `json:"definida"`
	Valor    string `json:"valor"`
	Sensivel bool   `json:"sensivel"`
}

// SugestaoConta e uma conta derivada do ambiente (ex.: CLAUDE_CONFIG_DIR),
// pronta para virar um engine_accounts.
type SugestaoConta struct {
	Alias     string `json:"alias"`
	ConfigDir string `json:"config_dir"`
	Origem    string `json:"origem"`
}

// SugestaoMotor e a proposta de cadastro de um harness detectado no ambiente. O
// campo JaCadastrado e preenchido pela camada de API (que conhece o banco); a
// deteccao em si nao o toca.
type SugestaoMotor struct {
	Nome           string          `json:"nome"`
	Instalado      bool            `json:"instalado"`
	CaminhoCLI     string          `json:"caminho_cli"`
	JaCadastrado   bool            `json:"ja_cadastrado"`
	ModeloExec     string          `json:"modelo_exec"`
	ModeloAnalise  string          `json:"modelo_analise"`
	ModeloConsulta string          `json:"modelo_consulta"`
	Modelos        []string        `json:"modelos"`
	BudgetFaseUSD  float64         `json:"budget_fase_usd"`
	TimeoutMin     int             `json:"timeout_min"`
	Esforco        string          `json:"esforco"`
	Capacidades    Capacidades     `json:"capacidades"`
	Contas         []SugestaoConta `json:"contas"`
	Variaveis      []VarAmbiente   `json:"variaveis"`
	Observacao     string          `json:"observacao"`
}

// DetectarMotores inspeciona o ambiente do processo (PATH + variaveis) e devolve
// uma sugestao de cadastro para cada harness conhecido.
func DetectarMotores() []SugestaoMotor {
	return detectarMotores(os.LookupEnv, caminhoCLIMotor)
}

// detectarMotores e a forma testavel de DetectarMotores: recebe as seams de
// leitura de ambiente (lookup) e de localizacao do CLI (caminho).
func detectarMotores(lookup func(string) (string, bool), caminho func(string) (string, bool)) []SugestaoMotor {
	out := make([]SugestaoMotor, 0, len(catalogoMotores))
	for _, c := range catalogoMotores {
		s := SugestaoMotor{
			Nome:           c.nome,
			ModeloExec:     c.modeloExec,
			ModeloAnalise:  c.modeloAnalise,
			ModeloConsulta: c.modeloConsulta,
			Modelos:        append([]string(nil), c.modelos...),
			BudgetFaseUSD:  c.budgetFaseUSD,
			TimeoutMin:     c.timeoutMin,
			Esforco:        c.esforco,
			Observacao:     c.observacao,
			Contas:         []SugestaoConta{},
			Variaveis:      []VarAmbiente{},
		}
		if m, err := Selecionar(c.nome); err == nil {
			s.Capacidades = m.Capacidades()
		}
		if p, ok := caminho(c.nome); ok {
			s.Instalado = true
			s.CaminhoCLI = p
		}
		for _, nome := range c.varsConta {
			v, ok := lookup(nome)
			s.Variaveis = append(s.Variaveis, VarAmbiente{Nome: nome, Definida: ok, Valor: valorAmbiente(v, ok)})
			if ok && strings.TrimSpace(v) != "" {
				s.Contas = append(s.Contas, SugestaoConta{
					Alias:     "principal",
					ConfigDir: strings.TrimSpace(v),
					Origem:    nome,
				})
			}
		}
		for _, nome := range c.varsSecreta {
			v, ok := lookup(nome)
			s.Variaveis = append(s.Variaveis, VarAmbiente{Nome: nome, Definida: ok, Valor: mascararValor(v, ok), Sensivel: true})
		}
		for _, nome := range c.varsExtra {
			v, ok := lookup(nome)
			s.Variaveis = append(s.Variaveis, VarAmbiente{Nome: nome, Definida: ok, Valor: valorAmbiente(v, ok)})
		}
		out = append(out, s)
	}
	return out
}

// caminhoCLIMotor devolve o caminho do CLI do harness no PATH, se instalado.
func caminhoCLIMotor(nome string) (string, bool) {
	p, err := exec.LookPath(normalizarNomeMotor(nome))
	if err != nil {
		return "", false
	}
	return p, true
}

// valorAmbiente devolve o valor da variavel quando definida (sem mascarar).
func valorAmbiente(v string, ok bool) string {
	if !ok {
		return ""
	}
	return v
}

// mascararValor nunca expoe o conteudo de uma credencial: informa apenas se
// esta definida.
func mascararValor(v string, ok bool) string {
	if !ok || strings.TrimSpace(v) == "" {
		return ""
	}
	return "•••• (definida)"
}
