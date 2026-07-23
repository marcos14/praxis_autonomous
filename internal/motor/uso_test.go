package motor

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// comandoHelperUso injeta variáveis extras no processo-helper (além do padrão).
func comandoHelperUso(extras ...string) fabricaComando {
	return func(ctx context.Context, nome string, args ...string) *exec.Cmd {
		cmd := comandoHelperAuth(ctx, nome, args...)
		cmd.Env = append(cmd.Env, extras...)
		return cmd
	}
}

func TestConsultarFranquiaClaudeIndisponivel(t *testing.T) {
	u := consultarFranquiaCom(context.Background(), "claude", t.TempDir(), comandoHelperUso())
	if u.Disponivel {
		t.Fatal("franquia do Claude deveria vir indisponível (sem API headless)")
	}
	if u.Mensagem == "" || u.Vendor != "claude" {
		t.Fatalf("diagnóstico incompleto: %+v", u)
	}
}

func TestConsultarFranquiaMotorSemPerfil(t *testing.T) {
	u := consultarFranquiaCom(context.Background(), "opencode", t.TempDir(), comandoHelperUso())
	if u.Disponivel || u.Mensagem == "" {
		t.Fatalf("motor sem perfil isolado deveria vir indisponível: %+v", u)
	}
}

func TestConsultarFranquiaCodexOK(t *testing.T) {
	u := consultarFranquiaCom(context.Background(), "codex", t.TempDir(), comandoHelperUso())
	if !u.Disponivel {
		t.Fatalf("franquia deveria estar disponível: %+v", u)
	}
	if len(u.Janelas) != 2 {
		t.Fatalf("janelas = %+v, quero 2", u.Janelas)
	}
	// primária: 300min → "5h", camelCase; secundária: 10080min → "7d", snake_case.
	if u.Janelas[0].Rotulo != "5h" || u.Janelas[0].UsadoPct != 34.5 {
		t.Fatalf("janela primária = %+v", u.Janelas[0])
	}
	if u.Janelas[1].Rotulo != "7d" || u.Janelas[1].UsadoPct != 12.0 {
		t.Fatalf("janela secundária = %+v", u.Janelas[1])
	}
	if u.Janelas[0].ResetEm == nil || !u.Janelas[0].ResetEm.After(u.VerificadoEm) {
		t.Fatalf("reset da primária deveria ser após a verificação: %+v", u.Janelas[0])
	}
}

func TestConsultarFranquiaCodexSemContrato(t *testing.T) {
	u := consultarFranquiaCom(context.Background(), "codex", t.TempDir(),
		comandoHelperUso("PRAXIS_USO_SEM_CONTRATO=1"))
	if u.Disponivel {
		t.Fatalf("versão sem o contrato deveria vir indisponível: %+v", u)
	}
	if !strings.Contains(u.Mensagem, "não expõe a franquia") {
		t.Fatalf("mensagem = %q", u.Mensagem)
	}
}

func TestConsultarFranquiaCodexCLIAusente(t *testing.T) {
	// PATH vazio: o exec.LookPath falha e a mensagem aponta o CLI ausente.
	t.Setenv("PATH", t.TempDir())
	u := ConsultarFranquia(context.Background(), "codex", t.TempDir())
	if u.Disponivel {
		t.Fatalf("sem CLI deveria vir indisponível: %+v", u)
	}
	if !strings.Contains(u.Mensagem, "não encontrado") {
		t.Fatalf("mensagem = %q", u.Mensagem)
	}
	_ = os.Unsetenv("PRAXIS_USO_SEM_CONTRATO")
}
