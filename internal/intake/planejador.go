package intake

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/marcos14/praxis-autonomous/internal/db"
	"github.com/marcos14/praxis-autonomous/internal/motor"
)

// SchemaPlanejador é o JSON Schema da saída do planejador: o plano em markdown
// (para o card mostrar) e a lista de micro-fases (que o Praxis executa). Motores
// com schema nativo (claude/codex) o usam; os demais recebem o schema no prompt.
const SchemaPlanejador = `{"type":"object","required":["plano_md","fases"],"properties":{
"plano_md":{"type":"string"},
"fases":{"type":"array","items":{"type":"object","required":["codigo","titulo"],"properties":{
  "codigo":{"type":"string"},
  "titulo":{"type":"string"},
  "depende_de":{"type":"array","items":{"type":"string"}},
  "requer_humano":{"type":"boolean"},
  "gate_extra":{"type":"string"},
  "observacao":{"type":"string"}}}}}}`

// FasePlanejada é uma fase na saída do planejador (antes de virar db.Fase).
type FasePlanejada struct {
	Codigo       string   `json:"codigo"`
	Titulo       string   `json:"titulo"`
	DependeDe    []string `json:"depende_de"`
	RequerHumano bool     `json:"requer_humano"`
	GateExtra    string   `json:"gate_extra"`
	Observacao   string   `json:"observacao"`
}

// SaidaPlanejador é a saída estruturada do planejador.
type SaidaPlanejador struct {
	PlanoMD string          `json:"plano_md"`
	Fases   []FasePlanejada `json:"fases"`
}

// Planejador roda o harness em modo somente leitura sobre o código do projeto
// para derivar, a partir do PRD e das respostas do analista, um plano em markdown
// e a lista de micro-fases (Fase 3c). É o MECANISMO: todas as dependências chegam
// explícitas (espelha o Analista). O Servico resolve config do banco e o monta.
//
// O planejador NÃO edita nada: roda com SomenteLeitura no diretório do repo
// principal do projeto (não num worktree — o plano antecede a branch da demanda).
type Planejador struct {
	Store *db.DB // fila/estado no banco (demanda, chat, perguntas, fases, runs, eventos)

	// Parâmetros do run resolvidos (pelo Servico, a partir do banco):
	Motor      string   // nome base do motor (claude/codex/opencode)
	Modelo     string   // modelo de planejamento; vazio → default do motor
	Esforco    string   // esforço; vazio → default do motor
	Conta      string   // alias do perfil usado (registro no run); "" = sem conta
	ConfigDir  string   // CLAUDE_CONFIG_DIR/CODEX_HOME da conta (afinidade); "" = sem conta
	Dir        string   // raiz do repo do projeto (cmd.Dir do harness — só leitura)
	DirLogs    string   // pasta dos .jsonl das execuções
	AddDirs    []string // diretórios extras liberados ao harness (só leitura)
	BudgetUSD  float64  // teto de custo do run (0 = sem teto)
	TimeoutMin int      // timeout do run

	// Seams de teste (nil em produção):
	Selecionar func(nome string) (motor.Motor, error) // default: motor.Selecionar
	Prompt     func(ctx context.Context, nome string) (string, error)
	Agora      func() time.Time // default: time.Now
}

// Planejar conduz o planejamento da demanda: roda o harness readonly com o PRD +
// as perguntas/respostas do analista, decodifica a saída, persiste o plano_md e as
// fases, grava a fala do planejador no chat e transita planejando→aguardando_aprovacao.
//
// O erro de retorno é reservado a falhas de infraestrutura/harness. Falha lógica
// (JSON inválido, sem fases, harness IsError) carimba a demanda `falhou` com o
// motivo e devolve nil.
func (p *Planejador) Planejar(ctx context.Context, demandaID int64) error {
	dem, err := p.Store.ObterDemanda(ctx, demandaID)
	if err != nil {
		return fmt.Errorf("planejador: obter demanda %d: %w", demandaID, err)
	}
	// Só planeja quem está planejando. Replanejar (rejeição com comentário) parte
	// de aguardando_aprovacao; ambos são aceitos.
	if dem.Status != db.StatusDemandaPlanejando && dem.Status != db.StatusDemandaAguardandoAprovacao {
		return fmt.Errorf("planejador: demanda %d não está em planejamento (status %q)", demandaID, dem.Status)
	}

	prd, err := p.montarPRD(ctx, demandaID)
	if err != nil {
		return err
	}
	if strings.TrimSpace(prd) == "" {
		return p.falhar(ctx, dem, "planejamento sem PRD: a demanda não tem mensagem do usuário para planejar")
	}
	qa, err := p.montarQA(ctx, demandaID)
	if err != nil {
		return err
	}

	// planejando (garante o estado mesmo vindo de aguardando_aprovacao/replanejar)
	p.marcarStatus(ctx, &dem, db.StatusDemandaPlanejando)
	p.registrarEvento(dem, "planejamento_iniciado", "Praxis: planejamento iniciado",
		fmt.Sprintf("Planejador (%s%s), modo somente leitura.", p.Motor, sufixoModelo(p.Modelo)))

	res, motorUsado, custo, err := p.rodar(ctx, dem, prd, qa)
	if err != nil {
		return p.falhar(ctx, dem, fmt.Sprintf("planejamento falhou: %v", err))
	}
	if res.IsError {
		return p.falhar(ctx, dem, fmt.Sprintf("planejamento terminou com erro (%s) — log: %s", res.Subtipo, res.LogPath))
	}

	var saida SaidaPlanejador
	if err := motor.DecodificarEstruturado(res, &saida); err != nil {
		return p.falhar(ctx, dem, fmt.Sprintf("planejamento não devolveu JSON válido (%v) — log: %s", err, res.LogPath))
	}

	fases, msg := montarFasesDaSaida(saida.Fases)
	if msg != "" {
		return p.falhar(ctx, dem, "planejamento inválido: "+msg)
	}
	if len(fases) == 0 {
		return p.falhar(ctx, dem, "o planejador não retornou nenhuma fase")
	}

	if _, err := p.Store.SubstituirFases(ctx, demandaID, fases); err != nil {
		return fmt.Errorf("planejador: persistir fases: %w", err)
	}

	// grava o plano_md na demanda e transita para aguardando_aprovacao.
	dem.PlanoMD = strings.TrimSpace(saida.PlanoMD)
	dem.Status = db.StatusDemandaAguardandoAprovacao
	atual, err := p.Store.AtualizarDemanda(ctx, dem)
	if err != nil {
		return fmt.Errorf("planejador: atualizar demanda: %w", err)
	}
	dem = atual

	p.registrarFalaPlanejador(ctx, dem, saida, motorUsado, custo, len(fases))
	p.registrarEvento(dem, "planejamento_concluido", "Praxis: plano gerado",
		fmt.Sprintf("%d fase(s) · custo US$ %.2f · aguardando aprovação", len(fases), custo))
	return nil
}

// montarFasesDaSaida valida a saída do planejador e a converte em []db.Fase.
// Devolve uma mensagem não-vazia em falha de validação (código/título
// obrigatórios, códigos únicos, dependências existentes).
func montarFasesDaSaida(brutas []FasePlanejada) ([]db.Fase, string) {
	codigos := map[string]bool{}
	fases := make([]db.Fase, 0, len(brutas))
	for _, fp := range brutas {
		codigo := strings.TrimSpace(fp.Codigo)
		if codigo == "" {
			return nil, "toda fase precisa de um código"
		}
		if strings.TrimSpace(fp.Titulo) == "" {
			return nil, "a fase " + codigo + " precisa de um título"
		}
		if codigos[codigo] {
			return nil, "código de fase repetido: " + codigo
		}
		codigos[codigo] = true
		fases = append(fases, db.Fase{
			Codigo:       codigo,
			Titulo:       strings.TrimSpace(fp.Titulo),
			Status:       db.StatusFasePendente,
			DependeDe:    limparStrings(fp.DependeDe),
			RequerHumano: fp.RequerHumano,
			GateExtra:    strings.TrimSpace(fp.GateExtra),
			Observacao:   strings.TrimSpace(fp.Observacao),
		})
	}
	// dependências não podem apontar para um código inexistente (travaria a fila).
	for _, f := range fases {
		for _, dep := range f.DependeDe {
			if !codigos[dep] {
				return nil, "a fase " + f.Codigo + " depende de um código inexistente: " + dep
			}
		}
	}
	return fases, ""
}

// rodar executa o harness readonly com o prompt do planejador e devolve o
// resultado, o motor efetivamente usado e o custo. Registra a execução no banco
// (operacao=planejador, sem fase).
func (p *Planejador) rodar(ctx context.Context, dem db.Demanda, prd, qa string) (*motor.ResultadoRun, string, float64, error) {
	tpl, err := p.prompt(ctx, PromptPlanejador)
	if err != nil {
		return nil, p.Motor, 0, err
	}

	exec, err := p.Store.CriarExecucao(ctx, db.Execucao{
		DemandID: dem.ID, Operacao: db.OperacaoPlanejador, Engine: p.Motor, Conta: p.Conta, Modelo: p.Modelo,
	})
	if err != nil {
		return nil, p.Motor, 0, fmt.Errorf("registrar execução: %w", err)
	}

	m, err := p.selecionar(p.Motor)
	if err != nil {
		p.fecharExecComErro(ctx, exec)
		return nil, p.Motor, 0, err
	}
	res, runErr := m.Rodar(motor.OpcoesRun{
		Dir: p.Dir, DirLogs: p.DirLogs,
		Prompt: renderPrompt(tpl, map[string]string{"PRD": prd, "QA": qa}),
		Modelo: p.Modelo, Esforco: p.Esforco, PerfilDir: p.ConfigDir,
		AddDirs: p.AddDirs, BudgetUSD: p.BudgetUSD, TimeoutMin: p.TimeoutMin,
		Schema: SchemaPlanejador, SomenteLeitura: true, ProibirCommit: true,
		RotuloLog: "planejador", Ctx: ctx,
	})

	custo := 0.0
	exec.TerminadoEm = p.agoraISO()
	exec.Engine = m.Nome()
	if res != nil {
		custo = res.CustoUSD
		exec.CustoUSD = res.CustoUSD
		exec.TokensIn = int64(res.TokensIn)
		exec.TokensOut = int64(res.TokensOut)
		exec.IsError = res.IsError
		exec.LogRef = res.LogPath
	} else {
		exec.IsError = true
	}
	if _, err := p.Store.AtualizarExecucao(ctx, exec); err != nil {
		p.registrarEvento(dem, "aviso", "Praxis: falha ao registrar execução do planejador", err.Error())
	}
	if runErr != nil {
		return res, m.Nome(), custo, runErr
	}
	return res, m.Nome(), custo, nil
}

// montarPRD concatena as falas do usuário no chat (PRD + complementos) em ordem
// cronológica.
func (p *Planejador) montarPRD(ctx context.Context, demandaID int64) (string, error) {
	msgs, err := p.Store.ListarMensagensChat(ctx, demandaID)
	if err != nil {
		return "", fmt.Errorf("planejador: ler chat da demanda %d: %w", demandaID, err)
	}
	partes := make([]string, 0, len(msgs))
	for _, m := range msgs {
		if m.Papel == db.PapelUser {
			if c := strings.TrimSpace(m.Conteudo); c != "" {
				partes = append(partes, c)
			}
		}
	}
	return strings.Join(partes, "\n\n"), nil
}

// montarQA formata as perguntas do analista e as respostas do usuário para
// alimentar o prompt. Perguntas sem resposta caem na sugestão do analista (o
// usuário pode ter clicado em "gerar plano" sem responder).
func (p *Planejador) montarQA(ctx context.Context, demandaID int64) (string, error) {
	perguntas, err := p.Store.ListarPerguntas(ctx, demandaID)
	if err != nil {
		return "", fmt.Errorf("planejador: ler perguntas da demanda %d: %w", demandaID, err)
	}
	if len(perguntas) == 0 {
		return "(o analista não gerou perguntas para esta demanda)", nil
	}
	var b strings.Builder
	for i, q := range perguntas {
		fmt.Fprintf(&b, "%d. %s\n", i+1, strings.TrimSpace(q.Pergunta))
		resposta := strings.TrimSpace(q.Resposta)
		if resposta == "" {
			if s := strings.TrimSpace(q.Sugestao); s != "" {
				resposta = s + " (sugestão do analista, não confirmada)"
			} else {
				resposta = "(sem resposta)"
			}
		}
		fmt.Fprintf(&b, "   Resposta: %s\n", resposta)
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

// registrarFalaPlanejador grava a fala do planejador no chat, com meta anexando
// custo e motor (a UI usa).
func (p *Planejador) registrarFalaPlanejador(ctx context.Context, dem db.Demanda, saida SaidaPlanejador, motorUsado string, custo float64, nFases int) {
	texto := fmt.Sprintf("Gerei o plano com %d fase(s) — revise na aba Plano & Fases, ajuste se precisar e aprove para executar.", nFases)
	meta, err := json.Marshal(map[string]any{
		"custo_usd": custo,
		"motor":     motorUsado,
		"modelo":    p.Modelo,
		"fases":     nFases,
	})
	if err != nil {
		meta = []byte("{}")
	}
	if _, err := p.Store.CriarMensagemChat(ctx, db.MensagemChat{
		DemandID: dem.ID, Papel: db.PapelPlanejador, Conteudo: texto, Meta: meta,
	}); err != nil {
		p.registrarEvento(dem, "aviso", "Praxis: falha ao registrar fala do planejador", err.Error())
	}
}

// falhar carimba a demanda como `falhou` com o motivo e devolve nil (desfecho
// lógico já persistido; não é erro de infraestrutura para o chamador).
func (p *Planejador) falhar(ctx context.Context, dem db.Demanda, motivo string) error {
	dem.Erro = motivo
	p.marcarStatus(ctx, &dem, db.StatusDemandaFalhou)
	p.registrarEvento(dem, "planejamento_falhou", "Praxis: planejamento falhou", motivo)
	return nil
}

// marcarStatus persiste o novo status da demanda (best-effort) mantendo dem espelhado.
func (p *Planejador) marcarStatus(ctx context.Context, dem *db.Demanda, status string) {
	dem.Status = status
	atual, err := p.Store.AtualizarDemanda(ctx, *dem)
	if err != nil {
		p.registrarEvento(*dem, "aviso", "Praxis: falha ao atualizar status da demanda", err.Error())
		return
	}
	*dem = atual
}

// fecharExecComErro marca a execução como erro (best-effort) quando o run nem chegou a rodar.
func (p *Planejador) fecharExecComErro(ctx context.Context, exec db.Execucao) {
	exec.IsError = true
	exec.TerminadoEm = p.agoraISO()
	_, _ = p.Store.AtualizarExecucao(ctx, exec)
}

// registrarEvento grava um evento da demanda (best-effort).
func (p *Planejador) registrarEvento(dem db.Demanda, tipo, titulo, detalhe string) {
	pid, did := dem.ProjectID, dem.ID
	ev := db.Evento{Tipo: tipo, Titulo: titulo, Detalhe: detalhe}
	if pid > 0 {
		ev.ProjectID = &pid
	}
	if did > 0 {
		ev.DemandID = &did
	}
	_, _ = p.Store.RegistrarEvento(context.Background(), ev)
}

func (p *Planejador) selecionar(nome string) (motor.Motor, error) {
	if p.Selecionar != nil {
		return p.Selecionar(nome)
	}
	return motor.Selecionar(nome)
}

func (p *Planejador) prompt(ctx context.Context, nome string) (string, error) {
	if p.Prompt != nil {
		return p.Prompt(ctx, nome)
	}
	return ResolverPrompt(ctx, p.Store, nome)
}

func (p *Planejador) agora() time.Time {
	if p.Agora != nil {
		return p.Agora()
	}
	return time.Now()
}

// agoraISO devolve o horário atual em ISO-8601 UTC, no formato dos timestamps do banco.
func (p *Planejador) agoraISO() string {
	return p.agora().UTC().Format("2006-01-02T15:04:05.000Z")
}
