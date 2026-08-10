//go:build !windows

package servico

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCaminhoUnitRespeitaVariavelDeAmbiente(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PRAXIS_UNIT_DIR", dir)
	if got, esperado := caminhoUnit("praxis"), filepath.Join(dir, "praxis.service"); got != esperado {
		t.Fatalf("caminhoUnit = %q, esperado %q", got, esperado)
	}
}

// Consultar é o que o `praxis service status` chama, e tem de responder sem
// systemd e sem privilégio: a unit em disco é a fonte do que foi registrado.
func TestConsultarLeAUnitEmDisco(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PRAXIS_UNIT_DIR", dir)

	st, err := Consultar("praxis")
	if err != nil {
		t.Fatalf("Consultar sem unit: %v", err)
	}
	if st.Instalado {
		t.Fatal("sem unit em disco o serviço não pode aparecer como instalado")
	}

	o := Opcoes{
		Exe:     "/usr/local/bin/praxis",
		Args:    []string{"serve", "-addr", "127.0.0.1:7799"},
		Home:    "/home/ana/.config/praxis",
		Usuario: "ana",
	}.ComPadroes()
	if err := os.WriteFile(filepath.Join(dir, "praxis.service"), []byte(UnitSystemd(o)), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err = Consultar("praxis")
	if err != nil {
		t.Fatalf("Consultar com unit: %v", err)
	}
	if !st.Instalado {
		t.Fatal("com a unit em disco o serviço deveria aparecer como instalado")
	}
	if st.Exe != "/usr/local/bin/praxis" {
		t.Errorf("Exe = %q", st.Exe)
	}
	if st.Conta != "ana" {
		t.Errorf("Conta = %q, esperado ana", st.Conta)
	}
	if st.LinhaComando != "/usr/local/bin/praxis serve -addr 127.0.0.1:7799" {
		t.Errorf("LinhaComando = %q", st.LinhaComando)
	}
}

// Sem User= na unit, quem roda é o root — e o status tem de dizer isso, não deixar
// o campo em branco.
func TestConsultarSemUserAssumeRoot(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PRAXIS_UNIT_DIR", dir)
	o := Opcoes{Exe: "/usr/local/bin/praxis", Args: []string{"serve"}}.ComPadroes()
	if err := os.WriteFile(filepath.Join(dir, "praxis.service"), []byte(UnitSystemd(o)), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := Consultar("praxis")
	if err != nil {
		t.Fatal(err)
	}
	if st.Conta != "root" {
		t.Fatalf("Conta = %q, esperado root", st.Conta)
	}
}

func TestRemoverSemUnitFalhaComMensagemClara(t *testing.T) {
	t.Setenv("PRAXIS_UNIT_DIR", t.TempDir())
	err := Remover("praxis", nil)
	if err == nil {
		t.Fatal("remover um serviço inexistente deveria falhar")
	}
	if !strings.Contains(err.Error(), "não está instalado") {
		t.Fatalf("mensagem pouco clara: %v", err)
	}
}

// Instalar exige caminho absoluto: uma unit com ExecStart relativo é recusada pelo
// systemd depois, com uma mensagem muito menos óbvia.
func TestInstalarRecusaExeRelativo(t *testing.T) {
	t.Setenv("PRAXIS_UNIT_DIR", t.TempDir())
	if !temSystemd() {
		t.Skip("sem systemd nesta máquina")
	}
	err := Instalar(Opcoes{Exe: "praxis", Args: []string{"serve"}}, nil)
	if err == nil || !strings.Contains(err.Error(), "caminho absoluto") {
		t.Fatalf("esperava recusa por caminho relativo, veio: %v", err)
	}
}
