package motor

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func comandoHelperAuth(ctx context.Context, nome string, args ...string) *exec.Cmd {
	helperArgs := []string{"-test.run=TestProcessoHelperAuth", "--", nome}
	helperArgs = append(helperArgs, args...)
	cmd := exec.CommandContext(ctx, os.Args[0], helperArgs...)
	cmd.Env = append(os.Environ(), "PRAXIS_AUTH_HELPER=1")
	return cmd
}

func TestProcessoHelperAuth(t *testing.T) {
	if os.Getenv("PRAXIS_AUTH_HELPER") != "1" {
		return
	}
	sep := 0
	for i, arg := range os.Args {
		if arg == "--" {
			sep = i + 1
			break
		}
	}
	if sep == 0 || len(os.Args) <= sep {
		os.Exit(2)
	}
	nome := os.Args[sep]
	args := os.Args[sep+1:]
	switch nome {
	case "claude":
		helperClaude(args)
	case "codex":
		helperCodex(args)
	default:
		os.Exit(2)
	}
	os.Exit(0)
}

func helperClaude(args []string) {
	dir := os.Getenv("CLAUDE_CONFIG_DIR")
	marcador := filepath.Join(dir, "autenticado")
	if strings.Join(args, " ") == "auth status --json" {
		_, err := os.Stat(marcador)
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{
			"loggedIn": err == nil, "authMethod": map[bool]string{true: "oauth", false: "none"}[err == nil],
		})
		if err != nil {
			os.Exit(1)
		}
		return
	}
	if strings.Join(args, " ") != "auth login" {
		os.Exit(2)
	}
	fmt.Fprintln(os.Stdout, "Abra https://example.test/claude-login para continuar")
	sc := bufio.NewScanner(os.Stdin)
	if sc.Scan() && strings.TrimSpace(sc.Text()) == "codigo-claude" {
		_ = os.WriteFile(marcador, []byte("ok"), 0o600)
		return
	}
	os.Exit(1)
}

func helperCodex(args []string) {
	dir := os.Getenv("CODEX_HOME")
	marcador := filepath.Join(dir, "autenticado")
	if strings.Join(args, " ") == "login status" {
		if _, err := os.Stat(marcador); err != nil {
			fmt.Fprintln(os.Stdout, "Not logged in")
			os.Exit(1)
		}
		fmt.Fprintln(os.Stdout, "Logged in using ChatGPT")
		return
	}
	if !strings.HasPrefix(strings.Join(args, " "), "app-server") {
		os.Exit(2)
	}
	sc := bufio.NewScanner(os.Stdin)
	enc := json.NewEncoder(os.Stdout)
	for sc.Scan() {
		var req struct {
			ID     int    `json:"id"`
			Method string `json:"method"`
		}
		if json.Unmarshal(sc.Bytes(), &req) != nil {
			continue
		}
		switch req.Method {
		case "initialize":
			_ = enc.Encode(map[string]any{"id": req.ID, "result": map[string]any{"codexHome": dir}})
		case "account/login/start":
			_ = enc.Encode(map[string]any{"id": req.ID, "result": map[string]any{
				"type": "chatgptDeviceCode", "loginId": "login-1",
				"verificationUrl": "https://example.test/codex-device", "userCode": "ABCD-EFGH",
			}})
			_ = os.WriteFile(marcador, []byte("ok"), 0o600)
			_ = enc.Encode(map[string]any{"method": "account/login/completed", "params": map[string]any{
				"loginId": "login-1", "success": true,
			}})
			return
		}
	}
}

func aguardarSessao(t *testing.T, g *GerenteLogin, id string, aceitar func(SessaoLogin) bool) SessaoLogin {
	t.Helper()
	limite := time.Now().Add(5 * time.Second)
	for time.Now().Before(limite) {
		s, err := g.ObterLogin(id)
		if err != nil {
			t.Fatal(err)
		}
		if aceitar(s) {
			return s
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("sessão %s não atingiu o estado esperado", id)
	return SessaoLogin{}
}

func TestLoginClaudeAssistidoComCodigo(t *testing.T) {
	g := NovoGerenteLogin()
	g.comando = comandoHelperAuth
	g.duracao = 5 * time.Second
	dir := filepath.Join(t.TempDir(), "claude")

	s, err := g.IniciarLogin("claude", dir)
	if err != nil {
		t.Fatal(err)
	}
	s = aguardarSessao(t, g, s.ID, func(s SessaoLogin) bool { return s.Estado == LoginAguardando })
	if s.URL != "https://example.test/claude-login" || !s.RequerCodigo {
		t.Fatalf("sessão Claude inesperada: %+v", s)
	}
	if _, err := g.EnviarCodigo(s.ID, "codigo-claude"); err != nil {
		t.Fatal(err)
	}
	s = aguardarSessao(t, g, s.ID, func(s SessaoLogin) bool { return loginTerminal(s.Estado) })
	if s.Estado != LoginConcluido || s.Codigo != "" {
		t.Fatalf("login Claude não concluiu de forma segura: %+v", s)
	}
	d := verificarAutenticacaoCom(context.Background(), "claude", dir, comandoHelperAuth)
	if !d.Autenticado || d.Metodo != "oauth" {
		t.Fatalf("diagnóstico Claude: %+v", d)
	}
}

func TestLoginCodexDeviceCode(t *testing.T) {
	g := NovoGerenteLogin()
	g.comando = comandoHelperAuth
	g.duracao = 5 * time.Second
	dir := filepath.Join(t.TempDir(), "codex")

	s, err := g.IniciarLogin("codex", dir)
	if err != nil {
		t.Fatal(err)
	}
	s = aguardarSessao(t, g, s.ID, func(s SessaoLogin) bool { return loginTerminal(s.Estado) })
	if s.Estado != LoginConcluido {
		t.Fatalf("login Codex: %+v", s)
	}
	// O fluxo pode concluir rapidamente, mas a URL/código públicos continuam
	// disponíveis para a UI renderizar o último estado.
	if s.URL != "https://example.test/codex-device" || s.Codigo != "ABCD-EFGH" {
		t.Fatalf("device code Codex inesperado: %+v", s)
	}
	d := verificarAutenticacaoCom(context.Background(), "codex", dir, comandoHelperAuth)
	if !d.Autenticado || d.Metodo != "chatgpt" {
		t.Fatalf("diagnóstico Codex: %+v", d)
	}
}

func TestCancelarLoginClaude(t *testing.T) {
	g := NovoGerenteLogin()
	g.comando = comandoHelperAuth
	g.duracao = 5 * time.Second
	s, err := g.IniciarLogin("claude", filepath.Join(t.TempDir(), "claude"))
	if err != nil {
		t.Fatal(err)
	}
	_ = aguardarSessao(t, g, s.ID, func(s SessaoLogin) bool { return s.Estado == LoginAguardando })
	s, err = g.CancelarLogin(s.ID)
	if err != nil || s.Estado != LoginCancelado {
		t.Fatalf("cancelamento: sessão=%+v erro=%v", s, err)
	}
}
