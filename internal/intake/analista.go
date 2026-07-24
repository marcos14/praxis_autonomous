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

// SchemaAnalista é o JSON Schema da saída do analista: um resumo do que o código
// mostra, os arquivos que a implementação provavelmente toca e as perguntas
// estruturadas para o usuário. Motores com schema nativo (claude/codex) o usam;
// os demais recebem o schema no prompt (motor.promptComSchema).
const SchemaAnalista = `{"type":"object","required":["resumo","perguntas"],"properties":{
"resumo":{"type":"string"},
"arquivos_provaveis":{"type":"array","items":{"type":"string"}},
"perguntas":{"type":"array","items":{"type":"object","required":["pergunta"],"properties":{
  "pergunta":{"type":"string"},
  "contexto":{"type":"string"},
  "tipo":{"type":"string","enum":["escolha","texto"]},
  "opcoes":{"type":"array","items":{"type":"string"}},
  "sugestao":{"type":"string"},
  "impacto":{"type":"string","enum":["alto","medio","baixo"]}}}}}}`

// PerguntaAnalista é uma pergunta na saída do analista (antes de virar db.Pergunta).
type PerguntaAnalista struct {
	Pergunta string   `json:"pergunta"`
	Contexto string   `json:"contexto"`
	Tipo     string   `json:"tipo"`
	Opcoes   []string `json:"opcoes"`
	Sugestao string   `json:"sugestao"`
	Impacto  string   `json:"impacto"`
}

// SaidaAnalista é a saída estruturada do analista.
type SaidaAnalista struct {
	Resumo            string             `json:"resumo"`
	ArquivosProvaveis []string           `json:"arquivos_provaveis"`
	Perguntas         []PerguntaAnalista `json:"perguntas"`
}

// Analista roda o harness em modo somente leitura sobre o código do projeto para
// gerar perguntas estruturadas a partir do PRD (Fase 3b). É o MECANISMO: todas as
// dependências chegam explícitas (espelha o ContextoExec do pipeline). O
// scheduler/serve resolve config do banco e monta este struct (ver Servico).
//
// O analista NÃO edita nada: roda com SomenteLeitura no diretório do repo
// principal do projeto (não num worktree — a análise antecede a branch da demanda).
type Analista struct {
	Store *db.DB // fila/estado no banco (demanda, chat, perguntas, runs, eventos)

	// Parâmetros do run resolvidos (pelo Servico, a partir do banco):
	Motor      string   // nome base do motor (claude/codex/opencode)
	Modelo     string   // modelo de análise; vazio → default do motor
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

// Analisar conduz a análise da demanda: transita recebida→analisando, roda o
// harness readonly com o PRD (montado do chat), decodifica a saída, persiste as
// perguntas e a fala do analista no chat, e transita analisando→aguardando_respostas.
//
// O erro de retorno é reservado a falhas de infraestrutura/harness. Nesses casos
// a demanda é carimbada `falhou` com o motivo (a UI mostra e o usuário pode
// complementar o PRD e replanejar). Sucesso deixa a demanda pronta para respostas.
func (a *Analista) Analisar(ctx context.Context, demandaID int64) error {
	dem, err := a.Store.ObterDemanda(ctx, demandaID)
	if err != nil {
		return fmt.Errorf("analista: obter demanda %d: %w", demandaID, err)
	}
	// Só analisa demandas que estão esperando análise. Reanálise (após
	// complemento) parte de aguardando_respostas; ambas são aceitas.
	if dem.Status != db.StatusDemandaRecebida && dem.Status != db.StatusDemandaAnalisando &&
		dem.Status != db.StatusDemandaAguardandoRespostas {
		return fmt.Errorf("analista: demanda %d não está em análise (status %q)", demandaID, dem.Status)
	}

	prd, err := a.montarPRD(ctx, demandaID)
	if err != nil {
		return err
	}
	if strings.TrimSpace(prd) == "" {
		return a.falhar(ctx, dem, "análise sem PRD: a demanda não tem mensagem do usuário para analisar")
	}

	// recebida → analisando
	a.marcarStatus(ctx, &dem, db.StatusDemandaAnalisando)
	a.registrarEvento(dem, "analise_iniciada", "Praxis: análise iniciada",
		fmt.Sprintf("Analista (%s%s), modo somente leitura.", a.Motor, sufixoModelo(a.Modelo)))

	res, motorUsado, custo, err := a.rodar(ctx, dem, prd)
	if err != nil {
		return a.falhar(ctx, dem, fmt.Sprintf("análise falhou: %v", err))
	}
	if res.IsError {
		return a.falhar(ctx, dem, motivoRunErro("análise", res))
	}

	var saida SaidaAnalista
	if err := motor.DecodificarEstruturado(res, &saida); err != nil {
		return a.falhar(ctx, dem, fmt.Sprintf("análise não devolveu JSON válido (%v) — log: %s", err, res.LogPath))
	}

	perguntas := make([]db.Pergunta, 0, len(saida.Perguntas))
	for _, p := range saida.Perguntas {
		perguntas = append(perguntas, db.Pergunta{
			Pergunta: strings.TrimSpace(p.Pergunta),
			Contexto: strings.TrimSpace(p.Contexto),
			Tipo:     strings.ToLower(strings.TrimSpace(p.Tipo)),
			Opcoes:   p.Opcoes,
			Sugestao: strings.TrimSpace(p.Sugestao),
			Impacto:  strings.ToLower(strings.TrimSpace(p.Impacto)),
		})
	}
	if _, err := a.Store.SubstituirPerguntas(ctx, demandaID, perguntas); err != nil {
		return fmt.Errorf("analista: persistir perguntas: %w", err)
	}

	a.registrarFalaAnalista(ctx, dem, saida, motorUsado, custo, len(perguntas))

	// analisando → aguardando_respostas
	a.marcarStatus(ctx, &dem, db.StatusDemandaAguardandoRespostas)
	a.registrarEvento(dem, "analise_concluida", "Praxis: análise concluída",
		fmt.Sprintf("%d pergunta(s) gerada(s) · custo US$ %.2f", len(perguntas), custo))
	return nil
}

// rodar executa o harness readonly com o prompt do analista e devolve o
// resultado, o motor efetivamente usado e o custo. Registra a execução no banco
// (operacao=analista, sem fase).
func (a *Analista) rodar(ctx context.Context, dem db.Demanda, prd string) (*motor.ResultadoRun, string, float64, error) {
	tpl, err := a.prompt(ctx, PromptAnalista)
	if err != nil {
		return nil, a.Motor, 0, err
	}

	exec, err := a.Store.CriarExecucao(ctx, db.Execucao{
		DemandID: dem.ID, Operacao: db.OperacaoAnalista, Engine: a.Motor, Conta: a.Conta, Modelo: a.Modelo,
	})
	if err != nil {
		return nil, a.Motor, 0, fmt.Errorf("registrar execução: %w", err)
	}

	m, err := a.selecionar(a.Motor)
	if err != nil {
		a.fecharExecComErro(ctx, exec)
		return nil, a.Motor, 0, err
	}
	res, runErr := m.Rodar(motor.OpcoesRun{
		Dir: a.Dir, DirLogs: a.DirLogs, Prompt: renderPrompt(tpl, map[string]string{"PRD": prd}),
		Modelo: a.Modelo, Esforco: a.Esforco, PerfilDir: a.ConfigDir,
		AddDirs: a.AddDirs, BudgetUSD: a.BudgetUSD, TimeoutMin: a.TimeoutMin,
		Schema: SchemaAnalista, SomenteLeitura: true, ProibirCommit: true,
		RotuloLog: "analista", Ctx: ctx,
		// log_ref no início do run: o log ao vivo (SSE) acompanha a análise em
		// andamento em vez de esperar o run fechar.
		OnLogPath: func(caminho string) {
			exec.LogRef = caminho
			_, _ = a.Store.AtualizarExecucao(ctx, exec)
		},
	})

	custo := 0.0
	exec.TerminadoEm = a.agoraISO()
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
	if _, err := a.Store.AtualizarExecucao(ctx, exec); err != nil {
		a.registrarEvento(dem, "aviso", "Praxis: falha ao registrar execução do analista", err.Error())
	}
	if runErr != nil {
		return res, m.Nome(), custo, runErr
	}
	return res, m.Nome(), custo, nil
}

// montarPRD concatena as falas do usuário no chat (PRD + complementos) em ordem
// cronológica — é a entrada do analista.
func (a *Analista) montarPRD(ctx context.Context, demandaID int64) (string, error) {
	msgs, err := a.Store.ListarMensagensChat(ctx, demandaID)
	if err != nil {
		return "", fmt.Errorf("analista: ler chat da demanda %d: %w", demandaID, err)
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

// registrarFalaAnalista grava a fala do analista no chat (resumo + destaque das
// perguntas), com meta anexando arquivos_provaveis, custo e motor (a UI usa).
func (a *Analista) registrarFalaAnalista(ctx context.Context, dem db.Demanda, saida SaidaAnalista, motorUsado string, custo float64, nPerguntas int) {
	texto := strings.TrimSpace(saida.Resumo)
	if nPerguntas > 0 {
		texto = strings.TrimSpace(texto + fmt.Sprintf("\n\nGerei %d pergunta(s) — veja a aba Perguntas.", nPerguntas))
	} else if texto != "" {
		texto += "\n\nSem perguntas: a demanda está clara o suficiente para planejar."
	} else {
		texto = "Análise concluída."
	}

	meta, err := json.Marshal(map[string]any{
		"arquivos_provaveis": limparStrings(saida.ArquivosProvaveis),
		"custo_usd":          custo,
		"motor":              motorUsado,
		"modelo":             a.Modelo,
	})
	if err != nil {
		meta = []byte("{}")
	}
	if _, err := a.Store.CriarMensagemChat(ctx, db.MensagemChat{
		DemandID: dem.ID, Papel: db.PapelAnalista, Conteudo: texto, Meta: meta,
	}); err != nil {
		a.registrarEvento(dem, "aviso", "Praxis: falha ao registrar fala do analista", err.Error())
	}
}

// falhar carimba a demanda como `falhou` com o motivo e devolve nil (o desfecho
// lógico já foi persistido; não é erro de infraestrutura para o chamador).
func (a *Analista) falhar(ctx context.Context, dem db.Demanda, motivo string) error {
	dem.Erro = motivo
	a.marcarStatus(ctx, &dem, db.StatusDemandaFalhou)
	a.registrarEvento(dem, "analise_falhou", "Praxis: análise falhou", motivo)
	return nil
}

// marcarStatus persiste o novo status da demanda (best-effort) mantendo dem
// espelhado.
func (a *Analista) marcarStatus(ctx context.Context, dem *db.Demanda, status string) {
	dem.Status = status
	atual, err := a.Store.AtualizarDemanda(ctx, *dem)
	if err != nil {
		a.registrarEvento(*dem, "aviso", "Praxis: falha ao atualizar status da demanda", err.Error())
		return
	}
	*dem = atual
}

// fecharExecComErro marca a execução como erro (best-effort) quando o run nem chegou a rodar.
func (a *Analista) fecharExecComErro(ctx context.Context, exec db.Execucao) {
	exec.IsError = true
	exec.TerminadoEm = a.agoraISO()
	_, _ = a.Store.AtualizarExecucao(ctx, exec)
}

// registrarEvento grava um evento da demanda (best-effort).
func (a *Analista) registrarEvento(dem db.Demanda, tipo, titulo, detalhe string) {
	pid, did := dem.ProjectID, dem.ID
	ev := db.Evento{Tipo: tipo, Titulo: titulo, Detalhe: detalhe}
	if pid > 0 {
		ev.ProjectID = &pid
	}
	if did > 0 {
		ev.DemandID = &did
	}
	_, _ = a.Store.RegistrarEvento(context.Background(), ev)
}

func (a *Analista) selecionar(nome string) (motor.Motor, error) {
	if a.Selecionar != nil {
		return a.Selecionar(nome)
	}
	return motor.Selecionar(nome)
}

func (a *Analista) prompt(ctx context.Context, nome string) (string, error) {
	if a.Prompt != nil {
		return a.Prompt(ctx, nome)
	}
	return ResolverPrompt(ctx, a.Store, nome)
}

func (a *Analista) agora() time.Time {
	if a.Agora != nil {
		return a.Agora()
	}
	return time.Now()
}

// agoraISO devolve o horário atual em ISO-8601 UTC, no formato dos timestamps do banco.
func (a *Analista) agoraISO() string {
	return a.agora().UTC().Format("2006-01-02T15:04:05.000Z")
}

// limparStrings apara e descarta strings vazias, garantindo slice não-nil (para
// serializar como [] e não null no meta do chat).
func limparStrings(itens []string) []string {
	out := []string{}
	for _, it := range itens {
		if s := strings.TrimSpace(it); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// motivoRunErro descreve o desfecho de erro de um run do harness para o campo
// erro da demanda, com uma dica acionável quando o run estourou o teto de custo
// (subtipos como error_max_budget_usd): o usuário pode aumentar o budget do
// motor e usar "Tentar novamente" no card — o novo run relê a config do banco.
func motivoRunErro(etapa string, res *motor.ResultadoRun) string {
	motivo := fmt.Sprintf("%s terminou com erro (%s) — log: %s", etapa, motor.ResumoErro(res), res.LogPath)
	if strings.Contains(res.Subtipo, "max_budget") {
		motivo += ` · o run atingiu o teto de custo (budget): aumente o budget do motor na tela Motores e clique em "Tentar novamente" no card da demanda`
	}
	if strings.Contains(res.Subtipo, "structured_output") {
		motivo += ` · o motor concluiu o trabalho mas não conseguiu formatar a resposta estruturada, mesmo após o resgate automático da sessão — falha transitória do harness: clique em "Tentar novamente" no card da demanda`
	}
	return motivo
}

// sufixoModelo formata " / modelo" quando há modelo (para mensagens).
func sufixoModelo(modelo string) string {
	if strings.TrimSpace(modelo) == "" {
		return ""
	}
	return "/" + modelo
}
