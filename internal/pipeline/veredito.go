package pipeline

// SchemaVeredito e o JSON Schema que o revisor precisa satisfazer. Portado
// integralmente de executar.go (schemaVeredito) do Praxis atual.
const SchemaVeredito = `{"type":"object","required":["veredito","problemas"],"properties":{"veredito":{"type":"string","enum":["APROVADO","REPROVADO"]},"problemas":{"type":"array","items":{"type":"string"}},"fases_novas":{"type":"array","items":{"type":"object","required":["titulo","descricao","valor"],"properties":{"titulo":{"type":"string"},"descricao":{"type":"string"},"valor":{"type":"string","enum":["alto","baixo"]},"checklist":{"type":"array","items":{"type":"string"}},"depende_de":{"type":"array","items":{"type":"string"}},"gate_extra":{"type":"string"},"observacao":{"type":"string"}}}}}}`

// Veredito e a saida estruturada do revisor. Portado de executar.go.
type Veredito struct {
	Veredito   string     `json:"veredito"`
	Problemas  []string   `json:"problemas"`
	FasesNovas []FaseNova `json:"fases_novas"`
}

// Aprovado informa se o veredito aprovou a entrega.
func (v Veredito) Aprovado() bool { return v.Veredito == "APROVADO" }

// FaseNova descreve uma fase que o revisor propos criar ao aprovar a entrega.
// Portado de FaseNova do Praxis atual (campos do schema acima). No Praxis
// Autonomous a INSERCAO dessas fases na fila e responsabilidade do scheduler
// (Fase 2d/2g): o pipeline apenas as devolve em ResultadoFase.FasesNovas.
type FaseNova struct {
	Titulo     string   `json:"titulo"`
	Descricao  string   `json:"descricao"`
	Valor      string   `json:"valor"`
	Checklist  []string `json:"checklist"`
	DependeDe  []string `json:"depende_de"`
	GateExtra  string   `json:"gate_extra"`
	Observacao string   `json:"observacao"`
}
