package pipeline

import "testing"

func TestResolverGates(t *testing.T) {
	r := &Runner{}

	// sem gates fixos e sem gates na config → nil (etapa aprovada).
	if g := r.resolverGates(Config{}, ""); g != nil {
		t.Fatalf("sem gates deveria devolver nil, got %#v", g)
	}

	// gates na config → monta um RunnerGates com os comandos.
	cfg := Config{Gates: []Gate{{Nome: "gates", Comandos: []string{"go build ./..."}}}}
	g := r.resolverGates(cfg, "/tmp/logs")
	rg, ok := g.(*RunnerGates)
	if !ok {
		t.Fatalf("esperava *RunnerGates, got %T", g)
	}
	if len(rg.Gates) != 1 || rg.Gates[0].Comandos[0] != "go build ./..." {
		t.Fatalf("gates não propagados: %#v", rg.Gates)
	}

	// Gates fixo no Runner tem precedência (usado pelos testes).
	fixo := &RunnerGates{}
	r2 := &Runner{Gates: fixo}
	if g := r2.resolverGates(cfg, ""); g != fixo {
		t.Fatalf("Gates fixo deveria ter precedência")
	}
}
