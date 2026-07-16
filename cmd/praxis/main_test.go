package main

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestRunVersion(t *testing.T) {
	var out, errOut bytes.Buffer
	if err := run(context.Background(), []string{"-version"}, &out, &errOut); err != nil {
		t.Fatalf("run(-version) retornou erro: %v", err)
	}
	if got := strings.TrimSpace(out.String()); got != versao {
		t.Fatalf("saída = %q, esperado %q", got, versao)
	}
}

func TestRunSemSubcomando(t *testing.T) {
	var out, errOut bytes.Buffer
	if err := run(context.Background(), nil, &out, &errOut); err == nil {
		t.Fatal("run(nil) deveria exigir subcomando, mas não retornou erro")
	}
}

func TestRunSubcomandoDesconhecido(t *testing.T) {
	var out, errOut bytes.Buffer
	if err := run(context.Background(), []string{"foo"}, &out, &errOut); err == nil {
		t.Fatal("run(foo) deveria falhar para subcomando desconhecido")
	}
}

// TestServirListenerShutdownGracioso sobe o servidor em um listener efêmero,
// atende uma requisição real e então cancela o contexto — verificando que o
// shutdown é limpo (retorna nil) e não deixa goroutines vazando.
func TestServirListenerShutdownGracioso(t *testing.T) {
	antes := runtime.NumGoroutine()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /ping", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	ctx, cancel := context.WithCancel(context.Background())
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	var out bytes.Buffer
	done := make(chan error, 1)
	go func() { done <- servirListener(ctx, ln, mux, &out, logger) }()

	// Aguarda o servidor aceitar conexões e faz uma requisição real.
	resp := esperarResposta(t, "http://"+addr+"/ping")
	if resp != http.StatusNoContent {
		t.Fatalf("GET /ping = %d, quero %d", resp, http.StatusNoContent)
	}
	if !strings.Contains(out.String(), addr) {
		t.Errorf("saída não anuncia o endereço %q: %q", addr, out.String())
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("servirListener retornou erro no shutdown: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("servirListener não encerrou dentro do prazo após cancelamento")
	}

	// O servidor não deve deixar goroutines penduradas. Damos um tempo para o
	// runtime recolher as goroutines do http.Server encerrado.
	esperarGoroutines(t, antes)
}

// esperarResposta tenta a URL até obter resposta (o listener pode levar um
// instante para começar a aceitar) e devolve o status.
func esperarResposta(t *testing.T, url string) int {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		resp, err := http.Get(url)
		if err == nil {
			resp.Body.Close()
			return resp.StatusCode
		}
		if time.Now().After(deadline) {
			t.Fatalf("GET %s nunca respondeu: %v", url, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// esperarGoroutines espera o número de goroutines voltar a no máximo base+margem,
// tolerando o atraso do runtime em recolher goroutines encerradas.
func esperarGoroutines(t *testing.T, base int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		n := runtime.NumGoroutine()
		if n <= base+2 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("goroutines vazadas: antes=%d, agora=%d", base, n)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
