package motor

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// motorClaude executa o Claude Code headless (`claude -p`) com stream-json.
// Suporta nativamente schema, budget e custo em USD.
//
// Nota de porte (Fase 1f): o laco de retentativa apos esgotamento de franquia
// (`rodarClaude`/`esperarResetFranquia` do Praxis atual) NAO foi trazido para
// ca — ele e responsabilidade do pipeline/scheduler e, conforme o plano, muda
// de comportamento (nao bloquear; devolver horario para reagendar) na Fase 2b.
// Aqui fica apenas a execucao de um unico run, que ja sinaliza LimiteSessao.
type motorClaude struct{}

func (motorClaude) Nome() string { return "claude" }

func (motorClaude) Capacidades() Capacidades {
	return Capacidades{SchemaNativo: true, BudgetNativo: true, CustoUSDNativo: true}
}

// eventoStreamClaude cobre o subconjunto dos eventos stream-json que interessa.
type eventoStreamClaude struct {
	Type             string          `json:"type"`
	Subtype          string          `json:"subtype"`
	IsError          bool            `json:"is_error"`
	Result           string          `json:"result"`
	Errors           []string        `json:"errors"`
	TotalCostUSD     float64         `json:"total_cost_usd"`
	NumTurns         int             `json:"num_turns"`
	SessionID        string          `json:"session_id"`
	StructuredOutput json.RawMessage `json:"structured_output"`
	Message          struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
			Name string `json:"name"`
		} `json:"content"`
	} `json:"message"`
}

// subtipoSaidaEstruturada e o subtype do result quando o claude esgota as
// tentativas de emitir a saida estruturada (StructuredOutput invalido 5x).
const subtipoSaidaEstruturada = "error_max_structured_output_retries"

// promptResgateEstruturado retoma uma sessao que morreu em
// error_max_structured_output_retries apenas para reemitir a saida estruturada.
// O padrao observado (logs analista-20260722/24) e o modelo corromper a tag de
// um parametro da ferramenta quando o payload e grande: o harness descarta o
// parametro malformado e a validacao acusa campo obrigatorio ausente, 5x na
// mesma sessao. Um turno novo, curto e explicito costuma destravar — e com o
// contexto em cache custa centavos, contra refazer o run inteiro.
const promptResgateEstruturado = "Sua execucao anterior terminou sem conseguir emitir a saida estruturada: " +
	"as chamadas da ferramenta StructuredOutput chegaram sem todos os campos obrigatorios " +
	"(parametros com tag malformada sao descartados pelo harness). O trabalho ja esta feito acima — " +
	"NAO o refaca e nao use outras ferramentas. Chame a ferramenta StructuredOutput UMA unica vez agora, " +
	"com o objeto completo que satisfaz o schema: inclua TODOS os campos obrigatorios, um por parametro, " +
	"e seja conciso nos campos de texto (frases curtas, sem markdown pesado)."

// limiteSessaoAtingido reconhece a mensagem que o claude imprime quando a
// franquia de tokens acaba.
func limiteSessaoAtingido(texto string) bool {
	t := strings.ToLower(texto)
	return (strings.Contains(t, "session limit") || strings.Contains(t, "usage limit")) &&
		strings.Contains(t, "reset")
}

// linhaLimite extrai a primeira linha do texto que menciona o limite/reset.
func linhaLimite(texto string) string {
	for _, l := range strings.Split(texto, "\n") {
		if limiteSessaoAtingido(l) {
			return strings.TrimSpace(l)
		}
	}
	return strings.TrimSpace(texto)
}

// autenticacaoFalhou reconhece as mensagens do claude quando o perfil esta
// deslogado ou com credencial invalida (ex.: "Not logged in · Please run
// /login" com error "authentication_failed" no stream-json).
func autenticacaoFalhou(texto string) bool {
	t := strings.ToLower(texto)
	return strings.Contains(t, "not logged in") ||
		strings.Contains(t, "please run /login") ||
		strings.Contains(t, "authentication_failed") ||
		strings.Contains(t, "invalid api key") ||
		strings.Contains(t, "oauth token has expired")
}

// Rodar executa `claude -p` com stream-json e devolve o resultado final. Se o
// run terminar em error_max_structured_output_retries (trabalho feito, mas a
// saida estruturada nunca validou), faz UM resgate: retoma a mesma sessao com
// --resume e um prompt que pede apenas a reemissao do StructuredOutput. O
// resgate reaproveita o contexto (cache) — custa centavos contra refazer o run.
func (m motorClaude) Rodar(op OpcoesRun) (*ResultadoRun, error) {
	res, err := m.rodarUma(op, "")
	if err != nil || res == nil || !res.IsError || res.Subtipo != subtipoSaidaEstruturada ||
		op.Schema == "" || res.SessionID == "" {
		return res, err
	}

	fmt.Println("  Saida estruturada nao validou; resgatando a sessao com --resume para reemiti-la.")
	opResgate := op
	opResgate.Prompt = promptResgateEstruturado
	opResgate.RotuloLog = op.RotuloLog + "-resgate"
	resgate, errResgate := m.rodarUma(opResgate, res.SessionID)
	if errResgate != nil || resgate == nil {
		// Cancelamento/pausa precisa subir para o chamador (vira pausa, nao falha).
		if op.Ctx != nil && op.Ctx.Err() != nil {
			return nil, errResgate
		}
		// Falha de infraestrutura do resgate: fica o desfecho do run original.
		return res, nil
	}
	// O resgate e a continuacao do MESMO trabalho: soma custo/turnos/tokens do
	// run original para o registro da execucao refletir o gasto total.
	resgate.CustoUSD += res.CustoUSD
	resgate.NumTurns += res.NumTurns
	resgate.TokensIn += res.TokensIn
	resgate.TokensOut += res.TokensOut
	if resgate.IsError && strings.TrimSpace(resgate.Resultado) == "" {
		resgate.Resultado = res.Resultado
	}
	return resgate, nil
}

// rodarUma faz uma unica execucao de `claude -p` com stream-json: mostra o
// progresso ao vivo no console, grava cada evento em um .jsonl e devolve o
// resultado final. resumeID != "" retoma a sessao correspondente (--resume).
func (motorClaude) rodarUma(op OpcoesRun, resumeID string) (*ResultadoRun, error) {
	// Falha rápida e acionável (Fase B): o CLI do claude RECUSA
	// --dangerously-skip-permissions com UID 0 — sem esta checagem o harness
	// morre no meio da demanda com um erro criptico do vendor.
	if err := verificarNaoRoot(); err != nil {
		return nil, err
	}
	args := []string{"-p", "--dangerously-skip-permissions", "--output-format", "stream-json", "--verbose"}
	if resumeID != "" {
		args = append(args, "--resume", resumeID)
	}
	if op.Modelo != "" {
		args = append(args, "--model", op.Modelo)
	}
	if op.Esforco != "" {
		args = append(args, "--effort", op.Esforco)
	}
	if op.BudgetUSD > 0 {
		args = append(args, "--max-budget-usd", fmt.Sprintf("%.2f", op.BudgetUSD))
	}
	for _, d := range op.AddDirs {
		args = append(args, "--add-dir", resolverDir(op.Dir, d))
	}
	if op.Schema != "" {
		args = append(args, "--json-schema", op.Schema)
	}
	if proibidos := proibidosClaude(op); len(proibidos) > 0 {
		args = append(args, "--disallowedTools")
		args = append(args, proibidos...)
	}

	logFile, logPath, err := abrirLog(op.DirLogs, op.RotuloLog, "jsonl")
	if err != nil {
		return nil, err
	}
	defer logFile.Close()
	if op.OnLogPath != nil {
		op.OnLogPath(logPath)
	}

	ctx, cancel, timeout := contextoTimeout(op.Ctx, op.TimeoutMin)
	defer cancel()

	fmt.Println("  AVISO: Claude em modo BYPASS (--dangerously-skip-permissions): acesso total ao sistema, sem prompts de permissao. Use apenas em ambiente controlado.")
	cmd := exec.CommandContext(ctx, ResolverCLI("claude"), args...)
	cmd.Dir = op.Dir
	if err := aplicarPerfil(cmd, "claude", perfilDirDaOp(op)); err != nil {
		return nil, err
	}
	cmd.Stdin = strings.NewReader(op.Prompt)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	prepararProcessoFilho(cmd)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("nao consegui executar `claude` (esta no PATH e logado?): %w", err)
	}
	defer registrarProcessoFilho(op, cmd)()

	var res *ResultadoRun
	var sessionID string
	var textoAcc strings.Builder
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 1024*1024), 64*1024*1024)
	for sc.Scan() {
		linha := sc.Bytes()
		_, _ = logFile.Write(linha)
		_, _ = logFile.Write([]byte{'\n'})
		var ev eventoStreamClaude
		if json.Unmarshal(linha, &ev) != nil {
			continue
		}
		if ev.SessionID != "" {
			sessionID = ev.SessionID
		}
		switch ev.Type {
		case "assistant":
			for _, c := range ev.Message.Content {
				switch c.Type {
				case "text":
					if t := strings.TrimSpace(c.Text); t != "" {
						textoAcc.WriteString(t + "\n")
						fmt.Println(indentar(t, "  | "))
					}
				case "tool_use":
					fmt.Printf("  -> %s\n", c.Name)
				}
			}
		case "result":
			resultado := ev.Result
			// Em erros como error_max_structured_output_retries o campo result vem
			// vazio e a causa fica no array errors — sem isto a falha apareceria so
			// como o subtype, escondendo a mensagem do harness.
			if strings.TrimSpace(resultado) == "" && len(ev.Errors) > 0 {
				resultado = strings.Join(ev.Errors, "; ")
			}
			textoAcc.WriteString(resultado + "\n")
			res = &ResultadoRun{
				IsError:     ev.IsError,
				Subtipo:     ev.Subtype,
				Resultado:   resultado,
				Estruturado: ev.StructuredOutput,
				CustoUSD:    ev.TotalCostUSD,
				NumTurns:    ev.NumTurns,
				SessionID:   sessionID,
				LogPath:     logPath,
			}
		}
	}
	errScan := sc.Err()
	errWait := cmd.Wait()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return nil, fmt.Errorf("claude excedeu o timeout de %v; log: %s", timeout, logPath)
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		return nil, fmt.Errorf("execucao interrompida; log: %s", logPath)
	}
	if texto := strings.TrimSpace(stderr.String()); limiteSessaoAtingido(texto) {
		return &ResultadoRun{
			IsError:       true,
			Subtipo:       "limite de sessao/uso",
			Resultado:     texto,
			LogPath:       logPath,
			LimiteSessao:  true,
			DetalheLimite: linhaLimite(texto),
		}, nil
	}
	if res == nil {
		return nil, fmt.Errorf("claude terminou sem evento de resultado (scan: %v, exit: %v)\nstderr: %s\nlog: %s",
			errScan, errWait, ultimasLinhas(stderr.String(), 15), logPath)
	}
	if res.CustoUSD > 0 {
		fmt.Printf("  (run: US$ %.2f, %d turnos)\n", res.CustoUSD, res.NumTurns)
	}
	if texto := textoAcc.String(); limiteSessaoAtingido(texto) {
		res.LimiteSessao = true
		res.DetalheLimite = linhaLimite(texto)
	}
	if texto := strings.TrimSpace(stderr.String()); limiteSessaoAtingido(texto) {
		res.LimiteSessao = true
		res.DetalheLimite = linhaLimite(texto)
		if res.Resultado == "" {
			res.Resultado = texto
		}
	}
	if res.IsError && (autenticacaoFalhou(res.Resultado) || autenticacaoFalhou(stderr.String())) {
		res.FalhaAutenticacao = true
	}
	return res, nil
}

// proibidosClaude traduz as intencoes genericas nas ferramentas que o Claude
// deve recusar: o commit e sempre do orquestrador; o revisor nao edita nada.
func proibidosClaude(op OpcoesRun) []string {
	var p []string
	if op.ProibirCommit || op.SomenteLeitura {
		p = append(p, "Bash(git commit*)", "Bash(git push*)")
	}
	if op.SomenteLeitura {
		p = append(p, "Edit", "Write", "NotebookEdit")
		return p
	}
	// Escrita liberada com raizes protegidas: nega edicao dentro de cada raiz
	// (padrao //<abs> = caminho absoluto nas regras de permissao do claude).
	for _, d := range op.DirsProtegidos {
		abs := strings.TrimSuffix(filepath.ToSlash(strings.TrimSpace(d)), "/")
		if abs == "" {
			continue
		}
		for _, tool := range []string{"Edit", "Write", "NotebookEdit"} {
			p = append(p, fmt.Sprintf("%s(//%s/**)", tool, abs))
		}
	}
	return p
}
