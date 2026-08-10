package api

// Testes da Fase D: instalação de harness pela API (job em background) e o
// gate de permissão (config.gerir) nas rotas do instalador.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/marcos14/praxis-autonomous/internal/motor"
)

func TestInstalarHarnessViaAPI(t *testing.T) {
	t.Setenv("PRAXIS_HOME", t.TempDir())
	// Distribuição fake do claude (formato "bin": /stable → versão → binário).
	dist := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/stable") {
			_, _ = w.Write([]byte("1.0.0-teste"))
			return
		}
		_, _ = w.Write([]byte("binario-fake"))
	}))
	defer dist.Close()
	t.Setenv("PRAXIS_DOWNLOAD_BASE_CLAUDE", dist.URL)

	srv := Novo(Opcoes{Banco: abrirBancoTemp(t)})

	// Vendor desconhecido → 400.
	rec := fazerReq(t, srv, http.MethodPost, "/api/v1/engines/instalar", map[string]string{"vendor": "foobar"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("vendor desconhecido: status %d, quero 400", rec.Code)
	}

	// Instala o claude fake.
	rec = fazerReq(t, srv, http.MethodPost, "/api/v1/engines/instalar", map[string]string{"vendor": "claude"})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("instalar: status %d (corpo=%q)", rec.Code, rec.Body.String())
	}
	var aceite struct {
		JobID string `json:"job_id"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &aceite)

	var job struct {
		Status  string                `json:"status"`
		Detalhe string                `json:"detalhe"`
		Info    *motor.InfoInstalacao `json:"info"`
	}
	prazo := time.Now().Add(30 * time.Second)
	for {
		rec = fazerReq(t, srv, http.MethodGet, "/api/v1/engines/instalar/"+aceite.JobID, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET job: status %d", rec.Code)
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &job)
		if job.Status != "instalando" {
			break
		}
		if time.Now().After(prazo) {
			t.Fatal("instalação não terminou no prazo")
		}
		time.Sleep(50 * time.Millisecond)
	}
	if job.Status != "concluido" || job.Info == nil {
		t.Fatalf("job = %s (%s), quero concluido com info", job.Status, job.Detalhe)
	}

	// O estado dos CLIs passa a listar o claude como instalado (gerenciado).
	rec = fazerReq(t, srv, http.MethodGet, "/api/v1/engines/instalaveis", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("instalaveis: status %d", rec.Code)
	}
	var estados []motor.InfoCLI
	_ = json.Unmarshal(rec.Body.Bytes(), &estados)
	achou := false
	for _, e := range estados {
		if e.Vendor == "claude" && e.Instalado {
			achou = true
		}
	}
	if !achou {
		t.Fatalf("claude não aparece instalado em /instalaveis: %+v", estados)
	}
}

func TestInstaladorExigeConfigGerir(t *testing.T) {
	srv := Novo(Opcoes{Banco: abrirBancoTemp(t)})
	comum := criarUsuarioComPapel(t, srv, "comum-inst@x.com", nil)

	rec := fazerReqToken(t, srv, http.MethodGet, "/api/v1/engines/instalaveis", comum, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("instalaveis sem config.gerir: status %d, quero 403", rec.Code)
	}
	rec = fazerReqToken(t, srv, http.MethodPost, "/api/v1/engines/instalar", comum, map[string]string{"vendor": "claude"})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("instalar sem config.gerir: status %d, quero 403", rec.Code)
	}
}
