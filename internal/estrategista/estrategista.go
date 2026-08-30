// Pacote estrategista implementa a feature de planejamentos: um especialista de
// produto/arquitetura (harness lendo os repos em modo somente leitura) que
// lapida PRDs e ADRs em conversa iterativa com PMs/POs/arquitetos, escrevendo
// os documentos canônicos (.md) e os artefatos visuais (.html autocontidos) na
// pasta do planejamento (PRAXIS_HOME/planejamentos/p<id>).
//
// Diferenças deliberadas em relação ao consultor:
//   - SEM o pós-filtro anti-código: ADRs citam componentes, caminhos e
//     trade-offs de implementação por definição (a permissão planejamentos.usar
//     reflete essa exposição);
//   - o harness ESCREVE — mas só na pasta do planejamento. Os repos entram como
//     AddDirs e DirsProtegidos (negação por motor) e a rede de segurança final
//     é a comparação do `git status` de cada repo antes/depois do turno.
package estrategista

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/marcos14/praxis-autonomous/internal/db"
	"github.com/marcos14/praxis-autonomous/internal/i18n"
	"github.com/marcos14/praxis-autonomous/internal/intake"
	"github.com/marcos14/praxis-autonomous/internal/motor"
	"github.com/marcos14/praxis-autonomous/internal/referencias"
)

// SchemaEstrategista é o JSON Schema da saída de um turno do estrategista: ou
// perguntas de clarificação (necessidade ambígua), ou a resposta do turno (o
// resumo do que mudou nos documentos/artefatos).
const SchemaEstrategista = `{"type":"object","required":["tipo"],"properties":{
"tipo":{"type":"string","enum":["perguntas","resposta"]},
"perguntas":{"type":"array","items":{"type":"object","required":["pergunta"],"properties":{
  "pergunta":{"type":"string"},
  "contexto":{"type":"string"}}}},
"resposta_md":{"type":"string"},
"documentos_alterados":{"type":"array","items":{"type":"string"}},
"artefatos":{"type":"array","items":{"type":"object","required":["arquivo"],"properties":{
  "arquivo":{"type":"string"},
  "titulo":{"type":"string"},
  "descricao":{"type":"string"}}}},
"confianca":{"type":"string","enum":["alta","media","baixa"]}}}`

// Tipos de turno do estrategista (campo tipo da saída estruturada).
const (
	TurnoPerguntas = "perguntas"
	TurnoResposta  = "resposta"
)

// Documentos canônicos que o estrategista mantém na pasta de trabalho. A
// ingestão pós-turno lê estes arquivos e versiona no banco o que mudou.
var documentosConhecidos = []string{"prd.md", "adrs.md"}

// nomeArtefatoValido limita os artefatos a .html simples na raiz da pasta
// (sem subpastas, sem caracteres de caminho) — é o mesmo filtro que o
// file-server da API aplica antes de servir.
var nomeArtefatoValido = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*\.html$`)

// NomeArtefatoValido informa se nome é um artefato servível (html simples na
// raiz da pasta do planejamento).
func NomeArtefatoValido(nome string) bool { return nomeArtefatoValido.MatchString(nome) }

// DirReferencias é a subpasta da pasta do planejamento onde ficam os documentos
// de referência anexados pelo usuário (ADRs de outros projetos, transcrições de
// reunião, rascunhos…). O harness a lê como insumo; a ingestão de artefatos a
// ignora (só varre a raiz). As regras são as do pacote referencias, comuns a
// planejamentos e consultas.
const DirReferencias = referencias.Dir

// NomeReferenciaValido informa se nome é um arquivo de referência aceitável
// (ver referencias.NomeValido).
func NomeReferenciaValido(nome string) bool { return referencias.NomeValido(nome) }

// PerguntaEstrategista é uma pergunta de clarificação na saída do estrategista.
type PerguntaEstrategista struct {
	Pergunta string `json:"pergunta"`
	Contexto string `json:"contexto"`
}

// ArtefatoDeclarado é a declaração de um artefato na saída do turno (título e
// descrição para a interface; o conteúdo é lido do disco).
type ArtefatoDeclarado struct {
	Arquivo   string `json:"arquivo"`
	Titulo    string `json:"titulo"`
	Descricao string `json:"descricao"`
}

// SaidaEstrategista é a saída estruturada de um turno do estrategista.
type SaidaEstrategista struct {
	Tipo                string                 `json:"tipo"`
	Perguntas           []PerguntaEstrategista `json:"perguntas"`
	RespostaMD          string                 `json:"resposta_md"`
	DocumentosAlterados []string               `json:"documentos_alterados"`
	Artefatos           []ArtefatoDeclarado    `json:"artefatos"`
	Confianca           string                 `json:"confianca"`
}

// Estrategista roda um turno da conversa de planejamento: harness com escrita
// confinada à pasta do planejamento, repos em leitura, histórico completo da
// conversa como contexto (o motor é stateless entre turnos). É o MECANISMO —
// todas as dependências chegam explícitas (espelha o Consultor); o Servico
// resolve config do banco e monta este struct.
type Estrategista struct {
	Store *db.DB

	// Parâmetros do run resolvidos (pelo Servico, a partir do banco):
	Motor       string   // nome base do motor (claude/codex/opencode)
	Modelo      string   // modelo; vazio → default do motor
	Esforco     string   // esforço; vazio → default do motor
	Conta       string   // alias do perfil usado (registro no run); "" = sem conta
	ConfigDir   string   // diretório isolado do perfil; "" = perfil padrão do CLI
	DirTrabalho string   // pasta do planejamento (cmd.Dir do harness — gravável)
	DirLogs     string   // pasta dos .jsonl das execuções
	Repos       []string // pastas git dos projetos (leitura + rede de segurança)
	DirsExtras  []string // add_dirs extras dos projetos (leitura, sem git)
	BudgetUSD   float64  // teto de custo do turno (0 = sem teto)
	TimeoutMin  int      // timeout do turno
	Idioma      string   // idioma de saída da IA (preferência de quem criou o planejamento)

	// ContextoRepos é o bloco de contexto injetado no prompt: overview(s) do(s)
	// repositório(s) e, em planejamento de grupo, a descrição da solução.
	ContextoRepos string
	// Foco e NivelVisual do planejamento (renderizados como fragmentos de
	// instrução no prompt).
	Foco        string
	NivelVisual string

	// Seams de teste (nil em produção):
	Selecionar func(nome string) (motor.Motor, error)
	Prompt     func(ctx context.Context, nome string) (string, error)
	Agora      func() time.Time
	StatusRepo func(dir string) (string, error) // git status --porcelain (rede de segurança)
}

// Responder conduz um turno: marca o planejamento como pensando, roda o harness
// com escrita confinada, verifica a rede de segurança git, ingere documentos e
// artefatos da pasta e persiste a fala do estrategista, devolvendo o
// planejamento a ocioso.
//
// Como no Consultor, o desfecho lógico é sempre persistido: falha de infra
// carimba o planejamento como falhou (com fala de sistema explicando) e
// devolve nil.
func (e *Estrategista) Responder(ctx context.Context, planejamentoID int64) error {
	plan, err := e.Store.ObterPlanejamento(ctx, planejamentoID)
	if err != nil {
		return fmt.Errorf("estrategista: obter planejamento %d: %w", planejamentoID, err)
	}

	historico, err := e.montarHistorico(ctx, planejamentoID)
	if err != nil {
		return err
	}
	if strings.TrimSpace(historico) == "" {
		return e.falhar(ctx, plan, "planejamento sem mensagem do usuário para responder")
	}

	plan.Status = db.StatusPlanejamentoPensando
	plan.Erro = ""
	if atual, err := e.Store.AtualizarPlanejamento(ctx, plan); err == nil {
		plan = atual
	}

	if err := os.MkdirAll(e.DirTrabalho, 0o755); err != nil {
		return e.falhar(ctx, plan, fmt.Sprintf("criar a pasta do planejamento: %v", err))
	}

	antes := e.fotografarRepos()
	res, motorUsado, custo, err := e.rodar(ctx, plan, historico)
	if err != nil {
		return e.falhar(ctx, plan, fmt.Sprintf("turno do estrategista falhou: %v", err))
	}
	if res.IsError {
		return e.falhar(ctx, plan, fmt.Sprintf("turno terminou com erro (%s)", motor.ResumoErro(res)))
	}

	// Rede de segurança: nenhum turno pode sujar o working tree dos repos. Não
	// revertemos nada (a árvore é do desenvolvedor e pode ter trabalho dele em
	// paralelo) — detectamos, listamos e falhamos o turno para um humano olhar.
	if viol := violacoesRepos(antes, e.fotografarRepos()); len(viol) > 0 {
		return e.falhar(ctx, plan,
			"o turno modificou arquivos de repositório, o que é proibido no planejamento "+
				"(a escrita é permitida só na pasta do planejamento). Verifique e descarte as mudanças:\n"+
				strings.Join(viol, "\n"))
	}

	var saida SaidaEstrategista
	if err := motor.DecodificarEstruturado(res, &saida); err != nil {
		return e.falhar(ctx, plan, fmt.Sprintf("turno não devolveu JSON válido (%v)", err))
	}

	docs, err := e.ingerirDocumentos(ctx, plan.ID)
	if err != nil {
		return e.falhar(ctx, plan, fmt.Sprintf("versionar documentos do turno: %v", err))
	}
	arts, err := e.ingerirArtefatos(ctx, plan.ID, saida.Artefatos)
	if err != nil {
		return e.falhar(ctx, plan, fmt.Sprintf("indexar artefatos do turno: %v", err))
	}

	conteudo, meta := e.montarFala(saida, docs, arts, motorUsado, custo)
	if _, err := e.Store.CriarMensagemPlanejamento(ctx, db.MensagemPlanejamento{
		PlanejamentoID: plan.ID, Papel: db.PapelPlanejamentoEstrategista, Conteudo: conteudo, Meta: meta,
	}); err != nil {
		return e.falhar(ctx, plan, fmt.Sprintf("persistir fala do estrategista: %v", err))
	}

	plan.Status = db.StatusPlanejamentoOcioso
	plan.CustoUSD += custo
	if _, err := e.Store.AtualizarPlanejamento(ctx, plan); err != nil {
		return fmt.Errorf("estrategista: atualizar planejamento %d: %w", plan.ID, err)
	}
	e.registrarEvento(plan, "estrategia_respondida", "Praxis: planejamento respondido",
		fmt.Sprintf("Turno %s · %d documento(s), %d artefato(s) · custo US$ %.2f",
			saida.Tipo, len(docs), len(arts), custo))
	return nil
}

// rodar executa o harness com escrita confinada à pasta do planejamento e
// devolve o resultado, o motor efetivamente usado e o custo. Registra a
// execução em planejamento_runs (com o log_ref gravado NO INÍCIO do run, para o
// SSE de progresso acompanhar o turno em andamento).
func (e *Estrategista) rodar(ctx context.Context, plan db.Planejamento, historico string) (*motor.ResultadoRun, string, float64, error) {
	tpl, err := e.prompt(ctx, intake.PromptEstrategista)
	if err != nil {
		return nil, e.Motor, 0, err
	}

	exec, err := e.Store.CriarExecucaoPlanejamento(ctx, db.ExecucaoPlanejamento{
		PlanejamentoID: plan.ID, Engine: e.Motor, Conta: e.Conta, Modelo: e.Modelo,
	})
	if err != nil {
		return nil, e.Motor, 0, fmt.Errorf("registrar execução: %w", err)
	}

	m, err := e.selecionar(e.Motor)
	if err != nil {
		e.fecharExecComErro(ctx, exec)
		return nil, e.Motor, 0, err
	}
	prompt := renderPrompt(tpl, map[string]string{
		"CONTEXTO_REPOS": e.ContextoRepos,
		"HISTORICO":      historico,
		"FOCO":           fragmentoFoco(plan.Foco),
		"NIVEL_VISUAL":   fragmentoNivelVisual(plan.NivelVisual),
		"REFERENCIAS":    e.listarReferencias(),
		"IDIOMA":         i18n.NomeIdiomaOuInstancia(e.Idioma),
	})
	protegidos := append(append([]string{}, e.Repos...), e.DirsExtras...)
	res, runErr := m.Rodar(motor.OpcoesRun{
		Dir: e.DirTrabalho, DirLogs: e.DirLogs, Prompt: prompt,
		Modelo: e.Modelo, Esforco: e.Esforco, PerfilDir: e.ConfigDir,
		AddDirs: protegidos, DirsProtegidos: protegidos,
		BudgetUSD: e.BudgetUSD, TimeoutMin: e.TimeoutMin,
		Schema: SchemaEstrategista, ProibirCommit: true,
		RotuloLog: fmt.Sprintf("estrategista-p%d", plan.ID), Ctx: ctx,
		OnLogPath: func(caminho string) {
			exec.LogRef = caminho
			if _, err := e.Store.AtualizarExecucaoPlanejamento(ctx, exec); err == nil {
				return
			}
			// best-effort: o log_ref definitivo é gravado de novo no fechamento.
		},
	})

	custo := 0.0
	exec.TerminadoEm = e.agoraISO()
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
	if _, err := e.Store.AtualizarExecucaoPlanejamento(ctx, exec); err != nil {
		e.registrarEvento(plan, "aviso", "Praxis: falha ao registrar execução do estrategista", err.Error())
	}
	if runErr != nil {
		return res, m.Nome(), custo, runErr
	}
	return res, m.Nome(), custo, nil
}

// ingerirDocumentos lê os documentos canônicos da pasta do planejamento e grava
// uma revisão nova de cada um que mudou desde a última revisão no banco.
// Devolve os nomes dos documentos que ganharam revisão.
func (e *Estrategista) ingerirDocumentos(ctx context.Context, planejamentoID int64) ([]string, error) {
	alterados := []string{}
	for _, nome := range documentosConhecidos {
		b, err := os.ReadFile(filepath.Join(e.DirTrabalho, nome))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("ler %s: %w", nome, err)
		}
		conteudo := string(b)
		if strings.TrimSpace(conteudo) == "" {
			continue
		}
		ultima, err := e.Store.ObterDocumentoPlanejamento(ctx, planejamentoID, nome, 0)
		if err == nil && ultima.Conteudo == conteudo {
			continue // sem mudança — não grava revisão nova
		}
		if _, err := e.Store.SalvarRevisaoDocumento(ctx, planejamentoID, nome, conteudo); err != nil {
			return nil, err
		}
		alterados = append(alterados, nome)
	}
	return alterados, nil
}

// ingerirArtefatos varre os .html da raiz da pasta do planejamento, atualiza o
// índice no banco (hash/tamanho/revisão; título e descrição vêm da declaração
// do turno) e remove do índice os que sumiram do disco. Devolve os artefatos
// presentes.
func (e *Estrategista) ingerirArtefatos(ctx context.Context, planejamentoID int64, declarados []ArtefatoDeclarado) ([]db.ArtefatoPlanejamento, error) {
	porNome := map[string]ArtefatoDeclarado{}
	for _, d := range declarados {
		porNome[strings.TrimSpace(d.Arquivo)] = d
	}

	entradas, err := os.ReadDir(e.DirTrabalho)
	if err != nil {
		return nil, fmt.Errorf("ler a pasta do planejamento: %w", err)
	}
	presentes := []string{}
	artefatos := []db.ArtefatoPlanejamento{}
	for _, ent := range entradas {
		nome := ent.Name()
		if ent.IsDir() || !NomeArtefatoValido(nome) {
			continue
		}
		b, err := os.ReadFile(filepath.Join(e.DirTrabalho, nome))
		if err != nil {
			return nil, fmt.Errorf("ler artefato %s: %w", nome, err)
		}
		soma := sha256.Sum256(b)
		dec := porNome[nome]
		art, err := e.Store.UpsertArtefatoPlanejamento(ctx, db.ArtefatoPlanejamento{
			PlanejamentoID: planejamentoID, Arquivo: nome,
			Titulo: strings.TrimSpace(dec.Titulo), Descricao: strings.TrimSpace(dec.Descricao),
			Tamanho: int64(len(b)), Hash: hex.EncodeToString(soma[:]),
		})
		if err != nil {
			return nil, err
		}
		presentes = append(presentes, nome)
		artefatos = append(artefatos, art)
	}
	if err := e.Store.RemoverArtefatosAusentes(ctx, planejamentoID, presentes); err != nil {
		return nil, err
	}
	return artefatos, nil
}

// listarReferencias monta a listagem de referências anexadas para o marcador
// {REFERENCIAS} do prompt — o harness só lê o que a listagem apontar como
// existente (a pasta pode nem existir quando nada foi anexado). Os caminhos são
// relativos porque o cwd do harness JÁ é a pasta do planejamento.
func (e *Estrategista) listarReferencias() string {
	return referencias.BlocoPrompt(filepath.Join(e.DirTrabalho, DirReferencias), DirReferencias)
}

// montarFala converte a saída estruturada na fala persistida do estrategista.
// Devolve o conteúdo e o meta JSON (que carrega os documentos/artefatos do
// turno para a interface).
func (e *Estrategista) montarFala(saida SaidaEstrategista, docs []string, arts []db.ArtefatoPlanejamento, motorUsado string, custo float64) (string, json.RawMessage) {
	tipo := strings.ToLower(strings.TrimSpace(saida.Tipo))
	var conteudo string
	switch tipo {
	case TurnoPerguntas:
		conteudo = formatarPerguntas(saida.Perguntas)
		if conteudo == "" {
			tipo = TurnoResposta
			conteudo = strings.TrimSpace(saida.RespostaMD)
		}
	default:
		tipo = TurnoResposta
		conteudo = strings.TrimSpace(saida.RespostaMD)
	}
	if conteudo == "" {
		conteudo = "O turno terminou sem resposta textual — veja os documentos e artefatos atualizados."
	}

	resumoArts := make([]map[string]string, 0, len(arts))
	for _, a := range arts {
		resumoArts = append(resumoArts, map[string]string{
			"arquivo": a.Arquivo, "titulo": a.Titulo, "descricao": a.Descricao,
		})
	}
	meta, err := json.Marshal(map[string]any{
		"tipo":       tipo,
		"custo_usd":  custo,
		"motor":      motorUsado,
		"modelo":     e.Modelo,
		"confianca":  strings.ToLower(strings.TrimSpace(saida.Confianca)),
		"documentos": docs,
		"artefatos":  resumoArts,
	})
	if err != nil {
		meta = []byte("{}")
	}
	return conteudo, meta
}

// formatarPerguntas monta o texto da fala de clarificação (perguntas numeradas).
func formatarPerguntas(perguntas []PerguntaEstrategista) string {
	var linhas []string
	n := 0
	for _, p := range perguntas {
		texto := strings.TrimSpace(p.Pergunta)
		if texto == "" {
			continue
		}
		n++
		linha := fmt.Sprintf("%d. %s", n, texto)
		if ctxTexto := strings.TrimSpace(p.Contexto); ctxTexto != "" {
			linha += "\n   _" + ctxTexto + "_"
		}
		linhas = append(linhas, linha)
	}
	if len(linhas) == 0 {
		return ""
	}
	return "Para planejar com precisão, preciso decidir com você:\n\n" +
		strings.Join(linhas, "\n")
}

// montarHistorico concatena a conversa (falas do usuário e do estrategista, em
// ordem) rotulada para o prompt — o motor é stateless, então cada turno recebe
// a conversa inteira. Os documentos NÃO entram aqui: eles vivem na pasta de
// trabalho e o prompt manda o harness lê-los de lá.
func (e *Estrategista) montarHistorico(ctx context.Context, planejamentoID int64) (string, error) {
	msgs, err := e.Store.ListarMensagensPlanejamento(ctx, planejamentoID)
	if err != nil {
		return "", fmt.Errorf("estrategista: ler chat do planejamento %d: %w", planejamentoID, err)
	}
	partes := make([]string, 0, len(msgs))
	for _, m := range msgs {
		texto := strings.TrimSpace(m.Conteudo)
		if texto == "" {
			continue
		}
		switch m.Papel {
		case db.PapelPlanejamentoUser:
			partes = append(partes, "Usuário: "+texto)
		case db.PapelPlanejamentoEstrategista:
			partes = append(partes, "Estrategista: "+texto)
		}
	}
	return strings.Join(partes, "\n\n"), nil
}

// fotografarRepos captura o `git status --porcelain` de cada repo (mapa
// pasta → saída). Erros viram entrada vazia — a comparação só acusa violação
// quando as DUAS fotografias existem e diferem (fail-open deliberado: um repo
// que nem status responde não pode derrubar o turno por causa da rede).
func (e *Estrategista) fotografarRepos() map[string]string {
	fotos := map[string]string{}
	for _, repo := range e.Repos {
		saida, err := e.statusRepo(repo)
		if err != nil {
			continue
		}
		fotos[repo] = saida
	}
	return fotos
}

// violacoesRepos compara as fotografias antes/depois e devolve, por repo
// alterado, as linhas de status que APARECERAM durante o turno (arquivos que o
// harness tocou). Mudanças que sumiram (ex.: o desenvolvedor commitou no meio)
// não são violação.
func violacoesRepos(antes, depois map[string]string) []string {
	var viol []string
	for repo, dep := range depois {
		ant, ok := antes[repo]
		if !ok || ant == dep {
			continue
		}
		antigas := map[string]bool{}
		for _, l := range strings.Split(ant, "\n") {
			antigas[strings.TrimSpace(l)] = true
		}
		var novas []string
		for _, l := range strings.Split(dep, "\n") {
			l = strings.TrimSpace(l)
			if l != "" && !antigas[l] {
				novas = append(novas, l)
			}
		}
		if len(novas) > 0 {
			sort.Strings(novas)
			viol = append(viol, fmt.Sprintf("- %s: %s", repo, strings.Join(novas, ", ")))
		}
	}
	sort.Strings(viol)
	return viol
}

// fragmentoFoco devolve a instrução de escopo documental conforme o foco do
// planejamento (injetada no marcador {FOCO} do prompt).
func fragmentoFoco(foco string) string {
	switch foco {
	case db.FocoPlanejamentoADR:
		return "**Foco deste planejamento: ADRs (decisões arquiteturais).** Mantenha o arquivo " +
			"`adrs.md`. NÃO produza `prd.md` — as necessidades de negócio da conversa servem de " +
			"contexto para as decisões, não de entregável."
	case db.FocoPlanejamentoAmbos:
		return "**Foco deste planejamento: PRD + ADRs.** Mantenha `prd.md` (visão de negócio) e " +
			"`adrs.md` (decisões arquiteturais que sustentam o PRD). Mantenha os dois consistentes " +
			"entre si a cada turno."
	default: // prd
		return "**Foco deste planejamento: PRD (visão de negócio).** Mantenha o arquivo `prd.md`: " +
			"objetivo, contexto, escopo e fora-de-escopo, requisitos funcionais com critérios de " +
			"aceite, regras de negócio, requisitos não-funcionais relevantes, premissas e questões " +
			"em aberto. NÃO produza `adrs.md` neste planejamento."
	}
}

// fragmentoNivelVisual devolve a instrução de artefatos conforme o nível visual
// do planejamento (injetada no marcador {NIVEL_VISUAL} do prompt).
func fragmentoNivelVisual(nivel string) string {
	switch nivel {
	case db.NivelVisualDocumento:
		return "**Nível visual: documento.** NÃO produza artefatos .html — só os documentos .md."
	case db.NivelVisualPrototipo:
		return "**Nível visual: protótipo.** Além dos documentos, mantenha `apresentacao.html` " +
			"(resumo executivo visual do plano) e `prototipo.html` (simulação navegável das " +
			"telas/fluxos propostos). Regrave um artefato APENAS quando o turno mudar o que ele " +
			"apresenta — regenerá-los custa caro."
	default: // apresentacao
		return "**Nível visual: apresentação.** Além dos documentos, mantenha `apresentacao.html`: " +
			"a versão visual do plano (resumo executivo, infográficos, fluxograma do processo " +
			"proposto em SVG inline). Regrave-o APENAS quando o turno mudar o conteúdo do plano. " +
			"NÃO produza protótipo de telas neste nível."
	}
}

// falhar carimba o planejamento como falhou com o motivo, registra uma fala de
// sistema (a UI mostra na conversa) e devolve nil — o desfecho lógico já foi
// persistido; não é erro de infraestrutura para o chamador.
func (e *Estrategista) falhar(ctx context.Context, plan db.Planejamento, motivo string) error {
	plan.Status = db.StatusPlanejamentoFalhou
	plan.Erro = motivo
	if _, err := e.Store.AtualizarPlanejamento(ctx, plan); err != nil {
		return fmt.Errorf("estrategista: carimbar falha do planejamento %d: %w", plan.ID, err)
	}
	_, _ = e.Store.CriarMensagemPlanejamento(ctx, db.MensagemPlanejamento{
		PlanejamentoID: plan.ID, Papel: db.PapelPlanejamentoSistema,
		Conteudo: "O turno falhou: " + motivo + "\n\nVocê pode reenviar a mensagem para tentar de novo.",
	})
	e.registrarEvento(plan, "estrategia_falhou", "Praxis: planejamento falhou", motivo)
	return nil
}

// registrarEvento grava um evento do planejamento (best-effort). O evento
// carrega o project_id quando o planejamento é de projeto (planejamentos de
// grupo ficam sem vínculo — a tabela events só conhece projeto/demanda).
func (e *Estrategista) registrarEvento(plan db.Planejamento, tipo, titulo, detalhe string) {
	ev := db.Evento{Tipo: tipo, Titulo: titulo, Detalhe: detalhe}
	if plan.ProjectID != nil {
		ev.ProjectID = plan.ProjectID
	}
	_, _ = e.Store.RegistrarEvento(context.Background(), ev)
}

// fecharExecComErro marca a execução como erro (best-effort) quando o run nem
// chegou a rodar.
func (e *Estrategista) fecharExecComErro(ctx context.Context, exec db.ExecucaoPlanejamento) {
	exec.IsError = true
	exec.TerminadoEm = e.agoraISO()
	_, _ = e.Store.AtualizarExecucaoPlanejamento(ctx, exec)
}

func (e *Estrategista) selecionar(nome string) (motor.Motor, error) {
	if e.Selecionar != nil {
		return e.Selecionar(nome)
	}
	return motor.Selecionar(nome)
}

func (e *Estrategista) prompt(ctx context.Context, nome string) (string, error) {
	if e.Prompt != nil {
		return e.Prompt(ctx, nome)
	}
	return intake.ResolverPrompt(ctx, e.Store, nome)
}

func (e *Estrategista) statusRepo(dir string) (string, error) {
	if e.StatusRepo != nil {
		return e.StatusRepo(dir)
	}
	return statusPorcelainPadrao(dir)
}

func (e *Estrategista) agora() time.Time {
	if e.Agora != nil {
		return e.Agora()
	}
	return time.Now()
}

// agoraISO devolve o horário atual em ISO-8601 UTC, no formato dos timestamps do banco.
func (e *Estrategista) agoraISO() string {
	return e.agora().UTC().Format("2006-01-02T15:04:05.000Z")
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
