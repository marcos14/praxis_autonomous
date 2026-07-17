package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestServiceGeraConfig(t *testing.T) {
	var out, errOut bytes.Buffer
	if err := service([]string{"-exe", "/opt/praxis/praxis", "-addr", "127.0.0.1:7799", "-nome", "praxis"}, &out, &errOut); err != nil {
		t.Fatalf("service: %v", err)
	}
	s := out.String()
	// no ambiente de teste (não-Windows na CI típica) sai a unit systemd; no
	// Windows sai o sc.exe. Aceita qualquer um, mas o executável deve aparecer.
	if !strings.Contains(s, "/opt/praxis/praxis") && !strings.Contains(s, "praxis") {
		t.Fatalf("saída não menciona o executável:\n%s", s)
	}
	if !strings.Contains(s, "serve") {
		t.Fatalf("saída não inclui o subcomando serve:\n%s", s)
	}
}

func TestUnitSystemd(t *testing.T) {
	u := unitSystemd("/opt/praxis/praxis", "127.0.0.1:7799")
	for _, esperado := range []string{"[Unit]", "[Service]", "ExecStart=/opt/praxis/praxis serve -addr 127.0.0.1:7799", "WantedBy=multi-user.target"} {
		if !strings.Contains(u, esperado) {
			t.Fatalf("unit não contém %q:\n%s", esperado, u)
		}
	}
}

func TestComandoServicoWindows(t *testing.T) {
	c := comandoServicoWindows("praxis", `C:\praxis\praxis.exe`, "127.0.0.1:7799")
	if !strings.Contains(c, "sc.exe create praxis") || !strings.Contains(c, "serve -addr") {
		t.Fatalf("comando windows inesperado: %s", c)
	}
}
