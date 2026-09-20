package main

import (
	"bytes"
	"context"
	"flag"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// parseServico monta as opções de instalação a partir de args, como o
// `service install` faz — inclusive o registro de quais flags foram explícitas.
func parseServico(t *testing.T, args ...string) *opcoesServico {
	t.Helper()
	fs := flag.NewFlagSet("teste", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	o := flagsServico(fs)
	if err := fs.Parse(args); err != nil {
		t.Fatalf("parse %v: %v", args, err)
	}
	o.registrarExplicitas(fs)
	return o
}

func TestServiceSemSubcomando(t *testing.T) {
	var out, errOut bytes.Buffer
	if err := service(context.Background(), nil, &out, &errOut); err == nil {
		t.Fatal("service sem subcomando deveria falhar")
	}
	if !strings.Contains(errOut.String(), "install") || !strings.Contains(errOut.String(), "status") {
		t.Fatalf("uso não lista os modos:\n%s", errOut.String())
	}
	if err := service(context.Background(), []string{"foo"}, &out, &errOut); err == nil {
		t.Fatal("service foo deveria falhar")
	}
}

// TestServiceUnitNaoTocaNaMaquina: o modo `unit` só imprime o artefato de
// registro — funciona sem privilégio e sem SCM/systemd.
func TestServiceUnitNaoTocaNaMaquina(t *testing.T) {
	var out, errOut bytes.Buffer
	err := service(context.Background(), []string{"unit", "-exe", "/opt/praxis/praxis", "-addr", "0.0.0.0:7799", "-tls"}, &out, &errOut)
	if err != nil {
		t.Fatalf("service unit: %v", err)
	}
	s := out.String()
	for _, quer := range []string{"/opt/praxis/praxis", "-addr 0.0.0.0:7799", "-tls", nomeServico} {
		if !strings.Contains(s, quer) {
			t.Errorf("saída não contém %q:\n%s", quer, s)
		}
	}
	// Nunca registra `serve` cru no Windows nem esquece o home no unit.
	if !strings.Contains(s, "PRAXIS_HOME") && !strings.Contains(s, "-home") {
		t.Errorf("saída não fixa o PRAXIS_HOME do serviço:\n%s", s)
	}
}

// TestArgsServeSoExplicitas: só o que foi pedido na instalação vai para o
// registro (§1.2 da ADR) — defaults do binário não são congelados.
func TestArgsServeSoExplicitas(t *testing.T) {
	casos := []struct {
		args []string
		quer []string
	}{
		{nil, nil},
		{[]string{"-addr", enderecoPadrao}, []string{"-addr", enderecoPadrao}}, // explícito, mesmo igual ao default
		{[]string{"-tls"}, []string{"-tls"}},
		{[]string{"-tls=false"}, nil},
		{[]string{"-proxy-confiavel", "-tls-cert", "c.pem", "-tls-key", "k.pem"}, []string{"-tls-cert", "c.pem", "-tls-key", "k.pem", "-proxy-confiavel"}},
		{[]string{"-home", "/x", "-dir", "/y"}, nil}, // flags do registro, não do serve
	}
	for _, c := range casos {
		got := parseServico(t, c.args...).argsServe()
		if !reflect.DeepEqual(got, c.quer) {
			t.Errorf("args %v: argsServe = %v, quero %v", c.args, got, c.quer)
		}
	}
}

func TestUnitSystemd(t *testing.T) {
	o := parseServico(t, "-addr", "127.0.0.1:7799", "-home", "/var/lib/praxis")
	o.Usuario = "marco" // a flag -usuario só existe fora do Windows; o campo vale nas duas plataformas
	u := unitSystemd("/usr/local/bin/praxis", o)
	for _, quer := range []string{
		"[Unit]", "[Service]", "[Install]",
		"Type=simple",
		"User=marco",
		"Environment=PRAXIS_HOME=/var/lib/praxis",
		"Environment=PRAXIS_MANAGED=systemd",
		"EnvironmentFile=-" + caminhoEnvServico,
		"WorkingDirectory=/var/lib/praxis",
		"ExecStart=/usr/local/bin/praxis serve -addr 127.0.0.1:7799",
		"Restart=always",
		"RestartSec=",
		"After=network-online.target",
		"WantedBy=multi-user.target",
	} {
		if !strings.Contains(u, quer) {
			t.Errorf("unit não contém %q:\n%s", quer, u)
		}
	}
	for _, nao := range []string{"Type=forking", "PIDFile", "enable --now"} {
		if strings.Contains(u, nao) {
			t.Errorf("unit não deveria conter %q", nao)
		}
	}
	// Caminho com espaço vai entre aspas no ExecStart.
	u2 := unitSystemd("/opt/meu praxis/praxis", parseServico(t))
	if !strings.Contains(u2, `ExecStart="/opt/meu praxis/praxis" serve`) {
		t.Errorf("ExecStart não cita caminho com espaço:\n%s", u2)
	}
}

func TestComandoServicoWindows(t *testing.T) {
	o := parseServico(t, "-addr", "127.0.0.1:7799")
	o.Home = `C:\ProgramData\praxis`
	c := comandoServicoWindows("praxis", `C:\Program Files\Praxis\praxis.exe`, o)
	for _, quer := range []string{
		`sc.exe create praxis`,
		`\"C:\Program Files\Praxis\praxis.exe\" service run -home \"C:\ProgramData\praxis\" -addr 127.0.0.1:7799`,
		`start= auto`,
		`DisplayName= "` + nomeExibicaoServico + `"`,
	} {
		if !strings.Contains(c, quer) {
			t.Errorf("comando não contém %q:\n%s", quer, c)
		}
	}
	// O binPath registra `service run`, nunca `serve` direto (erro 1053 no SCM).
	if strings.Contains(c, `.exe\" serve`) {
		t.Errorf("binPath registra `serve` cru: %s", c)
	}
}

func TestExtrairFlagHome(t *testing.T) {
	casos := []struct {
		args  []string
		home  string
		resto []string
	}{
		{nil, "", nil},
		{[]string{"-addr", ":1"}, "", []string{"-addr", ":1"}},
		{[]string{"-home", `C:\x`, "-addr", ":1"}, `C:\x`, []string{"-addr", ":1"}},
		{[]string{"-addr", ":1", "--home=/var/lib/praxis", "-tls"}, "/var/lib/praxis", []string{"-addr", ":1", "-tls"}},
		{[]string{"-home"}, "", []string{"-home"}}, // sem valor: fica para o serve reclamar
	}
	for _, c := range casos {
		home, resto := extrairFlagHome(c.args)
		if home != c.home || !reflect.DeepEqual(resto, c.resto) {
			t.Errorf("extrairFlagHome(%v) = (%q, %v), quero (%q, %v)", c.args, home, resto, c.home, c.resto)
		}
	}
}

func TestDefinirHome(t *testing.T) {
	t.Setenv("PRAXIS_HOME", "original")
	if err := definirHome("  "); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("PRAXIS_HOME"); got != "original" {
		t.Fatalf("home vazio não deveria mexer no ambiente; PRAXIS_HOME = %q", got)
	}
	if err := definirHome("/novo"); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("PRAXIS_HOME"); got != "/novo" {
		t.Fatalf("PRAXIS_HOME = %q, quero /novo", got)
	}
}

// TestNovoLoggerSobSystemd: sob o systemd o log não carimba hora (o journald
// carimba); fora dele, carimba.
func TestNovoLoggerSobSystemd(t *testing.T) {
	var b bytes.Buffer
	t.Setenv("PRAXIS_MANAGED", "systemd")
	novoLogger(&b).Info("oi")
	if strings.Contains(b.String(), "time=") {
		t.Errorf("sob systemd não deveria carimbar hora: %q", b.String())
	}
	b.Reset()
	t.Setenv("PRAXIS_MANAGED", "")
	novoLogger(&b).Info("oi")
	if !strings.Contains(b.String(), "time=") {
		t.Errorf("fora do systemd deveria carimbar hora: %q", b.String())
	}
}

func TestInstalarBinario(t *testing.T) {
	dir := t.TempDir()
	origem := filepath.Join(dir, "origem.bin")
	if err := os.WriteFile(origem, []byte("v1"), 0o755); err != nil {
		t.Fatal(err)
	}
	destino := filepath.Join(dir, "bin", "praxis")

	copiado, err := instalarBinario(origem, destino)
	if err != nil || !copiado {
		t.Fatalf("primeira cópia: copiado=%v err=%v", copiado, err)
	}
	if b, _ := os.ReadFile(destino); string(b) != "v1" {
		t.Fatalf("destino = %q, quero v1", b)
	}
	// Idempotente: mesma origem/destino → nada a copiar.
	if copiado, err := instalarBinario(destino, destino); err != nil || copiado {
		t.Fatalf("mesmo arquivo: copiado=%v err=%v", copiado, err)
	}
	// Atualização: nova versão substitui a antiga.
	if err := os.WriteFile(origem, []byte("v2"), 0o755); err != nil {
		t.Fatal(err)
	}
	if copiado, err := instalarBinario(origem, destino); err != nil || !copiado {
		t.Fatalf("atualização: copiado=%v err=%v", copiado, err)
	}
	if b, _ := os.ReadFile(destino); string(b) != "v2" {
		t.Fatalf("destino = %q, quero v2", b)
	}
	if _, err := os.Stat(destino + ".novo"); !os.IsNotExist(err) {
		t.Fatalf("arquivo temporário não foi removido")
	}
}

// TestVerificarPorta: porta ocupada ou endereço inválido abortam antes de
// registrar; porta livre passa e é liberada de novo.
func TestVerificarPorta(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	ocupado := ln.Addr().String()
	if err := verificarPorta(ocupado); err == nil {
		t.Fatalf("porta ocupada %s deveria falhar", ocupado)
	}
	if err := verificarPorta("127.0.0.1:0"); err != nil {
		t.Fatalf("porta livre: %v", err)
	}
	if err := verificarPorta("nao-e-endereco"); err == nil {
		t.Fatal("endereço inválido deveria falhar")
	}
}

func TestURLAcessoEHostSaude(t *testing.T) {
	o := parseServico(t, "-addr", "0.0.0.0:7799", "-tls")
	if got := o.urlAcesso(); got != "https://<ip-desta-máquina>:7799" {
		t.Errorf("urlAcesso = %q", got)
	}
	if got := hostSaude(o.Addr, o.comTLS()); got != "https://127.0.0.1:7799/healthz" {
		t.Errorf("hostSaude = %q", got)
	}
	o = parseServico(t)
	if got := o.urlAcesso(); got != "http://127.0.0.1:7799" {
		t.Errorf("urlAcesso default = %q", got)
	}
	o = parseServico(t, "-addr", "[::1]:8443", "-tls-cert", "c.pem", "-tls-key", "k.pem")
	if !o.comTLS() || o.urlAcesso() != "https://[::1]:8443" || hostSaude(o.Addr, true) != "https://[::1]:8443/healthz" {
		t.Errorf("ipv6: url=%q health=%q tls=%v", o.urlAcesso(), hostSaude(o.Addr, true), o.comTLS())
	}
}

// TestEsperarSaude: responde assim que houver qualquer resposta HTTP (mesmo
// redirect, mesmo TLS autoassinado) e falha no prazo quando não há servidor.
func TestEsperarSaude(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			t.Errorf("path = %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusServiceUnavailable) // "degradado" ainda é resposta
	}))
	defer srv.Close()
	addr := strings.TrimPrefix(srv.URL, "https://")
	if err := esperarSaude(addr, true, 5*time.Second); err != nil {
		t.Fatalf("esperarSaude com servidor TLS: %v", err)
	}
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	livre := ln.Addr().String()
	ln.Close()
	if err := esperarSaude(livre, false, 600*time.Millisecond); err == nil {
		t.Fatal("sem servidor deveria falhar no prazo")
	}
}

// TestUnitSystemdPortaPrivilegiada: porta < 1024 com usuário comum ganha a
// capability de bind; com root ou porta alta, só o comentário.
func TestUnitSystemdPortaPrivilegiada(t *testing.T) {
	o := parseServico(t, "-addr", "0.0.0.0:443", "-tls")
	o.Usuario = "marco"
	if u := unitSystemd("/usr/local/bin/praxis", o); !strings.Contains(u, "\nAmbientCapabilities=CAP_NET_BIND_SERVICE\n") {
		t.Errorf("porta 443 com usuário comum sem capability:\n%s", u)
	}
	o.Usuario = "root"
	if u := unitSystemd("/usr/local/bin/praxis", o); strings.Contains(u, "\nAmbientCapabilities=") {
		t.Errorf("root não precisa de capability:\n%s", u)
	}
	o = parseServico(t, "-addr", "0.0.0.0:7799")
	o.Usuario = "marco"
	if u := unitSystemd("/usr/local/bin/praxis", o); strings.Contains(u, "\nAmbientCapabilities=") {
		t.Errorf("porta alta não precisa de capability:\n%s", u)
	}
}

func TestCitarUnit(t *testing.T) {
	if got := citarUnit("/usr/local/bin/praxis"); got != "/usr/local/bin/praxis" {
		t.Errorf("sem espaço: %q", got)
	}
	if got := citarUnit("/opt/a b/praxis"); got != `"/opt/a b/praxis"` {
		t.Errorf("com espaço: %q", got)
	}
}
