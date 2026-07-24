package motor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// vereditoTeste espelha a forma da saida estruturada de um revisor, so para
// exercitar a decodificacao (o tipo real vive no pacote de pipeline).
type vereditoTeste struct {
	Veredito  string   `json:"veredito"`
	Problemas []string `json:"problemas"`
}

func TestDecodificarEstruturadoPreferido(t *testing.T) {
	res := &ResultadoRun{
		Estruturado: json.RawMessage(`{"veredito":"APROVADO","problemas":[]}`),
		Resultado:   "texto qualquer",
	}
	var v vereditoTeste
	if err := DecodificarEstruturado(res, &v); err != nil {
		t.Fatal(err)
	}
	if v.Veredito != "APROVADO" || len(v.Problemas) != 0 {
		t.Fatalf("veredito inesperado: %+v", v)
	}
}

func TestDecodificarEstruturadoDoTextoComCercas(t *testing.T) {
	res := &ResultadoRun{
		Resultado: "Segue o veredito:\n```json\n{\"veredito\":\"REPROVADO\",\"problemas\":[\"faltou teste de migracao\"]}\n```\n",
	}
	var v vereditoTeste
	if err := DecodificarEstruturado(res, &v); err != nil {
		t.Fatal(err)
	}
	if v.Veredito != "REPROVADO" || len(v.Problemas) != 1 {
		t.Fatalf("veredito inesperado: %+v", v)
	}
}

func TestDecodificarEstruturadoSemJSON(t *testing.T) {
	res := &ResultadoRun{Resultado: "sem json nenhum"}
	var v vereditoTeste
	if err := DecodificarEstruturado(res, &v); err == nil {
		t.Fatal("esperava erro para resposta sem JSON")
	}
}

func TestParseEventoResult(t *testing.T) {
	linha := `{"type":"result","subtype":"success","is_error":false,"result":"tudo certo","total_cost_usd":3.21,"num_turns":17,"structured_output":{"veredito":"APROVADO","problemas":[]}}`
	var ev eventoStreamClaude
	if err := json.Unmarshal([]byte(linha), &ev); err != nil {
		t.Fatal(err)
	}
	if ev.Type != "result" || ev.IsError || ev.Result != "tudo certo" ||
		ev.TotalCostUSD != 3.21 || ev.NumTurns != 17 || len(ev.StructuredOutput) == 0 {
		t.Fatalf("evento result mal parseado: %+v", ev)
	}
}

func TestLimiteSessaoClaude(t *testing.T) {
	texto := "You've hit your session limit · resets 11:40pm"
	if !limiteSessaoAtingido(texto) {
		t.Fatalf("esperava detectar limite em %q", texto)
	}
	if got := linhaLimite("prefixo\n" + texto + "\nrodape"); got != texto {
		t.Fatalf("linha limite: %q", got)
	}
	if limiteSessaoAtingido("rate limit sem informacao de reset") {
		t.Fatal("nao deveria detectar limite sem reset")
	}
}

func TestProibidosClaude(t *testing.T) {
	// Revisor: nao commita/pusha e nao edita.
	rev := proibidosClaude(OpcoesRun{SomenteLeitura: true})
	if !contemString(rev, "Edit") || !contemString(rev, "Bash(git commit*)") {
		t.Fatalf("revisor deveria proibir edicao e commit: %+v", rev)
	}
	// Executor: pode editar, mas nao commita/pusha.
	exe := proibidosClaude(OpcoesRun{ProibirCommit: true})
	if contemString(exe, "Edit") {
		t.Fatalf("executor nao deveria proibir edicao: %+v", exe)
	}
	if !contemString(exe, "Bash(git push*)") {
		t.Fatalf("executor deveria proibir push: %+v", exe)
	}
	// Livre: nada proibido.
	if got := proibidosClaude(OpcoesRun{}); len(got) != 0 {
		t.Fatalf("sem restricoes nao deveria proibir nada: %+v", got)
	}
}

func TestMotorClaudeDetectaLimiteNoStderrSemResultado(t *testing.T) {
	dir := t.TempDir()
	nome := "claude"
	conteudo := "#!/bin/sh\necho \"You've hit your session limit - resets 11:40pm\" >&2\nexit 1\n"
	if runtime.GOOS == "windows" {
		nome = "claude.bat"
		conteudo = "@echo off\r\necho You've hit your session limit - resets 11:40pm 1>&2\r\nexit /b 1\r\n"
	}
	caminho := filepath.Join(dir, nome)
	if err := os.WriteFile(caminho, []byte(conteudo), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	worktree := t.TempDir()
	res, err := motorClaude{}.Rodar(OpcoesRun{Dir: worktree, DirLogs: filepath.Join(worktree, "logs"), Prompt: "teste", RotuloLog: "limite", TimeoutMin: 1})
	if err != nil {
		t.Fatalf("nao esperava erro, esperava ResultadoRun com limite: %v", err)
	}
	if res == nil || !res.LimiteSessao {
		t.Fatalf("limite nao detectado: %+v", res)
	}
	if res.DetalheLimite == "" {
		t.Fatalf("detalhe do limite deveria ser preenchido: %+v", res)
	}
}

func TestMotorClaudeUsaClaudeConfigDirNoAmbiente(t *testing.T) {
	dirBin := t.TempDir()
	registro := filepath.Join(t.TempDir(), "claude_env.txt")
	nome := "claude"
	conteudo := "#!/bin/sh\nprintf '%s' \"$CLAUDE_CONFIG_DIR\" > \"" + registro + "\"\necho '{\"type\":\"result\",\"subtype\":\"success\",\"is_error\":false,\"result\":\"ok\",\"total_cost_usd\":0,\"num_turns\":1}'\nexit 0\n"
	if runtime.GOOS == "windows" {
		nome = "claude.bat"
		conteudo = "@echo off\r\n>\"" + registro + "\" echo %CLAUDE_CONFIG_DIR%\r\necho {\"type\":\"result\",\"subtype\":\"success\",\"is_error\":false,\"result\":\"ok\",\"total_cost_usd\":0,\"num_turns\":1}\r\nexit /b 0\r\n"
	}
	caminho := filepath.Join(dirBin, nome)
	if err := os.WriteFile(caminho, []byte(conteudo), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dirBin+string(os.PathListSeparator)+os.Getenv("PATH"))

	worktree := t.TempDir()
	const cfgDir = `C:\Users\dev\.claude-alt`
	res, err := motorClaude{}.Rodar(OpcoesRun{Dir: worktree, DirLogs: filepath.Join(worktree, "logs"), Prompt: "teste", RotuloLog: "env", TimeoutMin: 1, ClaudeConfigDir: cfgDir})
	if err != nil {
		t.Fatalf("erro inesperado ao rodar claude fake: %v", err)
	}
	if res == nil || res.Subtipo != "success" {
		t.Fatalf("resultado inesperado: %+v", res)
	}
	b, err := os.ReadFile(registro)
	if err != nil {
		t.Fatalf("nao consegui ler registro de ambiente: %v", err)
	}
	if got := strings.TrimSpace(string(b)); got != cfgDir {
		t.Fatalf("CLAUDE_CONFIG_DIR inesperado: %q", got)
	}
}

func TestMotorClaudeGravaLogEmDirLogs(t *testing.T) {
	dirBin := t.TempDir()
	nome := "claude"
	conteudo := "#!/bin/sh\necho '{\"type\":\"result\",\"subtype\":\"success\",\"is_error\":false,\"result\":\"ok\",\"total_cost_usd\":0,\"num_turns\":1}'\nexit 0\n"
	if runtime.GOOS == "windows" {
		nome = "claude.bat"
		conteudo = "@echo off\r\necho {\"type\":\"result\",\"subtype\":\"success\",\"is_error\":false,\"result\":\"ok\",\"total_cost_usd\":0,\"num_turns\":1}\r\nexit /b 0\r\n"
	}
	if err := os.WriteFile(filepath.Join(dirBin, nome), []byte(conteudo), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dirBin+string(os.PathListSeparator)+os.Getenv("PATH"))

	worktree := t.TempDir()
	dirLogs := filepath.Join(t.TempDir(), "logs-fora-do-worktree")
	res, err := motorClaude{}.Rodar(OpcoesRun{Dir: worktree, DirLogs: dirLogs, Prompt: "x", RotuloLog: "executar", TimeoutMin: 1})
	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	if res.LogPath == "" {
		t.Fatal("LogPath deveria ser preenchido")
	}
	if filepath.Dir(res.LogPath) != dirLogs {
		t.Fatalf("log deveria ficar em DirLogs (%s), veio em %s", dirLogs, filepath.Dir(res.LogPath))
	}
	if _, err := os.Stat(res.LogPath); err != nil {
		t.Fatalf("arquivo de log nao foi criado: %v", err)
	}
	if !strings.HasPrefix(filepath.Base(res.LogPath), "executar-") {
		t.Fatalf("nome do log deveria comecar pelo rotulo: %s", filepath.Base(res.LogPath))
	}
}

const (
	jsonErroEstruturado = `{"type":"result","subtype":"error_max_structured_output_retries","is_error":true,"result":"","errors":["Failed to provide valid structured output after 5 attempts"],"total_cost_usd":3.0,"num_turns":23,"session_id":"sess-abc"}`
	jsonSucessoResgate  = `{"type":"result","subtype":"success","is_error":false,"result":"ok","structured_output":{"veredito":"APROVADO","problemas":[]},"total_cost_usd":0.5,"num_turns":1,"session_id":"sess-abc"}`
)

// claudeFalsoResgate instala um `claude` falso que na 1a chamada devolve
// error_max_structured_output_retries e nas seguintes grava os argumentos em
// argsPath e devolve o JSON de resgate informado. Toda chamada acrescenta uma
// linha em contadorPath.
func claudeFalsoResgate(t *testing.T, jsonResgate, argsPath, contadorPath string) {
	t.Helper()
	dirBin := t.TempDir()
	marcador := filepath.Join(t.TempDir(), "marcador.txt")
	nome := "claude"
	conteudo := "#!/bin/sh\n" +
		"echo x >> \"" + contadorPath + "\"\n" +
		"if [ -f \"" + marcador + "\" ]; then\n" +
		"  printf '%s' \"$*\" > \"" + argsPath + "\"\n" +
		"  echo '" + jsonResgate + "'\n" +
		"  exit 0\n" +
		"fi\n" +
		"echo m > \"" + marcador + "\"\n" +
		"echo '" + jsonErroEstruturado + "'\n" +
		"exit 1\n"
	if runtime.GOOS == "windows" {
		nome = "claude.bat"
		conteudo = "@echo off\r\n" +
			">>\"" + contadorPath + "\" echo x\r\n" +
			"if exist \"" + marcador + "\" goto resgate\r\n" +
			">\"" + marcador + "\" echo m\r\n" +
			"echo " + jsonErroEstruturado + "\r\n" +
			"exit /b 1\r\n" +
			":resgate\r\n" +
			">\"" + argsPath + "\" echo %*\r\n" +
			"echo " + jsonResgate + "\r\n" +
			"exit /b 0\r\n"
	}
	if err := os.WriteFile(filepath.Join(dirBin, nome), []byte(conteudo), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dirBin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func contarLinhas(t *testing.T, caminho string) int {
	t.Helper()
	b, err := os.ReadFile(caminho)
	if err != nil {
		return 0
	}
	return len(strings.Fields(string(b)))
}

// TestMotorClaudeResgataSaidaEstruturada: um run com schema que morre em
// error_max_structured_output_retries e resgatado retomando a MESMA sessao
// (--resume <session_id>); o resultado final e o do resgate, com custo/turnos
// somados aos do run original.
func TestMotorClaudeResgataSaidaEstruturada(t *testing.T) {
	argsPath := filepath.Join(t.TempDir(), "args.txt")
	contadorPath := filepath.Join(t.TempDir(), "contador.txt")
	claudeFalsoResgate(t, jsonSucessoResgate, argsPath, contadorPath)

	worktree := t.TempDir()
	var logsAoVivo []string
	res, err := motorClaude{}.Rodar(OpcoesRun{
		Dir: worktree, DirLogs: filepath.Join(worktree, "logs"), Prompt: "analise",
		RotuloLog: "analista", TimeoutMin: 1,
		Schema:    `{"type":"object","required":["veredito"]}`,
		OnLogPath: func(p string) { logsAoVivo = append(logsAoVivo, p) },
	})
	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	if res == nil || res.IsError || res.Subtipo != "success" {
		t.Fatalf("resgate deveria devolver sucesso: %+v", res)
	}
	var v vereditoTeste
	if err := DecodificarEstruturado(res, &v); err != nil || v.Veredito != "APROVADO" {
		t.Fatalf("saida estruturada do resgate nao decodificou: %v / %+v", err, v)
	}
	if dif := res.CustoUSD - 3.5; dif > 1e-9 || dif < -1e-9 {
		t.Fatalf("custo deveria somar run original + resgate (3.5), veio %v", res.CustoUSD)
	}
	if res.NumTurns != 24 {
		t.Fatalf("turnos deveriam somar (24), veio %d", res.NumTurns)
	}
	b, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatalf("resgate nao registrou argumentos: %v", err)
	}
	if args := string(b); !strings.Contains(args, "--resume sess-abc") {
		t.Fatalf("resgate deveria retomar a sessao com --resume sess-abc: %q", args)
	}
	if !strings.HasPrefix(filepath.Base(res.LogPath), "analista-resgate-") {
		t.Fatalf("log do resgate deveria usar o rotulo -resgate: %s", res.LogPath)
	}
	if len(logsAoVivo) != 2 || logsAoVivo[1] != res.LogPath {
		t.Fatalf("OnLogPath deveria acompanhar os dois runs (original e resgate): %+v", logsAoVivo)
	}
	if n := contarLinhas(t, contadorPath); n != 2 {
		t.Fatalf("esperava exatamente 2 execucoes do claude (run + resgate), veio %d", n)
	}
}

// TestMotorClaudeResgateFalhaMantemErro: se o resgate tambem morrer no mesmo
// subtipo, o desfecho de erro e mantido (com custos somados) e NAO ha um
// terceiro run — o resgate acontece uma unica vez.
func TestMotorClaudeResgateFalhaMantemErro(t *testing.T) {
	argsPath := filepath.Join(t.TempDir(), "args.txt")
	contadorPath := filepath.Join(t.TempDir(), "contador.txt")
	claudeFalsoResgate(t, jsonErroEstruturado, argsPath, contadorPath)

	worktree := t.TempDir()
	res, err := motorClaude{}.Rodar(OpcoesRun{
		Dir: worktree, DirLogs: filepath.Join(worktree, "logs"), Prompt: "analise",
		RotuloLog: "analista", TimeoutMin: 1,
		Schema: `{"type":"object","required":["veredito"]}`,
	})
	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	if res == nil || !res.IsError || res.Subtipo != subtipoSaidaEstruturada {
		t.Fatalf("erro do resgate deveria manter o subtipo original: %+v", res)
	}
	if dif := res.CustoUSD - 6.0; dif > 1e-9 || dif < -1e-9 {
		t.Fatalf("custo deveria somar os dois runs (6.0), veio %v", res.CustoUSD)
	}
	if !strings.Contains(res.Resultado, "Failed to provide valid structured output") {
		t.Fatalf("resultado deveria carregar a causa (array errors): %q", res.Resultado)
	}
	if n := contarLinhas(t, contadorPath); n != 2 {
		t.Fatalf("resgate deve rodar UMA vez (2 execucoes no total), veio %d", n)
	}
}

// TestMotorClaudeNaoResgataSemSchema: sem schema nao ha saida estruturada a
// resgatar — o erro e devolvido direto, com um unico run.
func TestMotorClaudeNaoResgataSemSchema(t *testing.T) {
	argsPath := filepath.Join(t.TempDir(), "args.txt")
	contadorPath := filepath.Join(t.TempDir(), "contador.txt")
	claudeFalsoResgate(t, jsonSucessoResgate, argsPath, contadorPath)

	worktree := t.TempDir()
	res, err := motorClaude{}.Rodar(OpcoesRun{
		Dir: worktree, DirLogs: filepath.Join(worktree, "logs"), Prompt: "analise",
		RotuloLog: "analista", TimeoutMin: 1,
	})
	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	if res == nil || !res.IsError || res.Subtipo != subtipoSaidaEstruturada {
		t.Fatalf("sem schema o erro deveria ser devolvido direto: %+v", res)
	}
	if n := contarLinhas(t, contadorPath); n != 1 {
		t.Fatalf("sem schema deveria haver um unico run, veio %d", n)
	}
}

// TestAutenticacaoFalhouClaude: reconhece as mensagens de perfil deslogado sem
// confundir com limite de sessao ou saida normal.
func TestAutenticacaoFalhouClaude(t *testing.T) {
	casos := []struct {
		texto string
		want  bool
	}{
		{"Not logged in · Please run /login", true},
		{"error: authentication_failed", true},
		{"Invalid API key", true},
		{"OAuth token has expired", true},
		{"You've hit your session limit · resets 2:20pm", false},
		{"implementei a fase com sucesso", false},
		{"", false},
	}
	for _, c := range casos {
		if got := autenticacaoFalhou(c.texto); got != c.want {
			t.Fatalf("autenticacaoFalhou(%q) = %v, quero %v", c.texto, got, c.want)
		}
	}
}

// TestMotorClaudeOnLogPathEFalhaAutenticacao: OnLogPath é chamado com o caminho
// do .jsonl assim que o run começa (é o que liga o log ao vivo à execução em
// andamento); um result com is_error e "Not logged in" liga FalhaAutenticacao.
func TestMotorClaudeOnLogPathEFalhaAutenticacao(t *testing.T) {
	dir := t.TempDir()
	linha := `{"type":"result","subtype":"success","is_error":true,"result":"Not logged in - Please run /login","total_cost_usd":0,"num_turns":1}`
	nome := "claude"
	conteudo := "#!/bin/sh\necho '" + linha + "'\nexit 0\n"
	if runtime.GOOS == "windows" {
		nome = "claude.bat"
		conteudo = "@echo off\r\necho " + linha + "\r\nexit /b 0\r\n"
	}
	if err := os.WriteFile(filepath.Join(dir, nome), []byte(conteudo), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	worktree := t.TempDir()
	var aoVivo string
	res, err := motorClaude{}.Rodar(OpcoesRun{
		Dir: worktree, DirLogs: filepath.Join(worktree, "logs"), Prompt: "teste",
		RotuloLog: "auth", TimeoutMin: 1,
		OnLogPath: func(p string) { aoVivo = p },
	})
	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	if aoVivo == "" || aoVivo != res.LogPath {
		t.Fatalf("OnLogPath = %q, esperava o LogPath do run (%q)", aoVivo, res.LogPath)
	}
	if !res.IsError || !res.FalhaAutenticacao {
		t.Fatalf("FalhaAutenticacao não detectada: %+v", res)
	}
	if res.LimiteSessao {
		t.Fatalf("não deveria marcar LimiteSessao: %+v", res)
	}
}
