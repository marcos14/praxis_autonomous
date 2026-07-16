package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunVersion(t *testing.T) {
	var out, errOut bytes.Buffer
	if err := run([]string{"-version"}, &out, &errOut); err != nil {
		t.Fatalf("run(-version) retornou erro: %v", err)
	}
	if got := strings.TrimSpace(out.String()); got != versao {
		t.Fatalf("saída = %q, esperado %q", got, versao)
	}
}

func TestRunServeStub(t *testing.T) {
	var out, errOut bytes.Buffer
	if err := run([]string{"serve"}, &out, &errOut); err != nil {
		t.Fatalf("run(serve) retornou erro: %v", err)
	}
	if !strings.Contains(out.String(), "serve") {
		t.Fatalf("saída do serve não menciona o subcomando: %q", out.String())
	}
}

func TestRunSemSubcomando(t *testing.T) {
	var out, errOut bytes.Buffer
	if err := run(nil, &out, &errOut); err == nil {
		t.Fatal("run(nil) deveria exigir subcomando, mas não retornou erro")
	}
}

func TestRunSubcomandoDesconhecido(t *testing.T) {
	var out, errOut bytes.Buffer
	if err := run([]string{"foo"}, &out, &errOut); err == nil {
		t.Fatal("run(foo) deveria falhar para subcomando desconhecido")
	}
}
