package servico

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func opcoesTeste() Opcoes {
	return Opcoes{
		Exe:     "/usr/local/bin/praxis",
		Args:    []string{"serve", "-addr", "127.0.0.1:7799", "-home", "/home/ana/.config/praxis"},
		Home:    "/home/ana/.config/praxis",
		Usuario: "ana",
	}
}

func TestComPadroesPreencheIdentificacao(t *testing.T) {
	o := Opcoes{}.ComPadroes()
	if o.Nome != NomePadrao || o.Display != DisplayPadrao || o.Descricao != DescricaoPadrao {
		t.Fatalf("padrões não aplicados: %+v", o)
	}
	// O que já veio preenchido não é sobrescrito.
	o = Opcoes{Nome: "praxis-teste"}.ComPadroes()
	if o.Nome != "praxis-teste" {
		t.Fatalf("nome sobrescrito: %q", o.Nome)
	}
}

func TestUnitSystemd(t *testing.T) {
	u := UnitSystemd(opcoesTeste())
	esperados := []string{
		"[Unit]",
		"[Service]",
		"Type=simple",
		"ExecStart=/usr/local/bin/praxis serve -addr 127.0.0.1:7799 -home /home/ana/.config/praxis",
		"Environment=PRAXIS_HOME=/home/ana/.config/praxis",
		"WorkingDirectory=/home/ana/.config/praxis",
		"User=ana",
		"Restart=on-failure",
		// O praxis encerra os harnesses no shutdown gracioso: o SIGTERM vai só ao
		// processo principal.
		"KillMode=mixed",
		"[Install]",
		"WantedBy=multi-user.target",
	}
	for _, e := range esperados {
		if !strings.Contains(u, e) {
			t.Fatalf("unit não contém %q:\n%s", e, u)
		}
	}
}

func TestUnitSystemdSemUsuarioNaoEmiteUser(t *testing.T) {
	o := opcoesTeste()
	o.Usuario = ""
	if u := UnitSystemd(o); strings.Contains(u, "User=") {
		t.Fatalf("unit sem conta não deveria trazer User=:\n%s", u)
	}
}

func TestUnitSystemdCitaCaminhoComEspaco(t *testing.T) {
	o := opcoesTeste()
	o.Exe = "/opt/mais praxis/praxis"
	o.Args = []string{"serve", "-home", "/srv/dados do praxis"}
	u := UnitSystemd(o)
	if !strings.Contains(u, `ExecStart="/opt/mais praxis/praxis" serve -home "/srv/dados do praxis"`) {
		t.Fatalf("ExecStart não citou os caminhos com espaço:\n%s", u)
	}
}

func TestComandoSC(t *testing.T) {
	o := opcoesTeste()
	o.Exe = `C:\Program Files\Praxis\praxis.exe`
	c := ComandoSC(o)
	for _, e := range []string{
		"sc.exe create praxis",
		// As aspas do caminho ficam escapadas: o binPath do sc.exe é UMA string.
		`\"C:\Program Files\Praxis\praxis.exe\"`,
		"start= auto",
		`obj= "ana"`,
	} {
		if !strings.Contains(c, e) {
			t.Fatalf("comando sc.exe não contém %q:\n%s", e, c)
		}
	}
}

func TestComandoSCSemUsuarioNaoPedeConta(t *testing.T) {
	o := opcoesTeste()
	o.Usuario = ""
	if c := ComandoSC(o); strings.Contains(c, "obj=") {
		t.Fatalf("sem -usuario o comando não deveria trazer obj=:\n%s", c)
	}
}

func TestExeDoComando(t *testing.T) {
	casos := []struct{ entrada, esperado string }{
		{`"C:\Program Files\Praxis\praxis.exe" serve -addr 127.0.0.1:7799`, `C:\Program Files\Praxis\praxis.exe`},
		{`C:\Praxis\praxis.exe serve -addr x`, `C:\Praxis\praxis.exe`},
		{`/usr/local/bin/praxis serve`, `/usr/local/bin/praxis`},
		{`/usr/local/bin/praxis`, `/usr/local/bin/praxis`},
		{`  "C:\a b\praxis.exe"  `, `C:\a b\praxis.exe`},
		{``, ``},
	}
	for _, c := range casos {
		if got := ExeDoComando(c.entrada); got != c.esperado {
			t.Errorf("ExeDoComando(%q) = %q, esperado %q", c.entrada, got, c.esperado)
		}
	}
}

func TestMesmoCaminho(t *testing.T) {
	if !MesmoCaminho("/opt/praxis/./praxis", "/opt/praxis/praxis") {
		t.Error("segmentos redundantes deveriam ser normalizados")
	}
	if MesmoCaminho("", "/opt/praxis") || MesmoCaminho("/opt/praxis", "") {
		t.Error("caminho vazio nunca é igual a outro")
	}
	// Maiúsculas só são ignoradas onde o sistema de arquivos as ignora.
	igualIgnorandoCaixa := MesmoCaminho(`C:\Praxis\praxis.exe`, `c:\praxis\PRAXIS.EXE`)
	if igualIgnorandoCaixa != (runtime.GOOS == "windows") {
		t.Errorf("comparação por caixa em %s = %v", runtime.GOOS, igualIgnorandoCaixa)
	}
}

func TestCopiarBinario(t *testing.T) {
	dir := t.TempDir()
	origem := filepath.Join(dir, "praxis-novo")
	if err := os.WriteFile(origem, []byte("versao 2"), 0o755); err != nil {
		t.Fatal(err)
	}
	destino := filepath.Join(dir, "destino", NomeExecutavel())

	if err := CopiarBinario(origem, destino); err != nil {
		t.Fatalf("primeira cópia: %v", err)
	}
	if b, err := os.ReadFile(destino); err != nil || string(b) != "versao 2" {
		t.Fatalf("conteúdo copiado = %q, err = %v", b, err)
	}

	// Reinstalação sobre um binário que já existe: é o caso normal, não erro.
	if err := os.WriteFile(origem, []byte("versao 3"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := CopiarBinario(origem, destino); err != nil {
		t.Fatalf("segunda cópia: %v", err)
	}
	if b, _ := os.ReadFile(destino); string(b) != "versao 3" {
		t.Fatalf("o binário não foi atualizado: %q", b)
	}

	// Copiar sobre si mesmo não faz nada (e não trunca o arquivo).
	if err := CopiarBinario(destino, destino); err != nil {
		t.Fatalf("cópia para o mesmo caminho: %v", err)
	}
	if b, _ := os.ReadFile(destino); string(b) != "versao 3" {
		t.Fatalf("cópia para o mesmo caminho alterou o arquivo: %q", b)
	}
}
