package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/marcos14/praxis-autonomous/internal/db"
	"github.com/marcos14/praxis-autonomous/internal/motor"
	"github.com/marcos14/praxis-autonomous/internal/uso"
)

func TestUsoMotoresEndpoint(t *testing.T) {
	banco := abrirBancoTemp(t)
	ctx := context.Background()

	claude, err := banco.CriarMotor(ctx, db.Motor{Nome: "claude", Ativo: true, Fallback: true})
	if err != nil {
		t.Fatal(err)
	}
	inativo, err := banco.CriarMotor(ctx, db.Motor{Nome: "opencode", Ativo: false, Fallback: true})
	if err != nil {
		t.Fatal(err)
	}
	_ = inativo
	conta, err := banco.CriarConta(ctx, db.Conta{EngineID: claude.ID, Alias: "principal", ConfigDir: t.TempDir(), Ativo: true})
	if err != nil {
		t.Fatal(err)
	}

	// uma execução de demanda registrada hoje no perfil.
	proj, err := banco.CriarProjeto(ctx, db.Projeto{Nome: "P", Slug: "p", Pasta: t.TempDir(),
		ModoIntegracao: db.ModoIntegracaoMergeRequest})
	if err != nil {
		t.Fatal(err)
	}
	dem, err := banco.CriarDemanda(ctx, db.Demanda{ProjectID: proj.ID, Titulo: "d"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := banco.CriarExecucao(ctx, db.Execucao{
		DemandID: dem.ID, Operacao: db.OperacaoExecutor, Engine: "claude", Conta: "principal",
		CustoUSD: 0.25, TokensIn: 100, TokensOut: 10,
	}); err != nil {
		t.Fatal(err)
	}

	monitor := uso.Novo(uso.Opcoes{Store: banco, Consultar: func(context.Context, string, string) motor.UsoFranquia {
		return motor.UsoFranquia{Vendor: "claude", Disponivel: true, VerificadoEm: time.Now().UTC(),
			Janelas: []motor.JanelaFranquia{{Rotulo: "5h", UsadoPct: 61.5}}}
	}})
	monitor.VerificarAgora(ctx)

	srv := Novo(Opcoes{Banco: banco, Uso: monitor})
	rec := fazerReq(t, srv, http.MethodGet, "/api/v1/engines/uso", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (corpo=%s)", rec.Code, rec.Body.String())
	}
	var resp struct {
		IntervaloMin int `json:"intervalo_min"`
		Motores      []struct {
			ID     int64 `json:"id"`
			Nome   string
			Perfis []struct {
				ID        int64  `json:"id"`
				Alias     string `json:"alias"`
				UsoPraxis struct {
					Hoje  db.UsoJanela `json:"hoje"`
					Total db.UsoJanela `json:"total"`
				} `json:"uso_praxis"`
				Franquia *motor.UsoFranquia `json:"franquia"`
			} `json:"perfis"`
		} `json:"motores"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decodificar resposta: %v (corpo=%s)", err, rec.Body.String())
	}
	if resp.IntervaloMin != uso.IntervaloPadraoMin {
		t.Fatalf("intervalo = %d, quero %d", resp.IntervaloMin, uso.IntervaloPadraoMin)
	}
	// só o motor ativo aparece.
	if len(resp.Motores) != 1 || resp.Motores[0].ID != claude.ID {
		t.Fatalf("motores = %+v, quero só o claude ativo", resp.Motores)
	}
	perfis := resp.Motores[0].Perfis
	if len(perfis) != 1 || perfis[0].ID != conta.ID || perfis[0].Alias != "principal" {
		t.Fatalf("perfis = %+v", perfis)
	}
	if perfis[0].UsoPraxis.Hoje.Execucoes != 1 || perfis[0].UsoPraxis.Total.CustoUSD != 0.25 {
		t.Fatalf("uso do praxis = %+v", perfis[0].UsoPraxis)
	}
	if perfis[0].Franquia == nil || !perfis[0].Franquia.Disponivel || perfis[0].Franquia.Janelas[0].UsadoPct != 61.5 {
		t.Fatalf("franquia = %+v", perfis[0].Franquia)
	}
}

// TestUsoMotoresSemMonitor: sem o monitor (nil), o endpoint ainda devolve o
// consumo do Praxis — a franquia apenas fica ausente.
func TestUsoMotoresSemMonitor(t *testing.T) {
	banco := abrirBancoTemp(t)
	ctx := context.Background()
	m, err := banco.CriarMotor(ctx, db.Motor{Nome: "claude", Ativo: true, Fallback: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := banco.CriarConta(ctx, db.Conta{EngineID: m.ID, Alias: "principal", ConfigDir: t.TempDir(), Ativo: true}); err != nil {
		t.Fatal(err)
	}
	srv := Novo(Opcoes{Banco: banco})
	rec := fazerReq(t, srv, http.MethodGet, "/api/v1/engines/uso", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d (corpo=%s)", rec.Code, rec.Body.String())
	}
	var resp respUsoMotores
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decodificar: %v", err)
	}
	if len(resp.Motores) != 1 || len(resp.Motores[0].Perfis) != 1 {
		t.Fatalf("resposta = %+v", resp)
	}
	if resp.Motores[0].Perfis[0].Franquia != nil {
		t.Fatal("sem monitor a franquia deveria vir ausente")
	}
}
