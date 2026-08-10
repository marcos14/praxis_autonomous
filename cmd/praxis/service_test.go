package main

import (
	"bytes"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestServiceExigeSubcomando(t *testing.T) {
	var out, errOut bytes.Buffer
	if err := service(nil, &out, &errOut); err == nil {
		t.Fatal("`praxis service` sem subcomando deveria falhar")
	}
	if !strings.Contains(errOut.String(), "install") {
		t.Fatalf("o uso deveria listar os subcomandos:\n%s", errOut.String())
	}
}

func TestServiceSubcomandoDesconhecido(t *testing.T) {
	var out, errOut bytes.Buffer
	if err := service([]string{"instalar-tudo"}, &out, &errOut); err == nil {
		t.Fatal("subcomando desconhecido deveria falhar")
	}
}

// print imprime o artefato da plataforma que está rodando — e é o mesmo serviço
// que o install registraria, com os mesmos argumentos.
func TestServicePrintDescreveOMesmoServico(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PRAXIS_HOME", home)

	var out, errOut bytes.Buffer
	if err := service([]string{"print", "-addr", "127.0.0.1:7799"}, &out, &errOut); err != nil {
		t.Fatalf("service print: %v (%s)", err, errOut.String())
	}
	s := out.String()
	for _, e := range []string{"serve", "-addr 127.0.0.1:7799", "-home " + home} {
		if !strings.Contains(s, e) {
			t.Fatalf("saída não contém %q:\n%s", e, s)
		}
	}
	esperado := "[Unit]"
	if runtime.GOOS == "windows" {
		esperado = "sc.exe create praxis"
	}
	if !strings.Contains(s, esperado) {
		t.Fatalf("saída não parece a da plataforma (%s), esperava %q:\n%s", runtime.GOOS, esperado, s)
	}
}

func TestServicePrintUsaODestinoEstavel(t *testing.T) {
	t.Setenv("PRAXIS_HOME", t.TempDir())
	var out, errOut bytes.Buffer
	if err := service([]string{"print", "-destino", filepath.Join("opt", "praxis")}, &out, &errOut); err != nil {
		t.Fatalf("service print: %v (%s)", err, errOut.String())
	}
	// O binário registrado é o do -destino, não o executável do teste: um serviço
	// apontando para a pasta de onde alguém rodou o instalador para de subir.
	if !strings.Contains(out.String(), filepath.Join("opt", "praxis", "praxis")) {
		t.Fatalf("o executável registrado não está em -destino:\n%s", out.String())
	}
}

// -sem-copia é a escapatória para quem quer o binário onde ele já está; o registro
// então aponta para o executável atual.
func TestServicePrintSemCopiaUsaOExecutavelAtual(t *testing.T) {
	t.Setenv("PRAXIS_HOME", t.TempDir())
	var out, errOut bytes.Buffer
	if err := service([]string{"print", "-sem-copia"}, &out, &errOut); err != nil {
		t.Fatalf("service print: %v (%s)", err, errOut.String())
	}
	if strings.Contains(out.String(), filepath.Join("Program Files", "Praxis")) ||
		strings.Contains(out.String(), "/usr/local/bin/praxis") {
		t.Fatalf("-sem-copia não deveria apontar para o destino padrão:\n%s", out.String())
	}
}

func TestArgsServe(t *testing.T) {
	// O -home vai SEMPRE: o serviço roda com outra conta (LocalSystem no Windows),
	// e sem ele abriria um banco vazio no perfil dessa conta.
	args := argsServe("0.0.0.0:7799", "/srv/praxis", false, "", "")
	esperado := "serve -addr 0.0.0.0:7799 -home /srv/praxis"
	if got := strings.Join(args, " "); got != esperado {
		t.Fatalf("argsServe = %q, esperado %q", got, esperado)
	}

	// As flags de TLS só entram quando pedidas — o serviço herda o default do
	// serve para o que não foi passado.
	args = argsServe("0.0.0.0:7799", "/srv/praxis", true, "/etc/praxis/cert.pem", "/etc/praxis/key.pem")
	for _, e := range []string{"-tls", "-tls-cert /etc/praxis/cert.pem", "-tls-key /etc/praxis/key.pem"} {
		if !strings.Contains(strings.Join(args, " "), e) {
			t.Fatalf("argsServe não inclui %q: %v", e, args)
		}
	}
}

func TestHomePadraoServicoRespeitaVariavelDeAmbiente(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PRAXIS_HOME", dir)
	got, err := homePadraoServico("")
	if err != nil {
		t.Fatal(err)
	}
	if got != dir {
		t.Fatalf("homePadraoServico = %q, esperado %q", got, dir)
	}
}

// No Linux, `sudo praxis service install` instala para a conta que chamou o sudo:
// é onde vivem o git, o ssh e a configuração do harness.
func TestContaPadraoSegueOSudoUser(t *testing.T) {
	t.Setenv("SUDO_USER", "ana")
	if got := contaPadrao(); got != "ana" {
		t.Fatalf("contaPadrao = %q, esperado ana", got)
	}
	// root já é o padrão da plataforma: não vale gravar User=root na unit.
	t.Setenv("SUDO_USER", "root")
	if got := contaPadrao(); got != "" {
		t.Fatalf("contaPadrao com SUDO_USER=root = %q, esperado vazio", got)
	}
	t.Setenv("SUDO_USER", "")
	if got := contaPadrao(); got != "" {
		t.Fatalf("contaPadrao sem SUDO_USER = %q, esperado vazio", got)
	}
}

func TestContaEmTextoNomeiaOPadraoDaPlataforma(t *testing.T) {
	if got := contaEmTexto("ana"); got != "ana" {
		t.Fatalf("contaEmTexto(ana) = %q", got)
	}
	esperado := "root"
	if runtime.GOOS == "windows" {
		esperado = "LocalSystem"
	}
	if got := contaEmTexto(""); got != esperado {
		t.Fatalf("contaEmTexto(\"\") em %s = %q, esperado %q", runtime.GOOS, got, esperado)
	}
}

// status responde sem privilégio e sem serviço instalado — é a primeira coisa que
// alguém roda quando o serviço "não está funcionando".
func TestServiceStatusSemServicoInstalado(t *testing.T) {
	t.Setenv("PRAXIS_UNIT_DIR", t.TempDir()) // ignorado no Windows
	var out, errOut bytes.Buffer
	if err := service([]string{"status", "-nome", "praxis-inexistente-teste"}, &out, &errOut); err != nil {
		t.Fatalf("service status: %v (%s)", err, errOut.String())
	}
	if !strings.Contains(out.String(), "não instalado") {
		t.Fatalf("status deveria dizer que não está instalado:\n%s", out.String())
	}
}
