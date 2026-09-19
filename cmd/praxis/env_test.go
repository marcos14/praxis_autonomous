package main

import "testing"

func TestEnvBool(t *testing.T) {
	const nome = "PRAXIS_TESTE_ENV_BOOL"
	casos := map[string]bool{
		"": false, "0": false, "false": false, "não": false, "off": false,
		"1": true, "true": true, "TRUE": true, "yes": true, "sim": true, "on": true, " 1 ": true,
	}
	for valor, quero := range casos {
		t.Setenv(nome, valor)
		if got := envBool(nome); got != quero {
			t.Errorf("envBool(%q) = %v, quero %v", valor, got, quero)
		}
	}
}
