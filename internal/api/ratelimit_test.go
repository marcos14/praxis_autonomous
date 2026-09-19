package api

import (
	"testing"
	"time"
)

func TestLimitadorLoginBloqueiaNoLimiteEDestravaComAJanela(t *testing.T) {
	base := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	agora := base
	l := novoLimitadorLogin(3, 10*time.Minute)
	l.agora = func() time.Time { return agora }

	for i := 0; i < 3; i++ {
		if bloq, _ := l.bloqueado("ip:a"); bloq {
			t.Fatalf("bloqueado antes do limite (falha %d)", i)
		}
		l.registrarFalha("ip:a")
		agora = agora.Add(time.Minute) // falhas em t=0, 1, 2 min
	}
	bloq, espera := l.bloqueado("ip:a")
	if !bloq {
		t.Fatal("deveria bloquear ao atingir o limite")
	}
	// a falha mais antiga (t=0) sai da janela em t=10min; agora é t=3min.
	if espera != 7*time.Minute {
		t.Fatalf("espera = %v, quero 7min", espera)
	}
	// outra chave não é afetada.
	if bloq, _ := l.bloqueado("ip:b"); bloq {
		t.Fatal("chave diferente não deveria estar bloqueada")
	}
	// em t=10min a falha de t=0 sai: sobram 2 → destrava.
	agora = base.Add(10*time.Minute + time.Second)
	if bloq, _ := l.bloqueado("ip:a"); bloq {
		t.Fatal("deveria destravar quando a falha mais antiga sai da janela")
	}
	// espera mínima é 1s mesmo quando a janela está no limiar.
	l2 := novoLimitadorLogin(1, time.Minute)
	l2.agora = func() time.Time { return agora }
	l2.registrarFalha("x")
	agora = agora.Add(time.Minute - time.Millisecond)
	if bloq, esp := l2.bloqueado("x"); !bloq || esp != time.Second {
		t.Fatalf("bloqueado=%v espera=%v, quero true/1s", bloq, esp)
	}
}

func TestLimitadorLoginLimparEPoda(t *testing.T) {
	agora := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	l := novoLimitadorLogin(2, time.Minute)
	l.agora = func() time.Time { return agora }

	l.registrarFalha("email:a")
	l.registrarFalha("email:a")
	if bloq, _ := l.bloqueado("email:a"); !bloq {
		t.Fatal("deveria bloquear")
	}
	l.limpar("email:a")
	if bloq, _ := l.bloqueado("email:a"); bloq {
		t.Fatal("limpar deveria zerar as falhas")
	}
	if _, existe := l.falhas["email:a"]; existe {
		t.Fatal("chave limpa deveria sair do mapa")
	}

	// falhas fora da janela são podadas e a chave some do mapa.
	l.registrarFalha("ip:z")
	agora = agora.Add(2 * time.Minute)
	if bloq, _ := l.bloqueado("ip:z"); bloq {
		t.Fatal("falha antiga não deveria contar")
	}
	if _, existe := l.falhas["ip:z"]; existe {
		t.Fatal("chave só com falhas antigas deveria ser removida do mapa")
	}
}
