package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func loggerSilencioso() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestComRecoverCapturaPanic(t *testing.T) {
	logger := loggerSilencioso()
	h := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	})
	env := encadear(h, comRecover(logger), comLog(logger))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	// Não deve propagar o panic para fora do ServeHTTP.
	env.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, quero 500", rec.Code)
	}
	var resp ErroResp
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decodificar erro: %v (corpo=%q)", err, rec.Body.String())
	}
	if resp.Erro.Codigo != "erro_interno" {
		t.Fatalf("codigo = %q, quero erro_interno", resp.Erro.Codigo)
	}
}

func TestComRecoverNaoAfetaHandlerNormal(t *testing.T) {
	logger := loggerSilencioso()
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		responderJSON(w, http.StatusCreated, map[string]string{"ok": "sim"})
	})
	env := encadear(h, comRecover(logger), comLog(logger))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	env.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, quero 201", rec.Code)
	}
}

func TestCapturaStatusDefault200(t *testing.T) {
	// Um handler que só escreve corpo (sem WriteHeader) deve registrar status 200.
	cw := &capturaStatus{ResponseWriter: httptest.NewRecorder(), status: http.StatusOK}
	if _, err := cw.Write([]byte("oi")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if cw.status != http.StatusOK {
		t.Fatalf("status = %d, quero 200", cw.status)
	}
	if cw.bytes != 2 {
		t.Fatalf("bytes = %d, quero 2", cw.bytes)
	}
}

func TestResponderErro(t *testing.T) {
	rec := httptest.NewRecorder()
	responderErro(rec, http.StatusBadRequest, "invalido", "entrada inválida")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, quero 400", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != tipoJSON {
		t.Fatalf("Content-Type = %q, quero %q", ct, tipoJSON)
	}
	var resp ErroResp
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decodificar: %v", err)
	}
	if resp.Erro.Codigo != "invalido" || resp.Erro.Mensagem != "entrada inválida" {
		t.Fatalf("resp = %+v, inesperado", resp)
	}
}
