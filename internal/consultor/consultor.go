package consultor

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/marcos14/praxis-autonomous/internal/db"
	"github.com/marcos14/praxis-autonomous/internal/intake"
	"github.com/marcos14/praxis-autonomous/internal/motor"
)

// SchemaConsultor é o JSON Schema da saída de um turno do consultor: ou
// perguntas de clarificação (quando a dúvida do usuário está ambígua), ou a
// resposta final em linguagem de negócio, ou uma recusa (pedido de bypass/
// exploração ou de código-fonte).
const SchemaConsultor = `{"type":"object","required":["tipo"],"properties":{
"tipo":{"type":"string","enum":["perguntas","resposta","recusa"]},
"perguntas":{"type":"array","items":{"type":"object","required":["pergunta"],"properties":{
  "pergunta":{"type":"string"},
  "contexto":{"type":"string"}}}},
"resposta_md":{"type":"string"},
"motivo_recusa":{"type":"string"},
"confianca":{"type":"string","enum":["alta","media","baixa"]},
"rotinas_citadas":{"type":"array","items":{"type":"string"}}}}`

// Tipos de turno do consultor (campo tipo da saída estruturada, espelhado no
// meta da fala persistida).
const (
	TurnoPerguntas = "perguntas"
	TurnoResposta  = "resposta"
	TurnoRecusa    = "recusa"
)

// PerguntaConsultor é uma pergunta de clarificação na saída do consultor.
type PerguntaConsultor struct {
	Pergunta string `json:"pergunta"`
	Contexto string `json:"contexto"`
}

// SaidaConsultor é a saída estruturada de um turno do consultor.
type SaidaConsultor struct {
	Tipo           string              `json:"tipo"`
	Perguntas      []PerguntaConsultor `json:"perguntas"`
	RespostaMD     string              `json:"resposta_md"`
	MotivoRecusa   string              `json:"motivo_recusa"`
	Confianca      string              `json:"confianca"`
	RotinasCitadas []string            `json:"rotinas_citadas"`
}

// Consultor roda um turno da conversa de consulta: harness em modo somente
// leitura sobre o(s) repositório(s), com o histórico completo da conversa (o
// motor é stateless entre turnos) e os overviews como contexto. É o MECANISMO —
// todas as dependências chegam explícitas (espelha o Analista do intake); o
// Servico resolve config do banco e monta este struct.
type Consultor struct {
	Store *db.DB

	// Parâmetros do run resolvidos (pelo Servico, a partir do banco):
	Motor      string   // nome base do motor (claude/codex/opencode)
	Modelo     string   // modelo de análise; vazio → default do motor
	Esforco    string   // esforço; vazio → default do motor
	Conta      string   // alias do perfil usado (registro no run); "" = sem conta
	ConfigDir  string   // diretório isolado do perfil; "" = perfil padrão do CLI
	Dir        string   // raiz do repo principal (cmd.Dir do harness — só leitura)
	DirLogs    string   // pasta dos .jsonl das execuções
	AddDirs    []string // repos/diretórios extras liberados (só leitura)
	BudgetUSD  float64  // teto de custo do turno (0 = sem teto)
	TimeoutMin int      // timeout do turno

	// ContextoRepos é o bloco de contexto injetado no prompt: overview(s) do(s)
	// repositório(s) e, em consulta de grupo, a descrição da solução.
	ContextoRepos string

	// Seams de teste (nil em produção):
	Selecionar func(nome string) (motor.Motor, error)
	Prompt     func(ctx context.Context, nome string) (string, error)
	Agora      func() time.Time
}

// Responder conduz um turno: marca a consulta como pensando, roda o harness
// readonly com o histórico + contexto, decodifica a saída, aplica o pós-filtro
// anti-código e persiste a fala do consultor, devolvendo a consulta a ociosa.
//
// Como no Analista, o desfecho lógico é sempre persistido: falha de infra
// carimba a consulta como falhou (com fala de sistema explicando) e devolve nil.
func (c *Consultor) Responder(ctx context.Context, consultaID int64) error {
	cons, err := c.Store.ObterConsulta(ctx, consultaID)
	if err != nil {
		return fmt.Errorf("consultor: obter consulta %d: %w", consultaID, err)
	}

	historico, err := c.montarHistorico(ctx, consultaID)
	if err != nil {
		return err
	}
	if strings.TrimSpace(historico) == "" {
		return c.falhar(ctx, cons, "consulta sem mensagem do usuário para responder")
	}

	cons.Status = db.StatusConsultaPensando
	cons.Erro = ""
	if atual, err := c.Store.AtualizarConsulta(ctx, cons); err == nil {
		cons = atual
	}

	res, motorUsado, custo, err := c.rodar(ctx, cons, historico)
	if err != nil {
		return c.falhar(ctx, cons, fmt.Sprintf("turno do consultor falhou: %v", err))
	}
	if res.IsError {
		return c.falhar(ctx, cons, fmt.Sprintf("turno terminou com erro (%s)", motor.ResumoErro(res)))
	}

	var saida SaidaConsultor
	if err := motor.DecodificarEstruturado(res, &saida); err != nil {
		return c.falhar(ctx, cons, fmt.Sprintf("turno não devolveu JSON válido (%v)", err))
	}

	conteudo, meta := c.montarFala(saida, motorUsado, custo)
	if _, err := c.Store.CriarMensagemConsulta(ctx, db.MensagemConsulta{
		ConsultaID: cons.ID, Papel: db.PapelConsultaConsultor, Conteudo: conteudo, Meta: meta,
	}); err != nil {
		return c.falhar(ctx, cons, fmt.Sprintf("persistir fala do consultor: %v", err))
	}

	cons.Status = db.StatusConsultaOciosa
	cons.CustoUSD += custo
	if _, err := c.Store.AtualizarConsulta(ctx, cons); err != nil {
		return fmt.Errorf("consultor: atualizar consulta %d: %w", cons.ID, err)
	}
	c.registrarEvento(cons, "consulta_respondida", "Praxis: consulta respondida",
		fmt.Sprintf("Turno %s · custo US$ %.2f", saida.Tipo, custo))
	return nil
}

// montarFala converte a saída estruturada na fala persistida do consultor,
// aplicando o pós-filtro anti-código (a rede de segurança que não depende do
// prompt). Devolve o conteúdo e o meta JSON.
func (c *Consultor) montarFala(saida SaidaConsultor, motorUsado string, custo float64) (string, json.RawMessage) {
	tipo := strings.ToLower(strings.TrimSpace(saida.Tipo))
	var (
		conteudo  string
		redigidos int
	)
	switch tipo {
	case TurnoPerguntas:
		conteudo, redigidos = formatarPerguntas(saida.Perguntas)
		if conteudo == "" {
			// Perguntas vazias/filtradas: trata como resposta vazia — recusa.
			tipo = TurnoRecusa
			conteudo = MensagemRecusaFiltro
		}
	case TurnoRecusa:
		limpo, n, recusar := Sanitizar(strings.TrimSpace(saida.MotivoRecusa))
		redigidos = n
		if recusar || strings.TrimSpace(limpo) == "" {
			conteudo = "Não posso ajudar com esse pedido: ele foge do que a consultoria cobre " +
				"(entender o comportamento do sistema, sem expor detalhes internos)."
		} else {
			conteudo = limpo
		}
	default: // resposta (e tipos desconhecidos caem no caminho mais protegido)
		limpo, n, recusar := Sanitizar(strings.TrimSpace(saida.RespostaMD))
		redigidos = n
		if recusar || strings.TrimSpace(limpo) == "" {
			tipo = TurnoRecusa
			conteudo = MensagemRecusaFiltro
		} else {
			tipo = TurnoResposta
			conteudo = limpo
		}
	}

	meta, err := json.Marshal(map[string]any{
		"tipo":            tipo,
		"custo_usd":       custo,
		"motor":           motorUsado,
		"modelo":          c.Modelo,
		"confianca":       strings.ToLower(strings.TrimSpace(saida.Confianca)),
		"redigido":        redigidos,
		"rotinas_citadas": limparStrings(saida.RotinasCitadas),
	})
	if err != nil {
		meta = []byte("{}")
	}
	return conteudo, meta
}

// formatarPerguntas monta o texto da fala de clarificação (perguntas numeradas,
// cada uma passada pelo pós-filtro). Devolve também o total de redações.
func formatarPerguntas(perguntas []PerguntaConsultor) (string, int) {
	var (
		linhas    []string
		redigidos int
		n         int
	)
	for _, p := range perguntas {
		texto := strings.TrimSpace(p.Pergunta)
		if texto == "" {
			continue
		}
		limpo, r, recusar := Sanitizar(texto)
		redigidos += r
		if recusar || strings.TrimSpace(limpo) == "" {
			continue
		}
		n++
		linha := fmt.Sprintf("%d. %s", n, strings.TrimSpace(limpo))
		if ctxTexto := strings.TrimSpace(p.Contexto); ctxTexto != "" {
			if limpoCtx, r2, recusarCtx := Sanitizar(ctxTexto); !recusarCtx && strings.TrimSpace(limpoCtx) != "" {
				redigidos += r2
				linha += "\n   _" + strings.TrimSpace(limpoCtx) + "_"
			}
		}
		linhas = append(linhas, linha)
	}
	if len(linhas) == 0 {
		return "", redigidos
	}
	return "Para te responder com precisão, preciso entender melhor:\n\n" +
		strings.Join(linhas, "\n"), redigidos
}

// rodar executa o harness readonly com o prompt do consultor e devolve o
// resultado, o motor efetivamente usado e o custo. Registra a execução em
// consulta_runs (operacao=consultor).
func (c *Consultor) rodar(ctx context.Context, cons db.Consulta, historico string) (*motor.ResultadoRun, string, float64, error) {
	tpl, err := c.prompt(ctx, intake.PromptConsultor)
	if err != nil {
		return nil, c.Motor, 0, err
	}

	exec, err := c.Store.CriarExecucaoConsulta(ctx, db.ExecucaoConsulta{
		ConsultaID: &cons.ID, Operacao: db.OperacaoConsultor, Engine: c.Motor, Conta: c.Conta, Modelo: c.Modelo,
	})
	if err != nil {
		return nil, c.Motor, 0, fmt.Errorf("registrar execução: %w", err)
	}

	m, err := c.selecionar(c.Motor)
	if err != nil {
		c.fecharExecComErro(ctx, exec)
		return nil, c.Motor, 0, err
	}
	prompt := renderPrompt(tpl, map[string]string{
		"CONTEXTO_REPOS": c.ContextoRepos,
		"HISTORICO":      historico,
	})
	res, runErr := m.Rodar(motor.OpcoesRun{
		Dir: c.Dir, DirLogs: c.DirLogs, Prompt: prompt,
		Modelo: c.Modelo, Esforco: c.Esforco, PerfilDir: c.ConfigDir,
		AddDirs: c.AddDirs, BudgetUSD: c.BudgetUSD, TimeoutMin: c.TimeoutMin,
		Schema: SchemaConsultor, SomenteLeitura: true, ProibirCommit: true,
		RotuloLog: fmt.Sprintf("consultor-c%d", cons.ID), Ctx: ctx,
	})

	custo := 0.0
	exec.TerminadoEm = c.agoraISO()
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
	if _, err := c.Store.AtualizarExecucaoConsulta(ctx, exec); err != nil {
		c.registrarEvento(cons, "aviso", "Praxis: falha ao registrar execução do consultor", err.Error())
	}
	if runErr != nil {
		return res, m.Nome(), custo, runErr
	}
	return res, m.Nome(), custo, nil
}

// montarHistorico concatena a conversa (falas do usuário e do consultor, em
// ordem) rotulada para o prompt — o motor é stateless, então cada turno recebe
// a conversa inteira.
func (c *Consultor) montarHistorico(ctx context.Context, consultaID int64) (string, error) {
	msgs, err := c.Store.ListarMensagensConsulta(ctx, consultaID)
	if err != nil {
		return "", fmt.Errorf("consultor: ler chat da consulta %d: %w", consultaID, err)
	}
	partes := make([]string, 0, len(msgs))
	for _, m := range msgs {
		texto := strings.TrimSpace(m.Conteudo)
		if texto == "" {
			continue
		}
		switch m.Papel {
		case db.PapelConsultaUser:
			partes = append(partes, "Usuário: "+texto)
		case db.PapelConsultaConsultor:
			partes = append(partes, "Consultor: "+texto)
		}
	}
	return strings.Join(partes, "\n\n"), nil
}

// falhar carimba a consulta como falhou com o motivo, registra uma fala de
// sistema (a UI mostra na conversa) e devolve nil — o desfecho lógico já foi
// persistido; não é erro de infraestrutura para o chamador.
func (c *Consultor) falhar(ctx context.Context, cons db.Consulta, motivo string) error {
	cons.Status = db.StatusConsultaFalhou
	cons.Erro = motivo
	if _, err := c.Store.AtualizarConsulta(ctx, cons); err != nil {
		return fmt.Errorf("consultor: carimbar falha da consulta %d: %w", cons.ID, err)
	}
	_, _ = c.Store.CriarMensagemConsulta(ctx, db.MensagemConsulta{
		ConsultaID: cons.ID, Papel: db.PapelConsultaSistema,
		Conteudo: "O turno falhou: " + motivo + "\n\nVocê pode reenviar a pergunta para tentar de novo.",
	})
	c.registrarEvento(cons, "consulta_falhou", "Praxis: consulta falhou", motivo)
	return nil
}

// registrarEvento grava um evento da consulta (best-effort). O evento carrega o
// project_id quando a consulta é de projeto (consultas de grupo ficam sem
// vínculo — a tabela events só conhece projeto/demanda).
func (c *Consultor) registrarEvento(cons db.Consulta, tipo, titulo, detalhe string) {
	ev := db.Evento{Tipo: tipo, Titulo: titulo, Detalhe: detalhe}
	if cons.ProjectID != nil {
		ev.ProjectID = cons.ProjectID
	}
	_, _ = c.Store.RegistrarEvento(context.Background(), ev)
}

// fecharExecComErro marca a execução como erro (best-effort) quando o run nem
// chegou a rodar.
func (c *Consultor) fecharExecComErro(ctx context.Context, exec db.ExecucaoConsulta) {
	exec.IsError = true
	exec.TerminadoEm = c.agoraISO()
	_, _ = c.Store.AtualizarExecucaoConsulta(ctx, exec)
}

func (c *Consultor) selecionar(nome string) (motor.Motor, error) {
	if c.Selecionar != nil {
		return c.Selecionar(nome)
	}
	return motor.Selecionar(nome)
}

func (c *Consultor) prompt(ctx context.Context, nome string) (string, error) {
	if c.Prompt != nil {
		return c.Prompt(ctx, nome)
	}
	return intake.ResolverPrompt(ctx, c.Store, nome)
}

func (c *Consultor) agora() time.Time {
	if c.Agora != nil {
		return c.Agora()
	}
	return time.Now()
}

// agoraISO devolve o horário atual em ISO-8601 UTC, no formato dos timestamps do banco.
func (c *Consultor) agoraISO() string {
	return c.agora().UTC().Format("2006-01-02T15:04:05.000Z")
}

// renderPrompt substitui os marcadores {VAR} do template pelos valores (mesma
// semântica do renderPrompt do intake/pipeline).
func renderPrompt(tpl string, valores map[string]string) string {
	pares := make([]string, 0, len(valores)*2)
	for k, v := range valores {
		pares = append(pares, "{"+k+"}", v)
	}
	return strings.NewReplacer(pares...).Replace(tpl)
}

// limparStrings apara e descarta strings vazias, garantindo slice não-nil.
func limparStrings(itens []string) []string {
	out := []string{}
	for _, it := range itens {
		if s := strings.TrimSpace(it); s != "" {
			out = append(out, s)
		}
	}
	return out
}
