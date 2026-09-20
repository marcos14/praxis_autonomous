//go:build windows

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc/mgr"
)

// TestConsultarServicoSemElevacao: a consulta pede só direitos de query e
// funciona de qualquer processo (D4) — um serviço inexistente responde
// "não instalado" sem erro, nunca "acesso negado".
func TestConsultarServicoSemElevacao(t *testing.T) {
	_, instalado, err := consultarServico("praxis-teste-inexistente-xyz")
	if err != nil {
		t.Fatalf("consultar serviço inexistente: %v", err)
	}
	if instalado {
		t.Fatal("serviço inexistente reportado como instalado")
	}
	// Um serviço que existe em qualquer Windows (Event Log) é lido sem elevação.
	info, instalado, err := consultarServico("EventLog")
	if err != nil {
		t.Fatalf("consultar EventLog: %v", err)
	}
	if !instalado || info.BinPath == "" {
		t.Fatalf("EventLog: instalado=%v info=%+v", instalado, info)
	}
}

func TestNormalizarConta(t *testing.T) {
	casos := map[string]string{
		"":                          "",
		"LocalSystem":               "",
		"localsystem":               "",
		`NT AUTHORITY\SYSTEM`:       "",
		"LocalService":              `NT AUTHORITY\LocalService`,
		`nt authority\localservice`: `NT AUTHORITY\LocalService`,
		"NetworkService":            `NT AUTHORITY\NetworkService`,
		`DOMINIO\marco`:             `DOMINIO\marco`,
		`.\marco`:                   `.\marco`,
	}
	for in, quer := range casos {
		if got := normalizarConta(in); got != quer {
			t.Errorf("normalizarConta(%q) = %q, quero %q", in, got, quer)
		}
	}
	if !contaInterna("") || !contaInterna(`NT AUTHORITY\LocalService`) || contaInterna(`.\marco`) {
		t.Error("contaInterna errada")
	}
}

// TestMontarBinPath: a linha registrada cita caminhos com espaço exatamente
// como o mgr.CreateService faz, para a comparação do D7 bater.
func TestMontarBinPath(t *testing.T) {
	got := montarBinPath(`C:\Program Files\Praxis\praxis.exe`, []string{"service", "run", "-home", `C:\ProgramData\praxis`, "-addr", "127.0.0.1:7799"})
	quer := `"C:\Program Files\Praxis\praxis.exe" service run -home C:\ProgramData\praxis -addr 127.0.0.1:7799`
	if got != quer {
		t.Fatalf("binPath = %s\nquero    %s", got, quer)
	}
	if !strings.Contains(got, "service run") {
		t.Fatal("binPath deve registrar `service run`")
	}
}

// TestXMLTarefaLogon: token interativo do usuário (sem senha), disparo no logon
// dele, sem limite de execução, reinício em falha, e a ação registra
// `service run -home … -log …` com caminhos citados e XML escapado.
func TestXMLTarefaLogon(t *testing.T) {
	x := xmlTarefaLogon(`AGNES-HOME\marco`, `C:\Users\marco\AppData\Local\Programs\Praxis\praxis.exe`,
		[]string{"service", "run", "-home", `C:\Users\marco\AppData\Local\praxis`, "-log", `C:\Users\m & n\servico.log`, "-addr", "0.0.0.0:7799", "-tls"})
	for _, quer := range []string{
		`<LogonType>InteractiveToken</LogonType>`,
		`<LogonTrigger><Enabled>true</Enabled><UserId>AGNES-HOME\marco</UserId></LogonTrigger>`,
		`<ExecutionTimeLimit>PT0S</ExecutionTimeLimit>`,
		`<RestartOnFailure><Interval>PT1M</Interval><Count>3</Count></RestartOnFailure>`,
		`<MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>`,
		`<Command>C:\Users\marco\AppData\Local\Programs\Praxis\praxis.exe</Command>`,
		`<Arguments>service run -home C:\Users\marco\AppData\Local\praxis -log &#34;C:\Users\m &amp; n\servico.log&#34; -addr 0.0.0.0:7799 -tls</Arguments>`,
	} {
		if !strings.Contains(x, quer) {
			t.Errorf("xml não contém %q:\n%s", quer, x)
		}
	}
	if strings.Contains(x, "<Password>") || strings.Contains(x, "S4U") {
		t.Error("tarefa de logon não pode depender de senha")
	}
}

func TestGravarUTF16(t *testing.T) {
	caminho := filepath.Join(t.TempDir(), "t.xml")
	if err := gravarUTF16(caminho, "<a/>"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(caminho)
	quer := []byte{0xFF, 0xFE, '<', 0, 'a', 0, '/', 0, '>', 0}
	if string(b) != string(quer) {
		t.Fatalf("bytes = % x, quero % x", b, quer)
	}
}

func TestAplicarDefaultsLogon(t *testing.T) {
	t.Setenv("LOCALAPPDATA", `C:\Users\x\AppData\Local`)
	o := parseServico(t, "-logon")
	aplicarDefaultsLogon(o)
	if o.Home != `C:\Users\x\AppData\Local\praxis` || o.Dir != `C:\Users\x\AppData\Local\Programs\Praxis` {
		t.Errorf("defaults do modo logon: home=%q dir=%q", o.Home, o.Dir)
	}
	o = parseServico(t, "-logon", "-home", `D:\praxis`)
	aplicarDefaultsLogon(o)
	if o.Home != `D:\praxis` || o.Dir != `C:\Users\x\AppData\Local\Programs\Praxis` {
		t.Errorf("-home explícito deve prevalecer: home=%q dir=%q", o.Home, o.Dir)
	}
	o = parseServico(t)
	aplicarDefaultsLogon(o)
	if strings.Contains(o.Home, "AppData") {
		t.Errorf("sem -logon o home segue o de máquina: %q", o.Home)
	}
}

// TestExtrairFlagLog: o `service run` retira -home e -log e repassa o resto ao serve.
func TestExtrairFlagLog(t *testing.T) {
	home, resto := extrairFlagHome([]string{"-home", `C:\h`, "-log", `C:\h\s.log`, "-addr", ":1", "-tls"})
	logArq, resto := extrairFlag(resto, "log")
	if home != `C:\h` || logArq != `C:\h\s.log` || strings.Join(resto, " ") != "-addr :1 -tls" {
		t.Errorf("home=%q log=%q resto=%v", home, logArq, resto)
	}
}

func TestRegistroDifere(t *testing.T) {
	quer := mgr.Config{
		ServiceType:    windows.SERVICE_WIN32_OWN_PROCESS,
		StartType:      mgr.StartAutomatic,
		BinaryPathName: `"C:\Program Files\Praxis\praxis.exe" service run -home C:\ProgramData\praxis`,
		DisplayName:    nomeExibicaoServico,
		Description:    descricaoServico,
	}
	igual := quer
	igual.ServiceStartName = "LocalSystem" // como o SCM devolve
	if registroDifere(igual, quer) {
		t.Error("registro igual reportado como diferente")
	}
	binAntigo := quer
	binAntigo.BinaryPathName = `"C:\Users\x\Downloads\praxis.exe" serve`
	if !registroDifere(binAntigo, quer) {
		t.Error("binPath diferente não detectado")
	}
	contaOutra := quer
	contaOutra.ServiceStartName = `NT AUTHORITY\LocalService`
	if !registroDifere(contaOutra, quer) {
		t.Error("conta diferente não detectada")
	}
}
